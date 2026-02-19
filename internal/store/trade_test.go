package store

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
)

func newTestTrade(id string, executedAt time.Time) *domain.Trade {
	return &domain.Trade{
		TradeID:    id,
		OrderID:    "order-1",
		Price:      10000, // $100.00
		Quantity:   10,
		ExecutedAt: executedAt,
	}
}

func TestTradeStore_Append_and_GetBySymbol(t *testing.T) {
	s := NewTradeStore()
	now := time.Now()

	t1 := newTestTrade("trade-1", now)
	t2 := newTestTrade("trade-2", now.Add(time.Second))

	s.Append("AAPL", t1)
	s.Append("AAPL", t2)

	trades := s.GetBySymbol("AAPL")
	if len(trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(trades))
	}
	if trades[0].TradeID != "trade-1" {
		t.Fatalf("expected trade-1 first, got %s", trades[0].TradeID)
	}
	if trades[1].TradeID != "trade-2" {
		t.Fatalf("expected trade-2 second, got %s", trades[1].TradeID)
	}
}

func TestTradeStore_GetBySymbol_Empty(t *testing.T) {
	s := NewTradeStore()

	trades := s.GetBySymbol("GOOG")
	if trades == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(trades) != 0 {
		t.Fatalf("expected 0 trades, got %d", len(trades))
	}
}

func TestTradeStore_GetBySymbol_ReturnsCopy(t *testing.T) {
	s := NewTradeStore()
	now := time.Now()

	s.Append("AAPL", newTestTrade("trade-1", now))

	trades := s.GetBySymbol("AAPL")
	trades[0] = nil // mutate the returned slice

	// Internal state should be unaffected.
	original := s.GetBySymbol("AAPL")
	if original[0] == nil {
		t.Fatal("GetBySymbol should return a copy; internal state was mutated")
	}
}

func TestTradeStore_MultipleSymbols(t *testing.T) {
	s := NewTradeStore()
	now := time.Now()

	s.Append("AAPL", newTestTrade("t1", now))
	s.Append("GOOG", newTestTrade("t2", now))
	s.Append("AAPL", newTestTrade("t3", now.Add(time.Second)))

	aapl := s.GetBySymbol("AAPL")
	if len(aapl) != 2 {
		t.Fatalf("expected 2 AAPL trades, got %d", len(aapl))
	}

	goog := s.GetBySymbol("GOOG")
	if len(goog) != 1 {
		t.Fatalf("expected 1 GOOG trade, got %d", len(goog))
	}
}

func TestTradeStore_ConcurrentAccess(t *testing.T) {
	s := NewTradeStore()
	var wg sync.WaitGroup
	now := time.Now()

	// Concurrently append trades to the same symbol.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Append("AAPL", newTestTrade(fmt.Sprintf("trade-%d", i), now.Add(time.Duration(i)*time.Millisecond)))
		}(i)
	}
	wg.Wait()

	trades := s.GetBySymbol("AAPL")
	if len(trades) != 100 {
		t.Fatalf("expected 100 trades, got %d", len(trades))
	}

	// Concurrent reads while appending more trades.
	for i := 100; i < 200; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			s.Append("AAPL", newTestTrade(fmt.Sprintf("trade-%d", i), now.Add(time.Duration(i)*time.Millisecond)))
		}(i)
		go func() {
			defer wg.Done()
			s.GetBySymbol("AAPL")
		}()
	}
	wg.Wait()

	trades = s.GetBySymbol("AAPL")
	if len(trades) != 200 {
		t.Fatalf("expected 200 trades, got %d", len(trades))
	}
}
