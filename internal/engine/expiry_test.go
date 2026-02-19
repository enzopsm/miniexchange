package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/store"
)

// mockWebhookDispatcher records calls to DispatchOrderExpired.
type mockWebhookDispatcher struct {
	mu      sync.Mutex
	expired []*domain.Order
}

func (m *mockWebhookDispatcher) DispatchOrderExpired(order *domain.Order) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expired = append(m.expired, order)
}

func (m *mockWebhookDispatcher) getExpired() []*domain.Order {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*domain.Order, len(m.expired))
	copy(result, m.expired)
	return result
}

// helper to create a test ExpiryManager with all dependencies.
func newTestExpiryManager(interval time.Duration, webhook WebhookDispatcher) (*ExpiryManager, *BookManager, *store.BrokerStore) {
	books := NewBookManager()
	orderStore := store.NewOrderStore()
	brokerStore := store.NewBrokerStore()
	em := NewExpiryManager(interval, books, orderStore, brokerStore, webhook)
	return em, books, brokerStore
}

// helper to create a limit order with an expiration time.
func newTestLimitOrder(id, brokerID, symbol string, side domain.OrderSide, price, qty int64, expiresAt time.Time) *domain.Order {
	return &domain.Order{
		OrderID:           id,
		Type:              domain.OrderTypeLimit,
		BrokerID:          brokerID,
		Side:              side,
		Symbol:            symbol,
		Price:             price,
		Quantity:          qty,
		RemainingQuantity: qty,
		Status:            domain.OrderStatusPending,
		ExpiresAt:         &expiresAt,
		CreatedAt:         time.Now(),
	}
}

func TestExpiryManager_Add_MaintainsSortOrder(t *testing.T) {
	em, _, _ := newTestExpiryManager(time.Second, nil)
	now := time.Now()

	o1 := newTestLimitOrder("o1", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(3*time.Second))
	o2 := newTestLimitOrder("o2", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(1*time.Second))
	o3 := newTestLimitOrder("o3", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(2*time.Second))

	em.Add(o1)
	em.Add(o2)
	em.Add(o3)

	if em.ActiveOrderCount() != 3 {
		t.Fatalf("expected 3 active orders, got %d", em.ActiveOrderCount())
	}

	// Verify sorted order: o2 (1s), o3 (2s), o1 (3s).
	em.mu.Lock()
	defer em.mu.Unlock()
	if em.activeOrders[0].OrderID != "o2" {
		t.Errorf("expected first order o2, got %s", em.activeOrders[0].OrderID)
	}
	if em.activeOrders[1].OrderID != "o3" {
		t.Errorf("expected second order o3, got %s", em.activeOrders[1].OrderID)
	}
	if em.activeOrders[2].OrderID != "o1" {
		t.Errorf("expected third order o1, got %s", em.activeOrders[2].OrderID)
	}
}

func TestExpiryManager_Add_NilExpiresAt_Ignored(t *testing.T) {
	em, _, _ := newTestExpiryManager(time.Second, nil)

	order := &domain.Order{
		OrderID:   "o1",
		ExpiresAt: nil,
	}
	em.Add(order)

	if em.ActiveOrderCount() != 0 {
		t.Fatalf("expected 0 active orders for nil ExpiresAt, got %d", em.ActiveOrderCount())
	}
}

func TestExpiryManager_Remove(t *testing.T) {
	em, _, _ := newTestExpiryManager(time.Second, nil)
	now := time.Now()

	o1 := newTestLimitOrder("o1", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(1*time.Second))
	o2 := newTestLimitOrder("o2", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(2*time.Second))
	o3 := newTestLimitOrder("o3", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(3*time.Second))

	em.Add(o1)
	em.Add(o2)
	em.Add(o3)

	em.Remove("o2")

	if em.ActiveOrderCount() != 2 {
		t.Fatalf("expected 2 active orders after remove, got %d", em.ActiveOrderCount())
	}

	em.mu.Lock()
	defer em.mu.Unlock()
	for _, o := range em.activeOrders {
		if o.OrderID == "o2" {
			t.Error("o2 should have been removed")
		}
	}
}

func TestExpiryManager_Remove_NonExistent(t *testing.T) {
	em, _, _ := newTestExpiryManager(time.Second, nil)
	now := time.Now()

	o1 := newTestLimitOrder("o1", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(1*time.Second))
	em.Add(o1)

	// Should not panic or change anything.
	em.Remove("nonexistent")

	if em.ActiveOrderCount() != 1 {
		t.Fatalf("expected 1 active order, got %d", em.ActiveOrderCount())
	}
}

