export interface PubSubAdapter {
  /**
   * Publish a patch or fill event to a specific Reconcile ID.
   */
  publish(
    reconcileId: string,
    event: "patch" | "fill" | "replace" | "confirm" | "abort",
    data: any,
  ): Promise<void>;

  /**
   * Subscribe to events for a specific Reconcile ID.
   * Resolves when the subscription is active.
   */
  subscribe(
    reconcileId: string,
    onMessage: (event: "patch" | "fill" | "replace" | "confirm" | "abort", data: any) => void,
  ): Promise<void>;

  /**
   * Unsubscribe and clean up resources for a specific Reconcile ID.
   */
  unsubscribe(reconcileId: string): Promise<void>;
}

export interface StateVaultAdapter {
  /**
   * Retrieve cached data by key.
   */
  get(key: string): Promise<any | null>;

  /**
   * Store data in the vault.
   */
  set(key: string, data: any): Promise<void>;
}
