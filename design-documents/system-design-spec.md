# Stack & Deployment

This section pins every technology choice, dependency, file path, build artifact, and runtime parameter. The implementation must be derivable from this section alone — no decisions are left to the developer.

## Language & Version

**Go ≥ 1.23** (`go 1.23` directive in `go.mod`).

Rationale:
- Goroutines and channels map directly to the per-symbol concurrency model (one writer goroutine per symbol, concurrent readers via `sync.RWMutex`).
- Single statically-linked binary — no runtime dependencies, trivial to containerize on `scratch`/`distroless`.
- `net/http` in the stdlib is production-grade; no framework required for a JSON REST API of this scope.
- `encoding/json`, `time`, `sync`, `net/http/httptest` cover the majority of the application's needs without third-party code.

## Go Module Path

```
module github.com/efreitasn/miniexchange
```

## Dependencies

Prefer stdlib where it suffices. Every third-party dependency listed below provides clear value that the stdlib does not.

| Import Path | Version | Purpose |
|---|---|---|
| `github.com/google/btree` | `v2.x` (latest v2) | B-tree for bid/ask sides of the order book. O(log n) insert/delete/min with cache-friendly node layout. Required by the Matching Engine spec. |
| `github.com/google/uuid` | `v1.x` (latest v1) | RFC 4122 UUID generation for `order_id`, `trade_id`, `webhook_id`, and `X-Delivery-Id`. Stdlib has no UUID package. |
| `github.com/go-chi/chi/v5` | `v5.x` (latest v5) | Lightweight HTTP router with URL parameter extraction (`/orders/{order_id}`, `/stocks/{symbol}/book`, etc.), middleware chaining, and `405 Method Not Allowed` handling. `net/http.ServeMux` lacks URL path parameters and method-based routing ergonomics. |
| `log/slog` | (stdlib, Go 1.21+) | Structured logging. No third-party logging library needed — `slog` is in the stdlib since Go 1.21. |

No other dependencies. Specifically:
- No ORM or database driver — the system is entirely in-memory.
- No configuration library — `os.Getenv` with a thin helper is sufficient (see Configuration below).
- No validation framework — validation logic is hand-written per the spec's explicit rules.
- No mocking framework — Go interfaces + hand-written test doubles.

## Project Layout

```
miniexchange/
├── cmd/
│   └── miniexchange/
│       └── main.go              # Entrypoint: config loading, dependency wiring, server startup
├── internal/
│   ├── config/
│   │   └── config.go            # Configuration struct, env var parsing, defaults
│   ├── domain/
│   │   ├── broker.go            # Broker type, balance fields, mutex
│   │   ├── order.go             # Order type, status constants, order-type variants
│   │   ├── trade.go             # Trade type
│   │   ├── webhook.go           # Webhook subscription type
│   │   └── symbol.go            # Symbol registry type
│   ├── engine/
│   │   ├── book.go              # OrderBook: bid/ask B-trees, secondary index, per-symbol lock
│   │   ├── matcher.go           # Matching algorithm: limit and market order procedures
│   │   └── expiry.go            # Background expiration goroutine
│   ├── service/
│   │   ├── broker.go            # Broker registration, balance queries
│   │   ├── order.go             # Order submission, retrieval, cancellation, listing
│   │   ├── webhook.go           # Webhook CRUD, dispatch (fire-and-forget HTTP POST)
│   │   └── stock.go             # Price (VWAP), book snapshot, quote simulation
│   ├── handler/
│   │   ├── broker.go            # HTTP handlers: POST /brokers, GET /brokers/{broker_id}/balance, GET /brokers/{broker_id}/orders
│   │   ├── order.go             # HTTP handlers: POST /orders, GET /orders/{order_id}, DELETE /orders/{order_id}
│   │   ├── webhook.go           # HTTP handlers: POST /webhooks, GET /webhooks, DELETE /webhooks/{webhook_id}
│   │   ├── stock.go             # HTTP handlers: GET /stocks/{symbol}/price, GET /stocks/{symbol}/book, GET /stocks/{symbol}/quote
│   │   ├── router.go            # chi router setup, route registration, middleware
│   │   └── response.go          # JSON response helpers, error response formatting
│   └── store/
│       ├── broker.go            # In-memory broker store (map + sync.RWMutex)
│       ├── order.go             # In-memory order store (map + sync.RWMutex)
│       ├── trade.go             # In-memory trade store (per-symbol trade log for VWAP)
│       └── webhook.go           # In-memory webhook store (map + sync.RWMutex)
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

Layer responsibilities:
- `cmd/miniexchange/main.go` — parses config, instantiates stores, engine, services, handlers, starts the HTTP server and expiration goroutine. No business logic.
- `internal/domain/` — pure data types and constants. No methods with side effects, no dependencies on other packages.
- `internal/store/` — in-memory data access. Thread-safe maps. No business logic beyond CRUD.
- `internal/engine/` — matching engine, order book data structure, expiration loop. Depends on `domain` and `store`.
- `internal/service/` — orchestration layer. Coordinates validation, engine calls, webhook dispatch. Depends on `domain`, `store`, `engine`.
- `internal/handler/` — HTTP layer. Parses requests, calls services, writes JSON responses. Depends on `service` and `domain`. No direct store or engine access.

The `internal/` prefix prevents external imports — standard Go convention for application-private packages.

## In-Memory Store Structures

Each store is a thin thread-safe wrapper around Go maps. No business logic — just CRUD and indexing.

| Store | Primary Index | Secondary Indexes |
|---|---|---|
| `BrokerStore` | `map[string]*domain.Broker` keyed by `broker_id` | None. |
| `OrderStore` | `map[string]*domain.Order` keyed by `order_id` | `map[string][]*domain.Order` keyed by `broker_id` (append-only, supports `GET /brokers/{broker_id}/orders`). |
| `TradeStore` | `map[string][]*domain.Trade` keyed by `symbol` (append-only slice per symbol, chronological order) | None. VWAP computation iterates the slice backwards from the tail until `executed_at` falls outside the window. |
| `WebhookStore` | `map[string]*domain.Webhook` keyed by `webhook_id` | `map[string]map[string]*domain.Webhook` keyed by `broker_id → event` (supports upsert by `(broker_id, event)` and listing by `broker_id`). |

Each store has its own `sync.RWMutex`. Store-level locks protect map access only — they are independent of the per-symbol and per-broker locks in the engine. Write operations (insert, update, delete) acquire the write lock; read operations acquire the read lock.

## Graceful Shutdown

On SIGINT or SIGTERM:

1. Stop the HTTP server: call `http.Server.Shutdown(ctx)` with the `SHUTDOWN_TIMEOUT` deadline. This stops accepting new connections and waits for in-flight requests (including any active matching passes) to complete.
2. Stop the expiration goroutine: signal it via a `context.Context` cancellation. The goroutine checks the context on each tick and exits when cancelled. Any expiration sweep already in progress completes before the goroutine exits.
3. Pending webhook deliveries that were already enqueued (in-flight HTTP POSTs) are abandoned — the `http.Client` uses `WEBHOOK_TIMEOUT`, so they will time out naturally. No drain step.
4. Exit.

## Build & Run

### Dockerfile

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /miniexchange ./cmd/miniexchange

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /miniexchange /miniexchange
EXPOSE 8080
ENTRYPOINT ["/miniexchange"]
```

Design choices:
- Multi-stage build: `golang:1.23-alpine` for compilation, `gcr.io/distroless/static-debian12:nonroot` for the final image. The final image contains only the static binary — no shell, no package manager, no libc.
- `CGO_ENABLED=0` produces a fully static binary (no glibc dependency), required for `distroless/static`.
- `-ldflags="-s -w"` strips debug symbols and DWARF info, reducing binary size.
- `nonroot` tag runs as UID 65534 — no root in the container.
- Pinned base image tags. `golang:1.23-alpine` tracks the latest Go 1.23.x patch. `distroless/static-debian12` is Google's maintained minimal image.

### docker-compose.yml

```yaml
services:
  miniexchange:
    build: .
    ports:
      - "8080:8080"
    environment:
      - PORT=8080
      - LOG_LEVEL=info
    healthcheck:
      test: ["CMD", "/miniexchange", "-healthcheck"]
      interval: 5s
      timeout: 2s
      retries: 3
      start_period: 2s
```

The `-healthcheck` flag causes the binary to make an HTTP GET to `http://localhost:${PORT}/healthz` and exit 0/1 based on the response. This avoids installing `curl` or `wget` in the distroless image. The `/healthz` endpoint returns `200 OK` with body `{"status": "ok"}` — it is a simple liveness check, not listed in the API spec because it carries no business logic.

### Makefile

```makefile
.PHONY: build run test lint

build:
	go build -o bin/miniexchange ./cmd/miniexchange

run:
	go run ./cmd/miniexchange

test:
	go test -race -count=1 ./...

lint:
	goimports -l .
	go vet ./...
```

## Configuration

All runtime configuration is read from environment variables. No config files, no flags (except `-healthcheck` for the Docker health check). Env vars are the standard mechanism for container-based deployment and the simplest approach for a single-binary system.

| Env Var | Type | Default | Description |
|---|---|---|---|
| `PORT` | int | `8080` | HTTP server listen port. |
| `LOG_LEVEL` | string | `info` | Structured log level. One of: `debug`, `info`, `warn`, `error`. |
| `EXPIRATION_INTERVAL` | duration | `1s` | Interval between order expiration sweeps. Go duration format (e.g., `1s`, `500ms`). |
| `WEBHOOK_TIMEOUT` | duration | `5s` | HTTP client timeout for webhook delivery POSTs. |
| `VWAP_WINDOW` | duration | `5m` | Time window for VWAP price calculation. |
| `READ_TIMEOUT` | duration | `5s` | HTTP server read timeout. |
| `WRITE_TIMEOUT` | duration | `10s` | HTTP server write timeout. |
| `IDLE_TIMEOUT` | duration | `60s` | HTTP server idle connection timeout. |
| `SHUTDOWN_TIMEOUT` | duration | `10s` | Graceful shutdown deadline. On SIGINT/SIGTERM, the server stops accepting new connections and waits up to this duration for in-flight requests to complete. |

The `config.go` module reads each variable with `os.Getenv`, applies the default if empty, and parses the value into the appropriate Go type (`time.ParseDuration` for durations, `strconv.Atoi` for ints). Invalid values cause the process to exit with a descriptive error at startup — fail fast, no silent fallbacks.

