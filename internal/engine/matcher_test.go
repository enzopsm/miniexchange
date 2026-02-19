package engine

import (
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/store"
)

// newTestMatcher creates a Matcher with fresh stores for testing.
func newTestMatcher() (*Matcher, *store.BrokerStore, *store.OrderStore, *store.TradeStore) {
	books := NewBookManager()
	brokerStore := store.NewBrokerStore()
	orderStore := store.NewOrderStore()
	tradeStore := store.NewTradeStore()
	symbols := domain.NewSymbolRegistry()
	m := NewMatcher(books, brokerStore, orderStore, tradeStore, symbols)
	return m, brokerStore, orderStore, tradeStore
}

// registerBroker is a helper that creates and stores a broker.
func registerBroker(bs *store.BrokerStore, id string, cash int64, holdings map[string]*domain.Holding) *domain.Broker {
	if holdings == nil {
		holdings = make(map[string]*domain.Holding)
	}
	b := &domain.Broker{
		BrokerID:    id,
		CashBalance: cash,
		Holdings:    holdings,
		CreatedAt:   time.Now(),
	}
	_ = bs.Create(b)
	return b
}

// newLimitOrder creates a limit order struct (not yet submitted to the matcher).
func newLimitOrder(brokerID string, side domain.OrderSide, symbol string, price, qty int64) *domain.Order {
	exp := time.Now().Add(time.Hour)
	return &domain.Order{
		Type:      domain.OrderTypeLimit,
		BrokerID:  brokerID,
		Side:      side,
		Symbol:    symbol,
		Price:     price,
		Quantity:  qty,
		ExpiresAt: &exp,
	}
}

func TestMatchLimitOrder_BidNoMatch_RestsOnBook(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 100000, nil) // $1000.00

	order := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 5) // $150 × 5
	trades, err := m.MatchLimitOrder(order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("expected 0 trades, got %d", len(trades))
	}
	if order.Status != domain.OrderStatusPending {
		t.Errorf("expected status pending, got %s", order.Status)
	}
	if order.RemainingQuantity != 5 {
		t.Errorf("expected remaining 5, got %d", order.RemainingQuantity)
	}
	if order.OrderID == "" {
		t.Error("expected order_id to be assigned")
	}

	// Verify it's on the book.
	book := m.books.GetOrCreate("AAPL")
	if book.BidCount() != 1 {
		t.Errorf("expected 1 bid on book, got %d", book.BidCount())
	}
}

func TestMatchLimitOrder_AskNoMatch_RestsOnBook(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})

	order := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 15000, 5)
	trades, err := m.MatchLimitOrder(order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("expected 0 trades, got %d", len(trades))
	}
	if order.Status != domain.OrderStatusPending {
		t.Errorf("expected status pending, got %s", order.Status)
	}

	book := m.books.GetOrCreate("AAPL")
	if book.AskCount() != 1 {
		t.Errorf("expected 1 ask on book, got %d", book.AskCount())
	}
}

func TestMatchLimitOrder_FullMatch(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})
	registerBroker(bs, "buyer", 1000000, nil) // $10,000

	// Seller places ask at $150.
	askOrder := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 15000, 5)
	_, err := m.MatchLimitOrder(askOrder)
	if err != nil {
		t.Fatalf("ask order error: %v", err)
	}

	// Buyer places bid at $150 — should fully match.
	bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 5)
	trades, err := m.MatchLimitOrder(bidOrder)
	if err != nil {
		t.Fatalf("bid order error: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	if trades[0].Price != 15000 {
		t.Errorf("expected execution price 15000, got %d", trades[0].Price)
	}
	if trades[0].Quantity != 5 {
		t.Errorf("expected fill qty 5, got %d", trades[0].Quantity)
	}
	if bidOrder.Status != domain.OrderStatusFilled {
		t.Errorf("expected bid status filled, got %s", bidOrder.Status)
	}
	if askOrder.Status != domain.OrderStatusFilled {
		t.Errorf("expected ask status filled, got %s", askOrder.Status)
	}

	// Neither order should be on the book.
	book := m.books.GetOrCreate("AAPL")
	if book.BidCount() != 0 || book.AskCount() != 0 {
		t.Errorf("expected empty book, got bids=%d asks=%d", book.BidCount(), book.AskCount())
	}
}

func TestMatchLimitOrder_ExecutionPriceIsAskPrice(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})
	registerBroker(bs, "buyer", 1000000, nil)

	// Ask at $100.
	askOrder := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 5)
	m.MatchLimitOrder(askOrder)

	// Bid at $150 — execution price should be $100 (the ask price).
	bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 5)
	trades, _ := m.MatchLimitOrder(bidOrder)

	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	if trades[0].Price != 10000 {
		t.Errorf("expected execution price 10000 (ask price), got %d", trades[0].Price)
	}
}

func TestMatchLimitOrder_IncomingAsk_ExecutionPriceIsIncomingAskPrice(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 1000000, nil)
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})

	// Bid at $150 rests on book.
	bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 5)
	m.MatchLimitOrder(bidOrder)

	// Ask at $100 comes in — execution price = incoming ask price = $100.
	askOrder := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 5)
	trades, _ := m.MatchLimitOrder(askOrder)

	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	if trades[0].Price != 10000 {
		t.Errorf("expected execution price 10000 (incoming ask price), got %d", trades[0].Price)
	}
}

