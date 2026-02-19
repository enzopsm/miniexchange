package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/engine"
	"github.com/efreitasn/miniexchange/internal/store"
	"pgregory.net/rapid"
)

// Feature: mini-stock-exchange, Property 17: VWAP computation
// Validates: Requirements 8.1, 8.2, 8.3

// TestProperty_VWAPComputation verifies that for any set of trades within the
// configured time window, the VWAP equals sum(price * quantity) / sum(quantity).
func TestProperty_VWAPComputation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		vwapWindow := 5 * time.Minute
		now := time.Now()

		// Generate trades inside the window.
		numTrades := rapid.IntRange(1, 20).Draw(t, "numTrades")

		tradeStore := store.NewTradeStore()
		symbols := domain.NewSymbolRegistry()
		symbols.Register("TEST")
		books := engine.NewBookManager()
		brokerStore := store.NewBrokerStore()
		orderStore := store.NewOrderStore()
		matcher := engine.NewMatcher(books, brokerStore, orderStore, tradeStore, symbols)
		svc := NewStockService(tradeStore, books, matcher, vwapWindow, symbols)

		// Also add some trades outside the window to ensure they're excluded.
		numOutside := rapid.IntRange(0, 5).Draw(t, "numOutside")
		for i := 0; i < numOutside; i++ {
			offsetSec := rapid.IntRange(301, 600).Draw(t, fmt.Sprintf("outsideOffset-%d", i))
			tradeStore.Append("TEST", &domain.Trade{
				TradeID:    fmt.Sprintf("t-out-%d", i),
				OrderID:    fmt.Sprintf("o-out-%d", i),
				Price:      rapid.Int64Range(1, 100000).Draw(t, fmt.Sprintf("outsidePrice-%d", i)),
				Quantity:   rapid.Int64Range(1, 10000).Draw(t, fmt.Sprintf("outsideQty-%d", i)),
				ExecutedAt: now.Add(-time.Duration(offsetSec) * time.Second),
			})
		}

		// Add trades inside the window and track them for manual VWAP computation.
		var expectedSumPQ int64
		var expectedSumQ int64
		for i := 0; i < numTrades; i++ {
			price := rapid.Int64Range(1, 100000).Draw(t, fmt.Sprintf("price-%d", i))
			quantity := rapid.Int64Range(1, 10000).Draw(t, fmt.Sprintf("qty-%d", i))
			// Place trades 1-299 seconds ago (well within the 5-minute window).
			offsetSec := rapid.IntRange(1, 299).Draw(t, fmt.Sprintf("offset-%d", i))
			executedAt := now.Add(-time.Duration(offsetSec) * time.Second)

			tradeStore.Append("TEST", &domain.Trade{
				TradeID:    fmt.Sprintf("t-in-%d", i),
				OrderID:    fmt.Sprintf("o-in-%d", i),
				Price:      price,
				Quantity:   quantity,
				ExecutedAt: executedAt,
			})

			expectedSumPQ += price * quantity
			expectedSumQ += quantity
		}

		expectedVWAP := expectedSumPQ / expectedSumQ

		resp, err := svc.GetPrice("TEST")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resp.CurrentPrice == nil {
			t.Fatal("expected non-nil current_price")
		}

		if *resp.CurrentPrice != expectedVWAP {
			t.Fatalf("VWAP mismatch: expected %d, got %d (sumPQ=%d, sumQ=%d, tradesInWindow=%d)",
				expectedVWAP, *resp.CurrentPrice, expectedSumPQ, expectedSumQ, resp.TradesInWindow)
		}
	})
}

