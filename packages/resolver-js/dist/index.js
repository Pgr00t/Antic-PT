"use strict";
var __defProp = Object.defineProperty;
var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
var __getOwnPropNames = Object.getOwnPropertyNames;
var __hasOwnProp = Object.prototype.hasOwnProperty;
var __export = (target, all) => {
  for (var name in all)
    __defProp(target, name, { get: all[name], enumerable: true });
};
var __copyProps = (to, from, except, desc) => {
  if (from && typeof from === "object" || typeof from === "function") {
    for (let key of __getOwnPropNames(from))
      if (!__hasOwnProp.call(to, key) && key !== except)
        __defProp(to, key, { get: () => from[key], enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable });
  }
  return to;
};
var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);

// src/index.ts
var index_exports = {};
__export(index_exports, {
  AnticipationResolver: () => AnticipationResolver,
  default: () => index_default
});
module.exports = __toCommonJS(index_exports);

// src/resolver.ts
function getClientId() {
  const KEY = "__antic_client_id";
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
function generateId() {
  const bytes = new Uint8Array(16);
  if (typeof crypto !== "undefined" && crypto.getRandomValues) {
    crypto.getRandomValues(bytes);
  } else {
    for (let i = 0; i < 16; i++) bytes[i] = Math.floor(Math.random() * 256);
  }
  return Array.from(bytes).map((b) => b.toString(16).padStart(2, "0")).join("");
}
var SignalChannel = class _SignalChannel {
  static instances = /* @__PURE__ */ new Map();
  es = null;
  handlers = /* @__PURE__ */ new Map();
  url;
  reconnectTimer = null;
  constructor(baseUrl, clientId) {
    this.url = `${baseUrl}/antic/signals?client_id=${clientId}`;
    this.connect();
  }
  static getInstance(baseUrl, clientId) {
    const key = `${baseUrl}::${clientId}`;
    if (!_SignalChannel.instances.has(key)) {
      _SignalChannel.instances.set(key, new _SignalChannel(baseUrl, clientId));
    }
    return _SignalChannel.instances.get(key);
  }
  connect() {
    if (typeof EventSource === "undefined") return;
    this.es = new EventSource(this.url);
    const SIGNAL_EVENTS = ["patch", "fill", "confirm", "replace", "abort"];
    for (const evType of SIGNAL_EVENTS) {
      this.es.addEventListener(evType, (e) => {
        const msg = e;
        try {
          const payload = JSON.parse(msg.data);
          const reconcileId = payload.id ?? msg.lastEventId;
          const handler = this.handlers.get(reconcileId);
          if (handler) handler(evType, payload);
        } catch {
        }
      });
    }
    this.es.onerror = () => {
      this.handlers.forEach((handler) => {
        handler("abort", { reason: "connection_lost", retryable: true });
      });
    };
  }
  /** Subscribe to signals for a specific Reconcile ID. Returns an unsubscribe function. */
  subscribe(reconcileId, handler) {
    this.handlers.set(reconcileId, handler);
    return () => this.handlers.delete(reconcileId);
  }
};
function parseVolatility(header) {
  const result = {};
  if (!header) return result;
  for (const part of header.split(",")) {
    const [key, val] = part.trim().split("=");
    if (key && val) {
      result[key.trim()] = val.trim();
    }
  }
  return result;
}
function parseDeferredFields(header) {
  if (!header || !header.trim()) return [];
  return header.split(",").map((s) => s.trim()).filter(Boolean);
}
var AnticipationResolver = class {
  path;
  baseUrl;
  maxWindow;
  onTimeout;
  clientId;
  handlers = {};
  status = "idle";
  // State for FILL-before-PATCH buffering (spec §9.4, §11.6)
  pendingFill = null;
  fillBufferTimer = null;
  deferredFields = [];
  maxWindowTimer = null;
  unsubscribeSignal = null;
  constructor(path, options = {}) {
    this.path = path;
    this.baseUrl = options.baseUrl ?? (typeof window !== "undefined" ? window.location.origin : "");
    this.maxWindow = options.maxWindow ?? 3e3;
    this.onTimeout = options.onTimeout ?? "refetch";
    this.clientId = getClientId();
  }
  /** Register an event handler. */
  on(event, handler) {
    this.handlers[event] = handler;
    return this;
  }
  /** Emit typed event to registered handler. */
  emit(event, ...args) {
    const h = this.handlers[event];
    if (h) h(...args);
  }
  /** Fetch the resource speculatively. */
  async fetch() {
    this.cleanup();
    this.status = "idle";
    const url = `${this.baseUrl}${this.path}`;
    let response;
    try {
      response = await globalThis.fetch(url, {
        headers: {
          "Accept": "application/json",
          "X-Antic-Client-Id": this.clientId
        }
      });
    } catch {
      this.emit("abort", "network_error", true);
      this.status = "error";
      return;
    }
    if (!response.ok) {
      this.emit("abort", `http_${response.status}`, response.status >= 500);
      this.status = "error";
      return;
    }
    let data;
    try {
      data = await response.json();
    } catch {
      this.emit("abort", "parse_error", false);
      this.status = "error";
      return;
    }
    const state = response.headers.get("X-Antic-State");
    if (state === "confirmed" || !state) {
      const meta2 = {
        staleness: 0,
        reconciledId: "",
        volatility: {},
        deferredFields: [],
        endpointVolatility: "low"
      };
      this.status = "confirmed";
      this.emit("speculative", data, meta2);
      this.emit("confirm");
      return;
    }
    const reconcileId = response.headers.get("X-Antic-Reconcile-Id") ?? "";
    const staleness = parseInt(response.headers.get("X-Antic-Staleness") ?? "0", 10);
    const maxWindowHeader = parseInt(response.headers.get("X-Antic-Max-Window") ?? String(this.maxWindow), 10);
    const volatility = parseVolatility(response.headers.get("X-Antic-Volatility"));
    this.deferredFields = parseDeferredFields(response.headers.get("X-Antic-Deferred-Fields"));
    const vals = Object.values(volatility);
    const endpointVolatility = vals.filter((v) => v === "high").length > vals.length / 2 ? "high" : "low";
    const meta = {
      staleness,
      reconciledId: reconcileId,
      volatility,
      deferredFields: this.deferredFields,
      endpointVolatility
    };
    this.status = "speculative";
    this.emit("speculative", data, meta);
    if (!reconcileId) {
      this.status = "confirmed";
      this.emit("confirm");
      return;
    }
    const channel = SignalChannel.getInstance(this.baseUrl, this.clientId);
    this.unsubscribeSignal = channel.subscribe(reconcileId, (event, payload) => {
      this.handleSignal(event, payload);
    });
    const effectiveWindow = Math.min(maxWindowHeader, this.maxWindow);
    this.maxWindowTimer = setTimeout(() => {
      this.cleanup();
      const meta2 = {
        reconciledId: reconcileId,
        reason: "timeout",
        elapsed: effectiveWindow,
        resource: this.path
      };
      this.emit("speculationAbandoned", meta2);
      if (this.onTimeout === "refetch") {
        this.refetch();
      } else {
        this.emit("abort", "timeout", true);
        this.status = "error";
      }
    }, effectiveWindow);
  }
  /** Directly re-fetch the resource (used after retryable ABORT or speculationAbandoned). */
  async refetch() {
    return this.fetch();
  }
  /** Handle a signal received from the signal channel. */
  handleSignal(event, data) {
    switch (event) {
      case "patch": {
        this.status = "patching";
        this.emit("patch", data.ops ?? []);
        if (this.pendingFill !== null) {
          if (this.fillBufferTimer !== null) {
            clearTimeout(this.fillBufferTimer);
            this.fillBufferTimer = null;
          }
          this.status = "filling";
          this.emit("fill", this.pendingFill);
          this.pendingFill = null;
          this.finalize();
          return;
        }
        if (this.deferredFields.length === 0) {
          this.finalize();
        }
        break;
      }
      case "fill": {
        const fields = data.fields ?? {};
        if (this.status === "speculative") {
          this.pendingFill = fields;
          this.fillBufferTimer = setTimeout(() => {
            this.status = "filling";
            this.emit("fill", this.pendingFill ?? {});
            this.pendingFill = null;
            this.finalize();
          }, 50);
        } else {
          this.status = "filling";
          this.emit("fill", fields);
          this.finalize();
        }
        break;
      }
      case "confirm": {
        this.emit("confirm");
        this.finalize();
        break;
      }
      case "replace": {
        this.emit("replace", data.data ?? data);
        this.finalize();
        break;
      }
      case "abort": {
        const reason = data.reason ?? "unknown";
        const retryable = data.retryable ?? false;
        this.cleanup();
        this.status = "error";
        this.emit("abort", reason, retryable);
        if (retryable && this.onTimeout === "refetch" && reason !== "connection_lost") {
          setTimeout(() => this.refetch(), 100);
        }
        break;
      }
    }
  }
  /** Finalize: mark as confirmed, clear timers, unsubscribe from signal channel. */
  finalize() {
    this.status = "confirmed";
    this.cleanup();
  }
  /** Clean up all timers and subscriptions. */
  cleanup() {
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
  static applyPatch(data, ops) {
    const result = { ...data };
    for (const op of ops) {
      const field = op.path.replace(/^\//, "");
      if (op.op === "replace" || op.op === "add") {
        result[field] = op.value;
      } else if (op.op === "remove") {
        delete result[field];
      }
    }
    return result;
  }
  get currentStatus() {
    return this.status;
  }
};

// src/index.ts
var index_default = AnticipationResolver;
// Annotate the CommonJS export names for ESM import in node:
0 && (module.exports = {
  AnticipationResolver
});
