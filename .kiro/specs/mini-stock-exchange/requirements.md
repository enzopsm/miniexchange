# Requirements Document

## Introduction

A mini Stock Exchange system that receives and processes orders from brokers, matches them using a Central Limit Order Book (CLOB), and executes trades. The system supports limit and market orders, broker registration with initial balances, order expiration, webhook notifications, and real-time market data endpoints. Built as a single Go binary with all state held in-memory. The system design spec at `design-documents/system-design-spec.md` is the source of truth for all implementation details.

## Glossary

- **Exchange**: The mini stock exchange system that processes orders and executes trades.
- **Broker**: A registered participant on the exchange identified by a unique `broker_id`. Brokers submit orders on behalf of their customers.
- **Order**: A bid (buy) or ask (sell) instruction submitted by a broker for a specific stock symbol, quantity, and optionally a price and expiration time.
- **Limit_Order**: An order with a specified price and expiration time. Rests on the book if not immediately filled.
- **Market_Order**: An order with no price that executes immediately at the best available prices using IOC (Immediate or Cancel) semantics.
- **Trade**: A record of a matched execution between a bid and an ask order, containing the execution price, quantity, and timestamp.
- **Order_Book**: A per-symbol data structure containing resting bid and ask orders sorted by price-time priority.
- **CLOB**: Central Limit Order Book. The matching model where bids are sorted price descending then time ascending, and asks are sorted price ascending then time ascending.
- **VWAP**: Volume-Weighted Average Price. Computed as `sum(price × quantity) / sum(quantity)` over a time window.
- **IOC**: Immediate or Cancel. Market order semantics where unfilled remainder is cancelled immediately.
- **Reservation**: Cash or shares locked by an active order to prevent over-commitment across concurrent orders.
- **Webhook**: An HTTP POST notification sent to a broker's registered URL when a subscribed event occurs.
- **Symbol**: A stock ticker (e.g., AAPL, GOOG) matching the pattern `^[A-Z]{1,10}$`.
- **Cents**: Internal integer representation of monetary values. `$148.50` is stored as `14850` cents.

## Requirements

### Requirement 1: Broker Registration

**User Story:** As a broker, I want to register on the exchange with an initial cash deposit and optional stock holdings, so that I can begin submitting orders.

#### Acceptance Criteria

1. WHEN a broker submits a registration request with a valid `broker_id`, `initial_cash`, and optional `initial_holdings`, THE Exchange SHALL create the broker record and return `201 Created` with the broker state including `broker_id`, `cash_balance`, `holdings`, and `created_at`.
2. WHEN a broker submits a registration request with a `broker_id` that already exists, THE Exchange SHALL reject the request with `409 Conflict` and error `broker_already_exists`.
3. THE Exchange SHALL validate that `broker_id` matches the pattern `^[a-zA-Z0-9_-]{1,64}$`, `initial_cash` is greater than or equal to zero with at most 2 decimal places, and each holding has a `symbol` matching `^[A-Z]{1,10}$` with `quantity` greater than zero and no duplicate symbols.
4. IF any validation rule is violated, THEN THE Exchange SHALL reject the request with `400 Bad Request` and a descriptive error message.
5. THE Exchange SHALL store all monetary values internally as `int64` cents, converting at the API boundary.

### Requirement 2: Limit Order Submission

**User Story:** As a broker, I want to submit limit orders (bid or ask) for a stock symbol, so that my customers can trade at specified prices.

#### Acceptance Criteria

