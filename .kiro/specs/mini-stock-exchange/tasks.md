# Implementation Plan: Mini Stock Exchange

## Overview

Build a complete mini Stock Exchange system in Go, following the system design spec at `design-documents/system-design-spec.md` as the source of truth. The implementation proceeds bottom-up: domain types → stores → engine → services → handlers → main.go → Docker/Makefile. Each task builds on previous tasks with no orphaned code.

## Tasks

- [x] 1. Project scaffolding and domain types
  - [x] 1.1 Initialize Go module and project structure
    - Create `go.mod` with module `github.com/efreitasn/miniexchange` and Go 1.23
    - Run `go get` for dependencies: `github.com/google/btree`, `github.com/google/uuid`, `github.com/go-chi/chi/v5`, `pgregory.net/rapid`
    - Create directory structure: `cmd/miniexchange/`, `internal/{config,domain,engine,service,handler,store}/`
    - _Requirements: 16.1_

  - [x] 1.2 Implement domain types (`internal/domain/`)
    - Create `broker.go`: `Broker` struct with `BrokerID`, `CashBalance`, `ReservedCash` (int64 cents), `Holdings` map, `CreatedAt`, `sync.Mutex`; `Holding` struct with `Quantity`, `ReservedQuantity`; helper methods `AvailableCash()`, `AvailableQuantity(symbol)`
    - Create `order.go`: `Order` struct with all fields per spec; `OrderType`, `OrderSide`, `OrderStatus` constants; helper `AveragePrice()` method
    - Create `trade.go`: `Trade` struct with `TradeID`, `OrderID`, `Price`, `Quantity`, `ExecutedAt`
    - Create `webhook.go`: `Webhook` struct with `WebhookID`, `BrokerID`, `Event`, `URL`, `CreatedAt`, `UpdatedAt`
    - Create `symbol.go`: `SymbolRegistry` — thread-safe `map[string]bool` with `Register(symbol)` and `Exists(symbol)` methods
    - Create `errors.go`: sentinel errors `ErrBrokerAlreadyExists`, `ErrBrokerNotFound`, `ErrOrderNotFound`, `ErrOrderNotCancellable`, `ErrInsufficientBalance`, `ErrInsufficientHoldings`, `ErrNoLiquidity`, `ErrSymbolNotFound`, `ErrWebhookNotFound`; `ValidationError` type with message field
    - _Requirements: 1.1, 1.3, 2.1, 2.2, 4.1, 5.1, 12.1, 17.4_

  - [x] 1.3 Implement configuration (`internal/config/config.go`)
    - `Config` struct with all fields: `Port`, `LogLevel`, `ExpirationInterval`, `WebhookTimeout`, `VWAPWindow`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `ShutdownTimeout`
    - `Load()` function: read env vars with `os.Getenv`, apply defaults, parse with `strconv.Atoi` and `time.ParseDuration`, return error on invalid values
    - _Requirements: 16.1, 16.2_

  - [x] 1.4 Write property test for configuration parsing
    - **Property 25: Configuration parsing**
    - **Validates: Requirements 16.1, 16.2**

  - [x] 1.5 Implement monetary conversion helpers
    - Create `internal/domain/money.go`: `DollarsTocents(f float64) (int64, error)` — validates ≤ 2 decimal places, multiplies by 100; `CentsToDollars(c int64) float64` — divides by 100.0
    - _Requirements: 1.5, 17.2_

  - [x] 1.6 Write property test for monetary round-trip
    - **Property 24: Monetary value round-trip**
    - **Validates: Requirements 1.5, 17.2**

- [x] 2. Checkpoint - Ensure all tests pass
  - Ensure all tests pass with `go test -race -count=1 ./...`, ask the user if questions arise.

