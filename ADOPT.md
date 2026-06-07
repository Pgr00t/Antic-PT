# Adopting Antic-PT: The 60-Minute Integration Guide

This guide is for the first engineer integrating Antic-PT into a real-world project. The goal is to get **Edge Proxy** running in front of your existing REST API and verify the dual-track lifecycle without changing any of your backend code.

---

## 0. The "Try It First" Working Example
Before pointing Edge Proxy at your own infrastructure, we recommend running the local React demo to see the lifecycle in action. 

```bash
cd demo/react-test
npm install
npm run dev
```
Open the localhost URL shown in your terminal to see real-time surgical reconciliations.

---

## 1. Get the Edge Proxy

Edge Proxy is a Node-based middleware proxy. It sits between your clients and your upstream API.

```bash
# In your project, install the proxy:
npm install @antic-pt/edge-proxy
```

---

## 2. The Minimum Viable Config

Create a file `proxy.ts` in your project. This tells Edge Proxy where your API is and how to connect to the SSE pub/sub system (e.g. Upstash Redis).

```typescript
import { EdgeProxy, RedisPubSub } from '@antic-pt/edge-proxy';

// 1. Initialize PubSub (using Upstash Redis for serverless support)
const pubsub = new RedisPubSub({
  url: process.env.UPSTASH_REDIS_REST_URL,
  token: process.env.UPSTASH_REDIS_REST_TOKEN,
});

// 2. Initialize Proxy
const proxy = new EdgeProxy({
  pubsub,
  upstreamUrl: 'https://your-real-api.com' // Your real backend
});

// 3. Mount in your server (e.g. Express)
import express from 'express';
const app = express();

// Handle speculative reads
app.get('/spec/*', async (req, res) => {
  const proxyReq = new Request(`http://localhost${req.url}`, {
    method: req.method,
    headers: req.headers as HeadersInit
  });
  
  const targetPath = req.path.replace('/spec', '');
  const response = await proxy.handleSpeculation(proxyReq, targetPath);
  
  response.headers.forEach((val, key) => res.setHeader(key, val));
  res.status(response.status).send(await response.text());
});

// Handle Signal Stream (SSE)
app.get('/antic/signals', async (req, res) => {
  const proxyReq = new Request(`http://localhost${req.url}`, {
    headers: req.headers as HeadersInit
  });
  
  const response = await proxy.handleSignalStream(proxyReq);
  response.headers.forEach((val, key) => res.setHeader(key, val));
  res.status(200);
  
  const reader = response.body!.getReader();
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    res.write(value);
  }
  res.end();
});

app.listen(4000, () => console.log('Edge Proxy running on :4000'));
```

---

## 3. The Client-Side Swap

You don't need to rewrite your frontend. You just need to wrap your fetches using one of our integration hooks: `@antic-pt/react-query` or `@antic-pt/swr`.

### React Query Example

```bash
npm install @antic-pt/react-query @tanstack/react-query
```

```javascript
import { useAnticQuery } from "@antic-pt/react-query";

function Dashboard() {
  const { data, status, meta } = useAnticQuery({
    queryKey: ['dashboard'],
    // Point to your Edge Proxy's /spec endpoint
    queryFn: () => fetch('http://localhost:4000/spec/v1/dashboard').then(r => r.json()),
    
    // Configure Anticipation Protocol behavior
    anticipation: {
      baseUrl: 'http://localhost:4000',
      maxWindow: 3000
    }
  });

  if (status === 'pending') return <div>Loading...</div>;

  return (
    <div>
      {/* status will be 'speculative', 'patching', or 'confirmed' */}
      <div>Status: {status}</div>
      <pre>{JSON.stringify(data, null, 2)}</pre>
    </div>
  );
}
```

---

## 4. Verification: How to know it's working

Open Chrome DevTools -> Network Tab.

1.  **The Fetch**: Trigger your fetch. You should see a request to `localhost:4000/spec/v1/dashboard`. Check the response headers:
    *   `X-Antic-State: speculative` (Success! You just saved ~300ms).
    *   `X-Antic-Reconcile-Id: arc_...` (The link to the formal track).
2.  **The Signal Channel**: Look for a persistent SSE connection to `localhost:4000/antic/signals`.
    *   Click it and view the `EventStream` tab.
    *   If you see a `CONFIRM` or `PATCH` message with matching `Id`, the background reconciliation is working.
3.  **The UI**: You should see your UI instantly render, and a few hundred milliseconds later, the data patches into place without a full screen flash.

---

## 5. Current Limitations (v0.2.2)

Before you deploy this past your local machine, you must understand what Edge Proxy *cannot* do right now.

*   **Caching Strategy**: Currently relies on the developer providing their own caching mechanism or extending the proxy. The Redis PubSub implementation relies entirely on pub/sub events.
*   **Cold Hits**: The very first request to any path/query combination is a mandatory "Cold Hit". The fast track only kicks in on the second request.

---

## 6. The Validation Milestone

If you get this running against a real endpoint you own, **we want to hear from you.** 
- Did the JSON Patch cause a weird UI flicker?
- Did the performance gain feel real to your users?

Open an issue or reach out directly. Your feedback is more important than our next feature.
