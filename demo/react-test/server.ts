import * as http from 'http';
import { webcrypto } from 'node:crypto';
if (!global.crypto) global.crypto = webcrypto as any;
import { EdgeProxy, PubSubAdapter, InMemoryStateVaultAdapter } from '@antic-pt/edge-proxy';

class MemoryPubSub implements PubSubAdapter {
  private listeners: Map<string, Array<(event: string, data: any) => void>> = new Map();

  async publish(reconcileId: string, event: 'patch' | 'fill' | 'replace' | 'confirm' | 'abort', data: any): Promise<void> {
    const handlers = this.listeners.get(reconcileId) || [];
    handlers.forEach(h => h(event, data));
  }

  async subscribe(reconcileId: string, onMessage: (event: 'patch' | 'fill' | 'replace' | 'confirm' | 'abort', data: any) => void): Promise<void> {
    const handlers = this.listeners.get(reconcileId) || [];
    handlers.push(onMessage);
    this.listeners.set(reconcileId, handlers);
  }

  async unsubscribe(reconcileId: string): Promise<void> {
    this.listeners.delete(reconcileId);
  }
}

const proxy = new EdgeProxy({
  pubsub: new MemoryPubSub(),
  vault: new InMemoryStateVaultAdapter(),
  upstreamUrl: 'http://127.0.0.1:4003' // Mock upstream
});

// Mock Upstream Server
const upstreamServer = http.createServer((req, res) => {
  res.setHeader('Access-Control-Allow-Origin', '*');
  res.setHeader('Access-Control-Allow-Headers', '*');
  res.setHeader('Content-Type', 'application/json');
  console.log(`[Upstream] ${req.method} ${req.url}`);

  if (req.method === 'OPTIONS') {
    res.writeHead(204);
    res.end();
    return;
  }

  if (req.method === 'GET' && req.url?.includes('api/v3/ticker')) {
    setTimeout(() => {
      res.writeHead(200);
      res.end(JSON.stringify({ symbol: 'BTCUSDT', lastPrice: '60000.00', priceChange: '100.00' }));
    }, 1000);
  } else if (req.method === 'POST' && req.url?.includes('api/v3/order')) {
    setTimeout(() => {
      res.writeHead(200);
      res.end(JSON.stringify({ orderId: 12345, status: 'FILLED' }));
    }, 1000);
  } else {
    res.writeHead(404);
    res.end();
  }
});
upstreamServer.listen(4003, '127.0.0.1');

// Web Request adapter
async function handleNodeRequest(req: http.IncomingMessage, res: http.ServerResponse) {
  res.setHeader('Access-Control-Allow-Origin', '*');
  res.setHeader('Access-Control-Allow-Headers', '*');
  if (req.method === 'OPTIONS') {
    res.writeHead(204);
    res.end();
    return;
  }

  const chunks: Buffer[] = [];
  for await (const chunk of req) {
    chunks.push(chunk);
  }
  const body = Buffer.concat(chunks);
  
  const headers = new Headers();
  for (const [k, v] of Object.entries(req.headers)) {
    if (v) headers.set(k, Array.isArray(v) ? v.join(',') : v);
  }

  const webReq = new Request(`http://127.0.0.1:4002${req.url}`, {
    method: req.method,
    headers,
    body: req.method !== 'GET' && req.method !== 'HEAD' ? body : undefined
  });

  let webRes: Response;
  try {
    if (req.url?.startsWith('/spec/')) {
      if (req.method === 'GET') {
        webRes = await proxy.handleSpeculation(webReq, req.url.replace('/spec', ''));
      } else {
        webRes = await proxy.handleProvisionalWrite(webReq, req.url.replace('/spec', ''));
      }
    } else if (req.url?.startsWith('/antic/signals')) {
      webRes = await proxy.handleSignalStream(webReq);
    } else {
      res.writeHead(404);
      res.end('Not found');
      return;
    }

    res.writeHead(webRes.status, Object.fromEntries(webRes.headers.entries()));
    res.flushHeaders(); // Explicitly flush headers so EventSource connects instantly
    if (webRes.body) {
      const reader = webRes.body.getReader();
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        res.write(value);
      }
      res.end();
    } else {
      res.end();
    }
  } catch (e) {
    console.error(e);
    res.writeHead(500);
    res.end('Server error');
  }
}

const proxyServer = http.createServer((req, res) => handleNodeRequest(req, res));
proxyServer.listen(4002, '127.0.0.1', () => console.log('Proxy on port 4002, Upstream on port 4003'));