## Testing Strategy

All tests use Go's built-in `testing` package. No third-party test frameworks or assertion libraries.

**Conventions:**
- Table-driven tests for any function with multiple input/output cases (validation, matching, price computation).
- `net/http/httptest` for HTTP handler tests — create a test server, send requests, assert on status codes and response bodies.
- Test files live alongside the code they test: `matcher.go` → `matcher_test.go`, `broker.go` → `broker_test.go`.
- Package-level tests (same package, `_test.go` suffix) for unit tests that need access to unexported fields. Separate `_test` package for handler tests that exercise the public API surface only.

**Commands:**
- `go test ./...` — run all tests.
- `go test -race ./...` — run all tests with the race detector enabled. This is the canonical test command (used in CI and the Makefile). The race detector is critical given the per-symbol and per-broker concurrency model.
- `go test -run TestMatcherLimitOrder ./internal/engine/` — run a specific test or subset.
- `go test -v -count=1 ./...` — verbose output, no test caching.

**What is not specified:** test coverage thresholds, integration test frameworks, or end-to-end test harnesses. The scope is a take-home challenge — unit tests and handler-level HTTP tests are sufficient.

## Code Conventions

**Formatting:** all code is formatted with `goimports` (superset of `gofmt` — also manages import grouping). Non-negotiable; enforced by the `lint` Makefile target.

**Import grouping** (enforced by `goimports`):
```go
import (
    "fmt"           // stdlib
    "net/http"

    "github.com/go-chi/chi/v5"  // third-party

    "github.com/efreitasn/miniexchange/internal/domain"  // project-internal
)
```

**Error handling:**
- All errors are wrapped with context: `fmt.Errorf("creating order: %w", err)`. The wrapping message describes what the current function was doing, not what went wrong — the wrapped error carries that.
- Never discard errors silently. If an error is intentionally ignored (e.g., `http.ResponseWriter.Write` in a response helper), add an explicit `// nolint` or `_ =` with a comment explaining why.
- Domain errors (validation failures, not-found, conflicts) are distinct types or sentinel values in `internal/domain/`. The handler layer maps them to HTTP status codes. The service layer never imports `net/http`.

**Naming:**
- Acronyms follow Go convention: `ID` not `Id`, `URL` not `Url`, `HTTP` not `Http`.
- Receiver names are short (1–2 letters): `func (b *Broker) AvailableCash()`.
- Interface names describe behavior: `OrderStore`, `BrokerStore` — not `IOrderStore`.

**JSON serialization:**
- All JSON field names use `snake_case`, matching the API spec. Struct tags: `json:"order_id"`.
- `omitempty` is used only where the spec explicitly states a field may be absent (e.g., `price` on market orders). Fields that are always present never use `omitempty` — `null` is serialized explicitly where the spec shows `null`.
- Monetary values cross the API boundary as `float64` (JSON numbers) and are immediately converted to/from `int64` cents. No `float64` arithmetic occurs in business logic.

**Concurrency:**
- Per-symbol `sync.RWMutex` for order book access (write lock for matching/cancel/expire, read lock for book/quote queries).
- Per-broker `sync.Mutex` for balance mutations across symbols.
- No `sync.Map` — explicit mutexes with clearly defined critical sections.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━


# General API Conventions

- All request and response bodies use JSON. Requests must include `Content-Type: application/json`. Requests with missing or incorrect `Content-Type` or malformed JSON bodies are rejected with `400 Bad Request`:
```json
{
  "error": "invalid_request",
  "message": "Request body must be valid JSON with Content-Type: application/json"
}
```
- All monetary values (prices, cash balances) are accepted and returned as decimal numbers with up to 2 decimal places. Values with more than 2 decimal places are rejected with `400 Bad Request`:
```json
{
  "error": "validation_error",
  "message": "Monetary values must have at most 2 decimal places"
}
```
  Internally, all monetary values are stored as `int64` cents. `$148.50` → `14850`. Conversion happens at the API boundary.
- All timestamps are ISO 8601 / RFC 3339 in UTC. Internally, timestamps are stored as `time.Time` with full nanosecond precision from `time.Now()`. When serialized to JSON, timestamps are formatted with second-level granularity and a trailing `Z` (e.g., `2026-02-17T19:00:00Z`) using `time.RFC3339`. Sub-second precision is not exposed in the API. The B-tree key uses `created_at` at full internal precision for ordering; the `order_id` tiebreaker handles collisions at any granularity.
- Authentication and authorization are out of scope for this implementation. All endpoints are unauthenticated.

# Core API

## POST /brokers

Register a new broker on the exchange with an initial cash deposit and optional stock holdings. This is the bootstrap mechanism — brokers must be registered before they can submit orders.

Request body:
```json
{
  "broker_id": "broker-123",
  "initial_cash": 1000000.00,
  "initial_holdings": [
    { "symbol": "AAPL", "quantity": 5000 },
    { "symbol": "GOOG", "quantity": 200 }
  ]
}
```

Validation rules:

| Field              | Rule                                                                 |
|--------------------|----------------------------------------------------------------------|
| `broker_id`        | Required. String matching `^[a-zA-Z0-9_-]{1,64}$`. Must be unique across the system. |
| `initial_cash`     | Required. Must be ≥ 0. At most 2 decimal places. Stored internally as `int64` cents. |
| `initial_holdings` | Optional. Defaults to empty array. Each entry requires `symbol` (string matching `^[A-Z]{1,10}$`) and `quantity` (integer, must be > 0). Duplicate symbols within the array are rejected. |

Response `201 Created`:
```json
{
  "broker_id": "broker-123",
  "cash_balance": 1000000.00,
  "holdings": [
    { "symbol": "AAPL", "quantity": 5000 },
    { "symbol": "GOOG", "quantity": 200 }
  ],
  "created_at": "2026-02-17T19:00:00Z"
}
```

Response `201 Created` (cash only, no holdings):
```json
{
  "broker_id": "broker-456",
  "cash_balance": 500000.00,
  "holdings": [],
  "created_at": "2026-02-17T19:00:00Z"
}
```

Response `409 Conflict` (broker already exists):
```json
{
  "error": "broker_already_exists",
  "message": "Broker broker-123 is already registered"
}
```

Response `400 Bad Request` (validation failure):
```json
{
  "error": "validation_error",
  "message": "initial_cash must be >= 0"
}
```

Key behaviors:
- Broker IDs are unique and immutable. Once registered, a broker cannot be re-registered or renamed. Attempting to register an existing `broker_id` returns `409 Conflict`.
- `initial_cash` follows the same internal representation as order prices: stored as `int64` cents. `$1,000,000.00` → `100000000`. The API accepts and returns decimal numbers with up to 2 decimal places; conversion happens at the boundary. Values with more than 2 decimal places are rejected.
- `initial_holdings` seeds the broker's stock positions. This represents positions transferred from outside the system (e.g., from another exchange). After registration, holdings change only through trade execution.
- There are no deposit, withdraw, or balance-adjustment endpoints. After registration, a broker's cash and holdings change exclusively through trade execution, order reservation, and reservation release (cancellation/expiration). This keeps the system's financial invariants simple and auditable.
- There is no separate `GET /brokers/{broker_id}` endpoint. `GET /brokers/{broker_id}/balance` is the canonical way to inspect a broker's current state, including cash, holdings, and reservations.
- `POST /orders` requires a valid, registered `broker_id`. Submitting an order with an unregistered broker returns `404 Not Found` with error `"broker_not_found"`.

## POST /orders

Submit a new order (bid or ask). This is the central endpoint.

Request body (limit order):
```json
{
  "type": "limit",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "price": 150.00,
  "quantity": 1000,
  "expires_at": "2026-02-20T18:00:00Z"
}
```

Request body (market order):
```json
{
  "type": "market",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "quantity": 1000
}
```

Validation rules by order type:

| Field        | `type: "limit"`       | `type: "market"`              |
|--------------|-----------------------|-------------------------------|
| `type`       | Required. Must be `"limit"` or `"market"`. | Required. Must be `"limit"` or `"market"`. |
| `price`      | Required, must be > 0. At most 2 decimal places. | Must be null or omitted       |
| `expires_at` | Required. Must be a future ISO 8601 / RFC 3339 timestamp in UTC. Rejected with `400 Bad Request` if in the past or not parseable. | Must be null or omitted       |
| `broker_id`  | Required. Must match `^[a-zA-Z0-9_-]{1,64}$`. | Required. Must match `^[a-zA-Z0-9_-]{1,64}$`. |
| `document_number` | Required. String matching `^[a-zA-Z0-9]{1,32}$`. Treated as an opaque label — no uniqueness or cross-order constraints are enforced. | Required. String matching `^[a-zA-Z0-9]{1,32}$`. Treated as an opaque label. |
| `side`       | Required (`bid`/`ask`)| Required (`bid`/`ask`)        |
| `symbol`     | Required. Must match `^[A-Z]{1,10}$`. | Required. Must match `^[A-Z]{1,10}$`. |
| `quantity`   | Required, integer, must be > 0 | Required, integer, must be > 0 |

Response `201 Created` (limit order, no immediate match):
```json
{
  "order_id": "ord-uuid-here",
  "type": "limit",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "price": 150.00,
  "quantity": 1000,
  "filled_quantity": 0,
  "remaining_quantity": 1000,
  "cancelled_quantity": 0,
  "status": "pending",
  "expires_at": "2026-02-20T18:00:00Z",
  "created_at": "2026-02-16T16:28:00Z",
  "cancelled_at": null,
  "expired_at": null,
  "average_price": null,
  "trades": []
}
```

Response `201 Created` (limit order, fully filled on submission):
```json
{
  "order_id": "ord-uuid-here",
  "type": "limit",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "price": 150.00,
  "quantity": 1000,
  "filled_quantity": 1000,
  "remaining_quantity": 0,
  "cancelled_quantity": 0,
  "status": "filled",
  "expires_at": "2026-02-20T18:00:00Z",
  "created_at": "2026-02-16T16:28:00Z",
  "cancelled_at": null,
  "expired_at": null,
  "average_price": 148.00,
  "trades": [
    { "trade_id": "trd-uuid-1", "price": 148.00, "quantity": 1000, "executed_at": "2026-02-16T16:28:00Z" }
  ]
}
```

