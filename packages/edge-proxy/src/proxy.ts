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

    // Kick off background revalidation using the waitUntil pattern (standard in Edge environments)
    // We pass it to the Edge platform so it survives the response returning
    this.revalidateInBackground(reconcileId, path).catch(console.error);

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
   * Handle the SSE signal connection request.
   * Route: /antic/signals?id=...
   */
  public async handleSignalStream(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const reconcileId = url.searchParams.get("id");

    if (!reconcileId) {
      return new Response("Missing reconcile id", { status: 400 });
    }

    const sse = new SSEConnection();

    // Subscribe to PubSub for this reconcile ID
    // Note: If the edge function is aborted/closed by the client, we must unsubscribe
    this.pubsub
      .subscribe(reconcileId, (event, data) => {
        sse.send(event, data).catch(console.error);
      })
      .catch(console.error);

    // Watch for client disconnect to clean up the subscription
    request.signal.addEventListener("abort", () => {
      this.pubsub.unsubscribe(reconcileId);
      sse.close().catch(console.error);
    });

    return sse.getResponse();
  }

  /**
   * Perform the background fetch and publish the JSON patch.
   */
  private async revalidateInBackground(
    reconcileId: string,
    path: string,
  ): Promise<void> {
    const upstreamUrl = `${this.upstreamUrl}${path}`;

    try {
      const response = await fetch(upstreamUrl);
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
      await this.pubsub.publish(reconcileId, "patch", ops);

      // Finally publish a replace/confirm event
      await this.pubsub.publish(reconcileId, "replace", upstreamData);
    } catch (error) {
      console.error(
        `[EdgeProxy] Revalidation failed for ${reconcileId}`,
        error,
      );
    }
  }
}
