/**
 * ANTIC-PT DEMO SERVER
 * Exposes both Standard REST and Antic-PT endpoints side-by-side
 * for live latency comparison.
 */

const express = require("express");
const cors = require("cors");
const path = require("path");
const { specLinkHandler } = require("./spec-link");
const { vaultGet } = require("./state-vault");

const app = express();
const PORT = 4000;

app.use(cors());
app.use(express.json());

// Serve the frontend client
app.use(express.static(path.join(__dirname, "../client")));

// ─────────────────────────────────────────────────────────
// STANDARD REST API — Sequential, blocking, realistic delay
// ─────────────────────────────────────────────────────────
const standardDB = {
  "user/1": { id: 1, name: "Alice Chen", role: "Product Designer", team: "Growth", avatar: "AC", projects: 12, tasks_open: 4, tasks_done: 91, streak_days: 14, last_active: "just now", kpi_score: 95 },
  "feed/1": { items: [
    { id: 0, author: "Marcus Roy", action: "opened issue #501", time: "just now", type: "code" },
    { id: 1, author: "Bob Kim", action: "merged PR #443", time: "3m ago", type: "code" },
    { id: 2, author: "Sara Lee", action: "commented on Design System", time: "8m ago", type: "design" },
    { id: 3, author: "Dev Ops", action: "deployed v2.4.1 to staging", time: "15m ago", type: "deploy" },
  ]},
  "dashboard/1": { revenue: 128400, revenue_delta: 12.4, active_users: 4821, users_delta: 8.1, conversion: 3.72, conv_delta: 0.43, latency_p99: 187, latency_delta: -34 },
};

app.get("/api/:resource/:id", async (req, res) => {
  const { resource, id } = req.params;
  const key = `${resource}/${id}`;

  // Simulate realistic DB latency
  const dbDelay = 300 + Math.random() * 100;
  await new Promise((r) => setTimeout(r, dbDelay));

  const data = standardDB[key];
  if (!data) return res.status(404).json({ error: "Not found" });

  res.json({
    ...data,
    _meta: { latency_ms: Math.round(dbDelay), source: "database" },
  });
});

// ─────────────────────────────────────────────────────────
// ANTIC-PT — Dual-stream SSE endpoint
// ─────────────────────────────────────────────────────────
app.get("/spec/:resource/:id", specLinkHandler);

// ─────────────────────────────────────────────────────────
// VAULT INSPECTOR — Dev tool to inspect State-Vault state
// ─────────────────────────────────────────────────────────
app.get("/vault/:resource/:id", (req, res) => {
  const { resource, id } = req.params;
  const entry = vaultGet(resource, id);
  if (!entry) return res.status(404).json({ error: "Not in vault" });
  res.json(entry);
});

// ─────────────────────────────────────────────────────────
// METRICS — Live stats
// ─────────────────────────────────────────────────────────
const metrics = { standard_requests: 0, antic_requests: 0, cache_hits: 0, confirms: 0, patches: 0, replaces: 0 };

app.get("/metrics", (req, res) => res.json(metrics));

app.listen(PORT, () => {
  console.log(`
╔══════════════════════════════════════════════════════╗
║          ANTIC-PT DEMO SERVER v0.2                   ║
╠══════════════════════════════════════════════════════╣
║  Server:        http://localhost:${PORT}                ║
║  Standard API:  http://localhost:${PORT}/api/*          ║
║  Antic-PT:      http://localhost:${PORT}/spec/*         ║
║  Vault:         http://localhost:${PORT}/vault/*        ║
║  Client:        http://localhost:${PORT}                ║
╚══════════════════════════════════════════════════════╝
  `);
});