func TestMatchLimitOrder_PartialFill_RestsRemainder(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})
	registerBroker(bs, "buyer", 1000000, nil)

	// Ask for 3 shares at $100.
	askOrder := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 3)
	m.MatchLimitOrder(askOrder)

	// Bid for 5 shares at $100 — fills 3, rests 2.
	bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 10000, 5)
	trades, _ := m.MatchLimitOrder(bidOrder)

	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	if trades[0].Quantity != 3 {
		t.Errorf("expected fill qty 3, got %d", trades[0].Quantity)
	}
	if bidOrder.Status != domain.OrderStatusPartiallyFilled {
		t.Errorf("expected bid status partially_filled, got %s", bidOrder.Status)
	}
	if bidOrder.RemainingQuantity != 2 {
		t.Errorf("expected remaining 2, got %d", bidOrder.RemainingQuantity)
	}
	if bidOrder.FilledQuantity != 3 {
		t.Errorf("expected filled 3, got %d", bidOrder.FilledQuantity)
	}

	// Remainder should be on the book.
	book := m.books.GetOrCreate("AAPL")
	if book.BidCount() != 1 {
		t.Errorf("expected 1 bid on book, got %d", book.BidCount())
	}
}

func TestMatchLimitOrder_MultipleFills(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "s1", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})
	registerBroker(bs, "s2", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})
	registerBroker(bs, "buyer", 2000000, nil)

	// Two asks at different prices.
	ask1 := newLimitOrder("s1", domain.OrderSideAsk, "AAPL", 10000, 3) // $100 × 3
	ask2 := newLimitOrder("s2", domain.OrderSideAsk, "AAPL", 11000, 4) // $110 × 4
	m.MatchLimitOrder(ask1)
	m.MatchLimitOrder(ask2)

	// Bid for 5 at $110 — fills 3 at $100 then 2 at $110.
	bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 11000, 5)
	trades, _ := m.MatchLimitOrder(bidOrder)

	if len(trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(trades))
	}
	if trades[0].Price != 10000 || trades[0].Quantity != 3 {
		t.Errorf("trade 0: expected price=10000 qty=3, got price=%d qty=%d", trades[0].Price, trades[0].Quantity)
	}
	if trades[1].Price != 11000 || trades[1].Quantity != 2 {
		t.Errorf("trade 1: expected price=11000 qty=2, got price=%d qty=%d", trades[1].Price, trades[1].Quantity)
	}
	if bidOrder.Status != domain.OrderStatusFilled {
		t.Errorf("expected bid status filled, got %s", bidOrder.Status)
	}

	// ask2 should still have 2 remaining on the book.
	if ask2.RemainingQuantity != 2 {
		t.Errorf("expected ask2 remaining 2, got %d", ask2.RemainingQuantity)
	}
}

func TestMatchLimitOrder_NoPriceCompatibility(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})
	registerBroker(bs, "buyer", 1000000, nil)

	// Ask at $200.
	askOrder := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 20000, 5)
	m.MatchLimitOrder(askOrder)

	// Bid at $100 — no match.
	bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 10000, 5)
	trades, _ := m.MatchLimitOrder(bidOrder)

	if len(trades) != 0 {
		t.Errorf("expected 0 trades, got %d", len(trades))
	}
	if bidOrder.Status != domain.OrderStatusPending {
		t.Errorf("expected pending, got %s", bidOrder.Status)
	}

	book := m.books.GetOrCreate("AAPL")
	if book.BidCount() != 1 || book.AskCount() != 1 {
		t.Errorf("expected 1 bid and 1 ask on book, got bids=%d asks=%d", book.BidCount(), book.AskCount())
	}
}

func TestMatchLimitOrder_InsufficientBalance(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 1000, nil) // only $10

	order := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 5) // needs $750
	_, err := m.MatchLimitOrder(order)
	if err != domain.ErrInsufficientBalance {
		t.Errorf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestMatchLimitOrder_InsufficientHoldings(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 2},
	})

	order := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 15000, 5) // needs 5, has 2
	_, err := m.MatchLimitOrder(order)
	if err != domain.ErrInsufficientHoldings {
		t.Errorf("expected ErrInsufficientHoldings, got %v", err)
	}
}

func TestMatchLimitOrder_BrokerNotFound(t *testing.T) {
	m, _, _, _ := newTestMatcher()

	order := newLimitOrder("nonexistent", domain.OrderSideBid, "AAPL", 15000, 5)
	_, err := m.MatchLimitOrder(order)
	if err != domain.ErrBrokerNotFound {
		t.Errorf("expected ErrBrokerNotFound, got %v", err)
	}
}

func TestMatchLimitOrder_BalanceSettlement(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	seller := registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})
	buyer := registerBroker(bs, "buyer", 1000000, nil) // $10,000

	// Ask at $100 for 5 shares.
	askOrder := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 5)
	m.MatchLimitOrder(askOrder)

	// Bid at $100 for 5 shares — full match.
	bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 10000, 5)
	m.MatchLimitOrder(bidOrder)

	// Buyer: cash = 10000 - (100*5) = $500 = 50000 cents.
	if buyer.CashBalance != 950000 {
		t.Errorf("expected buyer cash 950000, got %d", buyer.CashBalance)
	}
	if buyer.ReservedCash != 0 {
		t.Errorf("expected buyer reserved cash 0, got %d", buyer.ReservedCash)
	}
	if buyer.Holdings["AAPL"] == nil || buyer.Holdings["AAPL"].Quantity != 5 {
		t.Errorf("expected buyer to hold 5 AAPL, got %v", buyer.Holdings["AAPL"])
	}

	// Seller: cash = 0 + (100*5) = $500 = 50000 cents.
	if seller.CashBalance != 50000 {
		t.Errorf("expected seller cash 50000, got %d", seller.CashBalance)
	}
	if seller.Holdings["AAPL"].Quantity != 5 {
		t.Errorf("expected seller to hold 5 AAPL, got %d", seller.Holdings["AAPL"].Quantity)
	}
	if seller.Holdings["AAPL"].ReservedQuantity != 0 {
		t.Errorf("expected seller reserved qty 0, got %d", seller.Holdings["AAPL"].ReservedQuantity)
	}
}