Response `201 Created` (limit order, partially filled on submission — remainder rests on book):
```json
{
  "order_id": "ord-uuid-here",
  "type": "limit",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "price": 150.00,
  "quantity": 1000,
  "filled_quantity": 600,
  "remaining_quantity": 400,
  "cancelled_quantity": 0,
  "status": "partially_filled",
  "expires_at": "2026-02-20T18:00:00Z",
  "created_at": "2026-02-16T16:28:00Z",
  "cancelled_at": null,
  "expired_at": null,
  "average_price": 148.00,
  "trades": [
    { "trade_id": "trd-uuid-1", "price": 148.00, "quantity": 600, "executed_at": "2026-02-16T16:28:00Z" }
  ]
}
```

Response `201 Created` (market order, fully filled):
```json
{
  "order_id": "ord-uuid-here",
  "type": "market",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "quantity": 1000,
  "filled_quantity": 1000,
  "remaining_quantity": 0,
  "cancelled_quantity": 0,
  "status": "filled",
  "created_at": "2026-02-16T16:28:00Z",
  "average_price": 148.30,
  "trades": [
    { "trade_id": "trd-uuid-1", "price": 148.00, "quantity": 700, "executed_at": "2026-02-16T16:28:00Z" },
    { "trade_id": "trd-uuid-2", "price": 149.00, "quantity": 300, "executed_at": "2026-02-16T16:28:00Z" }
  ]
}
```

Response `201 Created` (market order, partially filled — IOC cancels remainder):
```json
{
  "order_id": "ord-uuid-here",
  "type": "market",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "quantity": 1000,
  "filled_quantity": 400,
  "remaining_quantity": 0,
  "cancelled_quantity": 600,
  "status": "cancelled",
  "created_at": "2026-02-16T16:28:00Z",
  "average_price": 148.00,
  "trades": [
    { "trade_id": "trd-uuid-1", "price": 148.00, "quantity": 400, "executed_at": "2026-02-16T16:28:00Z" }
  ]
}
```

Response `404 Not Found` (unregistered broker):
```json
{
  "error": "broker_not_found",
  "message": "Broker broker-999 does not exist"
}
```

Response `409 Conflict` (insufficient balance for bid):
```json
{
  "error": "insufficient_balance",
  "message": "Broker broker-123 has insufficient available cash for this order"
}
```

Response `409 Conflict` (insufficient holdings for ask):
```json
{
  "error": "insufficient_holdings",
  "message": "Broker broker-123 has insufficient available quantity of AAPL for this order"
}
```

Response `409 Conflict` (market order, no liquidity):
```json
{
  "error": "no_liquidity",
  "message": "No matching orders available for market order on AAPL"
}
```

Response `400 Bad Request` (unknown order type):
```json
{
  "error": "validation_error",
  "message": "Unknown order type: stop_loss. Must be one of: limit, market"
}
```

Response `400 Bad Request` (expires_at in the past):
```json
{
  "error": "validation_error",
  "message": "expires_at must be a future timestamp"
}
```

Key behaviors:
- The response returns the full order object — same shape as `GET /orders/{order_id}` and `DELETE /orders/{order_id}`. This keeps the API consistent: every endpoint that returns an order uses the same representation.
- The matching engine runs synchronously on submission. If the order matches immediately (fully or partially), the response already reflects that.
- `status` can be `pending`, `partially_filled`, `filled`, `cancelled`, or `expired`.
  - `pending` — order is on the book, no fills yet.
  - `partially_filled` — order is on the book and has received some fills, but is still active and can receive more.
  - `filled` — order is fully filled. Terminal.
  - `cancelled` — order was cancelled by the broker (`DELETE /orders/{id}`), or the unfilled remainder of a market/IOC order was automatically cancelled after matching. Terminal. Check `filled_quantity` and `cancelled_quantity` to distinguish partial fills from zero-fill cancellations. Note: market orders that partially fill terminate as `cancelled` (not `partially_filled`), because the unfilled remainder is immediately cancelled via IOC semantics.
  - `expired` — order reached its `expires_at` time without being fully filled. Terminal.
- Prices are stored internally as `int64` values representing **cents** (2 decimal places). `$148.50` → `14850`. The API accepts and returns prices as decimal numbers (e.g., `148.50`) with at most 2 decimal places; conversion happens at the boundary. Values with more than 2 decimal places are rejected. This avoids floating-point precision issues, keeps matching-engine comparisons as single-instruction integer ops, and follows the standard approach used by real exchanges.
- The `trades` array and `average_price` field are included in the response whenever trades were executed during submission. This is especially important for market orders where the execution price is unknown at submission time.
- **Broker validation**: `broker_id` must reference a registered broker (created via `POST /brokers`). If the broker does not exist, the order is rejected with `404 Not Found` and error `"broker_not_found"`.
- **Balance validation**: before any order is accepted, the engine checks the broker's *available* balance — not the total. For limit bids: `available_cash >= price × quantity`. For limit asks: `available_quantity >= quantity` for the given symbol. For market orders, see the Balance Validation section under Market Price Orders. If validation fails, the order is rejected with `409 Conflict` and error `"insufficient_balance"` (bids) or `"insufficient_holdings"` (asks).
- **Atomicity**: the matching engine processes one order at a time per symbol (single-threaded per symbol). Validation, reservation, and matching execute as a single atomic operation under the per-symbol write lock — no other order can modify the book between these steps. The same lock is shared with `DELETE /orders/{order_id}` and the order expiration process (see Order Expiration section). Broker balance fields are protected against concurrent cross-symbol mutations via a per-broker `sync.Mutex` (see Concurrency Model in the Matching Engine section).
- **Reservation**: when a limit order is accepted and placed on the book, the corresponding amount is reserved — cash for bids, shares for asks. This prevents over-commitment across concurrent orders. Reservations are released when orders fill, are cancelled, or expire.
- **No-liquidity rejection**: when a market order is rejected with `409 Conflict` (`no_liquidity`), no order record is created and no `order_id` is assigned. The rejection response is the only record of the attempt.

## GET /orders/{order_id}

Retrieve the current state of a previously submitted order, including all trades executed against it.

Response `200 OK` (limit order, partially filled):
```json
{
  "order_id": "ord-uuid",
  "type": "limit",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "price": 150.00,
  "quantity": 1000,
  "filled_quantity": 500,
  "remaining_quantity": 500,
  "cancelled_quantity": 0,
  "status": "partially_filled",
  "expires_at": "2026-02-20T18:00:00Z",
  "created_at": "2026-02-16T16:28:00Z",
  "cancelled_at": null,
  "expired_at": null,
  "average_price": 148.00,
  "trades": [
    {
      "trade_id": "trd-uuid",
      "price": 148.00,
      "quantity": 500,
      "executed_at": "2026-02-16T16:29:00Z"
    }
  ]
}
```

Response `200 OK` (limit order, pending — no fills yet):
```json
{
  "order_id": "ord-uuid",
  "type": "limit",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "ask",
  "symbol": "AAPL",
  "price": 155.00,
  "quantity": 1000,
  "filled_quantity": 0,
  "remaining_quantity": 1000,
  "cancelled_quantity": 0,
  "status": "pending",
  "expires_at": "2026-02-20T18:00:00Z",
  "created_at": "2026-02-16T16:28:00Z",
  "cancelled_at": null,
  "expired_at": null,
  "average_price": null,
  "trades": []
}
```

Response `200 OK` (limit order, cancelled — partial fills preserved):
```json
{
  "order_id": "ord-uuid",
  "type": "limit",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "price": 150.00,
  "quantity": 1000,
  "filled_quantity": 500,
  "remaining_quantity": 0,
  "cancelled_quantity": 500,
  "status": "cancelled",
  "expires_at": "2026-02-20T18:00:00Z",
  "created_at": "2026-02-16T16:28:00Z",
  "cancelled_at": "2026-02-17T10:15:00Z",
  "expired_at": null,
  "average_price": 148.00,
  "trades": [
    {
      "trade_id": "trd-uuid",
      "price": 148.00,
      "quantity": 500,
      "executed_at": "2026-02-16T16:29:00Z"
    }
  ]
}
```

Response `200 OK` (limit order, expired — partial fills preserved):
```json
{
  "order_id": "ord-uuid",
  "type": "limit",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "price": 150.00,
  "quantity": 1000,
  "filled_quantity": 500,
  "remaining_quantity": 0,
  "cancelled_quantity": 500,
  "status": "expired",
  "expires_at": "2026-02-20T18:00:00Z",
  "created_at": "2026-02-16T16:28:00Z",
  "cancelled_at": null,
  "expired_at": "2026-02-20T18:00:00Z",
  "average_price": 148.00,
  "trades": [
    {
      "trade_id": "trd-uuid",
      "price": 148.00,
      "quantity": 500,
      "executed_at": "2026-02-16T16:29:00Z"
    }
  ]
}
```

Response `200 OK` (market order, filled):
```json
{
  "order_id": "ord-uuid",
  "type": "market",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "quantity": 1000,
  "filled_quantity": 1000,
  "remaining_quantity": 0,
  "cancelled_quantity": 0,
  "status": "filled",
  "created_at": "2026-02-16T16:28:00Z",
  "average_price": 148.30,
  "trades": [
    { "trade_id": "trd-uuid-1", "price": 148.00, "quantity": 700, "executed_at": "2026-02-16T16:28:00Z" },
    { "trade_id": "trd-uuid-2", "price": 149.00, "quantity": 300, "executed_at": "2026-02-16T16:28:00Z" }
  ]
}
```

Response `404 Not Found`:
```json
{
  "error": "order_not_found",
  "message": "Order ord-nonexistent does not exist"
}
```

Key behaviors:
- The response shape varies by order type. Market orders omit `price`, `expires_at`, `cancelled_at`, and `expired_at` (they were never set and market orders resolve immediately). Limit orders always include `price` and `expires_at`, and always include `cancelled_at` and `expired_at` (`null` when not applicable).
- `cancelled_quantity` is always present. It is `0` for orders that have not been cancelled or expired. For cancelled/expired orders: `cancelled_quantity = quantity - filled_quantity`.
- `cancelled_at` is the timestamp when the order was cancelled via `DELETE /orders/{order_id}`. `null` for non-cancelled orders. Only applies to limit orders.
- `expired_at` is the timestamp when the order expired (equal to `expires_at`). `null` for non-expired orders. Only applies to limit orders.
- `average_price` is the weighted average across all trades: `sum(price × quantity) / sum(quantity)`. It is `null` when `trades` is empty.
- `trades` contains every trade executed against this order, in chronological order. Each trade reflects the execution price (always the ask/seller price — see the Matching Engine section), the quantity filled, and when it happened.
- Counterparty information is not exposed. Brokers see only their own side of each trade — this follows standard exchange practice to prevent information leakage between participants.