- [x] 3. In-memory stores (`internal/store/`)
  - [x] 3.1 Implement BrokerStore (`store/broker.go`)
    - Thread-safe map with `sync.RWMutex`
    - Methods: `Create(b *domain.Broker) error`, `Get(id string) (*domain.Broker, error)`, `Exists(id string) bool`
    - `Create` returns `ErrBrokerAlreadyExists` if broker_id exists
    - _Requirements: 1.1, 1.2_

  - [x] 3.2 Implement OrderStore (`store/order.go`)
    - Primary index: `map[string]*domain.Order` keyed by `order_id`
    - Secondary index: `map[string][]*domain.Order` keyed by `broker_id` (append-only)
    - Methods: `Create(o *domain.Order)`, `Get(id string) (*domain.Order, error)`, `ListByBroker(brokerID string, status *domain.OrderStatus, page, limit int) ([]*domain.Order, int)`
    - `ListByBroker` returns orders in reverse chronological order with pagination and optional status filter
    - _Requirements: 5.1, 11.1, 11.2, 11.3_

  - [x] 3.3 Implement TradeStore (`store/trade.go`)
    - `map[string][]*domain.Trade` keyed by symbol (append-only, chronological)
    - Methods: `Append(symbol string, t *domain.Trade)`, `GetBySymbol(symbol string) []*domain.Trade`
    - _Requirements: 8.1_

  - [x] 3.4 Implement WebhookStore (`store/webhook.go`)
    - Primary index: `map[string]*domain.Webhook` keyed by `webhook_id`
    - Secondary index: `map[string]map[string]*domain.Webhook` keyed by `broker_id → event`
    - Methods: `Upsert(w *domain.Webhook) bool` (returns true if created), `Get(id string) (*domain.Webhook, error)`, `ListByBroker(brokerID string) []*domain.Webhook`, `Delete(id string) error`, `GetByBrokerEvent(brokerID, event string) *domain.Webhook`
    - _Requirements: 12.1, 12.4, 12.5_

- [ ] 4. Matching engine (`internal/engine/`)
  - [x] 4.1 Implement OrderBook (`engine/book.go`)
    - `OrderBookEntry` struct: `Price`, `CreatedAt`, `OrderID`, `Order`
    - `OrderBook` struct: `symbol`, `sync.RWMutex`, bid B-tree (price DESC, time ASC, id ASC), ask B-tree (price ASC, time ASC, id ASC), secondary index `map[string]OrderBookEntry`
    - Bid `Less`: price descending, then created_at ascending, then order_id ascending
    - Ask `Less`: price ascending, then created_at ascending, then order_id ascending
    - Methods: `InsertBid`, `InsertAsk`, `Remove(orderID)`, `BestBid`, `BestAsk`, `TopBids(n)`, `TopAsks(n)`, `WalkAsks(fn)`, `WalkBids(fn)`, `BidCount`, `AskCount`
    - `BookManager` struct: `map[string]*OrderBook` with `sync.RWMutex`, `GetOrCreate(symbol)` method
    - _Requirements: 4.1, 4.7_

  - [x] 4.2 Write property test for order book sorting
    - **Property 4: Order book sorting invariant**
    - **Validates: Requirements 4.1**

  - [x] 4.3 Implement limit order matching (`engine/matcher.go`)
    - `Matcher` struct with `BookManager`, `BrokerStore`, `OrderStore`, `TradeStore`, `SymbolRegistry`
    - `MatchLimitOrder(order *domain.Order) ([]*domain.Trade, error)`: implements the step-by-step procedure from the spec — validate/reserve → match loop (peek best opposite, check price compatibility, compute fill qty, execute trade with settlement) → rest or complete → compute average_price
    - Execution price = ask price always
    - Per-symbol write lock held during entire matching pass
    - Per-broker mutex acquired/released sequentially for each balance mutation
    - _Requirements: 4.2, 4.3, 4.5, 4.6_

  - [x] 4.4 Write property tests for matching engine core
    - **Property 5: Price compatibility determines matching**
    - **Property 6: Execution price rule**
    - **Property 7: Quantity conservation**
    - **Property 13: Average price computation**
    - **Validates: Requirements 4.2, 4.3, 4.4, 2.1, 3.1, 6.1, 7.2, 4.6**

  - [x] 4.5 Implement market order matching
    - `MatchMarketOrder(order *domain.Order) ([]*domain.Trade, error)`: same as limit but no price compatibility check, execution price = resting order's price, IOC cancellation of remainder, no resting on book
    - Balance validation for market bids: simulate fill against current book to estimate cost
    - No-liquidity check: if opposite side empty, return `ErrNoLiquidity`
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6_

  - [x] 4.6 Write property test for market order IOC semantics
    - **Property 12: Market order IOC semantics**
    - **Validates: Requirements 3.1, 3.4, 3.5**

  - [x] 4.7 Implement order cancellation in engine
    - `CancelOrder(orderID string) (*domain.Order, error)`: acquire per-symbol write lock, validate status is pending/partially_filled, remove from book, update order fields, release reservation, return updated order
    - Return `ErrOrderNotFound` or `ErrOrderNotCancellable` as appropriate
    - _Requirements: 6.1, 6.2, 6.3_

  - [x] 4.8 Write property test for cancellation
    - **Property 15: Cancellation state transition**
    - **Validates: Requirements 6.1, 6.2, 6.4**

  - [x] 4.9 Implement quote simulation
    - `SimulateMarketOrder(symbol, side, quantity) *QuoteResult`: read-only walk of opposite side under read lock, compute estimated_average_price, estimated_total, quantity_available, fully_fillable, price_levels
    - _Requirements: 14.1, 14.2, 14.3_

  - [x] 4.10 Write property test for quote simulation
    - **Property 23: Quote simulation accuracy**
    - **Validates: Requirements 14.1, 14.2**

  - [x] 4.11 Implement expiration manager (`engine/expiry.go`)
    - `ExpiryManager` with sorted slice of active orders by `expires_at` ASC
    - `Add(order)`, `Remove(orderID)` methods to maintain the slice
    - `Start(ctx context.Context)`: goroutine that ticks on configurable interval, calls `tick(now)`
    - `tick(now)`: iterate from front, for each order where `expires_at <= now`, acquire per-symbol write lock, transition to expired, release reservation, remove from book, fire webhook
    - _Requirements: 7.1, 7.2, 7.3, 7.4_

  - [x] 4.12 Write property test for expiration
    - **Property 16: Expiration state transition**
    - **Validates: Requirements 7.2**