func TestMatchLimitOrder_PriceImprovement(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})
	buyer := registerBroker(bs, "buyer", 1000000, nil)

	// Ask at $100.
	askOrder := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 5)
	m.MatchLimitOrder(askOrder)

	// Bid at $150 — execution at $100, buyer gets price improvement.
	bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 5)
	m.MatchLimitOrder(bidOrder)

	// Buyer reserved 150*5=750 ($75000 cents), paid 100*5=500 ($50000 cents).
	// reserved_cash should be 0 (all released), cash = 1000000 - 50000 = 950000.
	if buyer.CashBalance != 950000 {
		t.Errorf("expected buyer cash 950000, got %d", buyer.CashBalance)
	}
	if buyer.ReservedCash != 0 {
		t.Errorf("expected buyer reserved cash 0, got %d", buyer.ReservedCash)
	}
}

func TestMatchLimitOrder_AveragePrice(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "s1", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})
	registerBroker(bs, "s2", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})
	registerBroker(bs, "buyer", 5000000, nil)

	// Two asks: 3 @ $100, 2 @ $110.
	m.MatchLimitOrder(newLimitOrder("s1", domain.OrderSideAsk, "AAPL", 10000, 3))
	m.MatchLimitOrder(newLimitOrder("s2", domain.OrderSideAsk, "AAPL", 11000, 2))

	// Bid for 5 @ $110.
	bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 11000, 5)
	m.MatchLimitOrder(bidOrder)

	// avg = (10000*3 + 11000*2) / 5 = (30000 + 22000) / 5 = 52000 / 5 = 10400
	avg, ok := bidOrder.AveragePrice()
	if !ok {
		t.Fatal("expected average price to be computed")
	}
	if avg != 10400 {
		t.Errorf("expected average price 10400, got %d", avg)
	}
}

func TestMatchLimitOrder_TradesAppendedToTradeStore(t *testing.T) {
	m, bs, _, ts := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})
	registerBroker(bs, "buyer", 1000000, nil)

	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 5))
	m.MatchLimitOrder(newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 10000, 5))

	// Each trade generates 2 trade records (one per order side).
	trades := ts.GetBySymbol("AAPL")
	if len(trades) != 2 {
		t.Errorf("expected 2 trade records in store, got %d", len(trades))
	}
}

func TestMatchLimitOrder_SymbolRegistered(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 1000000, nil)

	m.MatchLimitOrder(newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 10000, 1))

	if !m.symbols.Exists("AAPL") {
		t.Error("expected AAPL to be registered in symbol registry")
	}
}

func TestMatchLimitOrder_OrderStoredInOrderStore(t *testing.T) {
	m, bs, os, _ := newTestMatcher()
	registerBroker(bs, "buyer", 1000000, nil)

	order := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 10000, 1)
	m.MatchLimitOrder(order)

	retrieved, err := os.Get(order.OrderID)
	if err != nil {
		t.Fatalf("expected order in store, got error: %v", err)
	}
	if retrieved != order {
		t.Error("expected same order pointer from store")
	}
}

func TestMatchLimitOrder_ReservationForUnfilledBid(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	buyer := registerBroker(bs, "buyer", 1000000, nil)

	// Bid for 5 at $100 — no match, rests on book.
	m.MatchLimitOrder(newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 10000, 5))

	// Reserved: 10000 * 5 = 50000.
	if buyer.ReservedCash != 50000 {
		t.Errorf("expected reserved cash 50000, got %d", buyer.ReservedCash)
	}
}

func TestMatchLimitOrder_ReservationForUnfilledAsk(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	seller := registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})

	// Ask for 5 — no match, rests on book.
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 5))

	if seller.Holdings["AAPL"].ReservedQuantity != 5 {
		t.Errorf("expected reserved qty 5, got %d", seller.Holdings["AAPL"].ReservedQuantity)
	}
}

func TestMatchLimitOrder_SelfTrade(t *testing.T) {
	// A broker can trade with themselves if they have both sides.
	m, bs, _, _ := newTestMatcher()
	broker := registerBroker(bs, "broker1", 1000000, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})

	// Place ask.
	m.MatchLimitOrder(newLimitOrder("broker1", domain.OrderSideAsk, "AAPL", 10000, 5))
	// Place bid — matches own ask.
	bidOrder := newLimitOrder("broker1", domain.OrderSideBid, "AAPL", 10000, 5)
	trades, err := m.MatchLimitOrder(bidOrder)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}

	// Cash should be unchanged (paid self).
	if broker.CashBalance != 1000000 {
		t.Errorf("expected cash unchanged at 1000000, got %d", broker.CashBalance)
	}
	// Holdings should be unchanged (bought from self).
	if broker.Holdings["AAPL"].Quantity != 10 {
		t.Errorf("expected holdings unchanged at 10, got %d", broker.Holdings["AAPL"].Quantity)
	}
}

// newMarketOrder creates a market order struct (not yet submitted to the matcher).
func newMarketOrder(brokerID string, side domain.OrderSide, symbol string, qty int64) *domain.Order {
	return &domain.Order{
		Type:     domain.OrderTypeMarket,
		BrokerID: brokerID,
		Side:     side,
		Symbol:   symbol,
		Quantity: qty,
	}
}

// --- Market Order Tests ---

func TestMatchMarketOrder_FullFill_Buy(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 500000, nil)  // $5000
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Place resting ask at $100.
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 10))

	// Market buy 10 shares.
	order := newMarketOrder("buyer", domain.OrderSideBid, "AAPL", 10)
	trades, err := m.MatchMarketOrder(order)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	if order.Status != domain.OrderStatusFilled {
		t.Errorf("expected status filled, got %s", order.Status)
	}
	if order.FilledQuantity != 10 {
		t.Errorf("expected filled_quantity 10, got %d", order.FilledQuantity)
	}
	if order.RemainingQuantity != 0 {
		t.Errorf("expected remaining_quantity 0, got %d", order.RemainingQuantity)
	}
	if order.CancelledQuantity != 0 {
		t.Errorf("expected cancelled_quantity 0, got %d", order.CancelledQuantity)
	}
}

