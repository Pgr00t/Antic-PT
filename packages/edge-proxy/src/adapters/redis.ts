import { Redis } from "@upstash/redis";
import { PubSubAdapter } from "./index";

/**
 * A PubSubAdapter implementation using Upstash Redis.
 * Upstash Redis uses a REST API under the hood, making it perfectly compatible
 * with Cloudflare Workers and Vercel Edge functions.
 *
 * Note: Since Edge Workers don't support traditional Redis long-polling easily without
 * consuming execution time, this adapter leverages Upstash's `subscribe` (if available in their Edge SDK)
 * or falls back to periodic polling for the sake of the Edge environment.
 * In a true production environment, Cloudflare Durable Objects are heavily preferred over polling.
 */
export class UpstashRedisAdapter implements PubSubAdapter {
  private redis: Redis;
  private pollingIntervals: Map<string, any> = new Map();

  constructor(url: string, token: string) {
    this.redis = new Redis({
      url,
      token,
    });
  }

  async publish(
    reconcileId: string,
    event: "patch" | "fill" | "replace",
    data: any,
  ): Promise<void> {
    const payload = JSON.stringify({ event, data, timestamp: Date.now() });
    // Push the event to a list acting as a queue for this reconcileId
    await this.redis.rpush(`antic:pubsub:${reconcileId}`, payload);
    // Set a short expiration (e.g. 60 seconds) since we only care about real-time patches
    await this.redis.expire(`antic:pubsub:${reconcileId}`, 60);
  }

  async subscribe(
    reconcileId: string,
    onMessage: (event: "patch" | "fill" | "replace", data: any) => void,
  ): Promise<void> {
    // Edge environments can't hold raw TCP connections.
    // We simulate pub/sub by polling the list using Upstash's REST API.
    // This is a known limitation of Serverless Redis, highlighting why Durable Objects are better.
    let lastIndex = 0;

    const poll = async () => {
      try {
        const messages = await this.redis.lrange(
          `antic:pubsub:${reconcileId}`,
          lastIndex,
          -1,
        );
        if (messages && messages.length > 0) {
          for (const msgStr of messages) {
            const msg =
              typeof msgStr === "string" ? JSON.parse(msgStr) : msgStr;
            onMessage(msg.event, msg.data);
          }
          lastIndex += messages.length;
        }
      } catch (err) {
        console.error(`[RedisAdapter] Polling error for ${reconcileId}:`, err);
      }
    };

    const interval = setInterval(poll, 1000);
    this.pollingIntervals.set(reconcileId, interval);
  }

  async unsubscribe(reconcileId: string): Promise<void> {
    const interval = this.pollingIntervals.get(reconcileId);
    if (interval) {
      clearInterval(interval);
      this.pollingIntervals.delete(reconcileId);
    }
  }
}
