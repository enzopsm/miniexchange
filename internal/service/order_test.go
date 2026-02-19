package service

import (
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/engine"
	"github.com/efreitasn/miniexchange/internal/store"
)

// testOrderEnv bundles all dependencies needed for OrderService tests.
type testOrderEnv struct {
	brokerStore *store.BrokerStore
	orderStore  *store.OrderStore
	tradeStore  *store.TradeStore
	symbols     *domain.SymbolRegistry
	books       *engine.BookManager
	matcher     *engine.Matcher
	expiry      *engine.ExpiryManager
	svc         *OrderService
	brokerSvc   *BrokerService
}

func newTestOrderEnv() *testOrderEnv {
	bs := store.NewBrokerStore()
	os := store.NewOrderStore()
	ts := store.NewTradeStore()
	sr := domain.NewSymbolRegistry()
	bm := engine.NewBookManager()
	m := engine.NewMatcher(bm, bs, os, ts, sr)
	e := engine.NewExpiryManager(time.Second, bm, os, bs, nil)
	svc := NewOrderService(m, e, bs, os, ts, nil, sr)
	bsvc := NewBrokerService(bs, sr)
	return &testOrderEnv{
		brokerStore: bs,
		orderStore:  os,
		tradeStore:  ts,
		symbols:     sr,
		books:       bm,
		matcher:     m,
		expiry:      e,
		svc:         svc,
		brokerSvc:   bsvc,
	}
}

// registerBroker is a helper that registers a broker with cash and optional holdings.
func (env *testOrderEnv) registerBroker(t *testing.T, id string, cash float64, holdings []HoldingInput) {
	t.Helper()
	_, err := env.brokerSvc.Register(RegisterBrokerRequest{
		BrokerID:        id,
		InitialCash:     cash,
		InitialHoldings: holdings,
	})
	if err != nil {
		t.Fatalf("failed to register broker %s: %v", id, err)
	}
}

func futureTime() *time.Time {
	t := time.Now().Add(24 * time.Hour)
	return &t
}

func floatPtr(f float64) *float64 {
	return &f
}

// --- SubmitOrder: Limit Order Tests ---

func TestSubmitOrder_LimitBid_Pending(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "buyer", 100000.00, nil)

	order, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "buyer",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.OrderID == "" {
		t.Error("expected non-empty order_id")
	}
	if order.Type != domain.OrderTypeLimit {
		t.Errorf("got type %q, want %q", order.Type, domain.OrderTypeLimit)
	}
	if order.Status != domain.OrderStatusPending {
		t.Errorf("got status %q, want %q", order.Status, domain.OrderStatusPending)
	}
	if order.Price != 15000 {
		t.Errorf("got price %d, want 15000", order.Price)
	}
	if order.Quantity != 100 {
		t.Errorf("got quantity %d, want 100", order.Quantity)
	}
	if order.RemainingQuantity != 100 {
		t.Errorf("got remaining_quantity %d, want 100", order.RemainingQuantity)
	}
	if order.FilledQuantity != 0 {
		t.Errorf("got filled_quantity %d, want 0", order.FilledQuantity)
	}
	if len(order.Trades) != 0 {
		t.Errorf("got %d trades, want 0", len(order.Trades))
	}

	// Verify it was added to expiry manager.
	if env.expiry.ActiveOrderCount() != 1 {
		t.Errorf("expected 1 active order in expiry manager, got %d", env.expiry.ActiveOrderCount())
	}
}

func TestSubmitOrder_LimitAsk_Pending(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "seller", 0, []HoldingInput{{Symbol: "AAPL", Quantity: 500}})

	order, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "seller",
		DocumentNumber: "DOC002",
		Side:           domain.OrderSideAsk,
		Symbol:         "AAPL",
		Price:          floatPtr(155.00),
		Quantity:       200,
		ExpiresAt:      futureTime(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != domain.OrderStatusPending {
		t.Errorf("got status %q, want %q", order.Status, domain.OrderStatusPending)
	}
	if order.Side != domain.OrderSideAsk {
		t.Errorf("got side %q, want %q", order.Side, domain.OrderSideAsk)
	}
}

