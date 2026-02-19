package service

import (
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/engine"
	"github.com/efreitasn/miniexchange/internal/store"
)

// newTestStockService creates a StockService with fresh dependencies for testing.
func newTestStockService(vwapWindow time.Duration) (*StockService, *store.TradeStore, *engine.BookManager, *engine.Matcher, *domain.SymbolRegistry, *store.BrokerStore, *store.OrderStore) {
	tradeStore := store.NewTradeStore()
	brokerStore := store.NewBrokerStore()
	orderStore := store.NewOrderStore()
	symbols := domain.NewSymbolRegistry()
	books := engine.NewBookManager()
	matcher := engine.NewMatcher(books, brokerStore, orderStore, tradeStore, symbols)

	svc := NewStockService(tradeStore, books, matcher, vwapWindow, symbols)
	return svc, tradeStore, books, matcher, symbols, brokerStore, orderStore
}

// --- GetPrice tests ---

func TestGetPrice_SymbolNotFound(t *testing.T) {
	svc, _, _, _, _, _, _ := newTestStockService(5 * time.Minute)

	_, err := svc.GetPrice("AAPL")
	if err != domain.ErrSymbolNotFound {
		t.Fatalf("expected ErrSymbolNotFound, got %v", err)
	}
}

func TestGetPrice_NoTradesEver(t *testing.T) {
	svc, _, _, _, symbols, _, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	resp, err := svc.GetPrice("AAPL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.CurrentPrice != nil {
		t.Fatalf("expected nil current_price, got %d", *resp.CurrentPrice)
	}
	if resp.LastTradeAt != nil {
		t.Fatalf("expected nil last_trade_at, got %v", *resp.LastTradeAt)
	}
	if resp.TradesInWindow != 0 {
		t.Fatalf("expected 0 trades_in_window, got %d", resp.TradesInWindow)
	}
	if resp.Window != "5m" {
		t.Fatalf("expected window '5m', got %q", resp.Window)
	}
}

func TestGetPrice_VWAPWithTradesInWindow(t *testing.T) {
	svc, tradeStore, _, _, symbols, _, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	now := time.Now()
	// Trade 1: price=10000 (100.00), qty=200
	tradeStore.Append("AAPL", &domain.Trade{
		TradeID:    "t1",
		OrderID:    "o1",
		Price:      10000,
		Quantity:   200,
		ExecutedAt: now.Add(-2 * time.Minute),
	})
	// Trade 2: price=11000 (110.00), qty=300
	tradeStore.Append("AAPL", &domain.Trade{
		TradeID:    "t2",
		OrderID:    "o2",
		Price:      11000,
		Quantity:   300,
		ExecutedAt: now.Add(-1 * time.Minute),
	})

	resp, err := svc.GetPrice("AAPL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// VWAP = (10000*200 + 11000*300) / (200+300) = (2000000 + 3300000) / 500 = 10600
	expectedVWAP := int64(10600)
	if resp.CurrentPrice == nil || *resp.CurrentPrice != expectedVWAP {
		t.Fatalf("expected VWAP %d, got %v", expectedVWAP, resp.CurrentPrice)
	}
	if resp.TradesInWindow != 2 {
		t.Fatalf("expected 2 trades_in_window, got %d", resp.TradesInWindow)
	}
}

func TestGetPrice_FallbackToLastTrade(t *testing.T) {
	svc, tradeStore, _, _, symbols, _, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	// Trade outside the window.
	tradeStore.Append("AAPL", &domain.Trade{
		TradeID:    "t1",
		OrderID:    "o1",
		Price:      15000,
		Quantity:   100,
		ExecutedAt: time.Now().Add(-10 * time.Minute),
	})

	resp, err := svc.GetPrice("AAPL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No trades in window → fallback to last trade's price.
	if resp.CurrentPrice == nil || *resp.CurrentPrice != 15000 {
		t.Fatalf("expected fallback price 15000, got %v", resp.CurrentPrice)
	}
	if resp.TradesInWindow != 0 {
		t.Fatalf("expected 0 trades_in_window, got %d", resp.TradesInWindow)
	}
	if resp.LastTradeAt == nil {
		t.Fatal("expected non-nil last_trade_at")
	}
}

func TestGetPrice_MixedWindowAndOutside(t *testing.T) {
	svc, tradeStore, _, _, symbols, _, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	now := time.Now()
	// Old trade outside window.
	tradeStore.Append("AAPL", &domain.Trade{
		TradeID:    "t1",
		OrderID:    "o1",
		Price:      9000,
		Quantity:   100,
		ExecutedAt: now.Add(-10 * time.Minute),
	})
	// Recent trade inside window.
	tradeStore.Append("AAPL", &domain.Trade{
		TradeID:    "t2",
		OrderID:    "o2",
		Price:      11000,
		Quantity:   500,
		ExecutedAt: now.Add(-1 * time.Minute),
	})

	resp, err := svc.GetPrice("AAPL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the recent trade is in the window.
	// VWAP = 11000*500 / 500 = 11000
	if resp.CurrentPrice == nil || *resp.CurrentPrice != 11000 {
		t.Fatalf("expected VWAP 11000, got %v", resp.CurrentPrice)
	}
	if resp.TradesInWindow != 1 {
		t.Fatalf("expected 1 trade_in_window, got %d", resp.TradesInWindow)
	}
}

// --- GetBook tests ---

func TestGetBook_SymbolNotFound(t *testing.T) {
	svc, _, _, _, _, _, _ := newTestStockService(5 * time.Minute)

	_, err := svc.GetBook("AAPL", 10)
	if err != domain.ErrSymbolNotFound {
		t.Fatalf("expected ErrSymbolNotFound, got %v", err)
	}
}

func TestGetBook_InvalidDepth(t *testing.T) {
	svc, _, _, _, symbols, _, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	tests := []struct {
		name  string
		depth int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too large", 51},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.GetBook("AAPL", tc.depth)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if _, ok := err.(*domain.ValidationError); !ok {
				t.Fatalf("expected ValidationError, got %T: %v", err, err)
			}
		})
	}
}

