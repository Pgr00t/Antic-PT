/**
 * STATE-VAULT — In-Memory Implementation for Demo
 * Production: replace with Redis or DragonflyDB
 */

const store = new Map();

// Seed with demo data
const seedData = [
  {
    key: "user:1",
    version: 47,
    data: {
      id: 1,
      name: "Alice Chen",
      role: "Product Designer",
      team: "Growth",
      avatar: "AC",
      projects: 12,
      tasks_open: 4,
      tasks_done: 89,
      streak_days: 14,
      last_active: "2 min ago",
      kpi_score: 94,
    },
  },
  {
    key: "feed:1",
    version: 112,
    data: {
      items: [
        { id: 1, author: "Bob Kim", action: "merged PR #443", time: "3m ago", type: "code" },
        { id: 2, author: "Sara Lee", action: "commented on Design System", time: "8m ago", type: "design" },
        { id: 3, author: "Dev Ops", action: "deployed v2.4.1 to staging", time: "15m ago", type: "deploy" },
        { id: 4, author: "Alice Chen", action: "created milestone Q2 Sprint", time: "1h ago", type: "plan" },
      ],
    },
  },
  {
    key: "dashboard:1",
    version: 88,
    data: {
      revenue: 128400,
      revenue_delta: 12.4,
      active_users: 4821,
      users_delta: 8.1,
      conversion: 3.72,
      conv_delta: 0.43,
      latency_p99: 187,
      latency_delta: -34,
    },
  },
];

seedData.forEach(({ key, version, data }) => {
  store.set(key, { data, version, updated_at: Date.now() });
});

/**
 * Get a resource from the vault.
 * Returns { data, version, age_ms } or null if not found.
 */
function vaultGet(resource, id) {
  const key = `${resource}:${id}`;
  const entry = store.get(key);
  if (!entry) return null;
  return {
    data: entry.data,
    version: entry.version,
    age_ms: Date.now() - entry.updated_at,
  };
}

/**
 * Set a resource in the vault.
 */
function vaultSet(resource, id, data, version) {
  const key = `${resource}:${id}`;
  store.set(key, { data, version, updated_at: Date.now() });
}

module.exports = { vaultGet, vaultSet };