- [x] 5. Checkpoint - Ensure all tests pass
  - Ensure all tests pass with `go test -race -count=1 ./...`, ask the user if questions arise.

- [ ] 6. Conservation property tests
  - [x] 6.1 Write property test for cash conservation
    - **Property 8: Cash conservation**
    - **Validates: Requirements 4.5**

  - [x] 6.2 Write property test for holdings conservation
    - **Property 9: Holdings conservation**
    - **Validates: Requirements 4.5**

  - [x] 6.3 Write property test for reservation consistency
    - **Property 10: Reservation consistency**
    - **Validates: Requirements 2.3, 2.4, 2.8, 6.1, 7.2**

- [ ] 7. Service layer (`internal/service/`)
  - [x] 7.1 Implement BrokerService (`service/broker.go`)
    - `Register(req)`: validate broker_id regex, initial_cash ≥ 0, holdings (symbol regex, quantity > 0, no duplicates), convert monetary values to cents, check uniqueness, create broker, register symbols
    - `GetBalance(brokerID)`: retrieve broker, compute available_cash and available_quantity per symbol, return balance response
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 10.1, 10.2_

  - [x] 7.2 Implement OrderService (`service/order.go`)
    - `SubmitOrder(req)`: validate all fields per order type (limit vs market), validate broker exists, convert price to cents, call matcher (MatchLimitOrder or MatchMarketOrder), add to expiry manager if limit order rests on book, dispatch webhooks for trades
    - `GetOrder(orderID)`: retrieve from store, return full order with trades
    - `CancelOrder(orderID)`: call matcher.CancelOrder, dispatch order.cancelled webhook
    - `ListOrders(brokerID, status, page, limit)`: validate broker exists, call store.ListByBroker
    - _Requirements: 2.1, 2.2, 3.1, 3.2, 5.1, 5.2, 5.3, 6.1, 6.2, 6.3, 6.4, 11.1, 11.2, 11.3, 11.4, 11.5, 11.6_

  - [x] 7.3 Implement StockService (`service/stock.go`)
    - `GetPrice(symbol)`: check symbol exists, get trades by symbol, compute VWAP over window, fallback to last trade, return null if no trades
    - `GetBook(symbol, depth)`: check symbol exists, validate depth (1-50), get top N bids/asks from book, compute spread
    - `GetQuote(symbol, side, quantity)`: check symbol exists, call matcher.SimulateMarketOrder
    - _Requirements: 8.1, 8.2, 8.3, 8.4, 9.1, 9.2, 9.3, 9.4, 9.5, 14.1, 14.3, 14.4, 14.5_

  - [x] 7.4 Write property test for VWAP computation
    - **Property 17: VWAP computation**
    - **Validates: Requirements 8.1, 8.2, 8.3**

  - [x] 7.5 Write property test for book snapshot aggregation
    - **Property 18: Book snapshot aggregation**
    - **Validates: Requirements 9.1, 9.2, 9.3**

  - [x] 7.6 Implement WebhookService (`service/webhook.go`)
    - `Upsert(req)`: validate broker exists, validate URL (https, max 2048), validate events (non-empty, valid types, deduplicate), upsert each (broker_id, event) pair, return webhooks and whether any were created
    - `List(brokerID)`: validate broker exists, return all subscriptions
    - `Delete(webhookID)`: delete from store
    - `DispatchTradeExecuted`, `DispatchOrderExpired`, `DispatchOrderCancelled`: look up subscriptions, fire-and-forget HTTP POST with `X-Delivery-Id`, `X-Webhook-Id`, `X-Event-Type` headers, configurable timeout
    - _Requirements: 12.1, 12.2, 12.3, 12.4, 12.5, 12.6, 13.1, 13.2, 13.3, 13.4, 13.5, 13.6_

  - [x] 7.7 Write property test for webhook upsert idempotency
    - **Property 21: Webhook upsert idempotency**
    - **Validates: Requirements 12.1, 12.3**

