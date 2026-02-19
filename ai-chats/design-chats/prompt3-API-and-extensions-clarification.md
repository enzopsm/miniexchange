!> Consider the task described by the documents below.

I intend to implement all the extensions

# Request

Please clarify to me what's the expected behavior of the API and frontend part of the project, based on the statement and on best practices, maximizing API design quality

## Task Statement

/Users/enzopsm/Documents/personal/lifestask/Decade/challenge_statement/frontier-engineering-challenge.md


> 

Let me read the challenge statement first.
Reading file: /Users/enzopsm/Documents/personal/lifestask/Decade/challenge_statement/frontier-engineering-challenge.md, all lines (using tool: read)
 ✓ Successfully read 3188 bytes from /Users/enzopsm/Documents/personal/lifestask/Decade/challenge_statement/frontier-engineering-challenge.md
 - Completed in 0.0s

> Now let me check if there's any existing code in the project already.
Reading directory: /Users/enzopsm/Documents/personal/lifestask/Decade (using tool: read, max depth: 2, max entries: 1000, excluding: defaults)
 ✓ Successfully read directory /Users/enzopsm/Documents/personal/lifestask/Decade (13 entries)
 - Completed in 0.0s

> Let me also read your notes for additional context.
Batch fs_read operation with 2 operations (using tool: read)

↱ Operation 1: Reading file: /Users/enzopsm/Documents/personal/lifestask/Decade/personal-notes/statement-breakdown.md, all lines
↱ Operation 2: Reading file: /Users/enzopsm/Documents/personal/lifestask/Decade/personal-notes/progress-notepad.md, all lines
 ✓ Successfully read 4878 bytes from /Users/enzopsm/Documents/personal/lifestask/Decade/personal-notes/statement-breakdown.md

 ✓ Successfully read 1526 bytes from /Users/enzopsm/Documents/personal/lifestask/Decade/personal-notes/progress-notepad.md

 ⋮ 

- Summary: 2 operations processed, 2 successful, 0 failed
 - Completed in 0.0s

> Great, I have full context now. Here's a comprehensive breakdown of the expected API and frontend behavior:

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━


## Core API

### POST /orders

Submit a new order (bid or ask). This is the central endpoint.

Request body:
json
{
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",           // "bid" (buy) or "ask" (sell)
  "symbol": "AAPL",
  "price": 150.00,         // max willing to pay (bid) or min willing to accept (ask). Null for market orders.
  "quantity": 1000,
  "expires_at": "2026-02-20T18:00:00Z"
}


Response 201 Created:
json
{
  "order_id": "ord-uuid-here",
  "status": "pending",     // or "filled" / "partially_filled" if matched immediately
  "filled_quantity": 0,
  "remaining_quantity": 1000,
  "created_at": "2026-02-16T16:28:00Z"
}


Key behaviors:
- The matching engine runs synchronously on submission. If the order matches immediately (fully or partially), the response already reflects that.
- status can be pending, partially_filled, filled, cancelled, or expired.
- Price is in a fixed decimal representation (avoid floating point — use integer cents or a decimal type internally).

### GET /orders/{order_id}

Broker retrieves the current status of a previously submitted order.

Response 200 OK:
json
{
  "order_id": "ord-uuid",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "price": 150.00,
  "quantity": 1000,
  "filled_quantity": 500,
  "remaining_quantity": 500,
  "status": "partially_filled",
  "expires_at": "2026-02-20T18:00:00Z",
  "created_at": "2026-02-16T16:28:00Z",
  "trades": [
    {
      "trade_id": "trd-uuid",
      "price": 148.00,
      "quantity": 500,
      "counterparty_broker_id": "broker-456",
      "executed_at": "2026-02-16T16:29:00Z"
    }
  ]
}


The trades array gives the broker full visibility into how their order was filled — at what price, what quantity, and when.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━


## Extension Endpoints

### 1. GET /stocks/{symbol}/price — Current Stock Price

Returns a moving average of recent trade execution prices.

json
{
  "symbol": "AAPL",
  "current_price": 149.50,
  "window": "5m",
  "trades_in_window": 12,
  "updated_at": "2026-02-16T16:30:00Z"
}


Design decisions:
- "Moving average of ask/bids" from the statement is ambiguous. The most meaningful interpretation is a volume-weighted average price (VWAP) of recent executed trades, not of open orders. This is how real exchanges report "last price."
- Use a configurable time window (e.g., last 5 minutes). If no trades exist in the window, fall back to the last known trade price.
- If no trades have ever occurred for the symbol, return null for price with a 204 No Content or a field indicating no data.

### 2. GET /stocks/{symbol}/book — Order Book (Top of Book)

Returns the top N bids and asks for a given symbol.

Query params: ?depth=10 (default 10)

json
{
  "symbol": "AAPL",
  "bids": [
    { "price": 150.00, "total_quantity": 3000, "order_count": 4 },
    { "price": 149.50, "total_quantity": 1500, "order_count": 2 }
  ],
  "asks": [
    { "price": 151.00, "total_quantity": 2000, "order_count": 3 },
    { "price": 152.00, "total_quantity": 500, "order_count": 1 }
  ],
  "spread": 1.00
}


Key behaviors:
- Bids sorted descending by price, asks ascending — standard order book representation.
- Aggregate by price level: show total quantity and number of orders at each level, not individual orders (this is how real exchanges expose L2 data — individual order visibility would be L3 and is a security concern).
- spread = best ask - best bid. Useful for the frontend.

