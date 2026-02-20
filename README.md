# Mini Stock Exchange

A mini stock exchange system that receives orders from brokers, matches them using a Central Limit Order Book (CLOB), and executes trades. Built as a single Go binary with all state held in-memory.

## Prerequisites

- **Go 1.23+** (for local development)
- **Docker** (for containerized deployment)

## Quick Start

### Option 1: Docker (recommended)

```bash
docker compose up --build
```

The server starts on `http://localhost:8080`. The healthcheck runs automatically.

### Option 2: Local

```bash
make build
./miniexchange
```

Or simply:

```bash
make run
```

## Running Tests

```bash
make test
```

This runs `go test -race -count=1 ./...` — all unit tests, property-based tests, and integration tests with the race detector enabled.

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/brokers` | Register a new broker with initial cash and optional stock holdings. Required before submitting orders. |
| `GET` | `/brokers/{broker_id}/balance` | Current broker balance: cash, reserved cash, holdings, and reserved quantities. *(Extension: broker balance)* |
| `GET` | `/brokers/{broker_id}/orders` | Paginated list of a broker's orders with optional `?status=` filter. |
| `POST` | `/orders` | Submit a limit or market order. Matching runs synchronously — the response includes any trades. *(Core: order submission. Extension: market orders)* |
| `GET` | `/orders/{order_id}` | Retrieve full order state including all trades executed against it. *(Core: order status by identifier)* |
| `DELETE` | `/orders/{order_id}` | Cancel a pending or partially filled order. Releases reservations. |
| `GET` | `/stocks/{symbol}/price` | VWAP price over the last 5 minutes, with fallback to last trade price. *(Extension: current stock price)* |
| `GET` | `/stocks/{symbol}/book` | Top-of-book snapshot: aggregated bid/ask levels with `?depth=` control. *(Extension: order book listing)* |
| `GET` | `/stocks/{symbol}/quote` | Simulate a market order against the current book without placing it. |
| `POST` | `/webhooks` | Subscribe to event notifications (`trade.executed`, `order.expired`, `order.cancelled`). Upsert semantics. *(Extension: webhook notifications)* |
| `GET` | `/webhooks` | List webhook subscriptions for a broker (`?broker_id=`). |
| `DELETE` | `/webhooks/{webhook_id}` | Remove a webhook subscription. |
| `GET` | `/healthz` | Liveness check. |

## API Walkthrough

A complete walkthrough you can run with `curl` against a running server. Every endpoint is exercised. Copy-paste the blocks in order — each scenario builds on the state left by the previous one.

> **Note:** Replace `{order_id}` and `{webhook_id}` placeholders with actual IDs from previous responses.

### 1. Register brokers (POST /brokers)

```bash
# Register a buyer with $100,000 cash
curl -s -X POST http://localhost:8080/brokers \
  -H "Content-Type: application/json" \
  -d '{"broker_id":"buyer","initial_cash":100000.00}' | jq .

# Register a seller with 5000 AAPL shares and no cash
curl -s -X POST http://localhost:8080/brokers \
  -H "Content-Type: application/json" \
  -d '{"broker_id":"seller","initial_cash":0,"initial_holdings":[{"symbol":"AAPL","quantity":5000}]}' | jq .
```

### 2. Check broker balances (GET /brokers/{broker_id}/balance)

```bash
# Buyer: $100,000 cash, no holdings
curl -s http://localhost:8080/brokers/buyer/balance | jq .

# Seller: $0 cash, 5000 AAPL
curl -s http://localhost:8080/brokers/seller/balance | jq .
```

### 3. Limit order — same price match (POST /orders)

```bash
# Seller asks 100 AAPL @ $150 → rests on book
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"seller","document_number":"DOC001","side":"ask","symbol":"AAPL","price":150.00,"quantity":100,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

# Buyer bids 100 AAPL @ $150 → matches immediately, trade at $150
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"buyer","document_number":"DOC002","side":"bid","symbol":"AAPL","price":150.00,"quantity":100,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

# Verify: buyer $85,000 cash + 100 AAPL, seller $15,000 cash + 4900 AAPL
curl -s http://localhost:8080/brokers/buyer/balance | jq .
curl -s http://localhost:8080/brokers/seller/balance | jq .
```

### 4. Limit order — price gap match

```bash
# Seller asks 100 AAPL @ $148
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"seller","document_number":"DOC003","side":"ask","symbol":"AAPL","price":148.00,"quantity":100,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