1. WHEN a broker submits a valid limit order with `type`, `broker_id`, `document_number`, `side`, `symbol`, `price`, `quantity`, and `expires_at`, THE Exchange SHALL create the order, run the matching engine synchronously, and return `201 Created` with the full order state including any trades executed.
2. THE Exchange SHALL validate that `price` is greater than zero with at most 2 decimal places, `quantity` is a positive integer, `expires_at` is a future RFC 3339 timestamp, `broker_id` references a registered broker, `document_number` matches `^[a-zA-Z0-9]{1,32}$`, `symbol` matches `^[A-Z]{1,10}$`, and `side` is `bid` or `ask`.
3. WHEN a limit bid order is submitted, THE Exchange SHALL verify that the broker has `available_cash >= price × quantity` and reserve that amount.
4. WHEN a limit ask order is submitted, THE Exchange SHALL verify that the broker has `available_quantity >= quantity` for the given symbol and reserve those shares.
5. IF the broker does not exist, THEN THE Exchange SHALL reject the order with `404 Not Found` and error `broker_not_found`.
6. IF the broker has insufficient available cash for a bid, THEN THE Exchange SHALL reject the order with `409 Conflict` and error `insufficient_balance`.
7. IF the broker has insufficient available holdings for an ask, THEN THE Exchange SHALL reject the order with `409 Conflict` and error `insufficient_holdings`.
8. WHEN a limit order is placed on the book without fully filling, THE Exchange SHALL assign it status `pending` (no fills) or `partially_filled` (some fills), and the reservation for the unfilled portion remains active.

### Requirement 3: Market Order Submission

**User Story:** As a broker, I want to submit market orders that execute immediately at the best available prices, so that my customers can trade without specifying a price.

#### Acceptance Criteria

1. WHEN a broker submits a valid market order with `type`, `broker_id`, `document_number`, `side`, `symbol`, and `quantity`, THE Exchange SHALL execute the order immediately using IOC semantics and return `201 Created` with the full order state.
2. THE Exchange SHALL validate that market orders do not include `price` or `expires_at` fields, rejecting with `400 Bad Request` if either is present.
3. WHEN a market bid is submitted, THE Exchange SHALL simulate the fill against the current book to estimate cost and verify the broker has sufficient `available_cash`.
4. WHEN a market order partially fills due to insufficient liquidity, THE Exchange SHALL cancel the unfilled remainder, setting `cancelled_quantity` and status to `cancelled`.
5. WHEN a market order fully fills, THE Exchange SHALL set status to `filled`.
6. IF no liquidity exists on the opposite side of the book, THEN THE Exchange SHALL reject the market order with `409 Conflict` and error `no_liquidity`, creating no order record.

### Requirement 4: Matching Engine

**User Story:** As the exchange operator, I want orders to be matched using price-time priority in a Central Limit Order Book, so that trades execute fairly and deterministically.

#### Acceptance Criteria

1. THE Exchange SHALL maintain one order book per symbol with bids sorted by price descending then `created_at` ascending then `order_id` ascending, and asks sorted by price ascending then `created_at` ascending then `order_id` ascending.
2. WHEN an incoming order is price-compatible with resting orders on the opposite side (`bid.price >= ask.price`), THE Exchange SHALL execute trades starting from the best-priced resting order.
3. THE Exchange SHALL set the execution price to the ask (seller) price for all limit-vs-limit matches.
4. WHEN a market order matches a resting limit order, THE Exchange SHALL use the resting limit order's price as the execution price.
5. WHEN a trade executes, THE Exchange SHALL decrease the buying broker's `cash_balance` by `execution_price × fill_qty`, release the per-fill reservation, increase the buying broker's holdings, increase the selling broker's `cash_balance` by `execution_price × fill_qty`, and decrease the selling broker's holdings and reserved quantity.
6. THE Exchange SHALL compute `average_price` as `sum(trade.price × trade.quantity) / filled_quantity` using integer arithmetic, returning `null` when no trades exist.
7. THE Exchange SHALL use B-tree data structures (`github.com/google/btree` v2) for both sides of the order book, with a secondary index `map[order_id]→entry` for O(log n) removal.

### Requirement 5: Order Retrieval

**User Story:** As a broker, I want to retrieve the current state of a previously submitted order including all trades, so that I can track order progress.

