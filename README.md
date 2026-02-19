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

## API Walkthrough

Below is a complete walkthrough you can run with `curl` against a running server. It covers every endpoint and demonstrates the core matching scenarios from the challenge statement.

### 1. Register Brokers

```bash
# Register a buyer with $100,000 cash
curl -s -X POST http://localhost:8080/brokers \
  -H "Content-Type: application/json" \
  -d '{
    "broker_id": "buyer",
    "initial_cash": 100000.00
  }' | jq .

# Register a seller with AAPL shares
curl -s -X POST http://localhost:8080/brokers \
  -H "Content-Type: application/json" \
  -d '{
    "broker_id": "seller",
    "initial_cash": 0,
    "initial_holdings": [
      {"symbol": "AAPL", "quantity": 5000}
    ]
  }' | jq .
```

### 2. Check Broker Balance

```bash
curl -s http://localhost:8080/brokers/buyer/balance | jq .
curl -s http://localhost:8080/brokers/seller/balance | jq .
```

### 3. Submit Orders and See Matching


**Same price match** — seller asks $150, buyer bids $150 → trade at $150:

```bash
# Seller places an ask
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "type": "limit",
    "broker_id": "seller",
    "document_number": "DOC001",
    "side": "ask",
    "symbol": "AAPL",
    "price": 150.00,
    "quantity": 1000,
    "expires_at": "2027-01-01T00:00:00Z"
  }' | jq .

# Buyer places a matching bid → trade executes immediately
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "type": "limit",
    "broker_id": "buyer",
    "document_number": "DOC002",
    "side": "bid",
    "symbol": "AAPL",
    "price": 150.00,
    "quantity": 1000,
    "expires_at": "2027-01-01T00:00:00Z"
  }' | jq .
```

The bid response will show `"status": "filled"` with a trade at price `150.00`.

**Price gap** — seller asks $148, buyer bids $150 → trade at $148 (seller's price):

```bash
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "type": "limit",
    "broker_id": "seller",
    "document_number": "DOC003",
    "side": "ask",
    "symbol": "AAPL",
    "price": 148.00,
    "quantity": 500,
    "expires_at": "2027-01-01T00:00:00Z"
  }' | jq .

curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "type": "limit",
    "broker_id": "buyer",
    "document_number": "DOC004",
    "side": "bid",
    "symbol": "AAPL",
    "price": 150.00,
    "quantity": 500,
    "expires_at": "2027-01-01T00:00:00Z"
  }' | jq .
```

**No match** — seller asks $155, buyer bids $150 → both rest on book:

```bash
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "type": "limit",
    "broker_id": "seller",
    "document_number": "DOC005",
    "side": "ask",
    "symbol": "AAPL",
    "price": 155.00,
    "quantity": 200,
    "expires_at": "2027-01-01T00:00:00Z"
  }' | jq .

curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "type": "limit",
    "broker_id": "buyer",
    "document_number": "DOC006",
    "side": "bid",
    "symbol": "AAPL",
    "price": 150.00,
    "quantity": 200,
    "expires_at": "2027-01-01T00:00:00Z"
  }' | jq .
```

Both responses will show `"status": "pending"` with empty `trades` arrays.

**Partial fill** — seller offers 500, buyer wants 1000 → 500 filled, 500 remaining:

```bash
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "type": "limit",
    "broker_id": "seller",
    "document_number": "DOC007",
    "side": "ask",
    "symbol": "AAPL",
    "price": 149.00,
    "quantity": 500,
    "expires_at": "2027-01-01T00:00:00Z"
  }' | jq .

curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "type": "limit",
    "broker_id": "buyer",
    "document_number": "DOC008",
    "side": "bid",
    "symbol": "AAPL",
    "price": 149.00,
    "quantity": 1000,
    "expires_at": "2027-01-01T00:00:00Z"
  }' | jq .
```

The bid response will show `"status": "partially_filled"`, `"filled_quantity": 500`, `"remaining_quantity": 500`.

### 4. Market Orders

Market orders execute immediately at the best available prices (IOC semantics):

```bash
curl -s -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "type": "market",
    "broker_id": "buyer",
    "document_number": "MKT001",
    "side": "bid",
    "symbol": "AAPL",
    "quantity": 100
  }' | jq .
```

If there's liquidity, the response shows the fills. If not, you get `409` with `"error": "no_liquidity"`.

### 5. Retrieve an Order

```bash
curl -s http://localhost:8080/orders/{order_id} | jq .
```

Replace `{order_id}` with an actual ID from a previous response.

### 6. Cancel an Order

```bash
curl -s -X DELETE http://localhost:8080/orders/{order_id} | jq .
```

Only `pending` or `partially_filled` orders can be cancelled. Filled/cancelled/expired orders return `409`.

### 7. List Broker Orders

```bash
# All orders
curl -s "http://localhost:8080/brokers/buyer/orders" | jq .

# Filter by status
curl -s "http://localhost:8080/brokers/buyer/orders?status=filled" | jq .

# Pagination
curl -s "http://localhost:8080/brokers/buyer/orders?page=1&limit=5" | jq .
```

### 8. Stock Price (VWAP)

```bash
curl -s http://localhost:8080/stocks/AAPL/price | jq .
```

Returns the volume-weighted average price over the last 5 minutes (configurable). Falls back to the last trade price if no trades in the window. Returns `null` if no trades ever.

### 9. Order Book

```bash
# Default depth (10 levels)
curl -s http://localhost:8080/stocks/AAPL/book | jq .

# Custom depth
curl -s "http://localhost:8080/stocks/AAPL/book?depth=5" | jq .
```

Shows aggregated bid/ask price levels with total quantity and order count per level, plus the spread.

### 10. Quote Simulation

Preview a market order without placing it:

```bash
curl -s "http://localhost:8080/stocks/AAPL/quote?side=bid&quantity=500" | jq .
```

Returns estimated average price, total cost, available quantity, and price levels that would be swept.

### 11. Webhooks

```bash
# Subscribe to trade notifications
curl -s -X POST http://localhost:8080/webhooks \
  -H "Content-Type: application/json" \
  -d '{
    "broker_id": "buyer",
    "url": "https://your-server.com/hooks",
    "events": ["trade.executed", "order.expired", "order.cancelled"]
  }' | jq .

# List subscriptions
curl -s "http://localhost:8080/webhooks?broker_id=buyer" | jq .

# Delete a subscription
curl -s -X DELETE http://localhost:8080/webhooks/{webhook_id}
```

### 12. Health Check

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
