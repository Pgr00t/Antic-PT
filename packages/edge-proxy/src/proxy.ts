import { PubSubAdapter } from "./adapters";
import { SSEConnection } from "./sse";
import { compare } from "fast-json-patch";

export interface EdgeProxyOptions {
  pubsub: PubSubAdapter;
  upstreamUrl: string;
}

export class EdgeProxy {
  private pubsub: PubSubAdapter;
  private upstreamUrl: string;

  constructor(options: EdgeProxyOptions) {
    this.pubsub = options.pubsub;
    this.upstreamUrl = options.upstreamUrl;
  }

  /**
   * Generates a random reconcile ID for the speculation lifecycle
   */
  private generateReconcileId(): string {
    return crypto.randomUUID();
  }

  /**
   * Handle the primary speculation GET request from the client.
   * In a real edge worker, this is mapped to a route like /spec/*
   */
  public async handleSpeculation(
    request: Request,
    path: string,
  ): Promise<Response> {
    const reconcileId = this.generateReconcileId();

    // Simulate Cache/Speculation hit
    // In a full implementation, we'd look up the last known state from KV or Cache API
    // For this prototype, we immediately trigger background revalidation
    const speculativeData = {
      _status: "speculative",
      message: "Loading latest state...",
    };

    const clientId = request.headers.get("X-Antic-Client-Id") || "default-client";

    // Kick off background revalidation using the waitUntil pattern (standard in Edge environments)
    // We pass it to the Edge platform so it survives the response returning
    this.revalidateInBackground(request, reconcileId, clientId, path).catch(console.error);

    return new Response(JSON.stringify(speculativeData), {
      status: 200,
      headers: {
        "Content-Type": "application/json",
        "X-Antic-State": "speculative",
        "X-Antic-Reconcile-Id": reconcileId,
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Expose-Headers": "X-Antic-State, X-Antic-Reconcile-Id",
      },
    });
  }

  /**
   * Handle a Provisional Write (POST/PUT/PATCH) request from the client.
   * Intercepts the request, returns a 202 Accepted instantly, and processes the write in the background.
   */
  public async handleProvisionalWrite(
    request: Request,
    path: string,
  ): Promise<Response> {
    const reconcileId = this.generateReconcileId();
    
    // We need to clone the request to read its body for the provisional response,
    // while keeping the original intact for the background fetch.
    const reqClone = request.clone();
    let payload = {};
    try {
      const text = await reqClone.text();
      if (text) payload = JSON.parse(text);
    } catch (e) {
      // Body might not be JSON, ignore for prototype
    }

    const clientId = request.headers.get("X-Antic-Client-Id") || "default-client";

    // Execute the actual write mutation in the background
    this.executeWriteInBackground(request, path, reconcileId, clientId).catch(console.error);

    // Return the provisional state instantly
    return new Response(JSON.stringify(payload), {
      status: 202,
      headers: {
        "Content-Type": "application/json",
        "X-Antic-State": "provisional",
        "X-Antic-Reconcile-Id": reconcileId,
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Expose-Headers": "X-Antic-State, X-Antic-Reconcile-Id",
      },
    });
  }

  /**
   * Handle the SSE signal connection request.
   * Route: /antic/signals?id=...
   */
  public async handleSignalStream(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const clientId = url.searchParams.get("client_id");

    if (!clientId) {
      return new Response("Missing client_id", { status: 400 });
    }

    const sse = new SSEConnection();

    // Subscribe to PubSub for this client ID
    // Note: If the edge function is aborted/closed by the client, we must unsubscribe
    this.pubsub
      .subscribe(clientId, (event, data) => {
        console.log(`[EdgeProxy] SSE Sending event: ${event} to clientId: ${clientId}`);
        sse.send(event, data).catch(console.error);
      })
      .catch(console.error);

    // Watch for client disconnect to clean up the subscription
    request.signal.addEventListener("abort", () => {
      this.pubsub.unsubscribe(clientId);
      sse.close().catch(console.error);
    });

    return sse.getResponse();
  }

  /**
   * Perform the background fetch and publish the JSON patch.
   */
  private async revalidateInBackground(
    request: Request,
    reconcileId: string,
    clientId: string,
    path: string,
  ): Promise<void> {
    const upstreamUrl = `${this.upstreamUrl}${path}`;

    try {
      const fetchHeaders = new Headers(request.headers);
      fetchHeaders.delete("host");
      fetchHeaders.delete("connection");

      const response = await fetch(upstreamUrl, {
        headers: fetchHeaders
      });
      if (!response.ok) throw new Error("Upstream failed");

      const upstreamData = await response.json();

      // Calculate JSON Patch between the speculative state and actual state
      // (Assuming the speculative state was known, here we just replace for prototype)
      const speculativeData = {
        _status: "speculative",
        message: "Loading latest state...",
      };
      const ops = compare(speculativeData, upstreamData);

      // Publish the patch via PubSub to whichever Edge Isolate holds the SSE connection
      await this.pubsub.publish(clientId, "patch", { id: reconcileId, ops });

      // Finally publish a replace/confirm event
      await this.pubsub.publish(clientId, "replace", { id: reconcileId, data: upstreamData });
    } catch (error) {
      console.error(
        `[EdgeProxy] Revalidation failed for ${reconcileId}`,
        error,
      );
    }
  }

  /**
   * Execute the upstream write mutation in the background and publish CONFIRM/ABORT.
   */
  private async executeWriteInBackground(
    request: Request,
    path: string,
    reconcileId: string,
    clientId: string,
  ): Promise<void> {
    const upstreamUrl = `${this.upstreamUrl}${path}`;

    try {
      const fetchHeaders = new Headers(request.headers);
      fetchHeaders.delete("host");
      fetchHeaders.delete("connection");
      fetchHeaders.delete("content-length"); // Let fetch recalculate based on body

      // Forward the mutation upstream
      const response = await fetch(upstreamUrl, {
        method: request.method,
        headers: fetchHeaders,
        body: await request.arrayBuffer()
      });

      if (!response.ok) {
        throw new Error(`Upstream write failed with status: ${response.status}`);
      }

      let upstreamData = {};
      try {
        upstreamData = await response.json();
      } catch (e) {
        upstreamData = { _raw: await response.text() };
      }

      // Publish CONFIRM signal
      console.log(`[EdgeProxy] Publishing CONFIRM for clientId ${clientId}`);
      await this.pubsub.publish(clientId, "confirm", { id: reconcileId, data: upstreamData });
    } catch (error: any) {
      console.error(
        `[EdgeProxy] Write mutation failed for ${reconcileId}`,
        error,
      );
      // Publish ABORT signal with error details
      await this.pubsub.publish(clientId, "abort", { id: reconcileId, reason: error.message || "Unknown error", retryable: false });
    }
  }
}