### 3. GET /brokers/{broker_id}/balance — Broker Balance

json
{
  "broker_id": "broker-123",
  "cash_balance": 1000000.00,
  "holdings": [
    { "symbol": "AAPL", "quantity": 5000 },
    { "symbol": "GOOG", "quantity": 200 }
  ],
  "pending_orders": 3
}


Design decisions:
- This implies the system tracks broker portfolios. Brokers start with some initial cash and stock holdings (seeded or via a setup endpoint).
- Buying reduces cash, selling reduces holdings. The engine should reject orders that would exceed available balance/holdings (basic risk check).
- pending_orders count is a nice addition for the frontend.

### 4. Webhook — Trade Execution Notification

When a trade executes, the system POSTs to a pre-registered webhook URL.

Registration: POST /webhooks
json
{
  "broker_id": "broker-123",
  "url": "https://broker-system.example.com/trade-notifications",
  "events": ["trade.executed", "order.expired", "order.cancelled"]
}


Webhook payload (sent to the broker's URL):
json
{
  "event": "trade.executed",
  "timestamp": "2026-02-16T16:29:00Z",
  "data": {
    "trade_id": "trd-uuid",
    "order_id": "ord-uuid",
    "symbol": "AAPL",
    "side": "bid",
    "price": 148.00,
    "quantity": 500,
    "status": "partially_filled",
    "remaining_quantity": 500
  }
}


Key behaviors:
- Fire-and-forget with retries (e.g., 3 retries with exponential backoff).
- Include an HMAC signature header (X-Signature) so brokers can verify authenticity.
- Both sides of a trade get notified independently.

### 5. Market Price Orders

A market order has no price — it executes at whatever the best available price is on the opposite side.

Submit with price: null:
json
{
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "price": null,
  "quantity": 1000,
  "expires_at": "2026-02-20T18:00:00Z"
}


Behavior:
- A market buy matches against the best (lowest) ask immediately.
- A market sell matches against the best (highest) bid immediately.
- If the book doesn't have enough liquidity, the order partially fills and the remainder stays as a market order (or you can choose to cancel the unfilled portion — both are valid, but keeping it is more standard).
- Market orders always have priority over limit orders at the same price level.
- Market orders that can't fill at all (empty book) should either remain pending or be rejected — I'd recommend rejecting with a clear error, since a market order with no counterparty is meaningless.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━


## Frontend

The statement doesn't explicitly require a frontend, but since you're building one, here's what makes sense:

1. Order Book View — Real-time display of the top bids/asks for a selected symbol. Classic two-column layout (bids left in green, asks right in red), with price levels and aggregated quantities. Poll GET /stocks/{symbol}/book or use 
WebSockets if you add them.

2. Order Submission Form — Broker selects symbol, side (bid/ask), enters price (or toggles "market order"), quantity, and expiration. Calls POST /orders.

3. Order Status / History — List of the broker's orders with live status updates. Each row expandable to show the trades array.

4. Stock Price Ticker — Shows current price per symbol from the /stocks/{symbol}/price endpoint. A small sparkline or price chart if you want to impress.

5. Broker Balance Dashboard — Shows cash, holdings, and pending orders from /brokers/{broker_id}/balance.

The frontend is a nice-to-have showcase but the core value is in the engine and API. Keep it simple — a single-page app that demonstrates the API works correctly is more than enough.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━


## General API Design Principles Applied

- All IDs are UUIDs (not sequential integers — avoids information leakage).
- Timestamps in ISO 8601 with timezone.
- Prices as decimal strings or fixed-point integers internally (never float).
- Consistent error responses: { "error": "order_not_found", "message": "..." } with proper HTTP status codes.
- Pagination on any list endpoint via ?cursor= or ?page=&limit=.
- Idempotency key header (X-Idempotency-Key) on POST /orders to prevent duplicate submissions on network retries.

Want me to start scaffolding the project structure, or do you want to refine any of these decisions first?

 ▸ Credits: 1.57 • Time: 58s

!> Question: Is a frontend in the scope for this project? Or should it basically be a backend plus a API documentation?

> Re-reading the statement carefully:

│ "We want you to define the API for the system"
│ "provide an OCI-compliant container file with a reproducible environment to run the application"

There's zero mention of a frontend, UI, or user-facing interface anywhere in the statement or the email. The entire challenge is framed around brokers interacting with the system through an API — which is a machine-to-machine 
interface.

So no, a frontend is not in scope. What they're evaluating is:

1. The matching engine (core algorithm)
2. The API design (REST endpoints, request/response contracts)
3. The containerized, reproducible environment

For API documentation, you should provide one of:
- An OpenAPI/Swagger spec (ideal — it's the industry standard, and you can auto-generate it from code annotations in Go)
- A clear README with endpoint descriptions, request/response examples, and curl commands

A Swagger UI served by the container itself is a nice touch — it lets the evaluator explore and test the API interactively without any extra setup, and it's trivially easy to add in Go. That's worth more than a custom frontend 
because it directly showcases the API design quality they're asking for.

Don't spend time on a frontend. Put that energy into the engine correctness, API ergonomics, error handling, and solid documentation.

 ▸ Credits: 0.47 • Time: 10s

!> What would you like to do next?