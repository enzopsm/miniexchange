package engine

import (
	"time"

	"github.com/google/uuid"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/store"
)

// QuotePriceLevel represents a single price level in a quote simulation.
type QuotePriceLevel struct {
	Price    int64
	Quantity int64
}

// QuoteResult holds the result of a market order simulation.
type QuoteResult struct {
	QuantityAvailable int64
	FullyFillable     bool
	EstimatedAvgPrice *int64 // nil when no liquidity
	EstimatedTotal    *int64 // nil when no liquidity
	PriceLevels       []QuotePriceLevel
}

// Matcher implements the matching engine for limit and market orders.
type Matcher struct {
	books       *BookManager
	brokerStore *store.BrokerStore
	orderStore  *store.OrderStore
	tradeStore  *store.TradeStore
	symbols     *domain.SymbolRegistry
}

// NewMatcher creates a new Matcher with the given dependencies.
func NewMatcher(
	books *BookManager,
	brokerStore *store.BrokerStore,
	orderStore *store.OrderStore,
	tradeStore *store.TradeStore,
	symbols *domain.SymbolRegistry,
) *Matcher {
	return &Matcher{
		books:       books,
		brokerStore: brokerStore,
		orderStore:  orderStore,
		tradeStore:  tradeStore,
		symbols:     symbols,
	}
}

// MatchLimitOrder processes an incoming limit order through the matching
// engine. It validates and reserves balances, runs the match loop against
// the opposite side of the book, settles trades, and rests any unfilled
// remainder on the book.
//
// The caller must provide a fully populated Order with Type, BrokerID,
// Side, Symbol, Price, and Quantity set. The matcher assigns OrderID,
// CreatedAt, and manages all status transitions.
//
// The per-symbol write lock is held for the entire matching pass.
func (m *Matcher) MatchLimitOrder(order *domain.Order) ([]*domain.Trade, error) {
	book := m.books.GetOrCreate(order.Symbol)

	book.mu.Lock()
	defer book.mu.Unlock()

	// Step 1: Validate and reserve.
	broker, err := m.brokerStore.Get(order.BrokerID)
	if err != nil {
		return nil, domain.ErrBrokerNotFound
	}

	broker.Mu.Lock()
	if order.Side == domain.OrderSideBid {
		required := order.Price * order.Quantity
		if broker.AvailableCash() < required {
			broker.Mu.Unlock()
			return nil, domain.ErrInsufficientBalance
		}
		broker.ReservedCash += required
	} else {
		if broker.AvailableQuantity(order.Symbol) < order.Quantity {
			broker.Mu.Unlock()
			return nil, domain.ErrInsufficientHoldings
		}
		h := broker.Holdings[order.Symbol]
		if h == nil {
			h = &domain.Holding{}
			broker.Holdings[order.Symbol] = h
		}
		h.ReservedQuantity += order.Quantity
	}
	broker.Mu.Unlock()

	// Register the symbol and initialize the order record.
	m.symbols.Register(order.Symbol)

	order.OrderID = uuid.New().String()
	order.CreatedAt = time.Now()
	order.RemainingQuantity = order.Quantity
	order.FilledQuantity = 0
	order.CancelledQuantity = 0
	order.Status = domain.OrderStatusPending
	order.Trades = []*domain.Trade{}

	m.orderStore.Create(order)

	// Step 2–3: Match loop.
	executedAt := time.Now()
	var trades []*domain.Trade

	for order.RemainingQuantity > 0 {
		// Step 3a: Peek best opposite.
		var bestEntry OrderBookEntry
		var found bool

		if order.Side == domain.OrderSideBid {
			bestEntry, found = book.BestAsk()
		} else {
			bestEntry, found = book.BestBid()
		}
		if !found {
			break
		}

		// Step 3b: Check price compatibility.
		if order.Side == domain.OrderSideBid {
			if order.Price < bestEntry.Price {
				break
			}
		} else {
			if bestEntry.Price < order.Price {
				break
			}
		}

		resting := bestEntry.Order

		// Step 3c: Compute fill quantity.
		fillQty := order.RemainingQuantity
		if resting.RemainingQuantity < fillQty {
			fillQty = resting.RemainingQuantity
		}

		// Step 3d: Compute execution price (always the ask price).
		var executionPrice int64
		if order.Side == domain.OrderSideBid {
			executionPrice = resting.Price // resting is the ask
		} else {
			executionPrice = order.Price // incoming is the ask
		}

		// Step 3e: Execute the trade.
		tradeID := uuid.New().String()

		// Update both orders.
		order.RemainingQuantity -= fillQty
		order.FilledQuantity += fillQty
		resting.RemainingQuantity -= fillQty
		resting.FilledQuantity += fillQty

		if order.RemainingQuantity == 0 {
			order.Status = domain.OrderStatusFilled
		} else {
			order.Status = domain.OrderStatusPartiallyFilled
		}
		if resting.RemainingQuantity == 0 {
			resting.Status = domain.OrderStatusFilled
		} else {
			resting.Status = domain.OrderStatusPartiallyFilled
		}

		// Determine buyer and seller orders.
		var bidOrder, askOrder *domain.Order
		if order.Side == domain.OrderSideBid {
			bidOrder = order
			askOrder = resting
		} else {
			bidOrder = resting
			askOrder = order
		}

		// Settle buyer.
		buyer, _ := m.brokerStore.Get(bidOrder.BrokerID)
		buyer.Mu.Lock()
		buyer.CashBalance -= executionPrice * fillQty
		buyer.ReservedCash -= bidOrder.Price * fillQty
		if buyer.Holdings[order.Symbol] == nil {
			buyer.Holdings[order.Symbol] = &domain.Holding{}
		}
		buyer.Holdings[order.Symbol].Quantity += fillQty
		buyer.Mu.Unlock()

		// Settle seller.
		seller, _ := m.brokerStore.Get(askOrder.BrokerID)
		seller.Mu.Lock()
		seller.CashBalance += executionPrice * fillQty
		seller.Holdings[order.Symbol].Quantity -= fillQty
		seller.Holdings[order.Symbol].ReservedQuantity -= fillQty
		seller.Mu.Unlock()

		// Create trade records for both orders.
		incomingTrade := &domain.Trade{
			TradeID:    tradeID,
			OrderID:    order.OrderID,
			Price:      executionPrice,
			Quantity:   fillQty,
			ExecutedAt: executedAt,
		}
		restingTrade := &domain.Trade{
			TradeID:    tradeID,
			OrderID:    resting.OrderID,
			Price:      executionPrice,
			Quantity:   fillQty,
			ExecutedAt: executedAt,
		}

		order.Trades = append(order.Trades, incomingTrade)
		resting.Trades = append(resting.Trades, restingTrade)

		trades = append(trades, incomingTrade)

		// Append to trade store for both sides.
		m.tradeStore.Append(order.Symbol, incomingTrade)
		m.tradeStore.Append(order.Symbol, restingTrade)

		// Remove resting order from book if fully filled.
		if resting.RemainingQuantity == 0 {
			book.Remove(resting.OrderID)
		}
	}

	// Step 4: Rest or complete.
	if order.RemainingQuantity > 0 {
		entry := OrderBookEntry{
			Price:     order.Price,
			CreatedAt: order.CreatedAt,
			OrderID:   order.OrderID,
			Order:     order,
		}
		if order.Side == domain.OrderSideBid {
			book.InsertBid(entry)
		} else {
			book.InsertAsk(entry)
		}
	}

	return trades, nil
}