func TestMatchMarketOrder_FullFill_Sell(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 500000, nil)
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Place resting bid at $100.
	m.MatchLimitOrder(newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 10000, 10))

	// Market sell 10 shares.
	order := newMarketOrder("seller", domain.OrderSideAsk, "AAPL", 10)
	trades, err := m.MatchMarketOrder(order)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	if order.Status != domain.OrderStatusFilled {
		t.Errorf("expected status filled, got %s", order.Status)
	}
	if order.FilledQuantity != 10 {
		t.Errorf("expected filled_quantity 10, got %d", order.FilledQuantity)
	}
}

func TestMatchMarketOrder_PartialFill_IOCCancel(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 5000000, nil) // $50000
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Place resting ask for only 5 shares at $100.
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 5))

	// Market buy 10 shares — only 5 available.
	order := newMarketOrder("buyer", domain.OrderSideBid, "AAPL", 10)
	trades, err := m.MatchMarketOrder(order)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	if order.Status != domain.OrderStatusCancelled {
		t.Errorf("expected status cancelled, got %s", order.Status)
	}
	if order.FilledQuantity != 5 {
		t.Errorf("expected filled_quantity 5, got %d", order.FilledQuantity)
	}
	if order.CancelledQuantity != 5 {
		t.Errorf("expected cancelled_quantity 5, got %d", order.CancelledQuantity)
	}
	if order.RemainingQuantity != 0 {
		t.Errorf("expected remaining_quantity 0, got %d", order.RemainingQuantity)
	}
	// Quantity conservation: filled + cancelled = quantity.
	if order.FilledQuantity+order.CancelledQuantity != order.Quantity {
		t.Errorf("quantity conservation violated: %d + %d != %d",
			order.FilledQuantity, order.CancelledQuantity, order.Quantity)
	}
}

func TestMatchMarketOrder_NoLiquidity_Bid(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 500000, nil)

	// No asks on the book.
	order := newMarketOrder("buyer", domain.OrderSideBid, "AAPL", 10)
	_, err := m.MatchMarketOrder(order)

	if err != domain.ErrNoLiquidity {
		t.Fatalf("expected ErrNoLiquidity, got %v", err)
	}
	// No order record should be created.
	if order.OrderID != "" {
		t.Errorf("expected no order_id assigned, got %s", order.OrderID)
	}
}

func TestMatchMarketOrder_NoLiquidity_Ask(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// No bids on the book.
	order := newMarketOrder("seller", domain.OrderSideAsk, "AAPL", 10)
	_, err := m.MatchMarketOrder(order)

	if err != domain.ErrNoLiquidity {
		t.Fatalf("expected ErrNoLiquidity, got %v", err)
	}
	if order.OrderID != "" {
		t.Errorf("expected no order_id assigned, got %s", order.OrderID)
	}
}

func TestMatchMarketOrder_InsufficientBalance(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 1000, nil) // only $10
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Place resting ask at $100.
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 10))

	// Market buy 10 shares — costs $1000, buyer only has $10.
	order := newMarketOrder("buyer", domain.OrderSideBid, "AAPL", 10)
	_, err := m.MatchMarketOrder(order)

	if err != domain.ErrInsufficientBalance {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestMatchMarketOrder_InsufficientHoldings(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 500000, nil)
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 5},
	})

	// Place resting bid.
	m.MatchLimitOrder(newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 10000, 20))

	// Market sell 10 shares — seller only has 5.
	order := newMarketOrder("seller", domain.OrderSideAsk, "AAPL", 10)
	_, err := m.MatchMarketOrder(order)

	if err != domain.ErrInsufficientHoldings {
		t.Fatalf("expected ErrInsufficientHoldings, got %v", err)
	}
}

func TestMatchMarketOrder_ExecutionPriceIsRestingPrice(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 5000000, nil)
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Place asks at different prices.
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 5)) // $100
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 11000, 5)) // $110

	// Market buy 10 — should fill 5@$100 + 5@$110.
	order := newMarketOrder("buyer", domain.OrderSideBid, "AAPL", 10)
	trades, err := m.MatchMarketOrder(order)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(trades))
	}
	if trades[0].Price != 10000 {
		t.Errorf("expected first trade price 10000, got %d", trades[0].Price)
	}
	if trades[0].Quantity != 5 {
		t.Errorf("expected first trade qty 5, got %d", trades[0].Quantity)
	}
	if trades[1].Price != 11000 {
		t.Errorf("expected second trade price 11000, got %d", trades[1].Price)
	}
	if trades[1].Quantity != 5 {
		t.Errorf("expected second trade qty 5, got %d", trades[1].Quantity)
	}
}

func TestMatchMarketOrder_NeverRestsOnBook(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 5000000, nil)
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Place resting ask for only 3 shares.
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 3))

	// Market buy 10 — only 3 fill, remainder cancelled.
	order := newMarketOrder("buyer", domain.OrderSideBid, "AAPL", 10)
	m.MatchMarketOrder(order)

	// Verify the market order is NOT on the book.
	book := m.books.GetOrCreate("AAPL")
	if book.BidCount() != 0 {
		t.Errorf("expected 0 bids on book, got %d", book.BidCount())
	}
}

func TestMatchMarketOrder_OrderStoredInOrderStore(t *testing.T) {
	m, bs, os, _ := newTestMatcher()
	registerBroker(bs, "buyer", 5000000, nil)
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 10))

	order := newMarketOrder("buyer", domain.OrderSideBid, "AAPL", 10)
	m.MatchMarketOrder(order)

	stored, err := os.Get(order.OrderID)
	if err != nil {
		t.Fatalf("expected order in store, got error: %v", err)
	}
	if stored.OrderID != order.OrderID {
		t.Errorf("expected order_id %s, got %s", order.OrderID, stored.OrderID)
	}
}

