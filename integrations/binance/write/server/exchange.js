/**
 * Simulated exchange order server — Antic-PT write-side demo
 *
 * Models a real order matching engine with three deterministic outcomes:
 *   - FILLED: order executed immediately (happy path)
 *   - REJECTED: order failed business-rule validation (e.g. insufficient funds)
 *   - TIMEOUT: upstream takes too long to respond (unknown-outcome scenario)
 *
 * The server is intentionally simple — it holds order state in memory and
 * exposes it via REST. The Spec-Link write handler sits in front of this.
 */

const http = require("http");

const PORT = 4005;

// Simulated account state
let account = {
  balance: 1000.0,
  availableBalance: 1000.0,
  currency: "USDT",
};

// Order book — keyed by orderId
const orders = {};

let orderSeq = 1000;

function generateOrderId() {
  return `ord_${(++orderSeq).toString(36)}`;
}

// Simulate real BTC price drift around a base
let btcPrice = 74800;
function currentBtcPrice() {
  // Small random walk ±$20 each tick
  btcPrice += (Math.random() - 0.5) * 40;
  return Math.round(btcPrice * 100) / 100;
}

function handleCors(res) {
  res.setHeader("Access-Control-Allow-Origin", "*");
  res.setHeader("Access-Control-Allow-Methods", "GET, POST, OPTIONS");
  res.setHeader(
    "Access-Control-Allow-Headers",
    "Content-Type, X-Antic-Client-Id, X-Antic-Write-Mode, Idempotency-Key",
  );
}

function json(res, statusCode, body) {
  handleCors(res);
  res.writeHead(statusCode, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

const server = http.createServer((req, res) => {
  handleCors(res);

  if (req.method === "OPTIONS") {
    res.writeHead(204);
    res.end();
    return;
  }

  const url = new URL(req.url, `http://localhost:${PORT}`);

  // GET /account — current account balance
  if (req.method === "GET" && url.pathname === "/account") {
    return json(res, 200, account);
  }

  // GET /orders — all orders
  if (req.method === "GET" && url.pathname === "/orders") {
    return json(res, 200, Object.values(orders));
  }

  // POST /orders — place a new order
  if (req.method === "POST" && url.pathname === "/orders") {
    let body = "";
    req.on("data", (chunk) => {
      body += chunk;
    });
    req.on("end", () => {
      let payload;
      try {
        payload = JSON.parse(body);
      } catch {
        return json(res, 400, { error: "invalid_json" });
      }

      const { symbol, side, quantity, price, scenario } = payload;

      // Validate required fields
      if (!symbol || !side || !quantity || !price) {
        return json(res, 400, {
          error: "invalid_parameters",
          message: "symbol, side, quantity, price are required",
        });
      }

      const qty = parseFloat(quantity);
      const px = parseFloat(price);
      const orderValue = qty * px;

      // Scenario: TIMEOUT — server takes 12s to respond (past any reasonable max window)
      if (scenario === "timeout") {
        console.log("[exchange] Simulating upstream timeout...");
        setTimeout(() => {
          // Eventually responds but client has already given up
          const orderId = generateOrderId();
          orders[orderId] = {
            orderId,
            symbol,
            side,
            quantity,
            price,
            status: "FILLED",
            filledAt: Date.now(),
          };
          json(res, 200, orders[orderId]);
        }, 12000);
        return;
      }

      // Scenario: REJECT — insufficient funds
      if (scenario === "reject" || orderValue > account.availableBalance) {
        console.log(
          `[exchange] REJECTED — order value ${orderValue.toFixed(2)} > available ${account.availableBalance.toFixed(2)}`,
        );
        return json(res, 422, {
          error: "insufficient_funds",
          message: `Order value ${orderValue.toFixed(2)} USDT exceeds available balance ${account.availableBalance.toFixed(2)} USDT`,
          account: {
            balance: account.balance.toFixed(2),
            availableBalance: account.availableBalance.toFixed(2),
          },
        });
      }

      // Happy path — order fills at current market price (may differ from requested price)
      const filledPrice = currentBtcPrice();
      const orderId = generateOrderId();
      const filledAt = Date.now();

      // Deduct from available balance
      account.availableBalance =
        Math.round((account.availableBalance - orderValue) * 100) / 100;

      const order = {
        orderId,
        symbol,
        side,
        quantity: qty.toString(),
        requestedPrice: px.toFixed(2),
        price: filledPrice.toFixed(2), // filled at actual market price
        status: "FILLED",
        filledAt,
        fee: (orderValue * 0.001).toFixed(4),
        feeCurrency: "USDT",
      };
      orders[orderId] = order;

      console.log(
        `[exchange] FILLED — ${side} ${quantity} ${symbol} @ ${filledPrice.toFixed(2)} (requested: ${price})`,
      );

      // Small processing delay (realistic exchange latency)
      setTimeout(
        () => {
          json(res, 200, order);
        },
        280 + Math.random() * 120,
      );
    });
    return;
  }

  json(res, 404, { error: "not_found" });
});

server.listen(PORT, () => {
  console.log(
    `\n[exchange] Simulated order matching engine running on http://localhost:${PORT}`,
  );
  console.log("[exchange] Endpoints:");
  console.log("  GET  /account  — account balance");
  console.log("  GET  /orders   — all orders");
  console.log("  POST /orders   — place order (JSON body)");
  console.log(
    '                   scenario: "reject" | "timeout" | omit for fill\n',
  );
});