func TestSubmitOrder_LimitBid_FullyFilled(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "seller", 0, []HoldingInput{{Symbol: "AAPL", Quantity: 500}})
	env.registerBroker(t, "buyer", 100000.00, nil)

	// Place a resting ask.
	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "seller",
		DocumentNumber: "ASK001",
		Side:           domain.OrderSideAsk,
		Symbol:         "AAPL",
		Price:          floatPtr(148.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err != nil {
		t.Fatalf("unexpected error placing ask: %v", err)
	}

	// Place a matching bid.
	order, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "buyer",
		DocumentNumber: "BID001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != domain.OrderStatusFilled {
		t.Errorf("got status %q, want %q", order.Status, domain.OrderStatusFilled)
	}
	if order.FilledQuantity != 100 {
		t.Errorf("got filled_quantity %d, want 100", order.FilledQuantity)
	}
	if order.RemainingQuantity != 0 {
		t.Errorf("got remaining_quantity %d, want 0", order.RemainingQuantity)
	}
	if len(order.Trades) != 1 {
		t.Fatalf("got %d trades, want 1", len(order.Trades))
	}
	// Execution price should be the ask price (148.00 = 14800 cents).
	if order.Trades[0].Price != 14800 {
		t.Errorf("got trade price %d, want 14800", order.Trades[0].Price)
	}

	// Filled order should NOT be in expiry manager (but the resting ask
	// that was added when submitted is still tracked — the expiry tick
	// will skip it since it's already filled).
	// The resting ask was added when submitted as pending, so count is 1.
	if env.expiry.ActiveOrderCount() != 1 {
		t.Errorf("expected 1 active order in expiry manager (stale resting ask), got %d", env.expiry.ActiveOrderCount())
	}
}

func TestSubmitOrder_LimitBid_PartiallyFilled(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "seller", 0, []HoldingInput{{Symbol: "AAPL", Quantity: 500}})
	env.registerBroker(t, "buyer", 100000.00, nil)

	// Place a small resting ask.
	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "seller",
		DocumentNumber: "ASK001",
		Side:           domain.OrderSideAsk,
		Symbol:         "AAPL",
		Price:          floatPtr(148.00),
		Quantity:       50,
		ExpiresAt:      futureTime(),
	})
	if err != nil {
		t.Fatalf("unexpected error placing ask: %v", err)
	}

	// Place a larger bid.
	order, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "buyer",
		DocumentNumber: "BID001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != domain.OrderStatusPartiallyFilled {
		t.Errorf("got status %q, want %q", order.Status, domain.OrderStatusPartiallyFilled)
	}
	if order.FilledQuantity != 50 {
		t.Errorf("got filled_quantity %d, want 50", order.FilledQuantity)
	}
	if order.RemainingQuantity != 50 {
		t.Errorf("got remaining_quantity %d, want 50", order.RemainingQuantity)
	}

	// Partially filled order should be in expiry manager.
	// The resting ask (now filled) is also still tracked (stale entry).
	// So we have 2: the stale filled ask + the partially filled bid.
	if env.expiry.ActiveOrderCount() != 2 {
		t.Errorf("expected 2 active orders in expiry manager, got %d", env.expiry.ActiveOrderCount())
	}
}

// --- SubmitOrder: Market Order Tests ---

func TestSubmitOrder_MarketBid_FullyFilled(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "seller", 0, []HoldingInput{{Symbol: "AAPL", Quantity: 500}})
	env.registerBroker(t, "buyer", 100000.00, nil)

	// Place a resting ask.
	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "seller",
		DocumentNumber: "ASK001",
		Side:           domain.OrderSideAsk,
		Symbol:         "AAPL",
		Price:          floatPtr(148.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err != nil {
		t.Fatalf("unexpected error placing ask: %v", err)
	}

	// Place a market bid.
	order, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeMarket,
		BrokerID:       "buyer",
		DocumentNumber: "MKT001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Quantity:       100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != domain.OrderStatusFilled {
		t.Errorf("got status %q, want %q", order.Status, domain.OrderStatusFilled)
	}
	if order.Type != domain.OrderTypeMarket {
		t.Errorf("got type %q, want %q", order.Type, domain.OrderTypeMarket)
	}
	if order.FilledQuantity != 100 {
		t.Errorf("got filled_quantity %d, want 100", order.FilledQuantity)
	}
}

