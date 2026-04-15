/**
 * SPEC-LINK MIDDLEWARE — Demo Implementation
 *
 * Intercepts requests to /spec/* and forks into:
 *   - Fast Track: reads from State-Vault, streams speculative event
 *   - Formal Track: queries the "real" DB (simulated with delay), streams signal
 */

const { vaultGet, vaultSet } = require("./state-vault");

// Simulated DB — in production this is your actual database
const simulatedDB = {
  "user:1": {
    id: 1,
    name: "Alice Chen",
    role: "Product Designer",
    team: "Growth",
    avatar: "AC",
    projects: 12,
    tasks_open: 4,
    tasks_done: 91, // Slightly different from cache (2 new tasks done)
    streak_days: 14,
    last_active: "just now",
    kpi_score: 95,   // Updated score
  },
  "feed:1": {
    items: [
      { id: 0, author: "Marcus Roy", action: "opened issue #501", time: "just now", type: "code" },
      { id: 1, author: "Bob Kim", action: "merged PR #443", time: "3m ago", type: "code" },
      { id: 2, author: "Sara Lee", action: "commented on Design System", time: "8m ago", type: "design" },
      { id: 3, author: "Dev Ops", action: "deployed v2.4.1 to staging", time: "15m ago", type: "deploy" },
    ],
  },
  "dashboard:1": {
    revenue: 128400,
    revenue_delta: 12.4,
    active_users: 4821,
    users_delta: 8.1,
    conversion: 3.72,
    conv_delta: 0.43,
    latency_p99: 187,
    latency_delta: -34,
  },
};

// Simulated DB query latency (ms) — mimics real network + DB time
const DB_LATENCY_MS = 320;

/**
 * Simulate a database query with realistic latency
 */
async function queryDB(resource, id) {
  await new Promise((resolve) =>
    setTimeout(resolve, DB_LATENCY_MS + Math.random() * 80)
  );
  const key = `${resource}:${id}`;
  return simulatedDB[key] || null;
}

/**
 * Spec-Link SSE handler
 * Route: GET /spec/:resource/:id
 */
async function specLinkHandler(req, res) {
  const { resource, id } = req.params;
  const sessionId = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  const clientVersion = parseInt(req.headers["x-antic-version"] || "0");

  // --- Setup SSE ---
  res.setHeader("Content-Type", "text/event-stream");
  res.setHeader("Cache-Control", "no-cache");
  res.setHeader("Connection", "keep-alive");
  res.setHeader("X-Antic-Session-ID", sessionId);
  res.setHeader("Access-Control-Allow-Origin", "*");
  res.flushHeaders();

  const sendEvent = (event, id, data) => {
    res.write(`event: ${event}\n`);
    res.write(`id: ${id}\n`);
    res.write(`data: ${JSON.stringify(data)}\n\n`);
  };

  const fastTrackStart = Date.now();

  // ──────────────────────────────────────────────────────
  // FAST TRACK — Read from State-Vault
  // ──────────────────────────────────────────────────────
  const vaultEntry = vaultGet(resource, id);

  if (vaultEntry && vaultEntry.version >= clientVersion) {
    // Cache HIT — stream speculative response immediately
    const fastTrackLatency = Date.now() - fastTrackStart;

    sendEvent("speculative", vaultEntry.version, {
      ...vaultEntry.data,
      _antic: {
        version: vaultEntry.version,
        source: "vault",
        age_ms: vaultEntry.age_ms,
        fast_track_latency_ms: fastTrackLatency,
        session_id: sessionId,
      },
    });
  } else if (vaultEntry && vaultEntry.version < clientVersion) {
    // Version conflict — client has newer data than vault
    sendEvent("meta", 0, {
      type: "version-conflict",
      message: "Client version is ahead of vault. Awaiting formal track.",
    });
  } else {
    // Cache MISS — inform client, fall through to formal track
    sendEvent("meta", 0, {
      type: "cache-miss",
      message: "No cached entry found. Awaiting formal track.",
    });
  }

  // ──────────────────────────────────────────────────────
  // FORMAL TRACK — Query DB simultaneously
  // ──────────────────────────────────────────────────────
  try {
    const formalStart = Date.now();
    const dbData = await queryDB(resource, id);
    const formalLatency = Date.now() - formalStart;

    if (!dbData) {
      sendEvent("abort", 0, {
        reason: "not_found",
        code: 404,
        revert: true,
      });
      res.end();
      return;
    }

    const newVersion = (vaultEntry?.version || 0) + 1;

    // Write fresh data back to State-Vault
    vaultSet(resource, id, dbData, newVersion);

    if (!vaultEntry) {
      // Was a cache miss — send full replace
      sendEvent("replace", newVersion, {
        ...dbData,
        _antic: {
          version: newVersion,
          source: "live",
          formal_track_latency_ms: formalLatency,
          session_id: sessionId,
        },
      });
    } else {
      // Compare vault data vs DB data to determine signal type
      const specStr = JSON.stringify(vaultEntry.data);
      const dbStr = JSON.stringify(dbData);

      if (specStr === dbStr) {
        // Perfect cache hit — send CONFIRM
        sendEvent("confirm", newVersion, {
          status: "ok",
          version: newVersion,
          formal_track_latency_ms: formalLatency,
          total_latency_ms: Date.now() - fastTrackStart,
          session_id: sessionId,
        });
      } else {
        // Data changed — compute patch
        const patches = computePatches(vaultEntry.data, dbData);

        if (patches.length <= 5) {
          // Small difference — send PATCH
          sendEvent("patch", newVersion, {
            ops: patches,
            formal_track_latency_ms: formalLatency,
            session_id: sessionId,
          });
        } else {
          // Large difference — send REPLACE
          sendEvent("replace", newVersion, {
            ...dbData,
            _antic: {
              version: newVersion,
              source: "live",
              formal_track_latency_ms: formalLatency,
              session_id: sessionId,
            },
          });
        }
      }
    }
  } catch (err) {
    sendEvent("abort", 0, {
      reason: "db_error",
      code: 503,
      revert: true,
      message: err.message,
    });
  }

  res.end();
}

/**
 * Simple field-level patch computation (flat objects only for demo)
 * Production: use fast-json-patch or similar
 */
function computePatches(oldObj, newObj) {
  const ops = [];
  const allKeys = new Set([...Object.keys(oldObj), ...Object.keys(newObj)]);
  for (const key of allKeys) {
    if (JSON.stringify(oldObj[key]) !== JSON.stringify(newObj[key])) {
      ops.push({ op: "replace", path: `/${key}`, value: newObj[key] });
    }
  }
  return ops;
}

module.exports = { specLinkHandler };
