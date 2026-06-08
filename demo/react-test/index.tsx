import React from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StandardDashboard } from "./StandardDashboard";
import { AnticDashboard } from "./AnticDashboard";

const queryClient = new QueryClient();

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <div style={{ 
        display: "flex", 
        flexDirection: "column",
        minHeight: "100vh", 
        backgroundColor: "#121212", 
        color: "#fff", 
        fontFamily: "system-ui, -apple-system, sans-serif",
        padding: "40px"
      }}>
        <div style={{ textAlign: "center", marginBottom: "40px" }}>
          <h1 style={{ margin: 0, fontSize: "2.5em" }}>Antic-PT Protocol Demo</h1>
          <p style={{ color: "#aaa", fontSize: "1.2em" }}>The end of loading spinners.</p>
        </div>

        <div style={{ 
          display: "flex", 
          gap: "40px", 
          maxWidth: "1200px", 
          margin: "0 auto", 
          width: "100%" 
        }}>
          <StandardDashboard />
          <AnticDashboard />
        </div>
      </div>
    </QueryClientProvider>
  );
}

const container = document.getElementById("root");
const root = createRoot(container!);
root.render(<App />);
