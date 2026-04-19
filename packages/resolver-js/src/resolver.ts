/**
 * Antic-PT v0.2 — AnticipationResolver JS SDK
 *
 * Zero-dependency TypeScript SDK for consuming speculative HTTP responses
 * and reconciliation signals from a Spec-Link proxy.
 *
 * Architecture:
 *   - SignalChannel: singleton SSE connection to /antic/signals, shared by all resolvers.
 *   - AnticipationResolver: per-resource state machine. Fetches speculatively, then
 *     listens for signals (PATCH / FILL / CONFIRM / REPLACE / ABORT) on the channel.
 *
 * @see https://github.com/Pgr00t/Antic-PT
 */

// ── Types ─────────────────────────────────────────────────────────────────────

export type VolatilityLevel = 'high' | 'low' | 'invariant';

export type ResolverStatus =
  | 'idle'
  | 'speculative'
  | 'patching'
  | 'filling'
  | 'confirmed'
  | 'error';

/** Metadata attached to every speculative response, parsed from X-Antic-* headers. */
export interface ResolverMeta {
  /** Age of the State Vault entry at serve time (X-Antic-Staleness). */
  staleness: number;
  /** Unique ID linking this response to its reconciliation signal. */
  reconciledId: string;
  /** Per-field declared volatility, keyed by field name. */
  volatility: Record<string, VolatilityLevel>;
  /** Fields withheld from the speculative response (X-Antic-Deferred-Fields). */
  deferredFields: string[];
  /** Endpoint-level fallback for fields not in the volatility map. */
  endpointVolatility: VolatilityLevel;
}

export interface ResolverOptions {
  /**
   * Base URL of the Spec-Link proxy (e.g. "http://localhost:4000").
   * Defaults to the current origin in browser environments.
   */
  baseUrl?: string;
  /** Maximum ms to wait for a reconciliation signal before falling back. Default: 3000. */
  maxWindow?: number;
  /** What to do when X-Antic-Max-Window expires without a signal. Default: "refetch". */
  onTimeout?: 'refetch' | 'error';
}

export interface AbandonMeta {
  reconciledId: string;
  reason: 'timeout' | 'connection_lost';
  elapsed: number;
  resource: string;
}

export type PatchOp = {
  op: 'add' | 'remove' | 'replace' | 'move' | 'copy' | 'test';
  path: string;
  value?: unknown;
  from?: string;
};

type EventMap = {
  speculative: [data: any, meta: ResolverMeta];
  patch: [ops: PatchOp[]];
  fill: [fields: Record<string, any>];
  confirm: [];
  replace: [data: any];
  abort: [reason: string, retryable: boolean];
  speculationAbandoned: [meta: AbandonMeta];
};

type Handlers = {
  [K in keyof EventMap]?: (...args: EventMap[K]) => void;
};

// ── Client ID ─────────────────────────────────────────────────────────────────

/** Returns a stable per-client UUID, persisted in localStorage when available. */
function getClientId(): string {
  const KEY = '__antic_client_id';
  try {
    const stored = localStorage.getItem(KEY);
    if (stored) return stored;
    const id = generateId();
    localStorage.setItem(KEY, id);
    return id;
  } catch {
    return generateId();
  }
}

function generateId(): string {
  const bytes = new Uint8Array(16);
  if (typeof crypto !== 'undefined' && crypto.getRandomValues) {
    crypto.getRandomValues(bytes);
  } else {
    for (let i = 0; i < 16; i++) bytes[i] = Math.floor(Math.random() * 256);
  }
  return Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join('');
}

// ── Signal Channel ─────────────────────────────────────────────────────────────

type SignalHandler = (event: string, data: any) => void;

/**
 * SignalChannel manages the single persistent SSE connection to /antic/signals.
 * All AnticipationResolver instances share one connection per page, matched by Reconcile ID.
 */
class SignalChannel {
  private static instances = new Map<string, SignalChannel>();

  private es: EventSource | null = null;
  private handlers = new Map<string, SignalHandler>();
  private readonly url: string;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;