# Buyer bids 100 AAPL @ $150 → matches at seller's price $148
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"buyer","document_number":"DOC004","side":"bid","symbol":"AAPL","price":150.00,"quantity":100,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

# Verify: buyer $70,200 cash + 200 AAPL, seller $29,800 cash + 4800 AAPL
curl -s http://localhost:8080/brokers/buyer/balance | jq .
curl -s http://localhost:8080/brokers/seller/balance | jq .
```

### 5. Limit order — no match + order book + cancel

```bash
# Seller asks 100 AAPL @ $155 → rests on book (pending)
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"seller","document_number":"DOC005","side":"ask","symbol":"AAPL","price":155.00,"quantity":100,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

# Buyer bids 100 AAPL @ $150 → no match, rests on book (pending)
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"buyer","document_number":"DOC006","side":"bid","symbol":"AAPL","price":150.00,"quantity":100,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

# Balances unchanged
curl -s http://localhost:8080/brokers/buyer/balance | jq .
curl -s http://localhost:8080/brokers/seller/balance | jq .

# View the order book — both orders resting at their price levels (GET /stocks/{symbol}/book)
curl -s http://localhost:8080/stocks/AAPL/book | jq .

# Custom depth parameter
curl -s "http://localhost:8080/stocks/AAPL/book?depth=5" | jq .

# Cancel both resting orders to clean up (DELETE /orders/{order_id})
# Replace {ask_order_id} and {bid_order_id} with order_id values from above
curl -s -X DELETE http://localhost:8080/orders/{ask_order_id} | jq .
curl -s -X DELETE http://localhost:8080/orders/{bid_order_id} | jq .
```

### 6. Limit order — partial fill

```bash
# Seller asks 100 AAPL @ $149
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"seller","document_number":"DOC007","side":"ask","symbol":"AAPL","price":149.00,"quantity":100,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

# Buyer bids 200 AAPL @ $149 → 100 filled, 100 remaining on book (partially_filled)
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"buyer","document_number":"DOC008","side":"bid","symbol":"AAPL","price":149.00,"quantity":200,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

# Verify: buyer $55,300 cash + 300 AAPL (remaining 100 on book reserving $14,900)
curl -s http://localhost:8080/brokers/buyer/balance | jq .
# Seller $44,700 cash + 4700 AAPL
curl -s http://localhost:8080/brokers/seller/balance | jq .

# Cancel the buyer's partially filled order to clean up — releases the $14,900 reservation
# Replace {partial_order_id} with the buyer's order_id from above
curl -s -X DELETE http://localhost:8080/orders/{partial_order_id} | jq .
```

### 7. Retrieve an order (GET /orders/{order_id})

```bash
# Fetch full order state including all trades — replace {order_id} with any ID from above
curl -s http://localhost:8080/orders/{order_id} | jq .
```

### 8. List broker orders with filters (GET /brokers/{broker_id}/orders)

```bash
# All orders for the buyer
curl -s "http://localhost:8080/brokers/buyer/orders" | jq .

# Filter by status
curl -s "http://localhost:8080/brokers/buyer/orders?status=filled" | jq .
curl -s "http://localhost:8080/brokers/seller/orders?status=filled" | jq .
curl -s "http://localhost:8080/brokers/buyer/orders?status=cancelled" | jq .

# Pagination
curl -s "http://localhost:8080/brokers/buyer/orders?page=1&limit=2" | jq .
```

### 9. Market order — full fill (POST /orders with type=market)

```bash
# Seller places a resting ask to provide liquidity
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"seller","document_number":"DOC009","side":"ask","symbol":"AAPL","price":151.00,"quantity":100,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

# Buyer submits a market buy for 100 AAPL → fills immediately at $151
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"market","broker_id":"buyer","document_number":"MKT001","side":"bid","symbol":"AAPL","quantity":100}' | jq .

