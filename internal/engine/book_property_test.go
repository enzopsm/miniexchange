package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"pgregory.net/rapid"
)

// Feature: mini-stock-exchange, Property 4: Order book sorting invariant
// Validates: Requirements 4.1

// genOrderBookEntry generates a random OrderBookEntry with constrained values.
func genOrderBookEntry(id int) *rapid.Generator[OrderBookEntry] {
	return rapid.Custom(func(t *rapid.T) OrderBookEntry {
		price := rapid.Int64Range(1, 10000).Draw(t, "price")
		// Use a small range of seconds to encourage timestamp collisions and test tiebreaking.
		secOffset := rapid.IntRange(0, 20).Draw(t, "secOffset")
		createdAt := time.Date(2025, 1, 1, 0, 0, secOffset, 0, time.UTC)
		orderID := fmt.Sprintf("order-%d", id)

		return OrderBookEntry{
			Price:     price,
			CreatedAt: createdAt,
			OrderID:   orderID,
			Order: &domain.Order{
				OrderID:           orderID,
				Price:             price,
				RemainingQuantity: 1,
				CreatedAt:         createdAt,
			},
		}
	})
}

func TestProperty_BidSideSortingInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 50).Draw(t, "numEntries")
		book := NewOrderBook("TEST")

		for i := 0; i < n; i++ {
			entry := genOrderBookEntry(i).Draw(t, fmt.Sprintf("bid-%d", i))
			book.InsertBid(entry)
		}

		// Walk bids and verify ordering: price descending, then created_at ascending, then order_id ascending.
		var prev *OrderBookEntry
		book.WalkBids(func(entry OrderBookEntry) bool {
			if prev != nil {
				if entry.Price > prev.Price {
					t.Fatalf("bid side: price should be descending, got %d after %d", entry.Price, prev.Price)
				}
				if entry.Price == prev.Price {
					if entry.CreatedAt.Before(prev.CreatedAt) {
						t.Fatalf("bid side: same price %d, created_at should be ascending, got %v after %v",
							entry.Price, entry.CreatedAt, prev.CreatedAt)
					}
					if entry.CreatedAt.Equal(prev.CreatedAt) && entry.OrderID < prev.OrderID {
						t.Fatalf("bid side: same price %d and time, order_id should be ascending, got %q after %q",
							entry.Price, entry.OrderID, prev.OrderID)
					}
				}
			}
			cur := entry
			prev = &cur
			return true
		})
	})
}

func TestProperty_AskSideSortingInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 50).Draw(t, "numEntries")
		book := NewOrderBook("TEST")

		for i := 0; i < n; i++ {
			entry := genOrderBookEntry(i).Draw(t, fmt.Sprintf("ask-%d", i))
			book.InsertAsk(entry)
		}

		// Walk asks and verify ordering: price ascending, then created_at ascending, then order_id ascending.
		var prev *OrderBookEntry
		book.WalkAsks(func(entry OrderBookEntry) bool {
			if prev != nil {
				if entry.Price < prev.Price {
					t.Fatalf("ask side: price should be ascending, got %d after %d", entry.Price, prev.Price)
				}
				if entry.Price == prev.Price {
					if entry.CreatedAt.Before(prev.CreatedAt) {
						t.Fatalf("ask side: same price %d, created_at should be ascending, got %v after %v",
							entry.Price, entry.CreatedAt, prev.CreatedAt)
					}
					if entry.CreatedAt.Equal(prev.CreatedAt) && entry.OrderID < prev.OrderID {
						t.Fatalf("ask side: same price %d and time, order_id should be ascending, got %q after %q",
							entry.Price, entry.OrderID, prev.OrderID)
					}
				}
			}
			cur := entry
			prev = &cur
			return true
		})
	})
}