  private constructor(baseUrl: string, clientId: string) {
    this.url = `${baseUrl}/antic/signals?client_id=${clientId}`;
    this.connect();
  }

  static getInstance(baseUrl: string, clientId: string): SignalChannel {
    const key = `${baseUrl}::${clientId}`;
    if (!SignalChannel.instances.has(key)) {
      SignalChannel.instances.set(key, new SignalChannel(baseUrl, clientId));
    }
    return SignalChannel.instances.get(key)!;
  }

  private connect(): void {
    if (typeof EventSource === 'undefined') return; // SSR / non-browser

    this.es = new EventSource(this.url);

    const SIGNAL_EVENTS = ['patch', 'fill', 'confirm', 'replace', 'abort'] as const;

    for (const evType of SIGNAL_EVENTS) {
      this.es.addEventListener(evType, (e: Event) => {
        const msg = e as MessageEvent;
        try {
          const payload = JSON.parse(msg.data);
          // Reconcile ID is in the JSON payload (id) and in the SSE id field.
          const reconcileId: string = payload.id ?? msg.lastEventId;
          const handler = this.handlers.get(reconcileId);
          if (handler) handler(evType, payload);
        } catch {
          // Malformed SSE data — discard per spec §11.5
        }
      });
    }

    this.es.onerror = () => {
      // Emit synthetic connection_lost abort to all active handlers.
      this.handlers.forEach((handler) => {
        handler('abort', { reason: 'connection_lost', retryable: true });
      });
      // EventSource will auto-reconnect; clear stale handlers only if it fails permanently.
    };
  }

  /** Subscribe to signals for a specific Reconcile ID. Returns an unsubscribe function. */
  subscribe(reconcileId: string, handler: SignalHandler): () => void {
    this.handlers.set(reconcileId, handler);
    return () => this.handlers.delete(reconcileId);
  }
}

// ── Header Parsers ─────────────────────────────────────────────────────────────

/**
 * Parses X-Antic-Volatility: "cpuUsage=high, uptimeDays=low, region=invariant"
 * into a field-keyed map.
 */
function parseVolatility(header: string | null): Record<string, VolatilityLevel> {
  const result: Record<string, VolatilityLevel> = {};
  if (!header) return result;
  for (const part of header.split(',')) {
    const [key, val] = part.trim().split('=');
    if (key && val) {
      result[key.trim()] = val.trim() as VolatilityLevel;
    }
  }
  return result;
}

/**
 * Parses X-Antic-Deferred-Fields: "activeAlarms, accountBalance"
 * into an array of field names.
 */
function parseDeferredFields(header: string | null): string[] {
  if (!header || !header.trim()) return [];
  return header.split(',').map(s => s.trim()).filter(Boolean);
}

// ── AnticipationResolver ───────────────────────────────────────────────────────

/**
 * AnticipationResolver manages the full Antic-PT v0.2 protocol lifecycle
 * for a single resource. Multiple instances share one SSE signal channel.
 *
 * @example
 * const resolver = new AnticipationResolver('/spec/user/1', {
 *   baseUrl: 'http://localhost:4000',
 *   maxWindow: 3000,
 *   onTimeout: 'refetch',
 * });
 *
 * resolver.on('speculative', (data, meta) => render(data));
 * resolver.on('patch', (ops) => applyPatch(state, ops));
 * resolver.on('fill', (fields) => mergeFields(state, fields));
 * resolver.on('confirm', () => hideSyncIndicator());
 * resolver.on('replace', (data) => replaceUI(data));
 * resolver.on('abort', (reason, retryable) => showError(reason));
 * resolver.on('speculationAbandoned', (meta) => metrics.inc('abandoned'));
 *
 * resolver.fetch();
 */
export class AnticipationResolver {
  private readonly path: string;
  private readonly baseUrl: string;
  private readonly maxWindow: number;
  private readonly onTimeout: 'refetch' | 'error';
  private readonly clientId: string;