func TestSubmitOrder_MarketBid_NoLiquidity(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "buyer", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeMarket,
		BrokerID:       "buyer",
		DocumentNumber: "MKT001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Quantity:       100,
	})
	if err != domain.ErrNoLiquidity {
		t.Errorf("got error %v, want ErrNoLiquidity", err)
	}
}

func TestSubmitOrder_MarketOrder_WithPrice_Rejected(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "buyer", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeMarket,
		BrokerID:       "buyer",
		DocumentNumber: "MKT001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestSubmitOrder_MarketOrder_WithExpiresAt_Rejected(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "buyer", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeMarket,
		BrokerID:       "buyer",
		DocumentNumber: "MKT001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

// --- SubmitOrder: Validation Tests ---

func TestSubmitOrder_InvalidOrderType(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           "stop_loss",
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Quantity:       100,
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	ve, ok := err.(*domain.ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Message != "Unknown order type: stop_loss. Must be one of: limit, market" {
		t.Errorf("unexpected message: %s", ve.Message)
	}
}

func TestSubmitOrder_InvalidBrokerID(t *testing.T) {
	env := newTestOrderEnv()

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "invalid broker!",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestSubmitOrder_InvalidDocumentNumber(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC 001!", // invalid chars
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestSubmitOrder_InvalidSide(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           "buy",
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestSubmitOrder_InvalidSymbol(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "aapl", // lowercase
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestSubmitOrder_ZeroQuantity(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       0,
		ExpiresAt:      futureTime(),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestSubmitOrder_NegativeQuantity(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       -10,
		ExpiresAt:      futureTime(),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestSubmitOrder_LimitOrder_ZeroPrice(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(0),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestSubmitOrder_LimitOrder_PriceTooManyDecimals(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.123),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestSubmitOrder_LimitOrder_NoPrice(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestSubmitOrder_LimitOrder_NoExpiresAt(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestSubmitOrder_LimitOrder_PastExpiresAt(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	past := time.Now().Add(-1 * time.Hour)
	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      &past,
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestSubmitOrder_BrokerNotFound(t *testing.T) {
	env := newTestOrderEnv()

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "nonexistent",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err != domain.ErrBrokerNotFound {
		t.Errorf("got error %v, want ErrBrokerNotFound", err)
	}
}

func TestSubmitOrder_InsufficientBalance(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100.00, nil) // only $100

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100, // needs $15,000
		ExpiresAt:      futureTime(),
	})
	if err != domain.ErrInsufficientBalance {
		t.Errorf("got error %v, want ErrInsufficientBalance", err)
	}
}

func TestSubmitOrder_InsufficientHoldings(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "seller", 0, []HoldingInput{{Symbol: "AAPL", Quantity: 10}})

	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "seller",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideAsk,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100, // only has 10
		ExpiresAt:      futureTime(),
	})
	if err != domain.ErrInsufficientHoldings {
		t.Errorf("got error %v, want ErrInsufficientHoldings", err)
	}
}

// --- GetOrder Tests ---

func TestGetOrder_Success(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	submitted, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	retrieved, err := env.svc.GetOrder(submitted.OrderID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retrieved.OrderID != submitted.OrderID {
		t.Errorf("got order_id %q, want %q", retrieved.OrderID, submitted.OrderID)
	}
	if retrieved.Status != submitted.Status {
		t.Errorf("got status %q, want %q", retrieved.Status, submitted.Status)
	}
}

func TestGetOrder_NotFound(t *testing.T) {
	env := newTestOrderEnv()

	_, err := env.svc.GetOrder("nonexistent")
	if err != domain.ErrOrderNotFound {
		t.Errorf("got error %v, want ErrOrderNotFound", err)
	}
}

// --- CancelOrder Tests ---

func TestCancelOrder_PendingOrder(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	submitted, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "broker1",
		DocumentNumber: "DOC001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cancelled, err := env.svc.CancelOrder(submitted.OrderID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cancelled.Status != domain.OrderStatusCancelled {
		t.Errorf("got status %q, want %q", cancelled.Status, domain.OrderStatusCancelled)
	}
	if cancelled.CancelledQuantity != 100 {
		t.Errorf("got cancelled_quantity %d, want 100", cancelled.CancelledQuantity)
	}
	if cancelled.RemainingQuantity != 0 {
		t.Errorf("got remaining_quantity %d, want 0", cancelled.RemainingQuantity)
	}
	if cancelled.CancelledAt == nil {
		t.Error("expected cancelled_at to be set")
	}

	// Verify removed from expiry manager.
	if env.expiry.ActiveOrderCount() != 0 {
		t.Errorf("expected 0 active orders in expiry manager, got %d", env.expiry.ActiveOrderCount())
	}
}

func TestCancelOrder_PartiallyFilledOrder(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "seller", 0, []HoldingInput{{Symbol: "AAPL", Quantity: 500}})
	env.registerBroker(t, "buyer", 100000.00, nil)

	// Place a small ask.
	_, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "seller",
		DocumentNumber: "ASK001",
		Side:           domain.OrderSideAsk,
		Symbol:         "AAPL",
		Price:          floatPtr(148.00),
		Quantity:       50,
		ExpiresAt:      futureTime(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Place a larger bid that partially fills.
	bid, err := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "buyer",
		DocumentNumber: "BID001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bid.Status != domain.OrderStatusPartiallyFilled {
		t.Fatalf("expected partially_filled, got %s", bid.Status)
	}

	// Cancel the partially filled order.
	cancelled, err := env.svc.CancelOrder(bid.OrderID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cancelled.Status != domain.OrderStatusCancelled {
		t.Errorf("got status %q, want %q", cancelled.Status, domain.OrderStatusCancelled)
	}
	if cancelled.FilledQuantity != 50 {
		t.Errorf("got filled_quantity %d, want 50", cancelled.FilledQuantity)
	}
	if cancelled.CancelledQuantity != 50 {
		t.Errorf("got cancelled_quantity %d, want 50", cancelled.CancelledQuantity)
	}
	if cancelled.RemainingQuantity != 0 {
		t.Errorf("got remaining_quantity %d, want 0", cancelled.RemainingQuantity)
	}
	// Trades should be preserved.
	if len(cancelled.Trades) != 1 {
		t.Errorf("got %d trades, want 1", len(cancelled.Trades))
	}
}

func TestCancelOrder_AlreadyFilled(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "seller", 0, []HoldingInput{{Symbol: "AAPL", Quantity: 500}})
	env.registerBroker(t, "buyer", 100000.00, nil)

	// Place ask and matching bid.
	_, _ = env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "seller",
		DocumentNumber: "ASK001",
		Side:           domain.OrderSideAsk,
		Symbol:         "AAPL",
		Price:          floatPtr(148.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})
	bid, _ := env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "buyer",
		DocumentNumber: "BID001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})

	_, err := env.svc.CancelOrder(bid.OrderID)
	if err != domain.ErrOrderNotCancellable {
		t.Errorf("got error %v, want ErrOrderNotCancellable", err)
	}
}

func TestCancelOrder_NotFound(t *testing.T) {
	env := newTestOrderEnv()

	_, err := env.svc.CancelOrder("nonexistent")
	if err != domain.ErrOrderNotFound {
		t.Errorf("got error %v, want ErrOrderNotFound", err)
	}
}

// --- ListOrders Tests ---

func TestListOrders_Success(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, []HoldingInput{{Symbol: "AAPL", Quantity: 500}})

	// Submit a few orders.
	for i := 0; i < 3; i++ {
		_, err := env.svc.SubmitOrder(SubmitOrderRequest{
			Type:           domain.OrderTypeLimit,
			BrokerID:       "broker1",
			DocumentNumber: "DOC001",
			Side:           domain.OrderSideBid,
			Symbol:         "AAPL",
			Price:          floatPtr(150.00),
			Quantity:       10,
			ExpiresAt:      futureTime(),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	orders, total, err := env.svc.ListOrders("broker1", nil, 1, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 3 {
		t.Errorf("got total %d, want 3", total)
	}
	if len(orders) != 3 {
		t.Errorf("got %d orders, want 3", len(orders))
	}
}

func TestListOrders_WithStatusFilter(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "seller", 0, []HoldingInput{{Symbol: "AAPL", Quantity: 500}})
	env.registerBroker(t, "buyer", 100000.00, nil)

	// Place a resting ask.
	_, _ = env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "seller",
		DocumentNumber: "ASK001",
		Side:           domain.OrderSideAsk,
		Symbol:         "AAPL",
		Price:          floatPtr(148.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})

	// Place a matching bid (fills both).
	_, _ = env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "buyer",
		DocumentNumber: "BID001",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(150.00),
		Quantity:       100,
		ExpiresAt:      futureTime(),
	})

	// Place another pending bid.
	_, _ = env.svc.SubmitOrder(SubmitOrderRequest{
		Type:           domain.OrderTypeLimit,
		BrokerID:       "buyer",
		DocumentNumber: "BID002",
		Side:           domain.OrderSideBid,
		Symbol:         "AAPL",
		Price:          floatPtr(145.00),
		Quantity:       10,
		ExpiresAt:      futureTime(),
	})

	// Filter by filled status.
	filledStatus := domain.OrderStatusFilled
	orders, total, err := env.svc.ListOrders("buyer", &filledStatus, 1, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Errorf("got total %d, want 1", total)
	}
	if len(orders) != 1 {
		t.Errorf("got %d orders, want 1", len(orders))
	}

	// Filter by pending status.
	pendingStatus := domain.OrderStatusPending
	orders, total, err = env.svc.ListOrders("buyer", &pendingStatus, 1, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Errorf("got total %d, want 1", total)
	}
	if len(orders) != 1 {
		t.Errorf("got %d orders, want 1", len(orders))
	}
}

func TestListOrders_Pagination(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 1000000.00, nil)

	// Submit 5 orders.
	for i := 0; i < 5; i++ {
		_, err := env.svc.SubmitOrder(SubmitOrderRequest{
			Type:           domain.OrderTypeLimit,
			BrokerID:       "broker1",
			DocumentNumber: "DOC001",
			Side:           domain.OrderSideBid,
			Symbol:         "AAPL",
			Price:          floatPtr(150.00),
			Quantity:       10,
			ExpiresAt:      futureTime(),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Page 1, limit 2.
	orders, total, err := env.svc.ListOrders("broker1", nil, 1, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 5 {
		t.Errorf("got total %d, want 5", total)
	}
	if len(orders) != 2 {
		t.Errorf("got %d orders, want 2", len(orders))
	}

	// Page 3, limit 2 (only 1 remaining).
	orders, total, err = env.svc.ListOrders("broker1", nil, 3, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 5 {
		t.Errorf("got total %d, want 5", total)
	}
	if len(orders) != 1 {
		t.Errorf("got %d orders, want 1", len(orders))
	}
}

func TestListOrders_BrokerNotFound(t *testing.T) {
	env := newTestOrderEnv()

	_, _, err := env.svc.ListOrders("nonexistent", nil, 1, 20)
	if err != domain.ErrBrokerNotFound {
		t.Errorf("got error %v, want ErrBrokerNotFound", err)
	}
}

func TestListOrders_InvalidStatus(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	invalidStatus := domain.OrderStatus("open")
	_, _, err := env.svc.ListOrders("broker1", &invalidStatus, 1, 20)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestListOrders_InvalidPage(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	_, _, err := env.svc.ListOrders("broker1", nil, 0, 20)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestListOrders_InvalidLimit(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	tests := []struct {
		name  string
		limit int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too large", 101},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := env.svc.ListOrders("broker1", nil, 1, tt.limit)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if _, ok := err.(*domain.ValidationError); !ok {
				t.Errorf("expected *ValidationError, got %T: %v", err, err)
			}
		})
	}
}

func TestListOrders_EmptyResult(t *testing.T) {
	env := newTestOrderEnv()
	env.registerBroker(t, "broker1", 100000.00, nil)

	orders, total, err := env.svc.ListOrders("broker1", nil, 1, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 0 {
		t.Errorf("got total %d, want 0", total)
	}
	if len(orders) != 0 {
		t.Errorf("got %d orders, want 0", len(orders))
	}
}
