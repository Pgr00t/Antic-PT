import { webcrypto } from 'node:crypto';
if (!global.crypto) global.crypto = webcrypto as any;
import { EdgeProxy, PubSubAdapter } from '../src';

class MockPubSub implements PubSubAdapter {
  private listeners: Map<string, Array<(event: string, data: any) => void>> = new Map();

  async publish(reconcileId: string, event: 'patch' | 'fill' | 'replace', data: any): Promise<void> {
    console.log(`[MockPubSub] Publishing ${event}`);
    const handlers = this.listeners.get(reconcileId) || [];
    handlers.forEach(h => h(event, data));
  }

  async subscribe(reconcileId: string, onMessage: (event: 'patch' | 'fill' | 'replace', data: any) => void): Promise<void> {
    const handlers = this.listeners.get(reconcileId) || [];
    handlers.push(onMessage);
    this.listeners.set(reconcileId, handlers);
  }

  async unsubscribe(reconcileId: string): Promise<void> {
    this.listeners.delete(reconcileId);
  }
}

const originalFetch = global.fetch;
global.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  if (input.toString().includes('api/v3/ticker')) {
    await new Promise(r => setTimeout(r, 200));
    return new Response(JSON.stringify({
      symbol: 'BTCUSDT',
      priceChange: '100.00',
      lastPrice: '60000.00'
    }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  }
  return originalFetch(input, init);
};

async function runTest() {
  const proxy = new EdgeProxy({
    pubsub: new MockPubSub(),
    upstreamUrl: 'https://api.mock-exchange.com'
  });

  const specReq = new Request('http://edge-worker.local/spec/api/v3/ticker/24hr');
  const specRes = await proxy.handleSpeculation(specReq, '/api/v3/ticker/24hr');
  
  if (specRes.status !== 200) throw new Error("Failed speculation");
  const reconcileId = specRes.headers.get('X-Antic-Reconcile-Id');
  if (!reconcileId) throw new Error("Missing ID");

  const clientAbort = new AbortController();
  const sseReq = new Request(`http://edge-worker.local/antic/signals?id=${reconcileId}`, {
    signal: clientAbort.signal
  });
  
  const sseRes = await proxy.handleSignalStream(sseReq);
  const reader = sseRes.body!.getReader();
  const decoder = new TextDecoder();

  let patchReceived = false;
  let replaceReceived = false;

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    
    const text = decoder.decode(value);
    if (text.includes('event: patch')) patchReceived = true;
    if (text.includes('event: replace')) {
      replaceReceived = true;
      break;
    }
  }

  clientAbort.abort();
  
  if (patchReceived && replaceReceived) {
    console.log("✅ Edge Proxy Test Passed! Streaming and Patches work perfectly.");
  } else {
    throw new Error("Did not receive expected events");
  }
}

runTest().catch(console.error);
