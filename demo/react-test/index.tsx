import React from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useAnticQuery } from "@antic-pt/react-query";

const queryClient = new QueryClient();

function Dashboard() {
  const { data, anticStatus, anticMeta, deferredFields } = useAnticQuery(
    ["dashboard", 1],
    "http://localhost:4002/spec/api/v3/ticker/24hr?symbol=BTCUSDT",
  );

  return (
    <div style={{ padding: 20, fontFamily: "sans-serif" }}>
      <h1>Dashboard Demo</h1>
      <p>
        Status: <strong>{anticStatus}</strong>
      </p>
      {anticMeta && <p>Reconcile ID: {anticMeta.reconcileId}</p>}
      {deferredFields.length > 0 && (
        <p>Deferred Fields: {deferredFields.join(", ")}</p>
      )}

      <h3>Data:</h3>
      <pre style={{ background: "#f4f4f4", padding: 10 }}>
        {JSON.stringify(data, null, 2)}
      </pre>
    </div>
  );
}

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Dashboard />
    </QueryClientProvider>
  );
}

const container = document.getElementById("root");
const root = createRoot(container!);
root.render(<App />);