func TestGetBook_EmptyBook(t *testing.T) {
	svc, _, _, _, symbols, _, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	resp, err := svc.GetBook("AAPL", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Bids) != 0 {
		t.Fatalf("expected 0 bids, got %d", len(resp.Bids))
	}
	if len(resp.Asks) != 0 {
		t.Fatalf("expected 0 asks, got %d", len(resp.Asks))
	}
	if resp.Spread != nil {
		t.Fatalf("expected nil spread, got %d", *resp.Spread)
	}
}

func TestGetBook_WithOrders(t *testing.T) {
	svc, _, _, _, symbols, brokerStore, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	// Create brokers with holdings.
	brokerStore.Create(&domain.Broker{
		BrokerID:    "buyer",
		CashBalance: 10000000, // $100,000
		Holdings:    map[string]*domain.Holding{},
		CreatedAt:   time.Now(),
	})
	brokerStore.Create(&domain.Broker{
		BrokerID:    "seller",
		CashBalance: 0,
		Holdings: map[string]*domain.Holding{
			"AAPL": {Quantity: 10000},
		},
		CreatedAt: time.Now(),
	})

	// Place bid and ask orders via the matcher to populate the book.
	now := time.Now()
	expires := now.Add(1 * time.Hour)

	bidOrder := &domain.Order{
		Type:     domain.OrderTypeLimit,
		BrokerID: "buyer",
		Side:     domain.OrderSideBid,
		Symbol:   "AAPL",
		Price:    15000, // $150.00
		Quantity: 100,
		ExpiresAt: &expires,
	}
	svc.matcher.MatchLimitOrder(bidOrder)

	askOrder := &domain.Order{
		Type:     domain.OrderTypeLimit,
		BrokerID: "seller",
		Side:     domain.OrderSideAsk,
		Symbol:   "AAPL",
		Price:    16000, // $160.00
		Quantity: 200,
		ExpiresAt: &expires,
	}
	svc.matcher.MatchLimitOrder(askOrder)

	resp, err := svc.GetBook("AAPL", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Bids) != 1 {
		t.Fatalf("expected 1 bid level, got %d", len(resp.Bids))
	}
	if resp.Bids[0].Price != 15000 || resp.Bids[0].TotalQuantity != 100 || resp.Bids[0].OrderCount != 1 {
		t.Fatalf("unexpected bid level: %+v", resp.Bids[0])
	}

	if len(resp.Asks) != 1 {
		t.Fatalf("expected 1 ask level, got %d", len(resp.Asks))
	}
	if resp.Asks[0].Price != 16000 || resp.Asks[0].TotalQuantity != 200 || resp.Asks[0].OrderCount != 1 {
		t.Fatalf("unexpected ask level: %+v", resp.Asks[0])
	}

	// Spread = 16000 - 15000 = 1000
	if resp.Spread == nil || *resp.Spread != 1000 {
		t.Fatalf("expected spread 1000, got %v", resp.Spread)
	}
}