  private handlers: Handlers = {};
  private status: ResolverStatus = 'idle';
  public meta: ResolverMeta | null = null;

  // State for FILL-before-PATCH buffering (spec §9.4, §11.6)
  private pendingFill: Record<string, any> | null = null;
  private fillBufferTimer: ReturnType<typeof setTimeout> | null = null;
  private deferredFields: string[] = [];

  private maxWindowTimer: ReturnType<typeof setTimeout> | null = null;
  private unsubscribeSignal: (() => void) | null = null;

  constructor(path: string, options: ResolverOptions = {}) {
    this.path = path;
    this.baseUrl = options.baseUrl ?? (typeof window !== 'undefined' ? window.location.origin : '');
    this.maxWindow = options.maxWindow ?? 3000;
    this.onTimeout = options.onTimeout ?? 'refetch';
    this.clientId = getClientId();
  }

  /** Register an event handler. */
  on<K extends keyof EventMap>(event: K, handler: (...args: EventMap[K]) => void): this {
    (this.handlers as any)[event] = handler;
    return this;
  }

  /** Emit typed event to registered handler. */
  private emit<K extends keyof EventMap>(event: K, ...args: EventMap[K]): void {
    const h = (this.handlers as any)[event];
    if (h) (h as Function)(...args);
  }

  /** Fetch the resource speculatively. */
  async fetch(): Promise<void> {
    this.cleanup();
    this.status = 'idle';

    const url = `${this.baseUrl}${this.path}`;

    let response: Response;
    try {
      response = await globalThis.fetch(url, {
        headers: {
          'Accept': 'application/json',
          'X-Antic-Client-Id': this.clientId,
        },
      });
    } catch {
      this.emit('abort', 'network_error', true);
      this.status = 'error';
      return;
    }

    if (!response.ok) {
      this.emit('abort', `http_${response.status}`, response.status >= 500);
      this.status = 'error';
      return;
    }

    let data: any;
    try {
      data = await response.json();
    } catch {
      this.emit('abort', 'parse_error', false);
      this.status = 'error';
      return;
    }

    const state = response.headers.get('X-Antic-State');

    // ── Confirmed (cold miss / stale) — no signal forthcoming ────────────────
    if (state === 'confirmed' || !state) {
      this.meta = {
        staleness: 0,
        reconciledId: '',
        volatility: {},
        deferredFields: [],
        endpointVolatility: 'low',
      };
      this.status = 'confirmed';
      this.emit('speculative', data, this.meta);
      this.emit('confirm');
      return;
    }

    // ── Speculative — subscribe to signal channel ────────────────────────────
    const reconcileId = response.headers.get('X-Antic-Reconcile-Id') ?? '';
    const staleness = parseInt(response.headers.get('X-Antic-Staleness') ?? '0', 10);
    const maxWindowHeader = parseInt(response.headers.get('X-Antic-Max-Window') ?? String(this.maxWindow), 10);
    const volatility = parseVolatility(response.headers.get('X-Antic-Volatility'));
    this.deferredFields = parseDeferredFields(response.headers.get('X-Antic-Deferred-Fields'));

    // Infer endpoint-level volatility: most common value, or 'low'.
    const vals = Object.values(volatility);
    const endpointVolatility = (vals.filter(v => v === 'high').length > vals.length / 2 ? 'high' : 'low') as VolatilityLevel;

    this.meta = {
      staleness,
      reconciledId: reconcileId,
      volatility,
      deferredFields: this.deferredFields,
      endpointVolatility,
    };

    this.status = 'speculative';
    this.emit('speculative', data, this.meta);

    if (!reconcileId) {
      // No Reconcile ID — cannot receive signals. Treat as confirmed.
      this.status = 'confirmed';
      this.emit('confirm');
      return;
    }

    // Subscribe to the shared signal channel.
    const channel = SignalChannel.getInstance(this.baseUrl, this.clientId);
    this.unsubscribeSignal = channel.subscribe(reconcileId, (event, payload) => {
      this.handleSignal(event, payload);
    });

    // Start max-window timer.
    const effectiveWindow = Math.min(maxWindowHeader, this.maxWindow);
    this.maxWindowTimer = setTimeout(() => {
      this.cleanup();
      const meta: AbandonMeta = {
        reconciledId: reconcileId,
        reason: 'timeout',
        elapsed: effectiveWindow,
        resource: this.path,
      };
      this.emit('speculationAbandoned', meta);
      if (this.onTimeout === 'refetch') {
        this.refetch();
      } else {
        this.emit('abort', 'timeout', true);
        this.status = 'error';
      }
    }, effectiveWindow);
  }