#### Acceptance Criteria

1. WHEN a broker requests an order by `order_id` via `GET /orders/{order_id}`, THE Exchange SHALL return `200 OK` with the full order state including all trades.
2. WHEN the requested order is a market order, THE Exchange SHALL omit `price`, `expires_at`, `cancelled_at`, and `expired_at` from the response.
3. IF the order does not exist, THEN THE Exchange SHALL return `404 Not Found` with error `order_not_found`.

### Requirement 6: Order Cancellation

**User Story:** As a broker, I want to cancel pending or partially filled orders, so that I can manage my customers' positions.

#### Acceptance Criteria

1. WHEN a broker cancels an order with status `pending` or `partially_filled` via `DELETE /orders/{order_id}`, THE Exchange SHALL remove the order from the book, set status to `cancelled`, set `cancelled_quantity` to the remaining quantity, set `remaining_quantity` to zero, record `cancelled_at`, and release the reservation.
2. IF the order is in a terminal state (`filled`, `cancelled`, or `expired`), THEN THE Exchange SHALL return `409 Conflict` with error `order_not_cancellable`.
3. IF the order does not exist, THEN THE Exchange SHALL return `404 Not Found` with error `order_not_found`.
4. WHEN an order is cancelled, THE Exchange SHALL return `200 OK` with the full final order state preserving any completed trades.

### Requirement 7: Order Expiration

**User Story:** As the exchange operator, I want expired orders to be automatically removed from the book, so that stale orders do not participate in matching.

#### Acceptance Criteria

1. THE Exchange SHALL run a background goroutine on a configurable interval (default 1 second) that scans for orders where `expires_at <= now` and status is `pending` or `partially_filled`.
2. WHEN an order expires, THE Exchange SHALL atomically set status to `expired`, set `cancelled_quantity` to the remaining quantity, set `remaining_quantity` to zero, set `expired_at` to the order's `expires_at` value, remove the order from the book, and release the reservation.
3. THE Exchange SHALL use a secondary index sorted by `expires_at` ascending for efficient expiration scanning.
4. THE Exchange SHALL acquire the per-symbol write lock during expiration to ensure mutual exclusion with the matching engine.

### Requirement 8: Stock Price Endpoint

**User Story:** As a broker, I want to query the current reference price for a stock, so that I can make informed trading decisions.

#### Acceptance Criteria

1. WHEN a broker requests the price for a symbol via `GET /stocks/{symbol}/price`, THE Exchange SHALL return the VWAP computed over a configurable time window (default 5 minutes).
2. WHEN no trades exist within the VWAP window, THE Exchange SHALL fall back to the execution price of the most recent trade for that symbol.
3. WHEN no trades have ever occurred for the symbol, THE Exchange SHALL return `current_price` as `null`.
4. IF the symbol has never been seen in any order or broker registration, THEN THE Exchange SHALL return `404 Not Found` with error `symbol_not_found`.

### Requirement 9: Order Book Endpoint

**User Story:** As a broker, I want to view the top of the order book for a stock, so that I can see current market depth.

#### Acceptance Criteria

1. WHEN a broker requests the book for a symbol via `GET /stocks/{symbol}/book`, THE Exchange SHALL return the top N price levels (default 10, max 50) aggregated by price with `total_quantity` and `order_count` per level.
2. THE Exchange SHALL sort bids descending by price and asks ascending by price in the response.
3. THE Exchange SHALL compute `spread` as `best_ask - best_bid`, returning `null` if either side is empty.
4. IF the symbol has never been seen, THEN THE Exchange SHALL return `404 Not Found`.
5. WHEN the `depth` query parameter is invalid (less than 1 or greater than 50), THE Exchange SHALL return `400 Bad Request`.

### Requirement 10: Broker Balance Endpoint

**User Story:** As a broker, I want to view my current cash balance and stock holdings including reservations, so that I can manage my trading capacity.