// MatchMarketOrder processes an incoming market order through the matching
// engine. Market orders use IOC (Immediate or Cancel) semantics: fill what
// is available, cancel the remainder. They are never placed on the book.
//
// For market bids, balance validation simulates the fill against the current
// book to estimate cost. For market asks, available_quantity is checked and
// shares are reserved before matching.
//
// The per-symbol write lock is held for the entire matching pass.
func (m *Matcher) MatchMarketOrder(order *domain.Order) ([]*domain.Trade, error) {
	book := m.books.GetOrCreate(order.Symbol)

	book.mu.Lock()
	defer book.mu.Unlock()

	// Step 0: No-liquidity check — if opposite side is empty, reject immediately.
	if order.Side == domain.OrderSideBid {
		if _, ok := book.BestAsk(); !ok {
			return nil, domain.ErrNoLiquidity
		}
	} else {
		if _, ok := book.BestBid(); !ok {
			return nil, domain.ErrNoLiquidity
		}
	}

	// Step 1: Validate and reserve.
	broker, err := m.brokerStore.Get(order.BrokerID)
	if err != nil {
		return nil, domain.ErrBrokerNotFound
	}

	broker.Mu.Lock()
	if order.Side == domain.OrderSideBid {
		// Simulate fill against current book to estimate cost.
		var estimatedCost int64
		var simRemaining int64 = order.Quantity
		book.WalkAsks(func(entry OrderBookEntry) bool {
			if simRemaining <= 0 {
				return false
			}
			fillQty := simRemaining
			if entry.Order.RemainingQuantity < fillQty {
				fillQty = entry.Order.RemainingQuantity
			}
			estimatedCost += entry.Price * fillQty
			simRemaining -= fillQty
			return simRemaining > 0
		})
		if broker.AvailableCash() < estimatedCost {
			broker.Mu.Unlock()
			return nil, domain.ErrInsufficientBalance
		}
		// No reservation for market bids — they execute immediately.
	} else {
		// Market ask: check available quantity and reserve shares.
		if broker.AvailableQuantity(order.Symbol) < order.Quantity {
			broker.Mu.Unlock()
			return nil, domain.ErrInsufficientHoldings
		}
		h := broker.Holdings[order.Symbol]
		if h == nil {
			h = &domain.Holding{}
			broker.Holdings[order.Symbol] = h
		}
		h.ReservedQuantity += order.Quantity
	}
	broker.Mu.Unlock()

	// Register the symbol and initialize the order record.
	m.symbols.Register(order.Symbol)

	order.OrderID = uuid.New().String()
	order.CreatedAt = time.Now()
	order.RemainingQuantity = order.Quantity
	order.FilledQuantity = 0
	order.CancelledQuantity = 0
	order.Status = domain.OrderStatusPending
	order.Trades = []*domain.Trade{}

	m.orderStore.Create(order)

	// Step 2–3: Match loop (no price compatibility check for market orders).
	executedAt := time.Now()
	var trades []*domain.Trade

	for order.RemainingQuantity > 0 {
		// Peek best opposite.
		var bestEntry OrderBookEntry
		var found bool

		if order.Side == domain.OrderSideBid {
			bestEntry, found = book.BestAsk()
		} else {
			bestEntry, found = book.BestBid()
		}
		if !found {
			break
		}

		// No price compatibility check — market orders accept any price.

		resting := bestEntry.Order

		// Compute fill quantity.
		fillQty := order.RemainingQuantity
		if resting.RemainingQuantity < fillQty {
			fillQty = resting.RemainingQuantity
		}

		// Execution price = resting order's price.
		executionPrice := resting.Price

		// Execute the trade.
		tradeID := uuid.New().String()

		// Update both orders.
		order.RemainingQuantity -= fillQty
		order.FilledQuantity += fillQty
		resting.RemainingQuantity -= fillQty
		resting.FilledQuantity += fillQty

		if order.RemainingQuantity == 0 {
			order.Status = domain.OrderStatusFilled
		} else {
			order.Status = domain.OrderStatusPartiallyFilled
		}
		if resting.RemainingQuantity == 0 {
			resting.Status = domain.OrderStatusFilled
		} else {
			resting.Status = domain.OrderStatusPartiallyFilled
		}

		// Determine buyer and seller orders.
		var bidOrder, askOrder *domain.Order
		if order.Side == domain.OrderSideBid {
			bidOrder = order
			askOrder = resting
		} else {
			bidOrder = resting
			askOrder = order
		}

		// Settle buyer.
		buyer, _ := m.brokerStore.Get(bidOrder.BrokerID)
		buyer.Mu.Lock()
		buyer.CashBalance -= executionPrice * fillQty
		if bidOrder.Type == domain.OrderTypeLimit {
			buyer.ReservedCash -= bidOrder.Price * fillQty
		}
		if buyer.Holdings[order.Symbol] == nil {
			buyer.Holdings[order.Symbol] = &domain.Holding{}
		}
		buyer.Holdings[order.Symbol].Quantity += fillQty
		buyer.Mu.Unlock()

		// Settle seller.
		seller, _ := m.brokerStore.Get(askOrder.BrokerID)
		seller.Mu.Lock()
		seller.CashBalance += executionPrice * fillQty
		seller.Holdings[order.Symbol].Quantity -= fillQty
		seller.Holdings[order.Symbol].ReservedQuantity -= fillQty
		seller.Mu.Unlock()

		// Create trade records for both orders.
		incomingTrade := &domain.Trade{
			TradeID:    tradeID,
			OrderID:    order.OrderID,
			Price:      executionPrice,
			Quantity:   fillQty,
			ExecutedAt: executedAt,
		}
		restingTrade := &domain.Trade{
			TradeID:    tradeID,
			OrderID:    resting.OrderID,
			Price:      executionPrice,
			Quantity:   fillQty,
			ExecutedAt: executedAt,
		}

		order.Trades = append(order.Trades, incomingTrade)
		resting.Trades = append(resting.Trades, restingTrade)

		trades = append(trades, incomingTrade)

		// Append to trade store for both sides.
		m.tradeStore.Append(order.Symbol, incomingTrade)
		m.tradeStore.Append(order.Symbol, restingTrade)

		// Remove resting order from book if fully filled.
		if resting.RemainingQuantity == 0 {
			book.Remove(resting.OrderID)
		}
	}

	// Step 4: IOC cancellation — never rest on book.
	if order.RemainingQuantity > 0 {
		order.CancelledQuantity = order.RemainingQuantity
		order.RemainingQuantity = 0
		if order.FilledQuantity == order.Quantity {
			order.Status = domain.OrderStatusFilled
		} else {
			order.Status = domain.OrderStatusCancelled
		}
	}

	// Release remaining reservation for market asks.
	if order.Side == domain.OrderSideAsk && order.CancelledQuantity > 0 {
		seller, _ := m.brokerStore.Get(order.BrokerID)
		seller.Mu.Lock()
		seller.Holdings[order.Symbol].ReservedQuantity -= order.CancelledQuantity
		seller.Mu.Unlock()
	}

	return trades, nil
}