  /** Directly re-fetch the resource (used after retryable ABORT or speculationAbandoned). */
  async refetch(): Promise<void> {
    return this.fetch();
  }

  /** Handle a signal received from the signal channel. */
  private handleSignal(event: string, data: any): void {
    switch (event) {
      case 'patch': {
        this.status = 'patching';
        this.emit('patch', data.ops ?? []);

        // If FILL was buffered (arrived before PATCH), emit it now in order.
        if (this.pendingFill !== null) {
          if (this.fillBufferTimer !== null) {
            clearTimeout(this.fillBufferTimer);
            this.fillBufferTimer = null;
          }
          this.status = 'filling';
          this.emit('fill', this.pendingFill);
          this.pendingFill = null;
          this.finalize();
          return;
        }

        // If there are no deferred fields, PATCH is terminal.
        if (this.deferredFields.length === 0) {
          this.finalize();
        }
        // Otherwise wait for FILL.
        break;
      }

      case 'fill': {
        const fields: Record<string, any> = data.fields ?? {};

        if (this.status === 'speculative') {
          // FILL arrived before PATCH (spec §11.6) — buffer for 50ms.
          this.pendingFill = fields;
          this.fillBufferTimer = setTimeout(() => {
            // PATCH never arrived within window; emit FILL directly.
            this.status = 'filling';
            this.emit('fill', this.pendingFill ?? {});
            this.pendingFill = null;
            this.finalize();
          }, 50);
        } else {
          // Normal order: FILL after PATCH.
          this.status = 'filling';
          this.emit('fill', fields);
          this.finalize();
        }
        break;
      }

      case 'confirm': {
        this.emit('confirm');
        this.finalize();
        break;
      }

      case 'replace': {
        this.emit('replace', data.data ?? data);
        this.finalize();
        break;
      }

      case 'abort': {
        const reason: string = data.reason ?? 'unknown';
        const retryable: boolean = data.retryable ?? false;
        this.cleanup();
        this.status = 'error';
        this.emit('abort', reason, retryable);
        if (retryable && this.onTimeout === 'refetch' && reason !== 'connection_lost') {
          setTimeout(() => this.refetch(), 100);
        }
        break;
      }
    }
  }

  /** Finalize: mark as confirmed, clear timers, unsubscribe from signal channel. */
  private finalize(): void {
    this.status = 'confirmed';
    this.cleanup();
  }

  /** Clean up all timers and subscriptions. */
  private cleanup(): void {
    if (this.maxWindowTimer !== null) {
      clearTimeout(this.maxWindowTimer);
      this.maxWindowTimer = null;
    }
    if (this.fillBufferTimer !== null) {
      clearTimeout(this.fillBufferTimer);
      this.fillBufferTimer = null;
    }
    if (this.unsubscribeSignal) {
      this.unsubscribeSignal();
      this.unsubscribeSignal = null;
    }
    this.pendingFill = null;
  }

  /** Apply RFC 6902 JSON Patch operations to a data object. Returns the patched copy. */
  static applyPatch(data: Record<string, any>, ops: PatchOp[]): Record<string, any> {
    const result = { ...data };
    for (const op of ops) {
      const field = op.path.replace(/^\//, '');
      if (op.op === 'replace' || op.op === 'add') {
        result[field] = op.value;
      } else if (op.op === 'remove') {
        delete result[field];
      }
    }
    return result;
  }

  get currentStatus(): ResolverStatus {
    return this.status;
  }
}