#### Acceptance Criteria

1. WHEN a broker requests their balance via `GET /brokers/{broker_id}/balance`, THE Exchange SHALL return `cash_balance`, `reserved_cash`, `available_cash`, and holdings with `quantity`, `reserved_quantity`, and `available_quantity` per symbol.
2. IF the broker does not exist, THEN THE Exchange SHALL return `404 Not Found` with error `broker_not_found`.

### Requirement 11: Broker Order Listing

**User Story:** As a broker, I want to list all my orders with optional status filtering and pagination, so that I can manage my order portfolio.

#### Acceptance Criteria

1. WHEN a broker requests their orders via `GET /brokers/{broker_id}/orders`, THE Exchange SHALL return a paginated list of orders in reverse chronological order with `total`, `page`, and `limit` metadata.
2. WHERE the `status` query parameter is provided, THE Exchange SHALL filter orders to only those matching the specified status.
3. THE Exchange SHALL support `page` (default 1, minimum 1) and `limit` (default 20, minimum 1, maximum 100) query parameters.
4. THE Exchange SHALL return a summary view of each order (without the `trades` array) including `order_id`, `type`, `document_number`, `symbol`, `side`, `quantity`, `filled_quantity`, `remaining_quantity`, `cancelled_quantity`, `status`, `average_price`, and `created_at`.
5. IF the broker does not exist, THEN THE Exchange SHALL return `404 Not Found` with error `broker_not_found`.
6. IF the `status` parameter is an invalid value, THEN THE Exchange SHALL return `400 Bad Request`.

### Requirement 12: Webhook Subscriptions

**User Story:** As a broker, I want to subscribe to event notifications via webhooks, so that I receive real-time updates about trades and order state changes.

#### Acceptance Criteria

1. WHEN a broker registers webhooks via `POST /webhooks` with `broker_id`, `url`, and `events`, THE Exchange SHALL create or update subscriptions using upsert semantics keyed by `(broker_id, event)`.
2. THE Exchange SHALL validate that `url` is a valid absolute URL with `https` scheme (max 2048 characters), `events` is a non-empty array of valid event types (`trade.executed`, `order.expired`, `order.cancelled`), and `broker_id` references a registered broker.
3. WHEN at least one new subscription is created, THE Exchange SHALL return `201 Created`. WHEN all subscriptions already existed, THE Exchange SHALL return `200 OK`.
4. WHEN a broker lists webhooks via `GET /webhooks?broker_id=X`, THE Exchange SHALL return all subscriptions for that broker.
5. WHEN a broker deletes a webhook via `DELETE /webhooks/{webhook_id}`, THE Exchange SHALL remove the subscription and return `204 No Content`.
6. IF the webhook does not exist on delete, THEN THE Exchange SHALL return `404 Not Found`.

### Requirement 13: Webhook Delivery

**User Story:** As a broker, I want to receive HTTP POST notifications when subscribed events occur, so that my systems can react to trades and order state changes in real-time.

#### Acceptance Criteria

1. WHEN a trade executes and a broker has a `trade.executed` subscription, THE Exchange SHALL POST a notification to the broker's URL with the trade details and resulting order state, including headers `X-Delivery-Id`, `X-Webhook-Id`, and `X-Event-Type`.
2. WHEN a limit order expires and a broker has an `order.expired` subscription, THE Exchange SHALL POST a notification with the expired order state.
3. WHEN a limit order is cancelled via `DELETE /orders/{order_id}` and a broker has an `order.cancelled` subscription, THE Exchange SHALL POST a notification with the cancelled order state.
4. THE Exchange SHALL use fire-and-forget delivery with no retries, using a configurable HTTP client timeout (default 5 seconds).
5. THE Exchange SHALL dispatch webhooks outside the per-symbol lock to avoid blocking the matching engine on network I/O.
6. THE Exchange SHALL send one `trade.executed` notification per trade, notifying both sides of the trade independently.