# Verify balances
curl -s http://localhost:8080/brokers/buyer/balance | jq .
curl -s http://localhost:8080/brokers/seller/balance | jq .
```

### 10. Market order — no liquidity (409 Conflict)

```bash
# No asks on the book → market buy is rejected
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"market","broker_id":"buyer","document_number":"MKT002","side":"bid","symbol":"AAPL","quantity":100}' | jq .
# Response: 409 with "error": "no_liquidity"
```

### 11. Stock price — VWAP (GET /stocks/{symbol}/price)

```bash
# VWAP over the last 5 minutes based on executed trades
curl -s http://localhost:8080/stocks/AAPL/price | jq .
```

### 12. Quote simulation (GET /stocks/{symbol}/quote)

```bash
# Place some asks to provide liquidity for the quote
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"seller","document_number":"DOC010","side":"ask","symbol":"AAPL","price":152.00,"quantity":200,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"seller","document_number":"DOC011","side":"ask","symbol":"AAPL","price":153.00,"quantity":300,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

# Preview a market buy of 400 AAPL without placing it — shows estimated cost and price levels
curl -s "http://localhost:8080/stocks/AAPL/quote?side=bid&quantity=400" | jq .

# Preview a market sell
curl -s "http://localhost:8080/stocks/AAPL/quote?side=ask&quantity=100" | jq .

# Clean up the resting asks
# Replace {ask_id_1} and {ask_id_2} with order_id values from above
curl -s -X DELETE http://localhost:8080/orders/{ask_id_1} | jq .
curl -s -X DELETE http://localhost:8080/orders/{ask_id_2} | jq .
```

### 13. Webhooks — subscription CRUD (POST /webhooks, GET /webhooks, DELETE /webhooks/{webhook_id})

```bash
# Subscribe the buyer to trade, expiration, and cancellation notifications
curl -s -X POST http://localhost:8080/webhooks \
  -H "Content-Type: application/json" \
  -d '{"broker_id":"buyer","url":"https://example.com/hooks","events":["trade.executed","order.expired","order.cancelled"]}' | jq .

# List the buyer's webhook subscriptions
curl -s "http://localhost:8080/webhooks?broker_id=buyer" | jq .

# Delete a specific subscription — replace {webhook_id} with an ID from above
curl -s -X DELETE http://localhost:8080/webhooks/{webhook_id}
```

### 14. Webhooks — delivery verification (fire-and-forget HTTP POST)

The exchange POSTs event payloads to the broker's registered URL when trades execute, orders expire, or orders are cancelled. This section proves the delivery system works end-to-end.

**Step 1: Get a webhook receiver URL**

Go to [https://webhook.site](https://webhook.site). Copy the unique HTTPS URL shown on the page (e.g., `https://webhook.site/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`). Keep the page open — incoming requests appear in real time.

**Step 2: Subscribe the seller to all events**

Replace `{YOUR_WEBHOOK_SITE_URL}` with the URL you copied.

```bash
curl -s -X POST http://localhost:8080/webhooks \
  -H "Content-Type: application/json" \
  -d '{"broker_id":"seller","url":"{YOUR_WEBHOOK_SITE_URL}","events":["trade.executed","order.expired","order.cancelled"]}' | jq .
```

**Step 3: Trigger `trade.executed`**

Place a resting ask from the seller, then a matching bid from the buyer. The seller's webhook URL receives a `trade.executed` POST.

```bash
# Seller asks 50 AAPL @ $160 → rests on book
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"seller","document_number":"WH001","side":"ask","symbol":"AAPL","price":160.00,"quantity":50,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

# Buyer bids 50 AAPL @ $160 → matches, trade executes
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"buyer","document_number":"WH002","side":"bid","symbol":"AAPL","price":160.00,"quantity":50,"expires_at":"2027-01-01T00:00:00Z"}' | jq .
```

Check webhook.site — you should see a POST with headers `X-Event-Type: trade.executed`, `X-Delivery-Id`, and `X-Webhook-Id`, and a JSON body like:

```json
{
  "event": "trade.executed",
  "timestamp": "...",
  "data": {
    "trade_id": "...",
    "broker_id": "seller",
    "order_id": "...",
    "symbol": "AAPL",
    "side": "ask",
    "trade_price": 160.00,
    "trade_quantity": 50,
    "order_status": "filled",
    "order_filled_quantity": 50,
    "order_remaining_quantity": 0
  }
}
```

**Step 4: Trigger `order.cancelled`**

Place a resting ask, then cancel it. The seller's webhook URL receives an `order.cancelled` POST.

