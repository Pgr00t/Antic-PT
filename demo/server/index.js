/**
 * Antic-PT v0.2.1 — Demo Upstream API Server
 *
 * Role: Authoritative data source. Runs on :4001.
 * The Go Spec-Link proxy runs on :4000 and forwards Formal Track requests here.
 *
 * Routes:
 *   GET  /api/:resource/:id   Standard REST with simulated DB latency (300-400ms)
 *   GET  /vault/:resource/:id State Vault inspector (reads live vault via proxy vault API)
 *   GET  /metrics             Demo metrics
 *
 * The /spec/* and /antic/signals routes are NOT handled here.
 * They are handled by the Go Spec-Link proxy on :4000.
 */

const express = require("express");
const cors = require("cors");
const path = require("path");

const app = express();
const PORT = 4001;

app.use(cors());
app.use(express.json());

// Serve the frontend client assets.
app.use(express.static(path.join(__dirname, "../client")));

// ── Simulated upstream database ──────────────────────────────────────────────
// Two versions of each resource simulate the "cache drift" scenario:
// the vault starts with v1 (slightly older values), the DB returns v2 (current).

const dbV2 = {
  "user/1": {
    id: 1,
    name: "Alice Chen",
    role: "Product Designer",
    team: "Growth",
    avatar: "AC",
    projects: 12,
    tasks_open: 3,           // Changed: was 4 (task completed since last cache)
    tasks_done: 92,          // Changed: was 91
    streak_days: 15,         // Changed: +1 day
    last_active: "just now",
    kpi_score: 97,           // Changed: was 95 (improved score)
  },
  "feed/1": {
    items: [
      { id: 0, author: "Marcus Roy",  action: "opened issue #501",          time: "just now", type: "code"   },
      { id: 1, author: "Bob Kim",     action: "merged PR #443",             time: "3m ago",   type: "code"   },
      { id: 2, author: "Sara Lee",    action: "commented on Design System", time: "8m ago",   type: "design" },
      { id: 3, author: "Dev Ops",     action: "deployed v2.4.1 to staging", time: "15m ago",  type: "deploy" },
      { id: 4, author: "Alice Chen",  action: "started sprint #12",         time: "1m ago",   type: "plan"   },
    ],
  },
  "dashboard/1": {
    revenue:       131200,   // Changed: was 128400
    revenue_delta: 14.1,     // Changed: was 12.4
    active_users:  4930,     // Changed: was 4821
    users_delta:   9.2,      // Changed: was 8.1
    conversion:    3.81,     // Changed: was 3.72
    conv_delta:    0.51,     // Changed: was 0.43
    latency_p99:   174,      // Changed: was 187 (improved)
    latency_delta: -47,      // Changed: was -34
  },
};

// The vault is pre-seeded (via /seed) with slightly stale v1 values so the demo
// shows a non-trivial reconciliation. Call GET /seed on startup to prime it.
const dbV1 = {
  "user/1": {
    id: 1,
    name: "Alice Chen",
    role: "Product Designer",
    team: "Growth",
    avatar: "AC",
    projects: 12,
    tasks_open: 4,
    tasks_done: 91,
    streak_days: 14,
    last_active: "2 min ago",
    kpi_score: 95,
  },
  "feed/1": {
    items: [
      { id: 0, author: "Marcus Roy",  action: "opened issue #501",          time: "2m ago",   type: "code"   },
      { id: 1, author: "Bob Kim",     action: "merged PR #443",             time: "5m ago",   type: "code"   },
      { id: 2, author: "Sara Lee",    action: "commented on Design System", time: "10m ago",  type: "design" },
      { id: 3, author: "Dev Ops",     action: "deployed v2.4.1 to staging", time: "17m ago",  type: "deploy" },
    ],
  },
  "dashboard/1": {
    revenue:       128400,
    revenue_delta: 12.4,
    active_users:  4821,
    users_delta:   8.1,
    conversion:    3.72,
    conv_delta:    0.43,
    latency_p99:   187,
    latency_delta: -34,
  },
};

// ── Standard REST API ─────────────────────────────────────────────────────────
app.get("/api/*", async (req, res) => {
  const fullPath = req.params[0]; // e.g., "user/1"
  const data = dbV2[fullPath];
  if (!data) return res.status(404).json({ error: "Not found: " + fullPath });

  const dbDelay = 300 + Math.floor(Math.random() * 100);
  await new Promise((r) => setTimeout(r, dbDelay));

  const responseData = { ...data };
  if (fullPath === "dashboard/1") {
    // Induce drift on ~25% of requests to make the demo feel natural.
    // Natural metrics don't jump on every single poll.
    if (Math.random() < 0.25) {
      const drift = (Math.random() > 0.5 ? 1 : -1) * (2000 + Math.floor(Math.random() * 6000));
      responseData.revenue += drift;
    }
  }

  res.json(responseData);
});

// ── Vault seed endpoint ───────────────────────────────────────────────────────
app.get("/seed/*", (req, res) => {
  const fullPath = req.params[0]; // e.g., "api/user/1" or "user/1"
  // If it starts with api/, strip it to match dbV1 keys
  const key = fullPath.startsWith("api/") ? fullPath.slice(4) : fullPath;
  const data = dbV1[key];
  if (!data) return res.status(404).json({ error: "Not found in seed: " + key });
  res.json({ ...data });
});

// ── Metrics ───────────────────────────────────────────────────────────────────
const metrics = {
  requests: 0,
  api_requests: 0,
};
app.use((req, res, next) => {
  metrics.requests++;
  if (req.path.startsWith("/api/")) metrics.api_requests++;
  next();
});
app.get("/metrics", (req, res) => res.json(metrics));

// ── Health check ──────────────────────────────────────────────────────────────
app.get("/health", (req, res) => res.json({ status: "ok", port: PORT }));

// ── Start ─────────────────────────────────────────────────────────────────────
app.listen(PORT, () => {
  console.log(`
╔══════════════════════════════════════════════════════════╗
║        ANTIC-PT Demo API Server v0.2.1                   ║
╠══════════════════════════════════════════════════════════╣
║  Upstream API:  http://localhost:${PORT}/api/*              ║
║  Seed endpoint: http://localhost:${PORT}/seed/*             ║
║  Health:        http://localhost:${PORT}/health             ║
║                                                          ║
║  Go Spec-Link proxy should run on :4000 pointing here.   ║
╚══════════════════════════════════════════════════════════╝
`);
});