// CancelOrder cancels a pending or partially filled order. It acquires the
// per-symbol write lock, validates the order status, removes the order from
// the book, updates order fields, and releases the broker's reservation.
//
// Returns ErrOrderNotFound if the order does not exist.
// Returns ErrOrderNotCancellable if the order is in a terminal state
// (filled, cancelled, or expired).
func (m *Matcher) CancelOrder(orderID string) (*domain.Order, error) {
	// Step 1: Look up the order.
	order, err := m.orderStore.Get(orderID)
	if err != nil {
		return nil, domain.ErrOrderNotFound
	}

	// Step 2: Validate status — only pending or partially_filled can be cancelled.
	switch order.Status {
	case domain.OrderStatusPending, domain.OrderStatusPartiallyFilled:
		// OK — proceed with cancellation.
	default:
		return nil, domain.ErrOrderNotCancellable
	}

	// Step 3: Acquire per-symbol write lock.
	book := m.books.GetOrCreate(order.Symbol)
	book.mu.Lock()
	defer book.mu.Unlock()

	// Re-check status under lock (another goroutine may have changed it).
	switch order.Status {
	case domain.OrderStatusPending, domain.OrderStatusPartiallyFilled:
		// Still cancellable.
	default:
		return nil, domain.ErrOrderNotCancellable
	}

	// Step 4: Remove from book.
	book.Remove(order.OrderID)

	// Step 5: Update order fields.
	now := time.Now()
	order.CancelledQuantity = order.RemainingQuantity
	order.RemainingQuantity = 0
	order.Status = domain.OrderStatusCancelled
	order.CancelledAt = &now

	// Step 6: Release reservation.
	broker, err := m.brokerStore.Get(order.BrokerID)
	if err == nil {
		broker.Mu.Lock()
		if order.Side == domain.OrderSideBid {
			// Release reserved cash: price × cancelled_quantity.
			broker.ReservedCash -= order.Price * order.CancelledQuantity
		} else {
			// Release reserved shares.
			if h, ok := broker.Holdings[order.Symbol]; ok {
				h.ReservedQuantity -= order.CancelledQuantity
			}
		}
		broker.Mu.Unlock()
	}

	return order, nil
}