```bash
# Seller asks 50 AAPL @ $200 → rests on book
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"type":"limit","broker_id":"seller","document_number":"WH003","side":"ask","symbol":"AAPL","price":200.00,"quantity":50,"expires_at":"2027-01-01T00:00:00Z"}' | jq .

# Cancel it — replace {order_id} with the order_id from above
curl -s -X DELETE http://localhost:8080/orders/{order_id} | jq .
```

Check webhook.site — you should see a POST with `X-Event-Type: order.cancelled` and a body like:

```json
{
  "event": "order.cancelled",
  "timestamp": "...",
  "data": {
    "broker_id": "seller",
    "order_id": "...",
    "symbol": "AAPL",
    "side": "ask",
    "price": 200.00,
    "quantity": 50,
    "filled_quantity": 0,
    "cancelled_quantity": 50,
    "remaining_quantity": 0,
    "status": "cancelled"
  }
}
```

**Step 5: Trigger `order.expired`**

Place an order that expires 3 seconds from now. The background expiration sweep (runs every 1s) will expire it and POST to the webhook URL.

```bash
# Seller asks 50 AAPL @ $200, expiring in 3 seconds
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d "{\"type\":\"limit\",\"broker_id\":\"seller\",\"document_number\":\"WH004\",\"side\":\"ask\",\"symbol\":\"AAPL\",\"price\":200.00,\"quantity\":50,\"expires_at\":\"$(date -u -v+3S '+%Y-%m-%dT%H:%M:%SZ')\"}" | jq .

# Wait a few seconds for the expiration sweep to run
sleep 4

# Confirm the order expired
curl -s http://localhost:8080/brokers/seller/orders?status=expired | jq .
```

> **Note:** On Linux, replace `-v+3S` with `-d '+3 seconds'`: `$(date -u -d '+3 seconds' '+%Y-%m-%dT%H:%M:%SZ')`

Check webhook.site — you should see a POST with `X-Event-Type: order.expired` and a body like:

```json
{
  "event": "order.expired",
  "timestamp": "...",
  "data": {
    "broker_id": "seller",
    "order_id": "...",
    "symbol": "AAPL",
    "side": "ask",
    "price": 200.00,
    "quantity": 50,
    "filled_quantity": 0,
    "cancelled_quantity": 50,
    "remaining_quantity": 0,
    "status": "expired"
  }
}
```

**Step 6: Clean up**

```bash
# Delete the seller's webhook subscriptions
curl -s "http://localhost:8080/webhooks?broker_id=seller" | jq -r '.webhooks[].webhook_id' | while read id; do
  curl -s -X DELETE "http://localhost:8080/webhooks/$id"
done
```

### 15. Health check (GET /healthz)

```bash
curl -s http://localhost:8080/healthz | jq .
```

## Configuration

All settings are via environment variables:

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP server port |
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `EXPIRATION_INTERVAL` | `1s` | Order expiration sweep interval |
| `WEBHOOK_TIMEOUT` | `5s` | HTTP timeout for webhook delivery |
| `VWAP_WINDOW` | `5m` | Time window for VWAP price calculation |
| `READ_TIMEOUT` | `5s` | HTTP server read timeout |
| `WRITE_TIMEOUT` | `10s` | HTTP server write timeout |
| `IDLE_TIMEOUT` | `60s` | HTTP server idle timeout |
| `SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown deadline |

## Project Structure

```
cmd/miniexchange/main.go    → Entrypoint, dependency wiring, server lifecycle
internal/domain/            → Pure data types (Broker, Order, Trade, Webhook)
internal/store/             → Thread-safe in-memory stores
internal/engine/            → Matching engine, order book (B-tree), expiration
internal/service/           → Business logic orchestration
internal/handler/           → HTTP handlers and router
design-documents/           → System design specification
ai-chats/                   → AI conversation archive (design process)
```

## Design Documentation

The full system design spec is at [`design-documents/system-design-spec.md`](design-documents/system-design-spec.md). It covers the matching engine algorithm, concurrency model, data structures, and all API contracts in detail.

The `ai-chats/` folder contains the AI conversation archive that produced the design spec — it shows the iterative design process including decisions about CLOB semantics, market order handling, webhook design, and API conventions.
