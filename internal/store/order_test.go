package store

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
)

func newTestOrder(id, brokerID string, createdAt time.Time) *domain.Order {
	return &domain.Order{
		OrderID:           id,
		Type:              domain.OrderTypeLimit,
		BrokerID:          brokerID,
		DocumentNumber:    "DOC1",
		Side:              domain.OrderSideBid,
		Symbol:            "AAPL",
		Price:             15000,
		Quantity:          10,
		RemainingQuantity: 10,
		Status:            domain.OrderStatusPending,
		CreatedAt:         createdAt,
	}
}

func TestOrderStore_Create_and_Get(t *testing.T) {
	s := NewOrderStore()
	now := time.Now()
	o := newTestOrder("order-1", "broker-1", now)

	s.Create(o)

	got, err := s.Get("order-1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.OrderID != "order-1" {
		t.Fatalf("expected order-1, got %s", got.OrderID)
	}
	if got.BrokerID != "broker-1" {
		t.Fatalf("expected broker-1, got %s", got.BrokerID)
	}
}

func TestOrderStore_Get_NotFound(t *testing.T) {
	s := NewOrderStore()

	_, err := s.Get("no-such-order")
	if err != domain.ErrOrderNotFound {
		t.Fatalf("expected ErrOrderNotFound, got %v", err)
	}
}

func TestOrderStore_ListByBroker_ReverseChronological(t *testing.T) {
	s := NewOrderStore()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		o := newTestOrder(
			fmt.Sprintf("order-%d", i),
			"broker-1",
			base.Add(time.Duration(i)*time.Minute),
		)
		s.Create(o)
	}

	orders, total := s.ListByBroker("broker-1", nil, 1, 10)
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if len(orders) != 5 {
		t.Fatalf("expected 5 orders, got %d", len(orders))
	}

	// Should be newest first.
	for i := 0; i < len(orders)-1; i++ {
		if !orders[i].CreatedAt.After(orders[i+1].CreatedAt) {
			t.Fatalf("orders not in reverse chronological order at index %d", i)
		}
	}
}

func TestOrderStore_ListByBroker_StatusFilter(t *testing.T) {
	s := NewOrderStore()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	statuses := []domain.OrderStatus{
		domain.OrderStatusPending,
		domain.OrderStatusFilled,
		domain.OrderStatusPending,
		domain.OrderStatusCancelled,
		domain.OrderStatusPending,
	}

	for i, st := range statuses {
		o := newTestOrder(
			fmt.Sprintf("order-%d", i),
			"broker-1",
			base.Add(time.Duration(i)*time.Minute),
		)
		o.Status = st
		s.Create(o)
	}

	pending := domain.OrderStatusPending
	orders, total := s.ListByBroker("broker-1", &pending, 1, 10)
	if total != 3 {
		t.Fatalf("expected total 3 pending, got %d", total)
	}
	if len(orders) != 3 {
		t.Fatalf("expected 3 orders, got %d", len(orders))
	}
	for _, o := range orders {
		if o.Status != domain.OrderStatusPending {
			t.Fatalf("expected pending status, got %s", o.Status)
		}
	}
}

func TestOrderStore_ListByBroker_Pagination(t *testing.T) {
	s := NewOrderStore()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 10; i++ {
		o := newTestOrder(
			fmt.Sprintf("order-%d", i),
			"broker-1",
			base.Add(time.Duration(i)*time.Minute),
		)
		s.Create(o)
	}

	// Page 1, limit 3.
	orders, total := s.ListByBroker("broker-1", nil, 1, 3)
	if total != 10 {
		t.Fatalf("expected total 10, got %d", total)
	}
	if len(orders) != 3 {
		t.Fatalf("expected 3 orders on page 1, got %d", len(orders))
	}

	// Page 4, limit 3 → only 1 remaining.
	orders, total = s.ListByBroker("broker-1", nil, 4, 3)
	if total != 10 {
		t.Fatalf("expected total 10, got %d", total)
	}
	if len(orders) != 1 {
		t.Fatalf("expected 1 order on page 4, got %d", len(orders))
	}

	// Page beyond range.
	orders, total = s.ListByBroker("broker-1", nil, 5, 3)
	if total != 10 {
		t.Fatalf("expected total 10, got %d", total)
	}
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders beyond last page, got %d", len(orders))
	}
}

func TestOrderStore_ListByBroker_EmptyBroker(t *testing.T) {
	s := NewOrderStore()

	orders, total := s.ListByBroker("no-such-broker", nil, 1, 10)
	if total != 0 {
		t.Fatalf("expected total 0, got %d", total)
	}
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders, got %d", len(orders))
	}
}

func TestOrderStore_ListByBroker_MultipleBrokers(t *testing.T) {
	s := NewOrderStore()
	now := time.Now()

	s.Create(newTestOrder("o1", "broker-1", now))
	s.Create(newTestOrder("o2", "broker-2", now))
	s.Create(newTestOrder("o3", "broker-1", now.Add(time.Minute)))

	orders, total := s.ListByBroker("broker-1", nil, 1, 10)
	if total != 2 {
		t.Fatalf("expected 2 orders for broker-1, got %d", total)
	}
	if len(orders) != 2 {
		t.Fatalf("expected 2 orders, got %d", len(orders))
	}

	orders, total = s.ListByBroker("broker-2", nil, 1, 10)
	if total != 1 {
		t.Fatalf("expected 1 order for broker-2, got %d", total)
	}
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
}

func TestOrderStore_ConcurrentAccess(t *testing.T) {
	s := NewOrderStore()
	var wg sync.WaitGroup
	base := time.Now()

	// Concurrently create orders.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			o := newTestOrder(
				fmt.Sprintf("order-%d", i),
				fmt.Sprintf("broker-%d", i%5),
				base.Add(time.Duration(i)*time.Millisecond),
			)
			s.Create(o)
		}(i)
	}
	wg.Wait()

	// All 100 should be retrievable.
	for i := 0; i < 100; i++ {
		_, err := s.Get(fmt.Sprintf("order-%d", i))
		if err != nil {
			t.Fatalf("order-%d should exist, got %v", i, err)
		}
	}

	// Each of 5 brokers should have 20 orders.
	for b := 0; b < 5; b++ {
		_, total := s.ListByBroker(fmt.Sprintf("broker-%d", b), nil, 1, 100)
		if total != 20 {
			t.Fatalf("broker-%d expected 20 orders, got %d", b, total)
		}
	}

	// Concurrent reads while creating more.
	for i := 100; i < 200; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			o := newTestOrder(
				fmt.Sprintf("order-%d", i),
				"broker-0",
				base.Add(time.Duration(i)*time.Millisecond),
			)
			s.Create(o)
		}(i)
		go func(i int) {
			defer wg.Done()
			s.ListByBroker("broker-0", nil, 1, 10)
		}(i)
	}
	wg.Wait()
}