// TestProperty_VWAPFallbackToLastTrade verifies that when no trades exist in
// the VWAP window but trades have occurred, the price falls back to the last
// trade's price.
func TestProperty_VWAPFallbackToLastTrade(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		vwapWindow := 5 * time.Minute
		now := time.Now()

		tradeStore := store.NewTradeStore()
		symbols := domain.NewSymbolRegistry()
		symbols.Register("TEST")
		books := engine.NewBookManager()
		brokerStore := store.NewBrokerStore()
		orderStore := store.NewOrderStore()
		matcher := engine.NewMatcher(books, brokerStore, orderStore, tradeStore, symbols)
		svc := NewStockService(tradeStore, books, matcher, vwapWindow, symbols)

		// Generate trades all outside the window.
		numTrades := rapid.IntRange(1, 10).Draw(t, "numTrades")
		var lastPrice int64
		for i := 0; i < numTrades; i++ {
			price := rapid.Int64Range(1, 100000).Draw(t, fmt.Sprintf("price-%d", i))
			quantity := rapid.Int64Range(1, 10000).Draw(t, fmt.Sprintf("qty-%d", i))
			// Place trades 301-600 seconds ago (outside the 5-minute window).
			offsetSec := rapid.IntRange(301, 600).Draw(t, fmt.Sprintf("offset-%d", i))
			executedAt := now.Add(-time.Duration(offsetSec) * time.Second)

			tradeStore.Append("TEST", &domain.Trade{
				TradeID:    fmt.Sprintf("t-%d", i),
				OrderID:    fmt.Sprintf("o-%d", i),
				Price:      price,
				Quantity:   quantity,
				ExecutedAt: executedAt,
			})
			lastPrice = price
		}

		resp, err := svc.GetPrice("TEST")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resp.CurrentPrice == nil {
			t.Fatal("expected non-nil current_price (fallback to last trade)")
		}

		// Should fall back to the last trade's price (last appended).
		if *resp.CurrentPrice != lastPrice {
			t.Fatalf("expected fallback price %d, got %d", lastPrice, *resp.CurrentPrice)
		}

		if resp.TradesInWindow != 0 {
			t.Fatalf("expected 0 trades in window, got %d", resp.TradesInWindow)
		}
	})
}

// TestProperty_VWAPNullWhenNoTrades verifies that when no trades have ever
// occurred for a symbol, the price is null.
func TestProperty_VWAPNullWhenNoTrades(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Use a random VWAP window to show it doesn't matter.
		windowMinutes := rapid.IntRange(1, 60).Draw(t, "windowMinutes")
		vwapWindow := time.Duration(windowMinutes) * time.Minute

		tradeStore := store.NewTradeStore()
		symbols := domain.NewSymbolRegistry()

		// Generate a random symbol name.
		symbolName := fmt.Sprintf("SYM%d", rapid.IntRange(1, 999).Draw(t, "symbolSuffix"))
		symbols.Register(symbolName)

		books := engine.NewBookManager()
		brokerStore := store.NewBrokerStore()
		orderStore := store.NewOrderStore()
		matcher := engine.NewMatcher(books, brokerStore, orderStore, tradeStore, symbols)
		svc := NewStockService(tradeStore, books, matcher, vwapWindow, symbols)

		resp, err := svc.GetPrice(symbolName)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resp.CurrentPrice != nil {
			t.Fatalf("expected nil current_price when no trades, got %d", *resp.CurrentPrice)
		}

		if resp.LastTradeAt != nil {
			t.Fatalf("expected nil last_trade_at when no trades, got %v", *resp.LastTradeAt)
		}

		if resp.TradesInWindow != 0 {
			t.Fatalf("expected 0 trades in window, got %d", resp.TradesInWindow)
		}
	})
}

// Feature: mini-stock-exchange, Property 18: Book snapshot aggregation
// Validates: Requirements 9.1, 9.2, 9.3