func TestExpiryManager_Tick_ExpiresOrders(t *testing.T) {
	webhook := &mockWebhookDispatcher{}
	em, books, brokerStore := newTestExpiryManager(time.Second, webhook)
	now := time.Now()

	// Create a broker with cash for bid reservation.
	broker := &domain.Broker{
		BrokerID:     "b1",
		CashBalance:  100000,
		ReservedCash: 2000, // 100 * 10 + 100 * 10
		Holdings:     make(map[string]*domain.Holding),
		CreatedAt:    now,
	}
	brokerStore.Create(broker)

	// Create two orders that expire at different times.
	o1 := newTestLimitOrder("o1", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(-2*time.Second))
	o2 := newTestLimitOrder("o2", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(-1*time.Second))
	o3 := newTestLimitOrder("o3", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(10*time.Second))

	// Place orders on the book.
	book := books.GetOrCreate("AAPL")
	for _, o := range []*domain.Order{o1, o2, o3} {
		book.InsertBid(OrderBookEntry{
			Price:     o.Price,
			CreatedAt: o.CreatedAt,
			OrderID:   o.OrderID,
			Order:     o,
		})
		em.Add(o)
	}

	// Tick at now — o1 and o2 should expire, o3 should not.
	em.tick(now)

	// Verify o1 expired.
	if o1.Status != domain.OrderStatusExpired {
		t.Errorf("o1: expected status expired, got %s", o1.Status)
	}
	if o1.RemainingQuantity != 0 {
		t.Errorf("o1: expected remaining_quantity 0, got %d", o1.RemainingQuantity)
	}
	if o1.CancelledQuantity != 10 {
		t.Errorf("o1: expected cancelled_quantity 10, got %d", o1.CancelledQuantity)
	}
	if o1.ExpiredAt == nil || !o1.ExpiredAt.Equal(*o1.ExpiresAt) {
		t.Errorf("o1: expired_at should equal expires_at")
	}

	// Verify o2 expired.
	if o2.Status != domain.OrderStatusExpired {
		t.Errorf("o2: expected status expired, got %s", o2.Status)
	}

	// Verify o3 is still pending.
	if o3.Status != domain.OrderStatusPending {
		t.Errorf("o3: expected status pending, got %s", o3.Status)
	}

	// Verify orders removed from book.
	if book.BidCount() != 1 {
		t.Errorf("expected 1 bid remaining on book, got %d", book.BidCount())
	}

	// Verify reservation released: 2000 - (100*10 + 100*10) = 0.
	broker.Mu.Lock()
	if broker.ReservedCash != 0 {
		t.Errorf("expected reserved_cash 0, got %d", broker.ReservedCash)
	}
	broker.Mu.Unlock()

	// Verify webhooks fired for both expired orders.
	expired := webhook.getExpired()
	if len(expired) != 2 {
		t.Fatalf("expected 2 webhook dispatches, got %d", len(expired))
	}

	// Verify only o3 remains in active orders.
	if em.ActiveOrderCount() != 1 {
		t.Errorf("expected 1 active order remaining, got %d", em.ActiveOrderCount())
	}
}

func TestExpiryManager_Tick_AskOrder_ReleasesHoldings(t *testing.T) {
	webhook := &mockWebhookDispatcher{}
	em, books, brokerStore := newTestExpiryManager(time.Second, webhook)
	now := time.Now()

	broker := &domain.Broker{
		BrokerID:    "b1",
		CashBalance: 100000,
		Holdings: map[string]*domain.Holding{
			"AAPL": {Quantity: 100, ReservedQuantity: 50},
		},
		CreatedAt: now,
	}
	brokerStore.Create(broker)

	o1 := newTestLimitOrder("o1", "b1", "AAPL", domain.OrderSideAsk, 150, 50, now.Add(-1*time.Second))

	book := books.GetOrCreate("AAPL")
	book.InsertAsk(OrderBookEntry{
		Price:     o1.Price,
		CreatedAt: o1.CreatedAt,
		OrderID:   o1.OrderID,
		Order:     o1,
	})
	em.Add(o1)

	em.tick(now)

	if o1.Status != domain.OrderStatusExpired {
		t.Errorf("expected status expired, got %s", o1.Status)
	}

	broker.Mu.Lock()
	h := broker.Holdings["AAPL"]
	if h.ReservedQuantity != 0 {
		t.Errorf("expected reserved_quantity 0, got %d", h.ReservedQuantity)
	}
	broker.Mu.Unlock()

	if book.AskCount() != 0 {
		t.Errorf("expected 0 asks on book, got %d", book.AskCount())
	}
}