## DELETE /orders/{order_id}

Cancel a pending or partially filled order. Removes the unfilled portion from the order book and releases the associated reservation (cash for bids, shares for asks).

Responses:
- `200 OK` — order cancelled successfully. Returns the final order state.
- `404 Not Found` — no order exists with that ID.
- `409 Conflict` — order is already in a terminal state (`filled`, `cancelled`, or `expired`) and cannot be cancelled.

Response `200 OK` (cancelling a pending order — no fills):
```json
{
  "order_id": "ord-uuid",
  "type": "limit",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "price": 150.00,
  "quantity": 1000,
  "filled_quantity": 0,
  "remaining_quantity": 0,
  "cancelled_quantity": 1000,
  "status": "cancelled",
  "expires_at": "2026-02-20T18:00:00Z",
  "average_price": null,
  "trades": [],
  "created_at": "2026-02-16T16:28:00Z",
  "cancelled_at": "2026-02-17T10:15:00Z",
  "expired_at": null
}
```

Response `200 OK` (cancelling a partially filled order — trades preserved):
```json
{
  "order_id": "ord-uuid",
  "type": "limit",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "price": 150.00,
  "quantity": 1000,
  "filled_quantity": 500,
  "remaining_quantity": 0,
  "cancelled_quantity": 500,
  "status": "cancelled",
  "expires_at": "2026-02-20T18:00:00Z",
  "average_price": 148.00,
  "trades": [
    {
      "trade_id": "trd-uuid",
      "price": 148.00,
      "quantity": 500,
      "executed_at": "2026-02-16T16:29:00Z"
    }
  ],
  "created_at": "2026-02-16T16:28:00Z",
  "cancelled_at": "2026-02-17T10:15:00Z",
  "expired_at": null
}
```

Response `404 Not Found`:
```json
{
  "error": "order_not_found",
  "message": "Order ord-nonexistent does not exist"
}
```

Response `409 Conflict` (order already filled):
```json
{
  "error": "order_not_cancellable",
  "message": "Order ord-uuid is already filled and cannot be cancelled"
}
```

Response `409 Conflict` (order already cancelled):
```json
{
  "error": "order_not_cancellable",
  "message": "Order ord-uuid is already cancelled"
}
```

Response `409 Conflict` (order already expired):
```json
{
  "error": "order_not_cancellable",
  "message": "Order ord-uuid is already expired and cannot be cancelled"
}
```

Key behaviors:
- Only orders with status `pending` or `partially_filled` can be cancelled. Any terminal status (`filled`, `cancelled`, `expired`) returns `409 Conflict`.
- Market orders are never cancellable via this endpoint. They resolve immediately via IOC semantics and are always in a terminal state by the time the `POST /orders` response is returned. Attempting to cancel a market order will always return `409 Conflict`.
- On cancellation, `remaining_quantity` becomes `0` — nothing remains to be filled. The `cancelled_quantity` field indicates how much of the original `quantity` was cancelled (i.e., `quantity - filled_quantity`).
- The response returns the full final order state (same shape as `GET /orders/{order_id}`). This keeps the API consistent — consumers don't need a follow-up GET to see the result.
- Completed trades are preserved. Cancellation only affects the unfilled portion.
- Reservations are released on cancellation: for bid orders, the reserved cash for the unfilled portion (`price × cancelled_quantity`) is returned to `available_cash`. For ask orders, the reserved shares (`cancelled_quantity`) are returned to `available_quantity`.
- If the broker has a webhook subscription for `order.cancelled`, a notification is fired after successful cancellation. See the Webhook section for the payload format.

## Order Expiration

A background process runs on a fixed interval (every 1 second) to expire orders that have passed their `expires_at` time. This is an eager expiration model — expired orders are removed from the book proactively, not lazily on next access.

On each tick, the process scans for orders where `expires_at <= now` and status is `pending` or `partially_filled`. The scan uses a dedicated secondary index: a slice of pointers to active (on-book) orders, sorted by `expires_at` ascending. The expiration goroutine iterates from the front, processing all orders where `expires_at <= now`, and stops at the first order that has not yet expired. Orders are appended to this slice when placed on the book and removed when they leave the book (fill, cancel, or expire). This avoids scanning all orders or all books on every tick. For each expired order, the following happens atomically:

1. Status transitions to `expired`.
2. `cancelled_quantity` is set to the current `remaining_quantity`.
3. `remaining_quantity` becomes `0`.
4. `expired_at` is set to the order's `expires_at` value (not the wall-clock time when the process ran). This keeps the timestamp deterministic and consistent with what the broker originally set.
5. The order is removed from the book.
6. Reservations are released: for bid orders, `price × cancelled_quantity` is returned to `available_cash`. For ask orders, `cancelled_quantity` shares are returned to `available_quantity`.
7. If the broker has a webhook subscription for `order.expired`, the notification is fired.

The expiration process acquires the same per-symbol lock used by the matching engine. This guarantees mutual exclusion — an order cannot be matched and expired simultaneously. Expiration is just another writer competing for the same critical section as `POST /orders` and `DELETE /orders/{order_id}`.

Because expiration runs on a 1-second interval, there is a window of up to 1 second where an order past its `expires_at` may still be on the book and theoretically matchable. This is acceptable for this system's requirements. The invariant is: once the expiration process processes an order, it is atomically removed and no further matches can occur.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━


# Matching Engine

This section specifies the core matching algorithm. The API sections above define what goes in and what comes out; this section defines how the engine processes an incoming order against the order book.

All arithmetic in this section operates on the internal `int64` cents representation (see General API Conventions). When the spec writes `execution_price × fill_qty`, both operands are integers (cents and shares respectively) and the result is integer cents. No floating-point arithmetic occurs in the matching engine.

## Order Book Structure

The exchange maintains one order book per symbol. Each book has two sides:

- **Bid side (buy orders):** sorted by price **descending** (highest price first), then by `created_at` **ascending** (oldest first at the same price level).
- **Ask side (sell orders):** sorted by price **ascending** (lowest price first), then by `created_at` **ascending** (oldest first at the same price level).

This is a Central Limit Order Book (CLOB). The sorting defines price-time priority (also called FIFO within a price level): the best-priced order always matches first, and among orders at the same price, the one that arrived earliest matches first.

Only orders with status `pending` or `partially_filled` reside on the book. Terminal orders (`filled`, `cancelled`, `expired`) are never on the book.

### Data Structure

Each side of the book is implemented as a B-tree using `github.com/google/btree` (v2), with items keyed by `(price, created_at, order_id)`. This is the Go equivalent of C++ `std::map` — a balanced tree providing O(log n) insert, delete, and lookup with in-order iteration. B-trees additionally offer better CPU cache locality than red-black trees due to higher node fanout, which benefits the tight match loop.

Each side uses a single `btree.BTreeG[OrderBookEntry]` with a custom `Less` function:

- **Bid side:** compares by price descending, then `created_at` ascending, then `order_id` ascending (deterministic tiebreaker for orders with identical price and timestamp). The minimum entry (`Min()`) is the best bid.
- **Ask side:** compares by price ascending, then `created_at` ascending, then `order_id` ascending. The minimum entry (`Min()`) is the best ask.

To support O(log n) arbitrary removal by `order_id` (without scanning the tree), a secondary index `map[string]OrderBookEntry` maps `order_id → entry`. On cancel or expire, the entry is looked up in the map and then deleted from the B-tree by its composite key. Both structures are updated atomically under the per-symbol lock.

The `order_id` component in the key is a deterministic tiebreaker: if two orders arrive at the same price with the same `created_at` timestamp (possible at millisecond granularity under high throughput), the lexicographically smaller `order_id` (UUID) takes priority. This ensures the tree key is always unique and the sort order is fully deterministic.

## Matching Algorithm

### Price Compatibility

Two orders on opposite sides are price-compatible when:

```
bid.price >= ask.price
```

The buyer's price is the maximum they are willing to pay. The seller's price is the minimum they are willing to accept. A match occurs when these ranges overlap.

### Execution Price Rule

When two limit orders match, the execution price is always the **ask (seller) price**:

```
execution_price = ask.price
```

- If an incoming bid matches a resting ask: execution price = `resting_ask.price`.
- If an incoming ask matches a resting bid: execution price = `incoming_ask.price`.

The buyer always pays the seller's asking price. A buyer bidding $20 who matches a seller asking $10 pays $10. A seller asking $10 who matches a resting bid at $20 sells at $10 — the buyer gets a better price than they offered, and the seller gets exactly what they asked for.

When a **market order** is involved, the execution price is the **resting limit order's price** — the only stated price available. A market buy matching a resting ask pays the ask's price. A market sell matching a resting bid receives the bid's price. This is consistent with the ask-price rule: for a market buy, the resting ask is the seller and its price is used; for a market sell, the market order is the seller but has no price, so the resting bid's price (which the buyer offered) becomes the execution price. In both cases, the resting order's price determines the trade.

### Timestamp Assignment

`created_at` is assigned at the moment the order record is created — after validation passes and the reservation succeeds, but before the match loop begins. This is the timestamp used for price-time priority on the book.

All trades generated during a single matching pass share the same `executed_at` timestamp — the wall-clock time at the start of the match loop. This reflects that the entire matching pass is a single atomic operation.

### Step-by-Step Procedure: Incoming Limit Order

When `POST /orders` receives a new limit order, the matching engine executes the following steps under the per-symbol write lock (steps 1–6):

1. **Validate and reserve.** Validate the order (broker exists, balance sufficient, fields valid). Reserve the corresponding amount: `price × quantity` in cash for bids, `quantity` in shares for asks. If validation fails, reject the order — no order record is created. On success, create the order record: assign `order_id` (UUID), set `created_at` to the current timestamp, initialize `remaining_quantity = quantity`, `filled_quantity = 0`, `cancelled_quantity = 0`, `status = "pending"`.

2. **Determine the opposite side.** If the incoming order is a bid, the opposite side is the ask side. If it is an ask, the opposite side is the bid side.