func TestMatchMarketOrder_BalanceSettlement_Buy(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	buyer := registerBroker(bs, "buyer", 500000, nil) // $5000
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Place ask at $100.
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 5))

	// Market buy 5 shares at $100 each = $500 total.
	order := newMarketOrder("buyer", domain.OrderSideBid, "AAPL", 5)
	m.MatchMarketOrder(order)

	// Buyer should have $5000 - $500 = $4500.
	if buyer.CashBalance != 450000 {
		t.Errorf("expected buyer cash 450000, got %d", buyer.CashBalance)
	}
	// Buyer should have 5 AAPL shares.
	if buyer.Holdings["AAPL"] == nil || buyer.Holdings["AAPL"].Quantity != 5 {
		t.Errorf("expected buyer to hold 5 AAPL, got %v", buyer.Holdings["AAPL"])
	}
	// No reserved cash for market bids.
	if buyer.ReservedCash != 0 {
		t.Errorf("expected no reserved cash, got %d", buyer.ReservedCash)
	}
}

func TestMatchMarketOrder_BalanceSettlement_Sell(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 500000, nil)
	seller := registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Place bid at $100.
	m.MatchLimitOrder(newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 10000, 5))

	// Market sell 5 shares.
	order := newMarketOrder("seller", domain.OrderSideAsk, "AAPL", 5)
	m.MatchMarketOrder(order)

	// Seller should have $500 in cash.
	if seller.CashBalance != 50000 {
		t.Errorf("expected seller cash 50000, got %d", seller.CashBalance)
	}
	// Seller should have 95 AAPL shares (100 - 5).
	if seller.Holdings["AAPL"].Quantity != 95 {
		t.Errorf("expected seller to hold 95 AAPL, got %d", seller.Holdings["AAPL"].Quantity)
	}
}

func TestMatchMarketOrder_MarketAsk_ReservationReleasedOnPartialFill(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 500000, nil)
	seller := registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Place bid for only 3 shares.
	m.MatchLimitOrder(newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 10000, 3))

	// Market sell 10 — only 3 fill, 7 cancelled.
	order := newMarketOrder("seller", domain.OrderSideAsk, "AAPL", 10)
	m.MatchMarketOrder(order)

	if order.FilledQuantity != 3 {
		t.Errorf("expected filled 3, got %d", order.FilledQuantity)
	}
	if order.CancelledQuantity != 7 {
		t.Errorf("expected cancelled 7, got %d", order.CancelledQuantity)
	}
	// Reservation should be fully released.
	if seller.Holdings["AAPL"].ReservedQuantity != 0 {
		t.Errorf("expected reserved_quantity 0, got %d", seller.Holdings["AAPL"].ReservedQuantity)
	}
	// Seller should have 97 shares (100 - 3 sold).
	if seller.Holdings["AAPL"].Quantity != 97 {
		t.Errorf("expected quantity 97, got %d", seller.Holdings["AAPL"].Quantity)
	}
}

func TestMatchMarketOrder_BrokerNotFound(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 10))

	order := newMarketOrder("nonexistent", domain.OrderSideBid, "AAPL", 5)
	_, err := m.MatchMarketOrder(order)

	if err != domain.ErrBrokerNotFound {
		t.Fatalf("expected ErrBrokerNotFound, got %v", err)
	}
}

func TestMatchMarketOrder_MultiplePriceLevels(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 10000000, nil) // $100,000
	registerBroker(bs, "seller1", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})
	registerBroker(bs, "seller2", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 200},
	})
	registerBroker(bs, "seller3", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 50},
	})

	// Place asks at 3 different price levels.
	m.MatchLimitOrder(newLimitOrder("seller1", domain.OrderSideAsk, "AAPL", 10000, 100)) // $100
	m.MatchLimitOrder(newLimitOrder("seller2", domain.OrderSideAsk, "AAPL", 11000, 200)) // $110
	m.MatchLimitOrder(newLimitOrder("seller3", domain.OrderSideAsk, "AAPL", 12000, 50))  // $120

	// Market buy 250 — sweeps $100 (100), $110 (150 of 200).
	order := newMarketOrder("buyer", domain.OrderSideBid, "AAPL", 250)
	trades, err := m.MatchMarketOrder(order)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(trades))
	}
	if order.Status != domain.OrderStatusFilled {
		t.Errorf("expected status filled, got %s", order.Status)
	}

	// Verify average price: (100*10000 + 150*11000) / 250 = 2650000/250 = 10600
	avgPrice, ok := order.AveragePrice()
	if !ok {
		t.Fatal("expected average price to be computed")
	}
	if avgPrice != 10600 {
		t.Errorf("expected average price 10600, got %d", avgPrice)
	}
}

func TestMatchMarketOrder_TradesAppendedToTradeStore(t *testing.T) {
	m, bs, _, ts := newTestMatcher()
	registerBroker(bs, "buyer", 5000000, nil)
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 10))

	order := newMarketOrder("buyer", domain.OrderSideBid, "AAPL", 10)
	m.MatchMarketOrder(order)

	// Should have trades from the limit order placement (0 trades) + market order (2 trade records: one per side).
	allTrades := ts.GetBySymbol("AAPL")
	if len(allTrades) != 2 {
		t.Errorf("expected 2 trade records in store, got %d", len(allTrades))
	}
}

// --- CancelOrder tests ---