### Requirement 14: Market Order Quote

**User Story:** As a broker, I want to simulate a market order execution without placing an order, so that I can preview estimated costs or proceeds.

#### Acceptance Criteria

1. WHEN a broker requests a quote via `GET /stocks/{symbol}/quote` with `side` and `quantity` parameters, THE Exchange SHALL simulate the market order against the current book and return `estimated_average_price`, `estimated_total`, `quantity_available`, `fully_fillable`, and `price_levels`.
2. THE Exchange SHALL walk the opposite side of the book in price-time priority order: asks (lowest first) for bid quotes, bids (highest first) for ask quotes.
3. WHEN no liquidity exists, THE Exchange SHALL return `quantity_available` as 0, `fully_fillable` as false, and `estimated_average_price` and `estimated_total` as `null`.
4. IF the symbol has never been seen, THEN THE Exchange SHALL return `404 Not Found`.
5. THE Exchange SHALL validate that `side` is `bid` or `ask` and `quantity` is a positive integer, returning `400 Bad Request` for invalid values.

### Requirement 15: Concurrency and Thread Safety

**User Story:** As the exchange operator, I want the system to handle concurrent requests safely, so that no data corruption or race conditions occur.

#### Acceptance Criteria

1. THE Exchange SHALL use a per-symbol `sync.RWMutex` where the write lock is held for matching, cancellation, and expiration, and the read lock is held for book and quote queries.
2. THE Exchange SHALL use a per-broker `sync.Mutex` for balance mutations, acquired and released sequentially (never nested) to prevent deadlocks.
3. THE Exchange SHALL use per-store `sync.RWMutex` for thread-safe map access in all in-memory stores.

### Requirement 16: Configuration and Deployment

**User Story:** As the system operator, I want to configure the system via environment variables and deploy it as a Docker container, so that it runs reliably in containerized environments.

#### Acceptance Criteria

1. THE Exchange SHALL read all configuration from environment variables: `PORT` (default 8080), `LOG_LEVEL` (default `info`), `EXPIRATION_INTERVAL` (default `1s`), `WEBHOOK_TIMEOUT` (default `5s`), `VWAP_WINDOW` (default `5m`), `READ_TIMEOUT` (default `5s`), `WRITE_TIMEOUT` (default `10s`), `IDLE_TIMEOUT` (default `60s`), `SHUTDOWN_TIMEOUT` (default `10s`).
2. IF an environment variable has an invalid value, THEN THE Exchange SHALL exit with a descriptive error at startup.
3. THE Exchange SHALL build as a multi-stage Docker image using `golang:1.23-alpine` for compilation and `gcr.io/distroless/static-debian12:nonroot` for the final image.
4. THE Exchange SHALL support graceful shutdown on SIGINT/SIGTERM: stop the HTTP server with the shutdown timeout, stop the expiration goroutine, and exit.
5. THE Exchange SHALL expose a `/healthz` endpoint returning `200 OK` with `{"status": "ok"}` and support a `-healthcheck` flag for Docker health checks.

### Requirement 17: API Conventions

**User Story:** As a broker integrating with the exchange, I want consistent API behavior across all endpoints, so that I can build reliable client integrations.

#### Acceptance Criteria

1. THE Exchange SHALL require `Content-Type: application/json` on all request bodies and reject requests with missing or incorrect content type or malformed JSON with `400 Bad Request`.
2. THE Exchange SHALL accept and return monetary values as decimal numbers with at most 2 decimal places, rejecting values with more than 2 decimal places with `400 Bad Request`.
3. THE Exchange SHALL format all timestamps as RFC 3339 in UTC with second-level granularity (e.g., `2026-02-17T19:00:00Z`).
4. THE Exchange SHALL use `snake_case` for all JSON field names.
5. THE Exchange SHALL use structured logging via `log/slog`.
