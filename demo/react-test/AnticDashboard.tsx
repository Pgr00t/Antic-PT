import React, { useState } from "react";
import { useAnticQuery, useAnticMutation } from "@antic-pt/react-query";

export function AnticDashboard() {
  const [item, setItem] = useState("Modern Keyboard");
  const [quantity, setQuantity] = useState("2");
  const [address, setAddress] = useState("123 Developer Lane");

  // Fetch from Edge Proxy (Port 4002) with Anticipation Protocol
  const { data, isLoading, anticStatus } = useAnticQuery(
    ["dashboard", "antic"],
    "http://localhost:4002/spec/api/v3/ticker/24hr?symbol=BTCUSDT"
  );

  const mutation = useAnticMutation(
    "http://localhost:4002/spec/api/v3/order",
    "POST",
    {
      queryKeyToInvalidate: ["dashboard", "antic"]
    }
  );

  const isSpeculative = mutation.anticStatus === "speculative" || mutation.anticStatus === "patching" || mutation.anticStatus === "filling";
  const isConfirmed = mutation.anticStatus === "confirmed";

  return (
    <div style={{ flex: 1, padding: "40px", backgroundColor: "#1e1e24", color: "#e0e0e0", borderRadius: "12px", border: "1px solid #333" }}>
      <h2 style={{ textAlign: "center", marginBottom: "30px", color: "#fff" }}>
        Antic-PT React Query<br/>
        <span style={{ color: "#4caf50", fontSize: "0.8em", fontWeight: "normal" }}>(Instant)</span>
      </h2>

      <div style={{ backgroundColor: "#282936", padding: "30px", borderRadius: "8px", border: "1px solid #444", position: "relative" }}>
        <h3 style={{ marginTop: 0, marginBottom: "20px", color: "#fff" }}>Place Order</h3>
        
        <div style={{ marginBottom: "15px" }}>
          <label style={{ display: "block", marginBottom: "5px", fontSize: "0.9em", color: "#aaa" }}>Item</label>
          <input value={item} onChange={e => setItem(e.target.value)} style={inputStyle} />
        </div>
        
        <div style={{ marginBottom: "15px" }}>
          <label style={{ display: "block", marginBottom: "5px", fontSize: "0.9em", color: "#aaa" }}>Quantity</label>
          <input value={quantity} onChange={e => setQuantity(e.target.value)} style={inputStyle} />
        </div>
        
        <div style={{ marginBottom: "25px" }}>
          <label style={{ display: "block", marginBottom: "5px", fontSize: "0.9em", color: "#aaa" }}>Shipping Address</label>
          <input value={address} onChange={e => setAddress(e.target.value)} style={inputStyle} />
        </div>

        <button 
          onClick={() => mutation.mutate({ item, quantity, address })}
          disabled={isSpeculative || mutation.isPending}
          style={{
            ...buttonStyle,
            backgroundColor: isSpeculative ? "#4caf50" : "#444",
            cursor: isSpeculative ? "not-allowed" : "pointer"
          }}
        >
          {isSpeculative ? "Order Confirmed! (Instant)" : "Place Order"}
        </button>

        <div style={{ marginTop: "20px", minHeight: "40px", textAlign: "center" }}>
          {isSpeculative && (
            <div style={{ color: "#4caf50", fontWeight: "bold" }}>
              ⚡ Instant 202 Accepted!
              <div style={{ fontSize: "0.8em", fontWeight: "normal", color: "#aaa", marginTop: "5px" }}>
                (Background Check in Progress...)
              </div>
            </div>
          )}
          {isConfirmed && !isSpeculative && (
            <div style={{ color: "#4caf50", padding: "10px", backgroundColor: "rgba(76, 175, 80, 0.1)", borderRadius: "4px" }}>
              ✅ Confirmed by upstream server!
            </div>
          )}
          {mutation.error && (
            <div style={{ color: "#ff6b6b", padding: "10px", backgroundColor: "rgba(255, 0, 0, 0.1)", borderRadius: "4px" }}>
              ❌ Error: {mutation.error.message}
            </div>
          )}
        </div>
      </div>
      
      <div style={{ marginTop: "30px", fontSize: "0.85em", color: "#888" }}>
        <h4>Live Proxy Data (Status: {anticStatus}):</h4>
        <pre style={{ backgroundColor: "#111", padding: "10px", borderRadius: "4px", overflow: "auto" }}>
          {isLoading ? "Loading..." : JSON.stringify(data, null, 2)}
        </pre>
      </div>
    </div>
  );
}

const inputStyle = {
  width: "100%",
  padding: "10px",
  backgroundColor: "#1e1e24",
  border: "1px solid #444",
  borderRadius: "6px",
  color: "#fff",
  boxSizing: "border-box" as const
};

const buttonStyle = {
  width: "100%",
  padding: "12px",
  border: "none",
  borderRadius: "6px",
  color: "#fff",
  fontWeight: "bold",
  fontSize: "1em",
  transition: "background-color 0.2s"
};