func TestCancelOrder_PendingBid(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 1000000, nil) // $10,000

	order := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 5) // $150 × 5
	m.MatchLimitOrder(order)

	cancelled, err := m.CancelOrder(order.OrderID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cancelled.Status != domain.OrderStatusCancelled {
		t.Errorf("expected status cancelled, got %s", cancelled.Status)
	}
	if cancelled.CancelledQuantity != 5 {
		t.Errorf("expected cancelled_quantity 5, got %d", cancelled.CancelledQuantity)
	}
	if cancelled.RemainingQuantity != 0 {
		t.Errorf("expected remaining_quantity 0, got %d", cancelled.RemainingQuantity)
	}
	if cancelled.CancelledAt == nil {
		t.Error("expected cancelled_at to be set")
	}
	if cancelled.FilledQuantity != 0 {
		t.Errorf("expected filled_quantity 0, got %d", cancelled.FilledQuantity)
	}
}

func TestCancelOrder_PendingAsk(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	order := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 15000, 10)
	m.MatchLimitOrder(order)

	cancelled, err := m.CancelOrder(order.OrderID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cancelled.Status != domain.OrderStatusCancelled {
		t.Errorf("expected status cancelled, got %s", cancelled.Status)
	}
	if cancelled.CancelledQuantity != 10 {
		t.Errorf("expected cancelled_quantity 10, got %d", cancelled.CancelledQuantity)
	}
	if cancelled.RemainingQuantity != 0 {
		t.Errorf("expected remaining_quantity 0, got %d", cancelled.RemainingQuantity)
	}
}

func TestCancelOrder_PartiallyFilledBid(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 5000000, nil) // $50,000
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Seller places ask for 5 shares at $150.
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 15000, 5))

	// Buyer places bid for 10 shares at $150 — fills 5, rests 5.
	order := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 10)
	m.MatchLimitOrder(order)

	if order.Status != domain.OrderStatusPartiallyFilled {
		t.Fatalf("expected partially_filled, got %s", order.Status)
	}

	cancelled, err := m.CancelOrder(order.OrderID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cancelled.Status != domain.OrderStatusCancelled {
		t.Errorf("expected status cancelled, got %s", cancelled.Status)
	}
	if cancelled.FilledQuantity != 5 {
		t.Errorf("expected filled_quantity 5, got %d", cancelled.FilledQuantity)
	}
	if cancelled.CancelledQuantity != 5 {
		t.Errorf("expected cancelled_quantity 5, got %d", cancelled.CancelledQuantity)
	}
	if cancelled.RemainingQuantity != 0 {
		t.Errorf("expected remaining_quantity 0, got %d", cancelled.RemainingQuantity)
	}
	// Trades should be preserved.
	if len(cancelled.Trades) != 1 {
		t.Errorf("expected 1 trade preserved, got %d", len(cancelled.Trades))
	}
}

func TestCancelOrder_PartiallyFilledAsk(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 5000000, nil)
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Buyer places bid for 5 shares at $150.
	m.MatchLimitOrder(newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 5))

	// Seller places ask for 10 shares at $150 — fills 5, rests 5.
	order := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 15000, 10)
	m.MatchLimitOrder(order)

	if order.Status != domain.OrderStatusPartiallyFilled {
		t.Fatalf("expected partially_filled, got %s", order.Status)
	}

	cancelled, err := m.CancelOrder(order.OrderID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cancelled.Status != domain.OrderStatusCancelled {
		t.Errorf("expected status cancelled, got %s", cancelled.Status)
	}
	if cancelled.FilledQuantity != 5 {
		t.Errorf("expected filled_quantity 5, got %d", cancelled.FilledQuantity)
	}
	if cancelled.CancelledQuantity != 5 {
		t.Errorf("expected cancelled_quantity 5, got %d", cancelled.CancelledQuantity)
	}
	if len(cancelled.Trades) != 1 {
		t.Errorf("expected 1 trade preserved, got %d", len(cancelled.Trades))
	}
}

func TestCancelOrder_OrderNotFound(t *testing.T) {
	m, _, _, _ := newTestMatcher()

	_, err := m.CancelOrder("nonexistent-order-id")
	if err != domain.ErrOrderNotFound {
		t.Errorf("expected ErrOrderNotFound, got %v", err)
	}
}

func TestCancelOrder_FilledOrderNotCancellable(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 5000000, nil)
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Create a fully filled order.
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 15000, 10))
	order := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 10)
	m.MatchLimitOrder(order)

	if order.Status != domain.OrderStatusFilled {
		t.Fatalf("expected filled, got %s", order.Status)
	}

	_, err := m.CancelOrder(order.OrderID)
	if err != domain.ErrOrderNotCancellable {
		t.Errorf("expected ErrOrderNotCancellable, got %v", err)
	}
}

func TestCancelOrder_AlreadyCancelledNotCancellable(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 1000000, nil)

	order := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 5)
	m.MatchLimitOrder(order)

	// Cancel once.
	_, err := m.CancelOrder(order.OrderID)
	if err != nil {
		t.Fatalf("first cancel failed: %v", err)
	}

	// Cancel again — should fail.
	_, err = m.CancelOrder(order.OrderID)
	if err != domain.ErrOrderNotCancellable {
		t.Errorf("expected ErrOrderNotCancellable on second cancel, got %v", err)
	}
}

func TestCancelOrder_ReleasesReservedCashForBid(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	broker := registerBroker(bs, "buyer", 1000000, nil) // $10,000

	order := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 5) // $150 × 5 = $750
	m.MatchLimitOrder(order)

	// Verify reservation was made.
	broker.Mu.Lock()
	if broker.ReservedCash != 75000 { // 15000 × 5
		t.Errorf("expected reserved_cash 75000 before cancel, got %d", broker.ReservedCash)
	}
	broker.Mu.Unlock()

	m.CancelOrder(order.OrderID)

	// Verify reservation was released.
	broker.Mu.Lock()
	if broker.ReservedCash != 0 {
		t.Errorf("expected reserved_cash 0 after cancel, got %d", broker.ReservedCash)
	}
	broker.Mu.Unlock()
}

