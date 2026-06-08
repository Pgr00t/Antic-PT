import { StateVaultAdapter } from "./index";

/**
 * A simple in-memory state vault adapter for development and testing.
 * In a production Edge environment, use an adapter backed by KV or Redis.
 */
export class InMemoryStateVaultAdapter implements StateVaultAdapter {
  private vault = new Map<string, any>();

  async get(key: string): Promise<any | null> {
    return this.vault.get(key) || null;
  }

  async set(key: string, data: any): Promise<void> {
    // Deep clone the data before storing to prevent mutations from affecting the cache
    this.vault.set(key, JSON.parse(JSON.stringify(data)));
  }
}