func TestGetBook_DepthLimitsResults(t *testing.T) {
	svc, _, _, _, symbols, brokerStore, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	brokerStore.Create(&domain.Broker{
		BrokerID:    "seller",
		CashBalance: 0,
		Holdings: map[string]*domain.Holding{
			"AAPL": {Quantity: 100000},
		},
		CreatedAt: time.Now(),
	})

	expires := time.Now().Add(1 * time.Hour)

	// Place asks at 3 different price levels.
	for _, price := range []int64{15000, 16000, 17000} {
		order := &domain.Order{
			Type:      domain.OrderTypeLimit,
			BrokerID:  "seller",
			Side:      domain.OrderSideAsk,
			Symbol:    "AAPL",
			Price:     price,
			Quantity:  100,
			ExpiresAt: &expires,
		}
		svc.matcher.MatchLimitOrder(order)
	}

	// Request depth=2 — should only get 2 levels.
	resp, err := svc.GetBook("AAPL", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Asks) != 2 {
		t.Fatalf("expected 2 ask levels, got %d", len(resp.Asks))
	}
	if resp.Asks[0].Price != 15000 {
		t.Fatalf("expected first ask at 15000, got %d", resp.Asks[0].Price)
	}
	if resp.Asks[1].Price != 16000 {
		t.Fatalf("expected second ask at 16000, got %d", resp.Asks[1].Price)
	}
}

// --- GetQuote tests ---

func TestGetQuote_SymbolNotFound(t *testing.T) {
	svc, _, _, _, _, _, _ := newTestStockService(5 * time.Minute)

	_, err := svc.GetQuote("AAPL", domain.OrderSideBid, 100)
	if err != domain.ErrSymbolNotFound {
		t.Fatalf("expected ErrSymbolNotFound, got %v", err)
	}
}

func TestGetQuote_InvalidSide(t *testing.T) {
	svc, _, _, _, symbols, _, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	_, err := svc.GetQuote("AAPL", "invalid", 100)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestGetQuote_InvalidQuantity(t *testing.T) {
	svc, _, _, _, symbols, _, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	_, err := svc.GetQuote("AAPL", domain.OrderSideBid, 0)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}

	_, err = svc.GetQuote("AAPL", domain.OrderSideBid, -5)
	if err == nil {
		t.Fatal("expected validation error for negative quantity")
	}
}