func TestExpiryManager_Tick_SkipsFilledOrders(t *testing.T) {
	webhook := &mockWebhookDispatcher{}
	em, books, brokerStore := newTestExpiryManager(time.Second, webhook)
	now := time.Now()

	broker := &domain.Broker{
		BrokerID:    "b1",
		CashBalance: 100000,
		Holdings:    make(map[string]*domain.Holding),
		CreatedAt:   now,
	}
	brokerStore.Create(broker)

	o1 := newTestLimitOrder("o1", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(-1*time.Second))
	// Simulate that the order was filled between being added and the tick.
	o1.Status = domain.OrderStatusFilled
	o1.FilledQuantity = 10
	o1.RemainingQuantity = 0

	book := books.GetOrCreate("AAPL")
	book.InsertBid(OrderBookEntry{
		Price:     o1.Price,
		CreatedAt: o1.CreatedAt,
		OrderID:   o1.OrderID,
		Order:     o1,
	})
	em.Add(o1)

	em.tick(now)

	// Should still be filled, not expired.
	if o1.Status != domain.OrderStatusFilled {
		t.Errorf("expected status filled, got %s", o1.Status)
	}

	// No webhook should have been dispatched.
	expired := webhook.getExpired()
	if len(expired) != 0 {
		t.Errorf("expected 0 webhook dispatches, got %d", len(expired))
	}
}

func TestExpiryManager_Tick_PartiallyFilledOrder(t *testing.T) {
	webhook := &mockWebhookDispatcher{}
	em, books, brokerStore := newTestExpiryManager(time.Second, webhook)
	now := time.Now()

	broker := &domain.Broker{
		BrokerID:     "b1",
		CashBalance:  100000,
		ReservedCash: 500, // 100 * 5 remaining
		Holdings:     make(map[string]*domain.Holding),
		CreatedAt:    now,
	}
	brokerStore.Create(broker)

	o1 := newTestLimitOrder("o1", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(-1*time.Second))
	o1.Status = domain.OrderStatusPartiallyFilled
	o1.FilledQuantity = 5
	o1.RemainingQuantity = 5

	book := books.GetOrCreate("AAPL")
	book.InsertBid(OrderBookEntry{
		Price:     o1.Price,
		CreatedAt: o1.CreatedAt,
		OrderID:   o1.OrderID,
		Order:     o1,
	})
	em.Add(o1)

	em.tick(now)

	if o1.Status != domain.OrderStatusExpired {
		t.Errorf("expected status expired, got %s", o1.Status)
	}
	if o1.CancelledQuantity != 5 {
		t.Errorf("expected cancelled_quantity 5, got %d", o1.CancelledQuantity)
	}
	if o1.RemainingQuantity != 0 {
		t.Errorf("expected remaining_quantity 0, got %d", o1.RemainingQuantity)
	}
	if o1.FilledQuantity != 5 {
		t.Errorf("expected filled_quantity preserved at 5, got %d", o1.FilledQuantity)
	}

	broker.Mu.Lock()
	if broker.ReservedCash != 0 {
		t.Errorf("expected reserved_cash 0, got %d", broker.ReservedCash)
	}
	broker.Mu.Unlock()
}

func TestExpiryManager_Start_StopsOnContextCancel(t *testing.T) {
	webhook := &mockWebhookDispatcher{}
	em, books, brokerStore := newTestExpiryManager(50*time.Millisecond, webhook)
	now := time.Now()

	broker := &domain.Broker{
		BrokerID:     "b1",
		CashBalance:  100000,
		ReservedCash: 1000,
		Holdings:     make(map[string]*domain.Holding),
		CreatedAt:    now,
	}
	brokerStore.Create(broker)

	o1 := newTestLimitOrder("o1", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(-1*time.Second))

	book := books.GetOrCreate("AAPL")
	book.InsertBid(OrderBookEntry{
		Price:     o1.Price,
		CreatedAt: o1.CreatedAt,
		OrderID:   o1.OrderID,
		Order:     o1,
	})
	em.Add(o1)

	ctx, cancel := context.WithCancel(context.Background())
	em.Start(ctx)

	// Wait for at least one tick to process.
	time.Sleep(150 * time.Millisecond)

	// Read order status under the per-symbol lock to avoid data race.
	book.mu.RLock()
	status := o1.Status
	book.mu.RUnlock()

	if status != domain.OrderStatusExpired {
		t.Errorf("expected status expired, got %s", status)
	}

	// Cancel context to stop the goroutine.
	cancel()

	// Give goroutine time to exit.
	time.Sleep(100 * time.Millisecond)
}

