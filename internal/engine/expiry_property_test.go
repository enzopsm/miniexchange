package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/store"
	"pgregory.net/rapid"
)

// Feature: mini-stock-exchange, Property 16: Expiration state transition
// Validates: Requirements 7.2

func TestProperty_ExpirationStateTransition(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random order parameters.
		isBid := rapid.Bool().Draw(t, "isBid")
		price := rapid.Int64Range(100, 5000).Draw(t, "price")
		qty := rapid.Int64Range(1, 100).Draw(t, "qty")

		// Generate a random number of orders to expire (1-5).
		numOrders := rapid.IntRange(1, 5).Draw(t, "numOrders")

		// Decide whether to partially fill some orders before expiration.
		// Only possible when qty >= 2.
		doPartialFill := qty >= 2 && rapid.Bool().Draw(t, "doPartialFill")

		// Set up dependencies.
		books := NewBookManager()
		orderStore := store.NewOrderStore()
		brokerStore := store.NewBrokerStore()
		webhook := &mockWebhookDispatcher{}
		em := NewExpiryManager(time.Second, books, orderStore, brokerStore, webhook)

		// Also create a matcher for placing orders properly.
		symbols := domain.NewSymbolRegistry()
		tradeStore := store.NewTradeStore()
		m := NewMatcher(books, brokerStore, orderStore, tradeStore, symbols)

		// Base time: "now" is a fixed reference point.
		now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

		// Register brokers with enough resources. Use separate symbols per order
		// to avoid cross-matching between counterparty and previously placed orders.
		totalCash := price * qty * int64(numOrders) * 2
		totalShares := qty * int64(numOrders)

		if isBid {
			registerBroker(brokerStore, "main", totalCash, nil)
			if doPartialFill {
				// Counterparty needs shares in each symbol.
				counterHoldings := make(map[string]*domain.Holding)
				for i := 0; i < numOrders; i++ {
					sym := fmt.Sprintf("SYM%d", i)
					counterHoldings[sym] = &domain.Holding{Quantity: qty}
				}
				registerBroker(brokerStore, "counterparty", 0, counterHoldings)
			}
		} else {
			mainHoldings := make(map[string]*domain.Holding)
			for i := 0; i < numOrders; i++ {
				sym := fmt.Sprintf("SYM%d", i)
				mainHoldings[sym] = &domain.Holding{Quantity: qty}
			}
			registerBroker(brokerStore, "main", 0, mainHoldings)
			if doPartialFill {
				registerBroker(brokerStore, "counterparty", totalCash, nil)
			}
		}
		_ = totalShares

		type orderSnapshot struct {
			order          *domain.Order
			prevRemaining  int64
			prevFilledQty  int64
			prevTradeCount int
			prevTradeIDs   []string
			expiresAt      time.Time
			symbol         string
		}

		var snapshots []orderSnapshot

		for i := 0; i < numOrders; i++ {
			// Use a unique symbol per order to prevent cross-matching.
			sym := fmt.Sprintf("SYM%d", i)

			// Each order expires at a random time in the past relative to "now".
			pastOffset := rapid.Int64Range(1, 3600).Draw(t, fmt.Sprintf("pastOffset-%d", i))
			expiresAt := now.Add(-time.Duration(pastOffset) * time.Second)

			if doPartialFill {
				fillQty := rapid.Int64Range(1, qty-1).Draw(t, fmt.Sprintf("fillQty-%d", i))

				// Place a counterparty order at a compatible price first.
				if isBid {
					counterOrder := newLimitOrder("counterparty", domain.OrderSideAsk, sym, price, fillQty)
					_, err := m.MatchLimitOrder(counterOrder)
					if err != nil {
						t.Fatalf("failed to place counterparty ask %d: %v", i, err)
					}
				} else {
					counterOrder := newLimitOrder("counterparty", domain.OrderSideBid, sym, price, fillQty)
					_, err := m.MatchLimitOrder(counterOrder)
					if err != nil {
						t.Fatalf("failed to place counterparty bid %d: %v", i, err)
					}
				}
			}

			// Place the main order via the matcher.
			var order *domain.Order
			if isBid {
				order = newLimitOrder("main", domain.OrderSideBid, sym, price, qty)
			} else {
				order = newLimitOrder("main", domain.OrderSideAsk, sym, price, qty)
			}
			_, err := m.MatchLimitOrder(order)
			if err != nil {
				t.Fatalf("failed to place order %d: %v", i, err)
			}

			// Verify the order is in a cancellable state (pending or partially_filled).
			if order.Status != domain.OrderStatusPending && order.Status != domain.OrderStatusPartiallyFilled {
				t.Fatalf("order %d: expected pending or partially_filled, got %s", i, order.Status)
			}

			// Override ExpiresAt to be in the past for expiration.
			order.ExpiresAt = &expiresAt

			// Add to expiry manager.
			em.Add(order)

			// Capture pre-expiration state.
			tradeIDs := make([]string, len(order.Trades))
			for j, tr := range order.Trades {
				tradeIDs[j] = tr.TradeID
			}

			snapshots = append(snapshots, orderSnapshot{
				order:          order,
				prevRemaining:  order.RemainingQuantity,
				prevFilledQty:  order.FilledQuantity,
				prevTradeCount: len(order.Trades),
				prevTradeIDs:   tradeIDs,
				expiresAt:      expiresAt,
				symbol:         sym,
			})
		}

		// Capture pre-expiration broker reservation state.
		broker, _ := brokerStore.Get("main")
		broker.Mu.Lock()
		preReservedCash := broker.ReservedCash
		preReservedQty := make(map[string]int64)
		if !isBid {
			for _, snap := range snapshots {
				if h, ok := broker.Holdings[snap.symbol]; ok {
					preReservedQty[snap.symbol] = h.ReservedQuantity
				}
			}
		}
		broker.Mu.Unlock()

		// Run tick to expire all orders.
		em.tick(now)

		// Verify each order's expiration state.
		for i, snap := range snapshots {
			order := snap.order
			label := fmt.Sprintf("order-%d", i)

			// 1. Status must be expired.
			if order.Status != domain.OrderStatusExpired {
				t.Fatalf("%s: expected status=expired, got %s", label, order.Status)
			}

			// 2. cancelled_quantity must equal previous remaining_quantity.
			if order.CancelledQuantity != snap.prevRemaining {
				t.Fatalf("%s: expected cancelled_quantity=%d (prev remaining), got %d",
					label, snap.prevRemaining, order.CancelledQuantity)
			}

			// 3. remaining_quantity must be 0.
			if order.RemainingQuantity != 0 {
				t.Fatalf("%s: expected remaining_quantity=0, got %d", label, order.RemainingQuantity)
			}

			// 4. expired_at must equal expires_at.
			if order.ExpiredAt == nil {
				t.Fatalf("%s: expected expired_at to be set", label)
			}
			if !order.ExpiredAt.Equal(snap.expiresAt) {
				t.Fatalf("%s: expected expired_at=%v, got %v", label, snap.expiresAt, *order.ExpiredAt)
			}

			// 5. Quantity conservation: filled + remaining + cancelled == quantity.
			sum := order.FilledQuantity + order.RemainingQuantity + order.CancelledQuantity
			if sum != order.Quantity {
				t.Fatalf("%s: quantity conservation violated: filled(%d) + remaining(%d) + cancelled(%d) = %d != quantity(%d)",
					label, order.FilledQuantity, order.RemainingQuantity, order.CancelledQuantity, sum, order.Quantity)
			}

			// 6. All previous trades preserved.
			if order.FilledQuantity != snap.prevFilledQty {
				t.Fatalf("%s: filled_quantity changed: was %d, now %d",
					label, snap.prevFilledQty, order.FilledQuantity)
			}
			if len(order.Trades) != snap.prevTradeCount {
				t.Fatalf("%s: trade count changed: was %d, now %d",
					label, snap.prevTradeCount, len(order.Trades))
			}
			for j, tr := range order.Trades {
				if tr.TradeID != snap.prevTradeIDs[j] {
					t.Fatalf("%s: trade[%d] ID changed: was %s, now %s",
						label, j, snap.prevTradeIDs[j], tr.TradeID)
				}
			}

			// 7. Order removed from book.
			book := books.GetOrCreate(snap.symbol)
			_, onBook := book.index[order.OrderID]
			if onBook {
				t.Fatalf("%s: order %s should be removed from book", label, order.OrderID)
			}
		}

		// 8. Verify reservations released.
		broker.Mu.Lock()
		if isBid {
			expectedRelease := int64(0)
			for _, snap := range snapshots {
				expectedRelease += price * snap.prevRemaining
			}
			expectedReserved := preReservedCash - expectedRelease
			if broker.ReservedCash != expectedReserved {
				t.Fatalf("expected reserved_cash=%d after expiration, got %d (pre=%d, released=%d)",
					expectedReserved, broker.ReservedCash, preReservedCash, expectedRelease)
			}
		} else {
			for _, snap := range snapshots {
				h := broker.Holdings[snap.symbol]
				expectedReserved := preReservedQty[snap.symbol] - snap.prevRemaining
				if h.ReservedQuantity != expectedReserved {
					t.Fatalf("symbol %s: expected reserved_quantity=%d, got %d",
						snap.symbol, expectedReserved, h.ReservedQuantity)
				}
			}
		}
		broker.Mu.Unlock()

		// 9. Verify expiry manager has no active orders left.
		if em.ActiveOrderCount() != 0 {
			t.Fatalf("expected 0 active orders after expiration, got %d", em.ActiveOrderCount())
		}
	})
}