func TestCancelOrder_ReleasesReservedQuantityForAsk(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	broker := registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	order := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 15000, 10)
	m.MatchLimitOrder(order)

	// Verify reservation was made.
	broker.Mu.Lock()
	if broker.Holdings["AAPL"].ReservedQuantity != 10 {
		t.Errorf("expected reserved_quantity 10 before cancel, got %d", broker.Holdings["AAPL"].ReservedQuantity)
	}
	broker.Mu.Unlock()

	m.CancelOrder(order.OrderID)

	// Verify reservation was released.
	broker.Mu.Lock()
	if broker.Holdings["AAPL"].ReservedQuantity != 0 {
		t.Errorf("expected reserved_quantity 0 after cancel, got %d", broker.Holdings["AAPL"].ReservedQuantity)
	}
	broker.Mu.Unlock()
}

func TestCancelOrder_RemovesFromBook(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 1000000, nil)

	order := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 5)
	m.MatchLimitOrder(order)

	book := m.books.GetOrCreate("AAPL")
	if book.BidCount() != 1 {
		t.Fatalf("expected 1 bid on book, got %d", book.BidCount())
	}

	m.CancelOrder(order.OrderID)

	if book.BidCount() != 0 {
		t.Errorf("expected 0 bids on book after cancel, got %d", book.BidCount())
	}
}

func TestCancelOrder_QuantityConservation(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer", 5000000, nil)
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Partial fill: 3 out of 10.
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 15000, 3))
	order := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 10)
	m.MatchLimitOrder(order)

	m.CancelOrder(order.OrderID)

	// quantity == filled_quantity + remaining_quantity + cancelled_quantity
	total := order.FilledQuantity + order.RemainingQuantity + order.CancelledQuantity
	if total != order.Quantity {
		t.Errorf("quantity conservation violated: %d + %d + %d = %d, expected %d",
			order.FilledQuantity, order.RemainingQuantity, order.CancelledQuantity, total, order.Quantity)
	}
}

func TestCancelOrder_PartialFillReleasesOnlyUnfilledReservation(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	broker := registerBroker(bs, "buyer", 5000000, nil) // $50,000
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Seller offers 3 at $150.
	m.MatchLimitOrder(newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 15000, 3))

	// Buyer bids 10 at $150 — fills 3, rests 7.
	order := newLimitOrder("buyer", domain.OrderSideBid, "AAPL", 15000, 10)
	m.MatchLimitOrder(order)

	// Reserved cash should be for the remaining 7 shares: 15000 × 7 = 105000.
	broker.Mu.Lock()
	reservedBefore := broker.ReservedCash
	broker.Mu.Unlock()
	if reservedBefore != 105000 {
		t.Fatalf("expected reserved_cash 105000 before cancel, got %d", reservedBefore)
	}

	m.CancelOrder(order.OrderID)

	// After cancel, reserved cash should be 0 (released 15000 × 7 = 105000).
	broker.Mu.Lock()
	if broker.ReservedCash != 0 {
		t.Errorf("expected reserved_cash 0 after cancel, got %d", broker.ReservedCash)
	}
	broker.Mu.Unlock()
}

// --- SimulateMarketOrder tests ---

func TestSimulateMarketOrder_BidQuote_WalksAsks(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller1", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})
	registerBroker(bs, "seller2", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 100},
	})

	// Place asks at different prices.
	ask1 := newLimitOrder("seller1", domain.OrderSideAsk, "AAPL", 10000, 5)
	ask2 := newLimitOrder("seller2", domain.OrderSideAsk, "AAPL", 10500, 10)
	m.MatchLimitOrder(ask1)
	m.MatchLimitOrder(ask2)

	result := m.SimulateMarketOrder("AAPL", domain.OrderSideBid, 8)

	if result.QuantityAvailable != 8 {
		t.Errorf("expected quantity_available 8, got %d", result.QuantityAvailable)
	}
	if !result.FullyFillable {
		t.Error("expected fully_fillable true")
	}
	// 5 @ 10000 + 3 @ 10500 = 50000 + 31500 = 81500
	expectedTotal := int64(81500)
	if result.EstimatedTotal == nil || *result.EstimatedTotal != expectedTotal {
		t.Errorf("expected estimated_total %d, got %v", expectedTotal, result.EstimatedTotal)
	}
	expectedAvg := expectedTotal / 8 // 10187
	if result.EstimatedAvgPrice == nil || *result.EstimatedAvgPrice != expectedAvg {
		t.Errorf("expected estimated_avg_price %d, got %v", expectedAvg, result.EstimatedAvgPrice)
	}
	if len(result.PriceLevels) != 2 {
		t.Fatalf("expected 2 price levels, got %d", len(result.PriceLevels))
	}
	if result.PriceLevels[0].Price != 10000 || result.PriceLevels[0].Quantity != 5 {
		t.Errorf("level 0: expected 10000/5, got %d/%d", result.PriceLevels[0].Price, result.PriceLevels[0].Quantity)
	}
	if result.PriceLevels[1].Price != 10500 || result.PriceLevels[1].Quantity != 3 {
		t.Errorf("level 1: expected 10500/3, got %d/%d", result.PriceLevels[1].Price, result.PriceLevels[1].Quantity)
	}
}