func TestExpiryManager_Tick_NilWebhookSvc(t *testing.T) {
	// Verify tick works when webhookSvc is nil (no panic).
	em, books, brokerStore := newTestExpiryManager(time.Second, nil)
	now := time.Now()

	broker := &domain.Broker{
		BrokerID:     "b1",
		CashBalance:  100000,
		ReservedCash: 1000,
		Holdings:     make(map[string]*domain.Holding),
		CreatedAt:    now,
	}
	brokerStore.Create(broker)

	o1 := newTestLimitOrder("o1", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(-1*time.Second))

	book := books.GetOrCreate("AAPL")
	book.InsertBid(OrderBookEntry{
		Price:     o1.Price,
		CreatedAt: o1.CreatedAt,
		OrderID:   o1.OrderID,
		Order:     o1,
	})
	em.Add(o1)

	// Should not panic.
	em.tick(now)

	if o1.Status != domain.OrderStatusExpired {
		t.Errorf("expected status expired, got %s", o1.Status)
	}
}

func TestExpiryManager_Tick_EmptySlice(t *testing.T) {
	em, _, _ := newTestExpiryManager(time.Second, nil)
	// Should not panic on empty slice.
	em.tick(time.Now())
	if em.ActiveOrderCount() != 0 {
		t.Errorf("expected 0 active orders, got %d", em.ActiveOrderCount())
	}
}

func TestExpiryManager_Tick_MultipleSymbols(t *testing.T) {
	webhook := &mockWebhookDispatcher{}
	em, books, brokerStore := newTestExpiryManager(time.Second, webhook)
	now := time.Now()

	broker := &domain.Broker{
		BrokerID:     "b1",
		CashBalance:  100000,
		ReservedCash: 2000,
		Holdings: map[string]*domain.Holding{
			"GOOG": {Quantity: 100, ReservedQuantity: 10},
		},
		CreatedAt: now,
	}
	brokerStore.Create(broker)

	// Bid on AAPL, ask on GOOG — different symbols, different lock paths.
	o1 := newTestLimitOrder("o1", "b1", "AAPL", domain.OrderSideBid, 100, 10, now.Add(-2*time.Second))
	o2 := newTestLimitOrder("o2", "b1", "GOOG", domain.OrderSideAsk, 200, 10, now.Add(-1*time.Second))

	bookAAPL := books.GetOrCreate("AAPL")
	bookAAPL.InsertBid(OrderBookEntry{Price: o1.Price, CreatedAt: o1.CreatedAt, OrderID: o1.OrderID, Order: o1})
	em.Add(o1)

	bookGOOG := books.GetOrCreate("GOOG")
	bookGOOG.InsertAsk(OrderBookEntry{Price: o2.Price, CreatedAt: o2.CreatedAt, OrderID: o2.OrderID, Order: o2})
	em.Add(o2)

	em.tick(now)

	if o1.Status != domain.OrderStatusExpired {
		t.Errorf("o1: expected expired, got %s", o1.Status)
	}
	if o2.Status != domain.OrderStatusExpired {
		t.Errorf("o2: expected expired, got %s", o2.Status)
	}

	// Verify reservations released correctly.
	broker.Mu.Lock()
	if broker.ReservedCash != 1000 {
		t.Errorf("expected reserved_cash 1000 (only bid released), got %d", broker.ReservedCash)
	}
	h := broker.Holdings["GOOG"]
	if h.ReservedQuantity != 0 {
		t.Errorf("expected GOOG reserved_quantity 0, got %d", h.ReservedQuantity)
	}
	broker.Mu.Unlock()

	expired := webhook.getExpired()
	if len(expired) != 2 {
		t.Fatalf("expected 2 webhook dispatches, got %d", len(expired))
	}
}
