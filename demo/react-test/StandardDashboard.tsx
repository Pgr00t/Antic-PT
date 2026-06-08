import React, { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";

export function StandardDashboard() {
  const queryClient = useQueryClient();
  const [item, setItem] = useState("Modern Keyboard");
  const [quantity, setQuantity] = useState("2");
  const [address, setAddress] = useState("123 Developer Lane");

  // Direct fetch from Upstream (Port 4003)
  const { data, isLoading } = useQuery({
    queryKey: ["dashboard", "standard"],
    queryFn: async () => {
      const res = await fetch("http://localhost:4003/api/v3/ticker/24hr?symbol=BTCUSDT");
      return res.json();
    }
  });

  const mutation = useMutation({
    mutationFn: async (payload: any) => {
      const res = await fetch("http://localhost:4003/api/v3/order", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      });
      if (!res.ok) throw new Error("Network request failed");
      return res.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dashboard", "standard"] });
    }
  });

  return (
    <div style={{ flex: 1, padding: "40px", backgroundColor: "#1e1e24", color: "#e0e0e0", borderRadius: "12px", border: "1px solid #333" }}>
      <h2 style={{ textAlign: "center", marginBottom: "30px", color: "#fff" }}>
        Standard React Query<br/>
        <span style={{ color: "#888", fontSize: "0.8em", fontWeight: "normal" }}>(Slow)</span>
      </h2>

      <div style={{ backgroundColor: "#282936", padding: "30px", borderRadius: "8px", border: "1px solid #444" }}>
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
          disabled={mutation.isPending}
          style={{
            ...buttonStyle,
            backgroundColor: mutation.isPending ? "#555" : mutation.isError ? "#d32f2f" : "#444",
            cursor: mutation.isPending ? "not-allowed" : "pointer"
          }}
        >
          {mutation.isPending ? "Placing Order..." : mutation.isError ? "Retry Order" : "Place Order"}
        </button>

        <div style={{ marginTop: "20px", minHeight: "40px", textAlign: "center" }}>
          {mutation.isPending && (
            <div style={{ color: "#aaa" }}>
               ⏳ Waiting for server response...
            </div>
          )}
          {mutation.isError && (
            <div style={{ color: "#ff6b6b", padding: "10px", backgroundColor: "rgba(255, 0, 0, 0.1)", borderRadius: "4px" }}>
              ❌ Error: {mutation.error?.message}
            </div>
          )}
          {mutation.isSuccess && (
            <div style={{ color: "#4caf50" }}>
              ✅ Order Confirmed!
            </div>
          )}
        </div>
      </div>
      
      <div style={{ marginTop: "30px", fontSize: "0.85em", color: "#888" }}>
        <h4>Live Upstream Data:</h4>
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