// SimulateMarketOrder performs a read-only walk of the opposite side of the
// book to estimate the result of a market order without actually placing it.
// For bid quotes it walks asks (lowest first); for ask quotes it walks bids
// (highest first). The caller must ensure the symbol exists.
func (m *Matcher) SimulateMarketOrder(symbol string, side domain.OrderSide, quantity int64) *QuoteResult {
	book := m.books.GetOrCreate(symbol)

	book.mu.RLock()
	defer book.mu.RUnlock()

	result := &QuoteResult{
		PriceLevels: make([]QuotePriceLevel, 0),
	}

	var remaining int64 = quantity
	var totalCost int64

	walkFn := func(entry OrderBookEntry) bool {
		if remaining <= 0 {
			return false
		}
		fillQty := entry.Order.RemainingQuantity
		if fillQty > remaining {
			fillQty = remaining
		}
		totalCost += entry.Price * fillQty
		result.QuantityAvailable += fillQty
		remaining -= fillQty

		// Aggregate into price levels.
		if len(result.PriceLevels) > 0 && result.PriceLevels[len(result.PriceLevels)-1].Price == entry.Price {
			result.PriceLevels[len(result.PriceLevels)-1].Quantity += fillQty
		} else {
			result.PriceLevels = append(result.PriceLevels, QuotePriceLevel{
				Price:    entry.Price,
				Quantity: fillQty,
			})
		}
		return true
	}

	if side == domain.OrderSideBid {
		book.WalkAsks(walkFn)
	} else {
		book.WalkBids(walkFn)
	}

	if result.QuantityAvailable > 0 {
		avgPrice := totalCost / result.QuantityAvailable
		result.EstimatedAvgPrice = &avgPrice
		result.EstimatedTotal = &totalCost
	}
	result.FullyFillable = result.QuantityAvailable >= quantity

	return result
}