func TestGetQuote_NoLiquidity(t *testing.T) {
	svc, _, _, _, symbols, _, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	resp, err := svc.GetQuote("AAPL", domain.OrderSideBid, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.QuantityAvailable != 0 {
		t.Fatalf("expected 0 quantity_available, got %d", resp.QuantityAvailable)
	}
	if resp.FullyFillable {
		t.Fatal("expected fully_fillable=false")
	}
	if resp.EstimatedAvgPrice != nil {
		t.Fatalf("expected nil estimated_avg_price, got %d", *resp.EstimatedAvgPrice)
	}
	if resp.EstimatedTotal != nil {
		t.Fatalf("expected nil estimated_total, got %d", *resp.EstimatedTotal)
	}
	if len(resp.PriceLevels) != 0 {
		t.Fatalf("expected 0 price_levels, got %d", len(resp.PriceLevels))
	}
}

func TestGetQuote_BidWithLiquidity(t *testing.T) {
	svc, _, _, _, symbols, brokerStore, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	brokerStore.Create(&domain.Broker{
		BrokerID:    "seller",
		CashBalance: 0,
		Holdings: map[string]*domain.Holding{
			"AAPL": {Quantity: 10000},
		},
		CreatedAt: time.Now(),
	})

	expires := time.Now().Add(1 * time.Hour)

	// Place ask orders at two price levels.
	ask1 := &domain.Order{
		Type:      domain.OrderTypeLimit,
		BrokerID:  "seller",
		Side:      domain.OrderSideAsk,
		Symbol:    "AAPL",
		Price:     14800, // $148.00
		Quantity:  700,
		ExpiresAt: &expires,
	}
	svc.matcher.MatchLimitOrder(ask1)

	ask2 := &domain.Order{
		Type:      domain.OrderTypeLimit,
		BrokerID:  "seller",
		Side:      domain.OrderSideAsk,
		Symbol:    "AAPL",
		Price:     15000, // $150.00
		Quantity:  300,
		ExpiresAt: &expires,
	}
	svc.matcher.MatchLimitOrder(ask2)

	// Bid quote for 1000 shares — should sweep both levels.
	resp, err := svc.GetQuote("AAPL", domain.OrderSideBid, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.QuantityAvailable != 1000 {
		t.Fatalf("expected 1000 quantity_available, got %d", resp.QuantityAvailable)
	}
	if !resp.FullyFillable {
		t.Fatal("expected fully_fillable=true")
	}

	// estimated_total = 14800*700 + 15000*300 = 10360000 + 4500000 = 14860000
	expectedTotal := int64(14860000)
	if resp.EstimatedTotal == nil || *resp.EstimatedTotal != expectedTotal {
		t.Fatalf("expected estimated_total %d, got %v", expectedTotal, resp.EstimatedTotal)
	}

	// estimated_avg_price = 14860000 / 1000 = 14860
	expectedAvg := int64(14860)
	if resp.EstimatedAvgPrice == nil || *resp.EstimatedAvgPrice != expectedAvg {
		t.Fatalf("expected estimated_avg_price %d, got %v", expectedAvg, resp.EstimatedAvgPrice)
	}

	if len(resp.PriceLevels) != 2 {
		t.Fatalf("expected 2 price_levels, got %d", len(resp.PriceLevels))
	}
	if resp.PriceLevels[0].Price != 14800 || resp.PriceLevels[0].Quantity != 700 {
		t.Fatalf("unexpected first price level: %+v", resp.PriceLevels[0])
	}
	if resp.PriceLevels[1].Price != 15000 || resp.PriceLevels[1].Quantity != 300 {
		t.Fatalf("unexpected second price level: %+v", resp.PriceLevels[1])
	}
}

func TestGetQuote_PartialLiquidity(t *testing.T) {
	svc, _, _, _, symbols, brokerStore, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	brokerStore.Create(&domain.Broker{
		BrokerID:    "seller",
		CashBalance: 0,
		Holdings: map[string]*domain.Holding{
			"AAPL": {Quantity: 500},
		},
		CreatedAt: time.Now(),
	})

	expires := time.Now().Add(1 * time.Hour)
	ask := &domain.Order{
		Type:      domain.OrderTypeLimit,
		BrokerID:  "seller",
		Side:      domain.OrderSideAsk,
		Symbol:    "AAPL",
		Price:     14800,
		Quantity:  400,
		ExpiresAt: &expires,
	}
	svc.matcher.MatchLimitOrder(ask)

	// Request 1000 but only 400 available.
	resp, err := svc.GetQuote("AAPL", domain.OrderSideBid, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.QuantityAvailable != 400 {
		t.Fatalf("expected 400 quantity_available, got %d", resp.QuantityAvailable)
	}
	if resp.FullyFillable {
		t.Fatal("expected fully_fillable=false")
	}
	if resp.QuantityRequested != 1000 {
		t.Fatalf("expected quantity_requested=1000, got %d", resp.QuantityRequested)
	}
}

func TestGetQuote_AskSide(t *testing.T) {
	svc, _, _, _, symbols, brokerStore, _ := newTestStockService(5 * time.Minute)
	symbols.Register("AAPL")

	brokerStore.Create(&domain.Broker{
		BrokerID:    "buyer",
		CashBalance: 100000000, // $1,000,000
		Holdings:    map[string]*domain.Holding{},
		CreatedAt:   time.Now(),
	})

	expires := time.Now().Add(1 * time.Hour)
	bid := &domain.Order{
		Type:      domain.OrderTypeLimit,
		BrokerID:  "buyer",
		Side:      domain.OrderSideBid,
		Symbol:    "AAPL",
		Price:     15000, // $150.00
		Quantity:  500,
		ExpiresAt: &expires,
	}
	svc.matcher.MatchLimitOrder(bid)

	// Ask quote — walks bid side.
	resp, err := svc.GetQuote("AAPL", domain.OrderSideAsk, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.QuantityAvailable != 500 {
		t.Fatalf("expected 500 quantity_available, got %d", resp.QuantityAvailable)
	}
	if !resp.FullyFillable {
		t.Fatal("expected fully_fillable=true")
	}
	if resp.Side != domain.OrderSideAsk {
		t.Fatalf("expected side=ask, got %s", resp.Side)
	}
}

// --- formatDuration tests ---

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Minute, "5m"},
		{1 * time.Minute, "1m"},
		{30 * time.Second, "30s"},
		{0, "0s"},
		{90 * time.Second, "1m30s"},
	}

	for _, tc := range tests {
		got := formatDuration(tc.d)
		if got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