- [x] 8. Checkpoint - Ensure all tests pass
  - Ensure all tests pass with `go test -race -count=1 ./...`, ask the user if questions arise.

- [ ] 9. HTTP handler layer (`internal/handler/`)
  - [x] 9.1 Implement response helpers (`handler/response.go`)
    - `WriteJSON(w, status, data)`: set Content-Type, write status code, encode JSON
    - `WriteError(w, status, errorCode, message)`: write error response in standard format `{"error": "...", "message": "..."}`
    - `ParseJSON(r, v)`: decode request body, validate Content-Type header, return error for malformed JSON
    - _Requirements: 17.1, 17.4_

  - [x] 9.2 Implement broker handlers (`handler/broker.go`)
    - `POST /brokers`: parse request, call BrokerService.Register, map domain errors to HTTP status codes, return broker response with cents→dollars conversion
    - `GET /brokers/{broker_id}/balance`: call BrokerService.GetBalance, return balance response
    - `GET /brokers/{broker_id}/orders`: parse query params (status, page, limit), call OrderService.ListOrders, return paginated response
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 10.1, 10.2, 11.1, 11.2, 11.3, 11.4, 11.5, 11.6_

  - [x] 9.3 Implement order handlers (`handler/order.go`)
    - `POST /orders`: parse request, call OrderService.SubmitOrder, return full order response with conditional fields (market vs limit)
    - `GET /orders/{order_id}`: call OrderService.GetOrder, return full order with trades
    - `DELETE /orders/{order_id}`: call OrderService.CancelOrder, return final order state
    - JSON serialization: market orders omit `price`, `expires_at`, `cancelled_at`, `expired_at`; limit orders always include them (null when not set)
    - _Requirements: 2.1, 3.1, 5.1, 5.2, 6.1, 6.4_

  - [x] 9.4 Implement stock handlers (`handler/stock.go`)
    - `GET /stocks/{symbol}/price`: call StockService.GetPrice, return price response
    - `GET /stocks/{symbol}/book`: parse `depth` query param (default 10, max 50), call StockService.GetBook, return book response
    - `GET /stocks/{symbol}/quote`: parse `side` and `quantity` query params, call StockService.GetQuote, return quote response
    - _Requirements: 8.1, 8.4, 9.1, 9.4, 9.5, 14.1, 14.4, 14.5_

  - [x] 9.5 Implement webhook handlers (`handler/webhook.go`)
    - `POST /webhooks`: parse request, call WebhookService.Upsert, return 201 or 200 based on whether new subscriptions were created
    - `GET /webhooks`: parse `broker_id` query param (required), call WebhookService.List
    - `DELETE /webhooks/{webhook_id}`: call WebhookService.Delete, return 204
    - _Requirements: 12.1, 12.2, 12.3, 12.4, 12.5, 12.6_

  - [x] 9.6 Implement router (`handler/router.go`)
    - Create chi router with all routes registered
    - Add request logging middleware using `slog`
    - Add Content-Type validation middleware for POST/PUT/PATCH requests
    - Register `/healthz` endpoint returning `{"status": "ok"}`
    - _Requirements: 16.5, 17.1, 17.5_

  - [x] 9.7 Write handler integration tests
    - Use `net/http/httptest` to test all endpoints
    - Test request/response serialization, error code mapping, Content-Type validation
    - Test specific matching scenarios from the challenge statement (same price, no match, price gap, partial fills, chronological priority)
    - _Requirements: 17.1, 17.2, 17.3, 17.4_

