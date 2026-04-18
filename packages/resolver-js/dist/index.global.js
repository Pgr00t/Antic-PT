"use strict";
var AnticPT = (() => {
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
    AnticResolver: () => AnticResolver,
    SSEParser: () => SSEParser,
    applyPatch: () => applyPatch,
    default: () => index_default
  });

  // src/parser.ts
  var SSEParser = class {
    buffer = "";
    onEvent;
    constructor(onEvent) {
      this.onEvent = onEvent;
    }
    /**
     * Push a chunk of data (typically from a Uint8Array) into the parser.
     */
    push(chunk) {
      this.buffer += chunk;
      const lines = this.buffer.split(/\r?\n/);
      this.buffer = lines.pop() || "";
      let currentEvent = {};
      for (const line of lines) {
        if (line.trim() === "") {
          if (currentEvent.event || currentEvent.id || currentEvent.data) {
            this.onEvent({
              event: currentEvent.event || "message",
              id: currentEvent.id || "",
              data: currentEvent.data || ""
            });
            currentEvent = {};
          }
          continue;
        }
        const colonIndex = line.indexOf(":");
        if (colonIndex === -1) continue;
        const field = line.slice(0, colonIndex).trim();
        let value = line.slice(colonIndex + 1);
        if (value.startsWith(" ")) value = value.slice(1);
        switch (field) {
          case "event":
            currentEvent.event = value;
            break;
          case "id":
            currentEvent.id = value;
            break;
          case "data":
            currentEvent.data = currentEvent.data ? currentEvent.data + "\n" + value : value;
            break;
        }
      }
    }
  };

  // src/reconciler.ts
  function applyPatch(data, ops) {
    const result = JSON.parse(JSON.stringify(data));
    for (const op of ops) {
      applyOperation(result, op);
    }
    return result;
  }
  function applyOperation(obj, op) {
    const parts = op.path.split("/").filter((p) => p !== "");
    if (parts.length === 0) return;
    let current = obj;
    for (let i = 0; i < parts.length - 1; i++) {
      const key = parts[i];
      if (!(key in current)) {
        if (op.op === "add") {
          current[key] = {};
        } else {
          return;
        }
      }
      current = current[key];
    }
    const targetKey = parts[parts.length - 1];
    switch (op.op) {
      case "add":
      case "replace":
        current[targetKey] = op.value;
        break;
      case "remove":
        if (Array.isArray(current)) {
          const index = parseInt(targetKey, 10);
          if (!isNaN(index)) current.splice(index, 1);
        } else {
          delete current[targetKey];
        }
        break;
    }
  }

  // src/resolver.ts
  var AnticResolver = class {
    baseUrl;
    constructor(baseUrl = "") {
      this.baseUrl = baseUrl.endsWith("/") ? baseUrl.slice(0, -1) : baseUrl;
    }
    /**
     * Initiates a dual-track Antic-PT request.
     */
    async fetch(path, options) {
      const url = `${this.baseUrl}${path}`;
      const controller = new AbortController();
      const headers = {
        "Accept": "text/event-stream",
        "X-Antic-Intent": options.intent || "auto"
      };
      try {
        const response = await fetch(url, {
          headers,
          signal: controller.signal
        });
        if (!response.ok) {
          options.onAbort?.("initial_fetch_failed", response.status);
          return { cancel: () => {
          } };
        }
        if (!response.body) {
          options.onAbort?.("no_response_body");
          return { cancel: () => {
          } };
        }
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        const parser = new SSEParser((ev) => {
          this.handleEvent(ev, options);
        });
        (async () => {
          try {
            while (true) {
              const { done, value } = await reader.read();
              if (done) break;
              parser.push(decoder.decode(value, { stream: true }));
            }
          } catch (err) {
            if (err.name !== "AbortError") {
              options.onAbort?.("stream_interrupted");
            }
          }
        })();
        return {
          cancel: () => controller.abort()
        };
      } catch (err) {
        options.onAbort?.("network_error");
        return { cancel: () => {
        } };
      }
    }
    handleEvent(ev, options) {
      try {
        const payload = ev.data ? JSON.parse(ev.data) : null;
        const version = parseInt(ev.id, 10);
        switch (ev.event) {
          case "speculative":
            options.onSpeculative?.(payload, payload?._antic);
            break;
          case "confirm":
            options.onConfirm?.(version);
            break;
          case "patch":
            options.onPatch?.(payload.ops);
            break;
          case "replace":
            options.onReplace?.(payload);
            break;
          case "abort":
            options.onAbort?.(payload.reason, payload.code);
            break;
        }
      } catch (err) {
        console.error("[AnticResolver] Failed to parse event data:", err);
      }
    }
    /**
     * Helper utility for manual reconciliation (if needed).
     */
    static reconcile(data, ops) {
      return applyPatch(data, ops);
    }
    /**
     * Initiates an optimistic write operation (POST, PUT, PATCH, DELETE).
     *
     * Fires onOptimistic immediately with the predicted new state, then awaits
     * the server response and fires onCommitted (success) or onReverted (failure).
     *
     * @example
     * resolver.mutate('/spec/user/1', {
     *   method: 'PUT',
     *   body: { name: 'Bob' },
     *   onOptimistic: (data) => render(data),     // ~0ms
     *   onCommitted: (data) => syncState(data),   // ~120ms
     *   onReverted: (reason) => rollback(reason), // on error
     * });
     */
    async mutate(path, options) {
      const url = `${this.baseUrl}${path}`;
      const controller = new AbortController();
      const headers = {
        "Accept": "text/event-stream",
        "Content-Type": "application/json"
      };
      try {
        const response = await fetch(url, {
          method: options.method,
          headers,
          body: options.body ? JSON.stringify(options.body) : void 0,
          signal: controller.signal
        });
        if (!response.ok || !response.body) {
          options.onReverted?.("network_error", response.status);
          return { cancel: () => {
          } };
        }
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        const parser = new SSEParser((ev) => {
          this.handleMutationEvent(ev, options);
        });
        (async () => {
          try {
            while (true) {
              const { done, value } = await reader.read();
              if (done) break;
              parser.push(decoder.decode(value, { stream: true }));
            }
          } catch (err) {
            if (err.name !== "AbortError") {
              options.onReverted?.("stream_interrupted", 0);
            }
          }
        })();
        return { cancel: () => controller.abort() };
      } catch {
        options.onReverted?.("network_error", 0);
        return { cancel: () => {
        } };
      }
    }
    handleMutationEvent(ev, options) {
      try {
        const payload = ev.data ? JSON.parse(ev.data) : null;
        switch (ev.event) {
          case "optimistic":
            options.onOptimistic?.(payload?.data ?? null);
            break;
          case "committed":
            options.onCommitted?.(payload?.data ?? null, payload?.latency_ms ?? 0);
            break;
          case "reverted":
            options.onReverted?.(payload?.reason ?? "unknown", payload?.code ?? 0, payload?.detail);
            break;
        }
      } catch (err) {
        console.error("[AnticResolver] Failed to parse mutation event:", err);
      }
    }
  };

  // src/index.ts
  var index_default = AnticResolver;
  return __toCommonJS(index_exports);
})();
