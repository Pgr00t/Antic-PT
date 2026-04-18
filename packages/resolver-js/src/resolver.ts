import { SSEParser } from './parser';
import { applyPatch, PatchOp } from './reconciler';

export interface AnticOptions {
  intent?: 'auto' | 'guided' | 'bypass';
  hints?: Record<string, any>;
  onSpeculative?: (data: any, meta: any) => void;
  onConfirm?: (version: number) => void;
  onPatch?: (patch: PatchOp[]) => void;
  onReplace?: (data: any) => void;
  onAbort?: (reason: string, code?: number) => void;
  collisionPolicy?: 'server-wins' | 'client-wins'; // Default: server-wins
}

export interface ResolverResponse {
  cancel: () => void;
}

export class AnticResolver {
  private baseUrl: string;

  constructor(baseUrl: string = '') {
    this.baseUrl = baseUrl.endsWith('/') ? baseUrl.slice(0, -1) : baseUrl;
  }

  /**
   * Initiates a dual-track Antic-PT request.
   */
  async fetch(path: string, options: AnticOptions): Promise<ResolverResponse> {
    const url = `${this.baseUrl}${path}`;
    const controller = new AbortController();

    const headers: Record<string, string> = {
      'Accept': 'text/event-stream',
      'X-Antic-Intent': options.intent || 'auto',
    };

    // Note: X-Antic-Version would typically be tracked by the app and passed here.
    
    try {
      const response = await fetch(url, {
        headers,
        signal: controller.signal,
      });

      if (!response.ok) {
        options.onAbort?.('initial_fetch_failed', response.status);
        return { cancel: () => {} };
      }

      if (!response.body) {
        options.onAbort?.('no_response_body');
        return { cancel: () => {} };
      }

      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      
      const parser = new SSEParser((ev) => {
        this.handleEvent(ev, options);
      });

      // Background processing of the stream
      (async () => {
        try {
          while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            parser.push(decoder.decode(value, { stream: true }));
          }
        } catch (err: any) {
          if (err.name !== 'AbortError') {
            options.onAbort?.('stream_interrupted');
          }
        }
      })();

      return {
        cancel: () => controller.abort(),
      };
    } catch (err: any) {
       options.onAbort?.('network_error');
       return { cancel: () => {} };
    }
  }

  private handleEvent(ev: { event: string; id: string; data: string }, options: AnticOptions) {
    try {
      const payload = ev.data ? JSON.parse(ev.data) : null;
      const version = parseInt(ev.id, 10);

      switch (ev.event) {
        case 'speculative':
          options.onSpeculative?.(payload, payload?._antic);
          break;
        case 'confirm':
          options.onConfirm?.(version);
          break;
        case 'patch':
          options.onPatch?.(payload.ops);
          break;
        case 'replace':
          options.onReplace?.(payload);
          break;
        case 'abort':
          options.onAbort?.(payload.reason, payload.code);
          break;
      }
    } catch (err) {
      console.error('[AnticResolver] Failed to parse event data:', err);
    }
  }

  /**
   * Helper utility for manual reconciliation (if needed).
   */
  static reconcile(data: any, ops: PatchOp[]): any {
    return applyPatch(data, ops);
  }
}