// TestProperty_BookSnapshotAggregation verifies that for any order book state,
// the book endpoint returns price levels aggregated correctly: each level's
// total_quantity equals the sum of remaining_quantity of all orders at that price,
// order_count equals the number of orders at that price, bids are sorted descending,
// asks are sorted ascending, and spread = best_ask - best_bid (null if either side empty).
func TestProperty_BookSnapshotAggregation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		tradeStore := store.NewTradeStore()
		symbols := domain.NewSymbolRegistry()
		symbols.Register("TEST")
		books := engine.NewBookManager()
		brokerStore := store.NewBrokerStore()
		orderStore := store.NewOrderStore()
		matcher := engine.NewMatcher(books, brokerStore, orderStore, tradeStore, symbols)
		svc := NewStockService(tradeStore, books, matcher, 5*time.Minute, symbols)

		book := books.GetOrCreate("TEST")

		// Generate random bid orders.
		numBids := rapid.IntRange(0, 30).Draw(t, "numBids")
		// Track expected aggregation per price level.
		bidAgg := make(map[int64]struct {
			totalQty   int64
			orderCount int
		})
		for i := 0; i < numBids; i++ {
			price := rapid.Int64Range(100, 500).Draw(t, fmt.Sprintf("bidPrice-%d", i))
			remainQty := rapid.Int64Range(1, 1000).Draw(t, fmt.Sprintf("bidQty-%d", i))
			orderID := fmt.Sprintf("bid-%d", i)
			secOffset := rapid.IntRange(0, 100).Draw(t, fmt.Sprintf("bidSec-%d", i))
			createdAt := time.Date(2025, 1, 1, 0, 0, secOffset, 0, time.UTC)

			order := &domain.Order{
				OrderID:           orderID,
				Side:              domain.OrderSideBid,
				Price:             price,
				RemainingQuantity: remainQty,
				CreatedAt:         createdAt,
			}
			entry := engine.OrderBookEntry{
				Price:     price,
				CreatedAt: createdAt,
				OrderID:   orderID,
				Order:     order,
			}
			book.InsertBid(entry)

			agg := bidAgg[price]
			agg.totalQty += remainQty
			agg.orderCount++
			bidAgg[price] = agg
		}

		// Generate random ask orders.
		numAsks := rapid.IntRange(0, 30).Draw(t, "numAsks")
		askAgg := make(map[int64]struct {
			totalQty   int64
			orderCount int
		})
		for i := 0; i < numAsks; i++ {
			price := rapid.Int64Range(501, 1000).Draw(t, fmt.Sprintf("askPrice-%d", i))
			remainQty := rapid.Int64Range(1, 1000).Draw(t, fmt.Sprintf("askQty-%d", i))
			orderID := fmt.Sprintf("ask-%d", i)
			secOffset := rapid.IntRange(0, 100).Draw(t, fmt.Sprintf("askSec-%d", i))
			createdAt := time.Date(2025, 1, 1, 0, 0, secOffset, 0, time.UTC)

			order := &domain.Order{
				OrderID:           orderID,
				Side:              domain.OrderSideAsk,
				Price:             price,
				RemainingQuantity: remainQty,
				CreatedAt:         createdAt,
			}
			entry := engine.OrderBookEntry{
				Price:     price,
				CreatedAt: createdAt,
				OrderID:   orderID,
				Order:     order,
			}
			book.InsertAsk(entry)

			agg := askAgg[price]
			agg.totalQty += remainQty
			agg.orderCount++
			askAgg[price] = agg
		}

		// Use a depth large enough to capture all levels.
		depth := 50
		resp, err := svc.GetBook("TEST", depth)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify bid levels: total_quantity and order_count per level.
		for _, level := range resp.Bids {
			expected, ok := bidAgg[level.Price]
			if !ok {
				t.Fatalf("bid level at price %d not expected", level.Price)
			}
			if level.TotalQuantity != expected.totalQty {
				t.Fatalf("bid price %d: total_quantity expected %d, got %d",
					level.Price, expected.totalQty, level.TotalQuantity)
			}
			if level.OrderCount != expected.orderCount {
				t.Fatalf("bid price %d: order_count expected %d, got %d",
					level.Price, expected.orderCount, level.OrderCount)
			}
		}

		// Verify ask levels: total_quantity and order_count per level.
		for _, level := range resp.Asks {
			expected, ok := askAgg[level.Price]
			if !ok {
				t.Fatalf("ask level at price %d not expected", level.Price)
			}
			if level.TotalQuantity != expected.totalQty {
				t.Fatalf("ask price %d: total_quantity expected %d, got %d",
					level.Price, expected.totalQty, level.TotalQuantity)
			}
			if level.OrderCount != expected.orderCount {
				t.Fatalf("ask price %d: order_count expected %d, got %d",
					level.Price, expected.orderCount, level.OrderCount)
			}
		}

		// Verify bids are sorted descending by price.
		for i := 1; i < len(resp.Bids); i++ {
			if resp.Bids[i].Price > resp.Bids[i-1].Price {
				t.Fatalf("bids not sorted descending: price %d after %d",
					resp.Bids[i].Price, resp.Bids[i-1].Price)
			}
		}

		// Verify asks are sorted ascending by price.
		for i := 1; i < len(resp.Asks); i++ {
			if resp.Asks[i].Price < resp.Asks[i-1].Price {
				t.Fatalf("asks not sorted ascending: price %d after %d",
					resp.Asks[i].Price, resp.Asks[i-1].Price)
			}
		}

		// Verify spread computation.
		if numBids > 0 && numAsks > 0 {
			if resp.Spread == nil {
				t.Fatal("expected non-nil spread when both sides have orders")
			}
			bestBid := resp.Bids[0].Price
			bestAsk := resp.Asks[0].Price
			expectedSpread := bestAsk - bestBid
			if *resp.Spread != expectedSpread {
				t.Fatalf("spread expected %d (bestAsk=%d - bestBid=%d), got %d",
					expectedSpread, bestAsk, bestBid, *resp.Spread)
			}
		} else {
			if resp.Spread != nil {
				t.Fatalf("expected nil spread when a side is empty, got %d", *resp.Spread)
			}
		}
	})
}