- [x] 10. Checkpoint - Ensure all tests pass
  - Ensure all tests pass with `go test -race -count=1 ./...`, ask the user if questions arise.

- [ ] 11. Application entrypoint and deployment
  - [x] 11.1 Implement main.go (`cmd/miniexchange/main.go`)
    - Load config via `config.Load()`
    - Initialize `slog` logger with configured level
    - Instantiate all stores, symbol registry, book manager, matcher, expiry manager, services, handlers
    - Wire dependencies: stores → engine → services → handlers → router
    - Start expiration goroutine with context
    - Start HTTP server with configured timeouts
    - Handle `-healthcheck` flag: HTTP GET to `localhost:PORT/healthz`, exit 0/1
    - Graceful shutdown on SIGINT/SIGTERM: stop HTTP server, cancel context (stops expiry goroutine), exit
    - _Requirements: 16.1, 16.4, 16.5_

  - [x] 11.2 Create Dockerfile
    - Multi-stage build: `golang:1.23-alpine` builder, `gcr.io/distroless/static-debian12:nonroot` final
    - `CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /miniexchange ./cmd/miniexchange`
    - Expose port 8080
    - _Requirements: 16.3_

  - [x] 11.3 Create docker-compose.yml
    - Service `miniexchange` with build context, port mapping 8080:8080, env vars, healthcheck using `-healthcheck` flag
    - _Requirements: 16.3_

  - [x] 11.4 Create Makefile
    - Targets: `build`, `run`, `test` (`go test -race -count=1 ./...`), `lint` (`goimports -l .` + `go vet ./...`)
    - _Requirements: 16.1_

- [x] 12. Final checkpoint - Ensure all tests pass
  - Ensure all tests pass with `go test -race -count=1 ./...`, ask the user if questions arise.
  - Verify `docker build .` succeeds
  - Verify `docker-compose up` starts the service and healthcheck passes

## Notes

- Tasks marked with `*` are optional and can be skipped for faster MVP
- Each task references specific requirements for traceability
- The system design spec at `design-documents/system-design-spec.md` is the source of truth for all implementation details (exact JSON shapes, validation rules, error messages, etc.)
- Property tests use `pgregory.net/rapid` with minimum 100 iterations each
- All tests should be run with `-race` flag given the concurrent architecture
- Checkpoints ensure incremental validation at each layer boundary