3. **Match loop.** Record `executed_at` as the current timestamp (used for all trades in this pass). While the incoming order has `remaining_quantity > 0` and the opposite side is non-empty:

   a. Peek at the best order on the opposite side (first entry in the sorted map).

   b. **Check price compatibility:**
      - Incoming bid: match if `incoming.price >= best_ask.price`.
      - Incoming ask: match if `best_bid.price >= incoming.price`.
      - If not price-compatible, **stop** — no further matches are possible (the opposite side is sorted, so if the best price doesn't match, nothing behind it will either).

   c. **Compute fill quantity:** `fill_qty = min(incoming.remaining_quantity, resting.remaining_quantity)`.

   d. **Compute execution price:** If the incoming order is a bid, `execution_price = resting_ask.price`. If the incoming order is an ask, `execution_price = incoming_ask.price`. See Execution Price Rule above.

   e. **Execute the trade:**
      - Generate a `trade_id` (UUID).
      - Set `executed_at` to the timestamp recorded at the start of the match loop.
      - Reduce `remaining_quantity` by `fill_qty` on both orders.
      - Increase `filled_quantity` by `fill_qty` on both orders.
      - Update statuses: if `remaining_quantity == 0`, status → `filled`; otherwise status → `partially_filled`.
      - **Settle balances** (all amounts in integer cents):
        - The buying broker: decrease `cash_balance` by `execution_price × fill_qty`. Release the per-fill reservation: decrease `reserved_cash` by `bid_order.price × fill_qty`. The difference `(bid_order.price - execution_price) × fill_qty` returns to `available_cash` (this difference is zero when bid price equals ask price, positive when the buyer bid higher than the ask — this is price improvement). Increase holdings for the symbol by `fill_qty`.
        - The selling broker: increase `cash_balance` by `execution_price × fill_qty`. Decrease holdings for the symbol by `fill_qty`. Decrease `reserved_quantity` by `fill_qty` (release the per-fill reservation).
      - Append the trade to both orders' `trades` arrays.
      - If the resting order is fully filled (`remaining_quantity == 0`), remove it from the book.

   f. **Collect webhook events:** if either broker has a `trade.executed` subscription, enqueue the notification for post-lock dispatch. Do not send HTTP requests while holding the lock.

   g. **Continue** to the next iteration of the match loop.

4. **Rest or complete.** After the match loop exits:
   - If the incoming order has `remaining_quantity > 0`: it did not fully fill. Place it on the appropriate side of the book (bid side for bids, ask side for asks) with its current status (`pending` if no fills occurred, `partially_filled` if some fills occurred). The reservation for the unfilled portion remains active.
   - If `remaining_quantity == 0`: the order is fully filled (`status: "filled"`). It is not placed on the book. The reservation has been fully consumed by the trades.

5. **Compute `average_price`.** If `filled_quantity > 0`: `average_price = sum(trade.price × trade.quantity for each trade) / filled_quantity`, using integer division truncating toward zero, then converted from cents to decimal at the API boundary. The result is always rendered with exactly 2 decimal places in JSON (e.g., `148.60`, not `148.6`). If `filled_quantity == 0`: `average_price = null`.

6. **Release the per-symbol lock.** The order book is now consistent.

7. **Dispatch webhooks.** Send all enqueued webhook notifications (fire-and-forget HTTP POSTs). This happens outside the lock to avoid blocking the matching engine on network I/O.

8. **Return the order.** The `POST /orders` response includes the full order state: all trades executed during this matching pass, the current status, filled/remaining/cancelled quantities, and `average_price`.

### Step-by-Step Procedure: Incoming Market Order

Market orders follow the same procedure as limit orders (steps 1–8 above) with these differences:

- **Step 1 — Validation and reservation:** Market orders have no `price` or `expires_at`. Balance validation for market bids uses the simulation approach described in the Market Price Orders section. For market asks, validation checks `available_quantity >= quantity` (same as limit asks). On success, the order record is created with `type = "market"` and no `price` or `expires_at` fields.
- **Step 3b — No price compatibility check.** Market orders accept any price on the opposite side. This step is skipped entirely. The loop continues as long as the opposite side is non-empty and the incoming order has `remaining_quantity > 0`.
- **Step 3d — Execution price is the resting order's price.** For a market buy, `execution_price = resting_ask.price`. For a market sell, `execution_price = resting_bid.price`. The market order has no price of its own.
- **Step 4 — No resting; IOC cancellation.** If the market order has `remaining_quantity > 0` after the match loop, the remainder is immediately cancelled: `cancelled_quantity = remaining_quantity`, `remaining_quantity = 0`. Final status is `filled` if `filled_quantity == quantity`, otherwise `cancelled`. The order is never placed on the book.

See the Market Price Orders section for full details on IOC semantics, balance validation, and example flows.

## Concurrency Model

The matching engine processes one order at a time per symbol. A per-symbol `sync.RWMutex` ensures that:

- Only one `POST /orders` matching pass runs at a time for a given symbol (write lock).
- `DELETE /orders/{order_id}` acquires the same write lock before removing an order from the book.
- The order expiration process acquires the same write lock before expiring orders.
- Read-only endpoints (`GET /stocks/{symbol}/book`, `GET /stocks/{symbol}/quote`) acquire a read lock, allowing concurrent readers without blocking each other.

Different symbols are independent — orders for AAPL and GOOG can be processed concurrently.

This single-writer-per-symbol model eliminates race conditions in the matching algorithm: balance checks, reservation, matching, and settlement all execute atomically within the lock.

### Broker Balance Access

Broker balances (`cash_balance`, `reserved_cash`, holdings, `reserved_quantity`) are shared across symbols — a single broker can have active orders on multiple symbols. The per-symbol lock does not protect against concurrent balance mutations from different symbols.

Each broker has a `sync.Mutex`. The matching engine acquires it before check-and-reserve (step 1) and before each broker's balance mutation in settlement (step 3e). It is held only for the duration of that individual broker's balance update, not the entire matching pass. In step 3e, the buyer's mutex and the seller's mutex are acquired and released sequentially — never nested — so no lock ordering is required and deadlock is impossible.

The per-symbol lock remains the primary synchronization mechanism. The per-broker mutex only guards against concurrent cross-symbol mutations of the same broker's balance fields.

### Read Endpoints

- `GET /stocks/{symbol}/book` and `GET /stocks/{symbol}/quote`: acquire the per-symbol read lock. Guarantees a consistent book snapshot.
- `GET /brokers/{broker_id}/balance`: reads the broker's current balance fields. No symbol lock needed.
- `GET /orders/{order_id}`, `GET /brokers/{broker_id}/orders`, `GET /stocks/{symbol}/price`: no lock required. Order records are in a valid state outside of an in-progress matching operation, and trade history is append-only.

Webhook dispatch (step 7) and the HTTP response (step 8) happen after the per-symbol lock is released. Locks protect only data mutations — not I/O.

## Invariants

The following invariants hold at all times (outside of an in-progress atomic matching operation):

1. **No crossed book:** for every symbol, `best_bid.price < best_ask.price` (or one/both sides are empty). If the best bid were ≥ best ask, the match loop would have matched them.
2. **Quantity conservation:** for every order, `quantity == filled_quantity + remaining_quantity + cancelled_quantity`.
3. **Cash conservation:** across all brokers, `sum(cash_balance) == sum(initial_cash)`. Trades transfer cash between brokers; they do not create or destroy it. All cash arithmetic uses `int64` cents — no rounding errors accumulate.
4. **Holdings conservation:** for every symbol, `sum(quantity across all brokers) == sum(initial_quantity seeded via POST /brokers)`. Trades transfer shares; they do not create or destroy them.
5. **Reservation consistency:** `reserved_cash == sum(price × remaining_quantity)` across all active limit bid orders for that broker. `reserved_quantity == sum(remaining_quantity)` across all active ask orders for that broker and symbol. Market orders are never on the book and carry no reservation.
6. **No stale orders on book:** every order on the book has status `pending` or `partially_filled` and `expires_at > now` (within the expiration process's 1-second granularity).
7. **Deterministic ordering:** the sorted map key `(price, created_at, order_id)` guarantees a total order — no two entries share the same key, and the sort is fully deterministic regardless of insertion order.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━


# Extension Endpoints

## 1. GET /stocks/{symbol}/price — Current Stock Price

Returns the current reference price for a symbol, computed as the volume-weighted average price (VWAP) of trades executed in the last 5 minutes. The window is fixed — not a query parameter.

Response `200 OK` (trades exist in window):
```json
{
  "symbol": "AAPL",
  "current_price": 149.50,
  "window": "5m",
  "trades_in_window": 12,
  "last_trade_at": "2026-02-16T16:30:00Z"
}
```

Response `200 OK` (no trades in window, fallback to last known trade):
```json
{
  "symbol": "AAPL",
  "current_price": 148.00,
  "window": "5m",
  "trades_in_window": 0,
  "last_trade_at": "2026-02-16T15:12:00Z"
}
```

Response `200 OK` (no trades have ever occurred for this symbol):
```json
{
  "symbol": "AAPL",
  "current_price": null,
  "window": "5m",
  "trades_in_window": 0,
  "last_trade_at": null
}
```

Key behaviors:
- `current_price` is a VWAP: `sum(price × quantity) / sum(quantity)` over trades in the window.
- If no trades exist in the 5-minute window, falls back to the execution price of the most recent trade for this symbol, regardless of when it occurred. `trades_in_window` will be `0` and `last_trade_at` reflects when that trade was executed.
- If no trades have ever occurred for the symbol, `current_price` and `last_trade_at` are `null`.
- The `window` field is informational metadata — it tells the consumer how the price was computed.
- Returns `404 Not Found` if the symbol does not exist in the system. A symbol "exists" once it has appeared in any submitted order (regardless of whether that order was filled, cancelled, or expired) or in a broker's `initial_holdings` during registration via `POST /brokers`. Symbols are implicitly registered through these paths — there is no separate symbol creation endpoint.

## 2. GET /stocks/{symbol}/book — Order Book (Top of Book)

Returns the top N price levels of resting limit orders (bids and asks) for a given symbol. Only orders with status `pending` or `partially_filled` are included. Market orders are never on the book and do not appear here.

Query params: `?depth=10` (optional, default 10, max 50, must be ≥ 1. Returns `400 Bad Request` for invalid values.)

Response `200 OK`:
```json
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
  "spread": 1.00,
  "snapshot_at": "2026-02-17T14:30:00Z"
}
```

Response `200 OK` (one or both sides empty):
```json
{
  "symbol": "AAPL",
  "bids": [],
  "asks": [
    { "price": 151.00, "total_quantity": 2000, "order_count": 3 }
  ],
  "spread": null,
  "snapshot_at": "2026-02-17T14:30:00Z"
}
```

Response `404 Not Found`:
```json
{
  "error": "symbol_not_found",
  "message": "Symbol XYZZ is not listed on this exchange"
}
```

Key behaviors:
- Bids sorted descending by price, asks ascending — standard L2 order book representation.
- Aggregated by price level: shows total quantity and number of orders at each level, not individual orders (L2 data — individual order visibility would be L3 and is a security concern).
- `spread` = best ask − best bid. `null` if either side of the book is empty.
- `snapshot_at` reflects when the book state was read. The book is a live structure; this timestamp gives consumers staleness awareness.
- Returns `404 Not Found` if the symbol has never been seen in any order submission or in a broker's `initial_holdings` during registration.

## 3. GET /brokers/{broker_id}/balance — Broker Balance

Returns the current financial state of a broker: cash position and stock holdings.

Response `200 OK`:
```json
{
  "broker_id": "broker-123",
  "cash_balance": 1000000.00,
  "reserved_cash": 150000.00,
  "available_cash": 850000.00,
  "holdings": [
    { "symbol": "AAPL", "quantity": 5000, "reserved_quantity": 1000, "available_quantity": 4000 },
    { "symbol": "GOOG", "quantity": 200, "reserved_quantity": 0, "available_quantity": 200 }
  ],
  "updated_at": "2026-02-17T14:30:00Z"
}
```

Response `404 Not Found`:
```json
{
  "error": "broker_not_found",
  "message": "Broker broker-999 does not exist"
}
```

Key behaviors:
- `cash_balance` = total cash the broker owns. `reserved_cash` = cash locked by active bid orders (`pending` or `partially_filled`). `available_cash` = `cash_balance - reserved_cash` — this is what the broker can use to place new bid orders.
- `quantity` = total shares held. `reserved_quantity` = shares locked by active ask orders. `available_quantity` = `quantity - reserved_quantity`.
- `POST /orders` validates against `available_cash` and `available_quantity`, not the totals — see the balance validation and reservation rules in that endpoint's key behaviors.
- Brokers are created via `POST /brokers` with an initial cash deposit and optional stock holdings. After registration, there are no deposit or withdraw endpoints — balances change exclusively through trade execution and order reservation/release.
- When a trade executes: buying increases holdings and decreases cash; selling decreases holdings and increases cash. Reservations are released as orders fill, cancel, or expire.
- `updated_at` reflects the last time this broker's balance changed (trade execution, order placement, cancellation, or expiration). Initialized to the broker's `created_at` timestamp on registration.

## 3.1. GET /brokers/{broker_id}/orders — Broker Order Listing

Returns a paginated list of orders belonging to a broker, with optional status filtering. Separated from the balance endpoint to keep concerns clean — balance is financial state, this is order management.

Full example: `GET /brokers/broker-123/orders?status=pending&page=2&limit=10`

Query parameters:

| Param    | Required | Default | Rules                                                                                                |
|----------|----------|---------|------------------------------------------------------------------------------------------------------|
| `status` | No       | (all)   | One of: `pending`, `partially_filled`, `filled`, `cancelled`, `expired`. Omit to return all statuses. |
| `page`   | No       | `1`     | Integer, must be ≥ 1.                                                                                |
| `limit`  | No       | `20`    | Integer, must be ≥ 1 and ≤ 100.                                                                     |

Response `200 OK` (pending limit orders):
```json
{
  "orders": [
    {
      "order_id": "ord-uuid-1",
      "type": "limit",
      "document_number": "12345678900",
      "symbol": "AAPL",
      "side": "bid",
      "price": 150.00,
      "quantity": 1000,
      "filled_quantity": 0,
      "remaining_quantity": 1000,
      "cancelled_quantity": 0,
      "status": "pending",
      "average_price": null,
      "created_at": "2026-02-16T16:28:00Z"
    },
    {
      "order_id": "ord-uuid-2",
      "type": "limit",
      "document_number": "98765432100",
      "symbol": "GOOG",
      "side": "ask",
      "price": 2800.00,
      "quantity": 200,
      "filled_quantity": 0,
      "remaining_quantity": 200,
      "cancelled_quantity": 0,
      "status": "pending",
      "average_price": null,
      "created_at": "2026-02-16T16:30:00Z"
    }
  ],
  "total": 2,
  "page": 1,
  "limit": 20
}
```

Response `200 OK` (mixed statuses, includes a cancelled market order — `?page=1&limit=20`):
```json
{
  "orders": [
    {
      "order_id": "ord-uuid-3",
      "type": "market",
      "document_number": "12345678900",
      "symbol": "AAPL",
      "side": "bid",
      "quantity": 500,
      "filled_quantity": 300,
      "remaining_quantity": 0,
      "cancelled_quantity": 200,
      "status": "cancelled",
      "average_price": 149.00,
      "created_at": "2026-02-16T17:00:00Z"
    },
    {
      "order_id": "ord-uuid-4",
      "type": "limit",
      "document_number": "98765432100",
      "symbol": "AAPL",
      "side": "ask",
      "price": 155.00,
      "quantity": 1000,
      "filled_quantity": 500,
      "remaining_quantity": 500,
      "cancelled_quantity": 0,
      "status": "partially_filled",
      "average_price": 155.00,
      "created_at": "2026-02-16T16:45:00Z"
    }
  ],
  "total": 2,
  "page": 1,
  "limit": 20
}
```

Response `200 OK` (no orders match the filter — `?status=expired`):
```json
{
  "orders": [],
  "total": 0,
  "page": 1,
  "limit": 20
}
```

Response `404 Not Found` (broker does not exist):
```json
{
  "error": "broker_not_found",
  "message": "Broker broker-999 does not exist"
}
```

Response `400 Bad Request` (invalid query parameter):
```json
{
  "error": "validation_error",
  "message": "Invalid status filter: 'open'. Must be one of: pending, partially_filled, filled, cancelled, expired"
}
```

Key behaviors:
- Orders are returned in reverse chronological order (`created_at` descending) — most recent first.
- `total` is the total number of orders matching the current filter (not just the current page). This allows consumers to compute total pages: `ceil(total / limit)`. This is an offset-based pagination model — acceptable for the expected data volumes in this system.
- Each order object is a summary view. It does not include the `trades` array — use `GET /orders/{order_id}` for the full order with trade details. This keeps the list response lightweight.
- The summary view includes these fields for every order: `order_id`, `type`, `document_number`, `symbol`, `side`, `quantity`, `filled_quantity`, `remaining_quantity`, `cancelled_quantity`, `status`, `created_at`.
- Conditional fields by order type: limit orders always include `price`; market orders omit it. `average_price` is always present; it is `null` when no fills exist (`filled_quantity == 0`).
- `cancelled_quantity` is included on every order for consistency. It is `0` for orders that were never cancelled.
- `broker_id` must reference a registered broker. Returns `404 Not Found` with error `"broker_not_found"` if the broker does not exist — consistent with `GET /brokers/{broker_id}/balance`.

## 4. Webhook — Event Notifications

Brokers can subscribe to event notifications. When a subscribed event occurs, the system POSTs a payload to the broker's pre-registered webhook URL.

### Registration: `POST /webhooks`

Register one or more webhook subscriptions for a broker. Uses upsert semantics — creating new subscriptions or updating existing ones in a single call.

Request body:
```json
{
  "broker_id": "broker-123",
  "url": "https://broker-system.example.com/trade-notifications",
  "events": ["trade.executed", "order.expired", "order.cancelled"]
}
```

Validation rules:

| Field       | Rule                                                                                                  |
|-------------|-------------------------------------------------------------------------------------------------------|
| `broker_id` | Required. Must reference a registered broker (created via `POST /brokers`).                           |
| `url`       | Required. Must be a valid absolute URL with `https` scheme. Max 2048 characters.                      |
| `events`    | Required. Non-empty array. Each element must be one of: `trade.executed`, `order.expired`, `order.cancelled`. Duplicates within the array are ignored (deduplicated, not rejected). |

Upsert semantics:
- The unique key is `(broker_id, event)`. Each broker has at most one URL per event type.
- If a subscription already exists for that broker + event, the URL is updated and `updated_at` is set.
- Re-registering the same URL for the same event is a no-op (idempotent) — the existing subscription is returned unchanged.
- The `webhook_id` is stable: updating the URL of an existing subscription does not change its `webhook_id`.

Response code logic:
- `201 Created` — at least one new subscription was created (regardless of whether others in the same request were updates).
- `200 OK` — all subscriptions in the request already existed (URL updated or identical re-registration). No new subscriptions were created.
- `404 Not Found` — broker does not exist.
- `400 Bad Request` — missing required fields, invalid URL format, empty events array, or unknown event type.

Response `201 Created` (all new subscriptions):
```json
{
  "webhooks": [
    {
      "webhook_id": "wh-uuid-1",
      "broker_id": "broker-123",
      "event": "trade.executed",
      "url": "https://broker-system.example.com/trade-notifications",
      "created_at": "2026-02-17T19:00:00Z",
      "updated_at": "2026-02-17T19:00:00Z"
    },
    {
      "webhook_id": "wh-uuid-2",
      "broker_id": "broker-123",
      "event": "order.expired",
      "url": "https://broker-system.example.com/trade-notifications",
      "created_at": "2026-02-17T19:00:00Z",
      "updated_at": "2026-02-17T19:00:00Z"
    },
    {
      "webhook_id": "wh-uuid-3",
      "broker_id": "broker-123",
      "event": "order.cancelled",
      "url": "https://broker-system.example.com/trade-notifications",
      "created_at": "2026-02-17T19:00:00Z",
      "updated_at": "2026-02-17T19:00:00Z"
    }
  ]
}
```

Response `201 Created` (mix — 2 new, 1 updated URL):
```json
{
  "webhooks": [
    {
      "webhook_id": "wh-uuid-1",
      "broker_id": "broker-123",
      "event": "trade.executed",
      "url": "https://new-url.example.com/notifications",
      "created_at": "2026-02-16T16:00:00Z",
      "updated_at": "2026-02-17T19:00:00Z"
    },
    {
      "webhook_id": "wh-uuid-4",
      "broker_id": "broker-123",
      "event": "order.expired",
      "url": "https://new-url.example.com/notifications",
      "created_at": "2026-02-17T19:00:00Z",
      "updated_at": "2026-02-17T19:00:00Z"
    },
    {
      "webhook_id": "wh-uuid-5",
      "broker_id": "broker-123",
      "event": "order.cancelled",
      "url": "https://new-url.example.com/notifications",
      "created_at": "2026-02-17T19:00:00Z",
      "updated_at": "2026-02-17T19:00:00Z"
    }
  ]
}
```

Response `200 OK` (all subscriptions already existed with same URL — idempotent no-op):
```json
{
  "webhooks": [
    {
      "webhook_id": "wh-uuid-1",
      "broker_id": "broker-123",
      "event": "trade.executed",
      "url": "https://broker-system.example.com/trade-notifications",
      "created_at": "2026-02-16T16:00:00Z",
      "updated_at": "2026-02-16T16:00:00Z"
    }
  ]
}
```

Response `200 OK` (existing subscription, URL changed):
```json
{
  "webhooks": [
    {
      "webhook_id": "wh-uuid-1",
      "broker_id": "broker-123",
      "event": "trade.executed",
      "url": "https://new-url.example.com/notifications",
      "created_at": "2026-02-16T16:00:00Z",
      "updated_at": "2026-02-17T19:00:00Z"
    }
  ]
}
```

Response `404 Not Found` (unregistered broker):
```json
{
  "error": "broker_not_found",
  "message": "Broker broker-999 does not exist"
}
```

Response `400 Bad Request` (missing required field):
```json
{
  "error": "validation_error",
  "message": "url is required"
}
```

Response `400 Bad Request` (invalid URL scheme):
```json
{
  "error": "validation_error",
  "message": "url must use https scheme"
}
```

Response `400 Bad Request` (empty events array):
```json
{
  "error": "validation_error",
  "message": "events must be a non-empty array"
}
```

Response `400 Bad Request` (unknown event type):
```json
{
  "error": "validation_error",
  "message": "Unknown event type: trade.matched. Must be one of: trade.executed, order.expired, order.cancelled"
}
```

### List subscriptions: `GET /webhooks?broker_id=broker-123`

Returns all webhook subscriptions for a broker.

Responses:
- `200 OK` — returns the list (empty array if no subscriptions exist).
- `400 Bad Request` — missing `broker_id` query parameter.
- `404 Not Found` — broker does not exist.

Response `200 OK`:
```json
{
  "webhooks": [
    {
      "webhook_id": "wh-uuid-1",
      "broker_id": "broker-123",
      "event": "trade.executed",
      "url": "https://broker-system.example.com/trade-notifications",
      "created_at": "2026-02-16T16:00:00Z",
      "updated_at": "2026-02-16T16:00:00Z"
    },
    {
      "webhook_id": "wh-uuid-2",
      "broker_id": "broker-123",
      "event": "order.expired",
      "url": "https://broker-system.example.com/trade-notifications",
      "created_at": "2026-02-16T16:00:00Z",
      "updated_at": "2026-02-17T10:00:00Z"
    }
  ]
}
```

Response `200 OK` (no subscriptions):
```json
{
  "webhooks": []
}
```

Response `400 Bad Request` (missing broker_id):
```json
{
  "error": "validation_error",
  "message": "broker_id query parameter is required"
}
```

Response `404 Not Found` (unregistered broker):
```json
{
  "error": "broker_not_found",
  "message": "Broker broker-999 does not exist"
}
```

### Delete subscription: `DELETE /webhooks/{webhook_id}`

Removes a single webhook subscription.

Response `204 No Content` — subscription deleted successfully. No response body.

Response `404 Not Found`:
```json
{
  "error": "webhook_not_found",
  "message": "Webhook wh-nonexistent does not exist"
}
```

### Webhook delivery payloads (sent to the broker's URL)

When a subscribed event occurs, the system sends an HTTP POST to the broker's registered URL.

Headers included in every delivery:
- `Content-Type: application/json`
- `X-Delivery-Id`: A unique UUID for this specific delivery attempt. Allows consumers to deduplicate notifications.
- `X-Webhook-Id`: The webhook subscription ID that triggered this delivery.
- `X-Event-Type`: The event type (e.g., `trade.executed`).

#### `trade.executed`

Fired when a trade is matched and executed against the broker's order. Each trade generates a separate notification. If a single order matches against multiple resting orders (e.g., a market order sweeping multiple price levels), the broker receives one `trade.executed` notification per trade.

The payload contains the trade details and the resulting order state after the trade:

```json
{
  "event": "trade.executed",
  "timestamp": "2026-02-16T16:29:00Z",
  "data": {
    "trade_id": "trd-uuid",
    "broker_id": "broker-123",
    "order_id": "ord-uuid",
    "symbol": "AAPL",
    "side": "bid",
    "trade_price": 148.00,
    "trade_quantity": 500,
    "order_status": "partially_filled",
    "order_filled_quantity": 500,
    "order_remaining_quantity": 500
  }
}
```

`trade.executed` — order fully filled by this trade:
```json
{
  "event": "trade.executed",
  "timestamp": "2026-02-16T16:29:00Z",
  "data": {
    "trade_id": "trd-uuid",
    "broker_id": "broker-123",
    "order_id": "ord-uuid",
    "symbol": "AAPL",
    "side": "bid",
    "trade_price": 148.00,
    "trade_quantity": 1000,
    "order_status": "filled",
    "order_filled_quantity": 1000,
    "order_remaining_quantity": 0
  }
}
```

#### `order.expired`

Fired when a limit order reaches its `expires_at` time without being fully filled. The unfilled portion is removed from the book and the reservation is released.

```json
{
  "event": "order.expired",
  "timestamp": "2026-02-17T18:00:00Z",
  "data": {
    "broker_id": "broker-123",
    "order_id": "ord-uuid",
    "symbol": "AAPL",
    "side": "bid",
    "price": 150.00,
    "quantity": 1000,
    "filled_quantity": 500,
    "cancelled_quantity": 500,
    "remaining_quantity": 0,
    "status": "expired"
  }
}
```

`order.expired` — no fills before expiration:
```json
{
  "event": "order.expired",
  "timestamp": "2026-02-17T18:00:00Z",
  "data": {
    "broker_id": "broker-123",
    "order_id": "ord-uuid",
    "symbol": "AAPL",
    "side": "ask",
    "price": 200.00,
    "quantity": 1000,
    "filled_quantity": 0,
    "cancelled_quantity": 1000,
    "remaining_quantity": 0,
    "status": "expired"
  }
}
```

#### `order.cancelled`

Fired when a limit order is cancelled via `DELETE /orders/{order_id}`. Market orders are excluded — their IOC cancellation is already reflected in the synchronous `POST /orders` response, so a webhook would be redundant.

```json
{
  "event": "order.cancelled",
  "timestamp": "2026-02-17T10:15:00Z",
  "data": {
    "broker_id": "broker-123",
    "order_id": "ord-uuid",
    "symbol": "AAPL",
    "side": "ask",
    "price": 155.00,
    "quantity": 1000,
    "filled_quantity": 0,
    "cancelled_quantity": 1000,
    "remaining_quantity": 0,
    "status": "cancelled"
  }
}
```

`order.cancelled` — partially filled order cancelled (trades already executed are preserved):
```json
{
  "event": "order.cancelled",
  "timestamp": "2026-02-17T10:15:00Z",
  "data": {
    "broker_id": "broker-123",
    "order_id": "ord-uuid",
    "symbol": "AAPL",
    "side": "bid",
    "price": 150.00,
    "quantity": 1000,
    "filled_quantity": 400,
    "cancelled_quantity": 600,
    "remaining_quantity": 0,
    "status": "cancelled"
  }
}
```

### Key behaviors:
- **Fire-and-forget**: the exchange POSTs to the broker's URL and does not wait for or depend on the response. A non-2xx response or network error is silently ignored — no retries.
- **Both sides of a trade get notified independently**: when a trade executes between broker A and broker B, each broker receives their own `trade.executed` notification (with their own `order_id`, `side`, etc.) if they have a subscription for that event.
- **One notification per trade**: a single order that matches against N resting orders produces N trades and N separate `trade.executed` notifications.
- **Delivery order**: notifications are sent in the order events occur. For a market order that sweeps multiple price levels, the `trade.executed` notifications are sent in the same order the trades were matched (price-time priority).
- **Market order IOC cancellations do not trigger webhooks**: the `POST /orders` response already contains the full outcome (fills, cancelled quantity, final status). Sending a redundant `order.cancelled` notification would be noise. The `order.cancelled` webhook fires only for limit orders cancelled via `DELETE /orders/{order_id}`.
- **Webhook subscriptions are independent of order lifecycle**: subscribing or unsubscribing does not affect existing orders. A broker who unsubscribes mid-order simply stops receiving notifications for subsequent events on that order.

## 5. Market Price Orders

Market orders execute immediately at the best available price on the opposite side of the book. They use **IOC (Immediate or Cancel) semantics**: fill what is available right now, cancel the unfilled remainder. Market orders are never placed on the book.

### Order Type

Market orders are submitted via the same `POST /orders` endpoint using `"type": "market"`. See the `POST /orders` section above for request/response schemas and validation rules.

### Matching Rules

When a market order arrives, the matching engine walks the opposite side of the book in price-time priority:

- A market **buy** matches against asks, starting from the **lowest** ask price and moving upward.
- A market **sell** matches against bids, starting from the **highest** bid price and moving downward.
- At each price level, orders are matched in **chronological order** (oldest first), same as limit orders.
- The execution price for each fill is the **resting limit order's price** (the order already on the book). Market orders have no price of their own. For a market buy, the resting ask's price is used. For a market sell, the resting bid's price is used. Note: the general Execution Price Rule (execution price = ask/seller price) applies to limit-vs-limit matching where both sides have a stated price. For market orders, the resting order's price is the only price available and determines the execution price.
- A market order can sweep multiple price levels in a single submission.

### IOC Semantics

Market orders follow Immediate or Cancel (IOC) behavior:

- The order fills as much as possible against the current book state at the moment of submission.
- Any unfilled remainder is **immediately cancelled** — it is never placed on the book.
- The order terminates in one of three outcomes:
  - `status: "filled"` — fully filled against available liquidity.
  - `status: "cancelled"` — partially filled, remainder cancelled due to insufficient liquidity. The `cancelled_quantity` field indicates how much was cancelled.
  - `409 Conflict` with `"error": "no_liquidity"` — no liquidity at all on the opposite side; the order is rejected entirely and no order record is created.

Because market orders are never placed on the book, two market orders on opposite sides can never match each other. A market order only ever matches against resting limit orders.

### Market-Specific Validation Errors

Market orders that include fields reserved for limit orders are rejected:

Response `400 Bad Request` (market order includes `price`):
```json
{
  "error": "validation_error",
  "message": "price must be null or omitted for market orders"
}
```

Response `400 Bad Request` (market order includes `expires_at`):
```json
{
  "error": "validation_error",
  "message": "expires_at must be null or omitted for market orders"
}
```

### Balance Validation

For limit orders, balance validation is straightforward: check `available_cash >= price × quantity` (bids) or `available_quantity >= quantity` (asks). See `POST /orders` key behaviors for details.

For market orders, the price is unknown upfront. Validation depends on the side:

- **Market bids (buy)**: the engine **simulates the fill against the current book state** — walks the ask side, accumulating `price × quantity` at each level the order would sweep. The simulation only considers liquidity actually available on the book: if the order requests 1000 shares but only 400 are on the ask side, the estimated cost covers only those 400 shares (the remainder will be IOC-cancelled after matching, not pre-validated). Checks that the broker's `available_cash` covers the total estimated cost. If not, rejects with `409 Conflict` and error `"insufficient_balance"`.
- **Market asks (sell)**: the quantity is known upfront (same as limit asks). Checks that the broker's `available_quantity` for the symbol covers the requested quantity. If not, rejects with `409 Conflict` and error `"insufficient_holdings"`.

Note: the actual execution prices are determined during matching, which happens immediately after validation. Both validation and matching execute as a single atomic operation (see the Atomicity constraint in `POST /orders` key behaviors). This guarantees the validation result is accurate.

### Example Flows

#### Buy-side: Full fill

Starting book state:
| Side | Price  | Quantity | Order Time |
|------|--------|----------|------------|
| Ask  | $10.00 | 100      | 09:00:00   |
| Ask  | $11.00 | 200      | 09:01:00   |
| Ask  | $12.00 | 50       | 09:02:00   |

1. Broker submits: **Market Buy 250 AAPL**
2. Engine walks the ask side lowest-first:
   - Fills 100 @ $10.00 (sweeps the entire $10.00 level)
   - Fills 150 @ $11.00 (partially fills the $11.00 level)
3. Result: order fully filled. Total cost: $100×10 + $150×11 = $2,650. Average price: $10.60.
4. Remaining book: 50 Ask @ $11.00, 50 Ask @ $12.00.

Response `201 Created`:
```json
{
  "order_id": "ord-uuid",
  "type": "market",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "quantity": 250,
  "filled_quantity": 250,
  "remaining_quantity": 0,
  "cancelled_quantity": 0,
  "status": "filled",
  "created_at": "2026-02-17T09:05:00Z",
  "average_price": 10.60,
  "trades": [
    { "trade_id": "trd-uuid-1", "price": 10.00, "quantity": 100, "executed_at": "2026-02-17T09:05:00Z" },
    { "trade_id": "trd-uuid-2", "price": 11.00, "quantity": 150, "executed_at": "2026-02-17T09:05:00Z" }
  ]
}
```

#### Buy-side: Partial fill (same book, requesting 400)

1. Broker submits: **Market Buy 400 AAPL**
2. Engine walks the ask side: fills 100 @ $10.00, 200 @ $11.00, 50 @ $12.00 = 350 filled.
3. 50 remaining, no more asks on the book. IOC cancels the remainder.

Response `201 Created`:
```json
{
  "order_id": "ord-uuid",
  "type": "market",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "bid",
  "symbol": "AAPL",
  "quantity": 400,
  "filled_quantity": 350,
  "remaining_quantity": 0,
  "cancelled_quantity": 50,
  "status": "cancelled",
  "created_at": "2026-02-17T09:05:00Z",
  "average_price": 10.86,
  "trades": [
    { "trade_id": "trd-uuid-1", "price": 10.00, "quantity": 100, "executed_at": "2026-02-17T09:05:00Z" },
    { "trade_id": "trd-uuid-2", "price": 11.00, "quantity": 200, "executed_at": "2026-02-17T09:05:00Z" },
    { "trade_id": "trd-uuid-3", "price": 12.00, "quantity": 50, "executed_at": "2026-02-17T09:05:00Z" }
  ]
}
```

#### Buy-side: No liquidity (empty book)

1. Broker submits: **Market Buy 100 AAPL** (no asks on the book)

Response `409 Conflict`:
```json
{
  "error": "no_liquidity",
  "message": "No matching orders available for market order on AAPL"
}
```

No order record is created.

#### Sell-side: Full fill

Starting book state:
| Side | Price  | Quantity | Order Time |
|------|--------|----------|------------|
| Bid  | $50.00 | 300      | 09:00:00   |
| Bid  | $49.00 | 200      | 09:01:00   |

1. Broker submits: **Market Sell 400 AAPL**
2. Engine walks the bid side highest-first:
   - Fills 300 @ $50.00 (sweeps the entire $50.00 level)
   - Fills 100 @ $49.00 (partially fills the $49.00 level)
3. Result: order fully filled. Total proceeds: $300×50 + $100×49 = $19,900. Average price: $49.75.

Response `201 Created`:
```json
{
  "order_id": "ord-uuid",
  "type": "market",
  "broker_id": "broker-123",
  "document_number": "12345678900",
  "side": "ask",
  "symbol": "AAPL",
  "quantity": 400,
  "filled_quantity": 400,
  "remaining_quantity": 0,
  "cancelled_quantity": 0,
  "status": "filled",
  "created_at": "2026-02-17T09:05:00Z",
  "average_price": 49.75,
  "trades": [
    { "trade_id": "trd-uuid-1", "price": 50.00, "quantity": 300, "executed_at": "2026-02-17T09:05:00Z" },
    { "trade_id": "trd-uuid-2", "price": 49.00, "quantity": 100, "executed_at": "2026-02-17T09:05:00Z" }
  ]
}
```

## 5.1. GET /stocks/{symbol}/quote — Market Order Quote

Simulates a market order execution against the current book without placing an order. Allows brokers to preview the estimated cost (bids) or proceeds (asks) and available liquidity before submitting a market order.

This is a read-only snapshot — not a reservation. The book can change between the quote and actual order submission.

Query parameters:

| Param      | Required | Rules                                                                                     |
|------------|----------|-------------------------------------------------------------------------------------------|
| `side`     | Yes      | Must be `bid` or `ask`. Refers to the **order side** (what the broker wants to do): `bid` = buy (walks the ask side of the book), `ask` = sell (walks the bid side of the book). |
| `quantity` | Yes      | Integer, must be > 0.                                                                     |

Response `200 OK` (bid quote — full liquidity available):
```json
{
  "symbol": "AAPL",
  "side": "bid",
  "quantity_requested": 1000,
  "quantity_available": 1000,
  "fully_fillable": true,
  "estimated_average_price": 148.60,
  "estimated_total": 148600.00,
  "price_levels": [
    { "price": 148.00, "quantity": 700 },
    { "price": 150.00, "quantity": 300 }
  ],
  "quoted_at": "2026-02-17T14:05:00Z"
}
```

Response `200 OK` (bid quote — partial liquidity):
```json
{
  "symbol": "AAPL",
  "side": "bid",
  "quantity_requested": 1000,
  "quantity_available": 400,
  "fully_fillable": false,
  "estimated_average_price": 148.00,
  "estimated_total": 59200.00,
  "price_levels": [
    { "price": 148.00, "quantity": 400 }
  ],
  "quoted_at": "2026-02-17T14:05:00Z"
}
```

Response `200 OK` (bid quote — no liquidity):
```json
{
  "symbol": "AAPL",
  "side": "bid",
  "quantity_requested": 1000,
  "quantity_available": 0,
  "fully_fillable": false,
  "estimated_average_price": null,
  "estimated_total": null,
  "price_levels": [],
  "quoted_at": "2026-02-17T14:05:00Z"
}
```

Response `200 OK` (ask quote — full liquidity, walks bid side):
```json
{
  "symbol": "AAPL",
  "side": "ask",
  "quantity_requested": 500,
  "quantity_available": 500,
  "fully_fillable": true,
  "estimated_average_price": 149.20,
  "estimated_total": 74600.00,
  "price_levels": [
    { "price": 150.00, "quantity": 300 },
    { "price": 148.00, "quantity": 200 }
  ],
  "quoted_at": "2026-02-17T14:05:00Z"
}
```

Response `404 Not Found`:
```json
{
  "error": "symbol_not_found",
  "message": "Symbol XYZZ is not listed on this exchange"
}
```

Response `400 Bad Request` (missing required param):
```json
{
  "error": "validation_error",
  "message": "side query parameter is required"
}
```

Response `400 Bad Request` (invalid side):
```json
{
  "error": "validation_error",
  "message": "Invalid side: 'buy'. Must be one of: bid, ask"
}
```

Response `400 Bad Request` (invalid quantity):
```json
{
  "error": "validation_error",
  "message": "quantity must be a positive integer"
}
```

Key behaviors:
- `side` uses the same semantics as `POST /orders`: `bid` means the broker wants to buy, so the engine walks the **ask** side of the book (lowest price first). `ask` means the broker wants to sell, so the engine walks the **bid** side (highest price first).
- `estimated_total` is side-neutral: for bids it represents the estimated cost; for asks it represents the estimated proceeds. Computed as `sum(price × quantity)` across the price levels that would be swept.
- `estimated_average_price` = `estimated_total / quantity_available`. `null` when `quantity_available` is `0`.
- `price_levels` are ordered in the same direction the engine would walk: ascending for bids (cheapest asks first), descending for asks (most expensive bids first).
- `quantity_available` reflects how much of the requested quantity can actually be filled against the current book. It is ≤ `quantity_requested`.
- This endpoint does not check broker balances — it is a pure book simulation. Balance validation happens at `POST /orders` submission time.
- Returns `404 Not Found` if the symbol has never been seen in any order submission — consistent with `GET /stocks/{symbol}/book`.