func TestSimulateMarketOrder_AskQuote_WalksBids(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "buyer1", 1000000, nil)
	registerBroker(bs, "buyer2", 1000000, nil)

	bid1 := newLimitOrder("buyer1", domain.OrderSideBid, "GOOG", 20000, 10)
	bid2 := newLimitOrder("buyer2", domain.OrderSideBid, "GOOG", 19500, 5)
	m.MatchLimitOrder(bid1)
	m.MatchLimitOrder(bid2)

	result := m.SimulateMarketOrder("GOOG", domain.OrderSideAsk, 12)

	if result.QuantityAvailable != 12 {
		t.Errorf("expected quantity_available 12, got %d", result.QuantityAvailable)
	}
	if !result.FullyFillable {
		t.Error("expected fully_fillable true")
	}
	// 10 @ 20000 + 2 @ 19500 = 200000 + 39000 = 239000
	expectedTotal := int64(239000)
	if result.EstimatedTotal == nil || *result.EstimatedTotal != expectedTotal {
		t.Errorf("expected estimated_total %d, got %v", expectedTotal, result.EstimatedTotal)
	}
	if len(result.PriceLevels) != 2 {
		t.Fatalf("expected 2 price levels, got %d", len(result.PriceLevels))
	}
	if result.PriceLevels[0].Price != 20000 || result.PriceLevels[0].Quantity != 10 {
		t.Errorf("level 0: expected 20000/10, got %d/%d", result.PriceLevels[0].Price, result.PriceLevels[0].Quantity)
	}
	if result.PriceLevels[1].Price != 19500 || result.PriceLevels[1].Quantity != 2 {
		t.Errorf("level 1: expected 19500/2, got %d/%d", result.PriceLevels[1].Price, result.PriceLevels[1].Quantity)
	}
}

func TestSimulateMarketOrder_NoLiquidity(t *testing.T) {
	m, _, _, _ := newTestMatcher()

	result := m.SimulateMarketOrder("AAPL", domain.OrderSideBid, 10)

	if result.QuantityAvailable != 0 {
		t.Errorf("expected quantity_available 0, got %d", result.QuantityAvailable)
	}
	if result.FullyFillable {
		t.Error("expected fully_fillable false")
	}
	if result.EstimatedAvgPrice != nil {
		t.Errorf("expected estimated_avg_price nil, got %d", *result.EstimatedAvgPrice)
	}
	if result.EstimatedTotal != nil {
		t.Errorf("expected estimated_total nil, got %d", *result.EstimatedTotal)
	}
	if len(result.PriceLevels) != 0 {
		t.Errorf("expected 0 price levels, got %d", len(result.PriceLevels))
	}
}

func TestSimulateMarketOrder_PartialLiquidity(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 5},
	})

	ask := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 5)
	m.MatchLimitOrder(ask)

	result := m.SimulateMarketOrder("AAPL", domain.OrderSideBid, 20)

	if result.QuantityAvailable != 5 {
		t.Errorf("expected quantity_available 5, got %d", result.QuantityAvailable)
	}
	if result.FullyFillable {
		t.Error("expected fully_fillable false")
	}
	expectedTotal := int64(50000)
	if result.EstimatedTotal == nil || *result.EstimatedTotal != expectedTotal {
		t.Errorf("expected estimated_total %d, got %v", expectedTotal, result.EstimatedTotal)
	}
	expectedAvg := int64(10000)
	if result.EstimatedAvgPrice == nil || *result.EstimatedAvgPrice != expectedAvg {
		t.Errorf("expected estimated_avg_price %d, got %v", expectedAvg, result.EstimatedAvgPrice)
	}
}

func TestSimulateMarketOrder_DoesNotModifyBook(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})

	ask := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 10000, 10)
	m.MatchLimitOrder(ask)

	book := m.books.GetOrCreate("AAPL")

	// Snapshot book state before simulation.
	book.mu.RLock()
	askCountBefore := book.AskCount()
	book.mu.RUnlock()

	m.SimulateMarketOrder("AAPL", domain.OrderSideBid, 5)

	// Book should be unchanged.
	book.mu.RLock()
	askCountAfter := book.AskCount()
	book.mu.RUnlock()

	if askCountBefore != askCountAfter {
		t.Errorf("simulation modified book: ask count before=%d, after=%d", askCountBefore, askCountAfter)
	}
}

func TestSimulateMarketOrder_SinglePriceLevel_MultipleOrders(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller1", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})
	registerBroker(bs, "seller2", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})

	// Two asks at the same price.
	ask1 := newLimitOrder("seller1", domain.OrderSideAsk, "AAPL", 10000, 5)
	ask2 := newLimitOrder("seller2", domain.OrderSideAsk, "AAPL", 10000, 7)
	m.MatchLimitOrder(ask1)
	m.MatchLimitOrder(ask2)

	result := m.SimulateMarketOrder("AAPL", domain.OrderSideBid, 10)

	if result.QuantityAvailable != 10 {
		t.Errorf("expected quantity_available 10, got %d", result.QuantityAvailable)
	}
	// All at same price → single price level.
	if len(result.PriceLevels) != 1 {
		t.Fatalf("expected 1 price level, got %d", len(result.PriceLevels))
	}
	if result.PriceLevels[0].Price != 10000 || result.PriceLevels[0].Quantity != 10 {
		t.Errorf("level 0: expected 10000/10, got %d/%d", result.PriceLevels[0].Price, result.PriceLevels[0].Quantity)
	}
	expectedTotal := int64(100000)
	if result.EstimatedTotal == nil || *result.EstimatedTotal != expectedTotal {
		t.Errorf("expected estimated_total %d, got %v", expectedTotal, result.EstimatedTotal)
	}
}

func TestSimulateMarketOrder_ExactQuantityMatch(t *testing.T) {
	m, bs, _, _ := newTestMatcher()
	registerBroker(bs, "seller", 0, map[string]*domain.Holding{
		"AAPL": {Quantity: 10},
	})

	ask := newLimitOrder("seller", domain.OrderSideAsk, "AAPL", 15000, 10)
	m.MatchLimitOrder(ask)

	result := m.SimulateMarketOrder("AAPL", domain.OrderSideBid, 10)

	if result.QuantityAvailable != 10 {
		t.Errorf("expected quantity_available 10, got %d", result.QuantityAvailable)
	}
	if !result.FullyFillable {
		t.Error("expected fully_fillable true")
	}
	expectedTotal := int64(150000)
	if result.EstimatedTotal == nil || *result.EstimatedTotal != expectedTotal {
		t.Errorf("expected estimated_total %d, got %v", expectedTotal, result.EstimatedTotal)
	}
}
