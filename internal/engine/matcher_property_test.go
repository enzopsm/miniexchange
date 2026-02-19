package engine

import (
	"fmt"
	"testing"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/store"
	"pgregory.net/rapid"
)

// Feature: mini-stock-exchange, Property 5: Price compatibility determines matching
// Validates: Requirements 4.2

func TestProperty_PriceCompatibilityDeterminesMatching(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		bidPrice := rapid.Int64Range(1, 10000).Draw(t, "bidPrice")
		askPrice := rapid.Int64Range(1, 10000).Draw(t, "askPrice")
		qty := rapid.Int64Range(1, 100).Draw(t, "qty")

		m, bs, _, _ := newTestMatcher()

		// Seller needs enough shares; buyer needs enough cash.
		sellerHoldings := map[string]*domain.Holding{
			"TEST": {Quantity: qty * 2},
		}
		registerBroker(bs, "seller", 0, sellerHoldings)
		registerBroker(bs, "buyer", bidPrice*qty*2, nil)

		// Place the ask order on the book first.
		askOrder := newLimitOrder("seller", domain.OrderSideAsk, "TEST", askPrice, qty)
		_, err := m.MatchLimitOrder(askOrder)
		if err != nil {
			t.Fatalf("failed to place ask: %v", err)
		}

		// Now submit the bid order.
		bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "TEST", bidPrice, qty)
		trades, err := m.MatchLimitOrder(bidOrder)
		if err != nil {
			t.Fatalf("failed to place bid: %v", err)
		}

		shouldMatch := bidPrice >= askPrice

		if shouldMatch && len(trades) == 0 {
			t.Fatalf("expected trade when bid=%d >= ask=%d, but got none", bidPrice, askPrice)
		}
		if !shouldMatch && len(trades) != 0 {
			t.Fatalf("expected no trade when bid=%d < ask=%d, but got %d trades", bidPrice, askPrice, len(trades))
		}

		// When no match, verify book remains uncrossed.
		if !shouldMatch {
			book := m.books.GetOrCreate("TEST")
			bestBid, hasBid := book.BestBid()
			bestAsk, hasAsk := book.BestAsk()
			if hasBid && hasAsk {
				if bestBid.Price >= bestAsk.Price {
					t.Fatalf("book is crossed: best bid %d >= best ask %d", bestBid.Price, bestAsk.Price)
				}
			}
		}
	})
}

// Feature: mini-stock-exchange, Property 6: Execution price rule
// Validates: Requirements 4.3, 4.4

func TestProperty_ExecutionPriceEqualsAskPrice(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate prices where bid >= ask to guarantee a match.
		askPrice := rapid.Int64Range(1, 5000).Draw(t, "askPrice")
		bidPremium := rapid.Int64Range(0, 5000).Draw(t, "bidPremium")
		bidPrice := askPrice + bidPremium
		qty := rapid.Int64Range(1, 100).Draw(t, "qty")

		m, bs, _, _ := newTestMatcher()

		sellerHoldings := map[string]*domain.Holding{
			"TEST": {Quantity: qty * 2},
		}
		registerBroker(bs, "seller", 0, sellerHoldings)
		registerBroker(bs, "buyer", bidPrice*qty*2, nil)

		// Place ask on book, then submit bid.
		askOrder := newLimitOrder("seller", domain.OrderSideAsk, "TEST", askPrice, qty)
		_, err := m.MatchLimitOrder(askOrder)
		if err != nil {
			t.Fatalf("failed to place ask: %v", err)
		}

		bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "TEST", bidPrice, qty)
		trades, err := m.MatchLimitOrder(bidOrder)
		if err != nil {
			t.Fatalf("failed to place bid: %v", err)
		}

		if len(trades) == 0 {
			t.Fatalf("expected trade with bid=%d >= ask=%d", bidPrice, askPrice)
		}

		for i, trade := range trades {
			if trade.Price != askPrice {
				t.Fatalf("trade[%d]: execution price %d != ask price %d", i, trade.Price, askPrice)
			}
		}

		// Also test the reverse: incoming ask matches resting bid.
		m2, bs2, _, _ := newTestMatcher()
		registerBroker(bs2, "buyer2", bidPrice*qty*2, nil)
		sellerHoldings2 := map[string]*domain.Holding{
			"TEST": {Quantity: qty * 2},
		}
		registerBroker(bs2, "seller2", 0, sellerHoldings2)

		// Place bid on book first, then submit ask.
		bidOrder2 := newLimitOrder("buyer2", domain.OrderSideBid, "TEST", bidPrice, qty)
		_, err = m2.MatchLimitOrder(bidOrder2)
		if err != nil {
			t.Fatalf("failed to place bid: %v", err)
		}

		askOrder2 := newLimitOrder("seller2", domain.OrderSideAsk, "TEST", askPrice, qty)
		trades2, err := m2.MatchLimitOrder(askOrder2)
		if err != nil {
			t.Fatalf("failed to place ask: %v", err)
		}

		if len(trades2) == 0 {
			t.Fatalf("expected trade with bid=%d >= ask=%d (reverse)", bidPrice, askPrice)
		}

		for i, trade := range trades2 {
			if trade.Price != askPrice {
				t.Fatalf("reverse trade[%d]: execution price %d != ask price %d", i, trade.Price, askPrice)
			}
		}
	})
}

// Feature: mini-stock-exchange, Property 7: Quantity conservation
// Validates: Requirements 2.1, 3.1, 6.1, 7.2

func checkQuantityInvariant(t *rapid.T, order *domain.Order, label string) {
	sum := order.FilledQuantity + order.RemainingQuantity + order.CancelledQuantity
	if sum != order.Quantity {
		t.Fatalf("%s: quantity invariant violated: filled(%d) + remaining(%d) + cancelled(%d) = %d != quantity(%d)",
			label, order.FilledQuantity, order.RemainingQuantity, order.CancelledQuantity, sum, order.Quantity)
	}
}

func TestProperty_QuantityConservation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numAsks := rapid.IntRange(1, 10).Draw(t, "numAsks")
		numBids := rapid.IntRange(1, 10).Draw(t, "numBids")

		// Pre-generate all ask and bid specs.
		type orderSpec struct {
			price int64
			qty   int64
		}
		var askSpecs []orderSpec
		var bidSpecs []orderSpec
		totalShares := int64(0)

		for i := 0; i < numAsks; i++ {
			price := rapid.Int64Range(100, 5000).Draw(t, fmt.Sprintf("askPrice-%d", i))
			qty := rapid.Int64Range(1, 100).Draw(t, fmt.Sprintf("askQty-%d", i))
			askSpecs = append(askSpecs, orderSpec{price, qty})
			totalShares += qty
		}
		for i := 0; i < numBids; i++ {
			price := rapid.Int64Range(100, 5000).Draw(t, fmt.Sprintf("bidPrice-%d", i))
			qty := rapid.Int64Range(1, 100).Draw(t, fmt.Sprintf("bidQty-%d", i))
			bidSpecs = append(bidSpecs, orderSpec{price, qty})
		}

		m, bs, _, _ := newTestMatcher()

		sellerHoldings := map[string]*domain.Holding{
			"TEST": {Quantity: totalShares},
		}
		registerBroker(bs, "seller", 0, sellerHoldings)
		registerBroker(bs, "buyer", 100_000_000, nil) // $1M

		var allOrders []*domain.Order

		// Place asks on the book.
		for i, a := range askSpecs {
			askOrder := newLimitOrder("seller", domain.OrderSideAsk, "TEST", a.price, a.qty)
			_, err := m.MatchLimitOrder(askOrder)
			if err != nil {
				t.Fatalf("failed to place ask %d: %v", i, err)
			}
			allOrders = append(allOrders, askOrder)
			checkQuantityInvariant(t, askOrder, fmt.Sprintf("ask-%d after placement", i))
		}

		// Submit bids that may or may not match.
		for i, b := range bidSpecs {
			bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "TEST", b.price, b.qty)
			_, err := m.MatchLimitOrder(bidOrder)
			if err != nil {
				t.Fatalf("failed to place bid %d: %v", i, err)
			}
			allOrders = append(allOrders, bidOrder)
		}

		// Verify quantity invariant holds for every order.
		for i, order := range allOrders {
			checkQuantityInvariant(t, order, fmt.Sprintf("order-%d final", i))
		}
	})
}

// Feature: mini-stock-exchange, Property 13: Average price computation
// Validates: Requirements 4.6

func TestProperty_AveragePriceComputation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numAsks := rapid.IntRange(1, 5).Draw(t, "numAsks")

		m, bs, _, _ := newTestMatcher()

		// Create multiple asks at different prices.
		totalShares := int64(0)
		type askSpec struct {
			price int64
			qty   int64
		}
		var asks []askSpec
		for i := 0; i < numAsks; i++ {
			price := rapid.Int64Range(100, 5000).Draw(t, fmt.Sprintf("askPrice-%d", i))
			qty := rapid.Int64Range(1, 50).Draw(t, fmt.Sprintf("askQty-%d", i))
			asks = append(asks, askSpec{price, qty})
			totalShares += qty
		}

		sellerHoldings := map[string]*domain.Holding{
			"TEST": {Quantity: totalShares},
		}
		registerBroker(bs, "seller", 0, sellerHoldings)
		registerBroker(bs, "buyer", 100_000_000, nil) // $1M

		// Place all asks on the book.
		for i, a := range asks {
			askOrder := newLimitOrder("seller", domain.OrderSideAsk, "TEST", a.price, a.qty)
			_, err := m.MatchLimitOrder(askOrder)
			if err != nil {
				t.Fatalf("failed to place ask %d: %v", i, err)
			}
		}

		// Submit a large bid that should match some or all asks.
		bidQty := rapid.Int64Range(1, totalShares).Draw(t, "bidQty")
		// Use a high price to ensure matching.
		bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "TEST", 10000, bidQty)
		trades, err := m.MatchLimitOrder(bidOrder)
		if err != nil {
			t.Fatalf("failed to place bid: %v", err)
		}

		if len(trades) == 0 {
			// No trades means no average price to verify.
			avgPrice, hasAvg := bidOrder.AveragePrice()
			if hasAvg {
				t.Fatalf("expected no average price for unfilled order, got %d", avgPrice)
			}
			return
		}

		// Compute expected average price from trades.
		var totalCost int64
		var totalQty int64
		for _, trade := range bidOrder.Trades {
			totalCost += trade.Price * trade.Quantity
			totalQty += trade.Quantity
		}

		if totalQty != bidOrder.FilledQuantity {
			t.Fatalf("sum of trade quantities %d != filled_quantity %d", totalQty, bidOrder.FilledQuantity)
		}

		expectedAvg := totalCost / totalQty
		actualAvg, hasAvg := bidOrder.AveragePrice()
		if !hasAvg {
			t.Fatalf("expected average price for order with %d trades", len(trades))
		}
		if actualAvg != expectedAvg {
			t.Fatalf("average price %d != expected %d (totalCost=%d, totalQty=%d)",
				actualAvg, expectedAvg, totalCost, totalQty)
		}
	})
}

// TestProperty_AveragePriceNullWhenNoTrades verifies that orders with no trades
// return no average price.
func TestProperty_AveragePriceNullWhenNoTrades(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		price := rapid.Int64Range(100, 5000).Draw(t, "price")
		qty := rapid.Int64Range(1, 100).Draw(t, "qty")

		m, bs, _, _ := newTestMatcher()
		registerBroker(bs, "buyer", price*qty*2, nil)

		// Place a bid with no asks on the book — no trades possible.
		bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "TEST", price, qty)
		trades, err := m.MatchLimitOrder(bidOrder)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(trades) != 0 {
			t.Fatalf("expected no trades on empty book, got %d", len(trades))
		}

		_, hasAvg := bidOrder.AveragePrice()
		if hasAvg {
			t.Fatalf("expected no average price for order with no trades")
		}
	})
}

// Feature: mini-stock-exchange, Property 12: Market order IOC semantics
// Validates: Requirements 3.1, 3.4, 3.5

func TestProperty_MarketOrderIOCSemantics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Choose a random side for the market order.
		isBid := rapid.Bool().Draw(t, "isBid")

		// Generate random resting orders on the opposite side.
		numResting := rapid.IntRange(1, 10).Draw(t, "numResting")

		type restingSpec struct {
			price int64
			qty   int64
		}
		var restingOrders []restingSpec
		var totalLiquidity int64

		for i := 0; i < numResting; i++ {
			price := rapid.Int64Range(100, 5000).Draw(t, fmt.Sprintf("restingPrice-%d", i))
			qty := rapid.Int64Range(1, 50).Draw(t, fmt.Sprintf("restingQty-%d", i))
			restingOrders = append(restingOrders, restingSpec{price, qty})
			totalLiquidity += qty
		}

		// Market order quantity: sometimes more than liquidity, sometimes less.
		marketQty := rapid.Int64Range(1, totalLiquidity*2).Draw(t, "marketQty")

		m, bs, _, _ := newTestMatcher()

		if isBid {
			// Market bid: resting orders are asks. Seller needs shares, buyer needs cash.
			sellerHoldings := map[string]*domain.Holding{
				"TEST": {Quantity: totalLiquidity},
			}
			registerBroker(bs, "seller", 0, sellerHoldings)
			// Give buyer enough cash for worst case (highest price × total quantity).
			registerBroker(bs, "buyer", 5000*marketQty*2, nil)

			// Place resting asks.
			for i, r := range restingOrders {
				askOrder := newLimitOrder("seller", domain.OrderSideAsk, "TEST", r.price, r.qty)
				_, err := m.MatchLimitOrder(askOrder)
				if err != nil {
					t.Fatalf("failed to place ask %d: %v", i, err)
				}
			}

			// Submit market bid.
			marketOrder := newMarketOrder("buyer", domain.OrderSideBid, "TEST", marketQty)
			_, err := m.MatchMarketOrder(marketOrder)
			if err != nil {
				t.Fatalf("unexpected error on market bid: %v", err)
			}

			verifyIOCSemantics(t, marketOrder, marketQty, totalLiquidity, m.books.GetOrCreate("TEST"))
		} else {
			// Market ask: resting orders are bids. Buyer needs cash, seller needs shares.
			registerBroker(bs, "buyer", 5000*totalLiquidity*2, nil)
			sellerHoldings := map[string]*domain.Holding{
				"TEST": {Quantity: marketQty},
			}
			registerBroker(bs, "seller", 0, sellerHoldings)

			// Place resting bids.
			for i, r := range restingOrders {
				bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "TEST", r.price, r.qty)
				_, err := m.MatchLimitOrder(bidOrder)
				if err != nil {
					t.Fatalf("failed to place bid %d: %v", i, err)
				}
			}

			// Submit market ask.
			marketOrder := newMarketOrder("seller", domain.OrderSideAsk, "TEST", marketQty)
			_, err := m.MatchMarketOrder(marketOrder)
			if err != nil {
				t.Fatalf("unexpected error on market ask: %v", err)
			}

			verifyIOCSemantics(t, marketOrder, marketQty, totalLiquidity, m.books.GetOrCreate("TEST"))
		}
	})
}

// verifyIOCSemantics checks all IOC invariants for a market order after execution.
func verifyIOCSemantics(t *rapid.T, order *domain.Order, marketQty, totalLiquidity int64, book *OrderBook) {
	// 1. Quantity conservation: filled + cancelled == quantity (remaining must be 0).
	if order.FilledQuantity+order.CancelledQuantity != order.Quantity {
		t.Fatalf("quantity conservation violated: filled(%d) + cancelled(%d) = %d != quantity(%d)",
			order.FilledQuantity, order.CancelledQuantity,
			order.FilledQuantity+order.CancelledQuantity, order.Quantity)
	}
	if order.RemainingQuantity != 0 {
		t.Fatalf("market order should have remaining_quantity=0, got %d", order.RemainingQuantity)
	}

	// 2. Status and fill semantics based on liquidity.
	if marketQty <= totalLiquidity {
		// Sufficient liquidity: should be fully filled.
		if order.Status != domain.OrderStatusFilled {
			t.Fatalf("expected status 'filled' with sufficient liquidity (qty=%d, liquidity=%d), got '%s'",
				marketQty, totalLiquidity, order.Status)
		}
		if order.FilledQuantity != order.Quantity {
			t.Fatalf("expected filled_quantity=%d == quantity=%d with sufficient liquidity",
				order.FilledQuantity, order.Quantity)
		}
		if order.CancelledQuantity != 0 {
			t.Fatalf("expected cancelled_quantity=0 with sufficient liquidity, got %d", order.CancelledQuantity)
		}
	} else {
		// Partial liquidity: should be cancelled with partial fill.
		if order.Status != domain.OrderStatusCancelled {
			t.Fatalf("expected status 'cancelled' with partial liquidity (qty=%d, liquidity=%d), got '%s'",
				marketQty, totalLiquidity, order.Status)
		}
		if order.FilledQuantity >= order.Quantity {
			t.Fatalf("expected filled_quantity(%d) < quantity(%d) with partial liquidity",
				order.FilledQuantity, order.Quantity)
		}
		if order.FilledQuantity != totalLiquidity {
			t.Fatalf("expected filled_quantity=%d == total_liquidity=%d, got %d",
				totalLiquidity, totalLiquidity, order.FilledQuantity)
		}
		expectedCancelled := order.Quantity - order.FilledQuantity
		if order.CancelledQuantity != expectedCancelled {
			t.Fatalf("expected cancelled_quantity=%d, got %d",
				expectedCancelled, order.CancelledQuantity)
		}
	}

	// 3. Market order is never placed on the book.
	_, onBook := book.index[order.OrderID]
	if onBook {
		t.Fatalf("market order %s should never be on the book", order.OrderID)
	}
}


// Feature: mini-stock-exchange, Property 15: Cancellation state transition
// Validates: Requirements 6.1, 6.2, 6.4

func TestProperty_CancellationStateTransition(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Choose random order side.
		isBid := rapid.Bool().Draw(t, "isBid")
		price := rapid.Int64Range(100, 5000).Draw(t, "price")
		qty := rapid.Int64Range(1, 100).Draw(t, "qty")

		// Decide whether to partially fill the order before cancelling.
		// Partial fill only possible when qty >= 2 (need at least 1 remaining).
		doPartialFill := qty >= 2 && rapid.Bool().Draw(t, "doPartialFill")

		m, bs, _, _ := newTestMatcher()

		var order *domain.Order

		if isBid {
			registerBroker(bs, "buyer", price*qty*2, nil)

			if doPartialFill {
				// fillQty in [1, qty-1] ensures partial fill (qty >= 2 guaranteed).
				fillQty := rapid.Int64Range(1, qty-1).Draw(t, "fillQty")

				sellerHoldings := map[string]*domain.Holding{
					"TEST": {Quantity: fillQty},
				}
				registerBroker(bs, "seller", 0, sellerHoldings)

				// Place the ask at a compatible price.
				askOrder := newLimitOrder("seller", domain.OrderSideAsk, "TEST", price, fillQty)
				_, err := m.MatchLimitOrder(askOrder)
				if err != nil {
					t.Fatalf("failed to place ask: %v", err)
				}
			}

			order = newLimitOrder("buyer", domain.OrderSideBid, "TEST", price, qty)
			_, err := m.MatchLimitOrder(order)
			if err != nil {
				t.Fatalf("failed to place bid: %v", err)
			}
		} else {
			sellerHoldings := map[string]*domain.Holding{
				"TEST": {Quantity: qty},
			}
			registerBroker(bs, "seller", 0, sellerHoldings)

			if doPartialFill {
				// fillQty in [1, qty-1] ensures partial fill (qty >= 2 guaranteed).
				fillQty := rapid.Int64Range(1, qty-1).Draw(t, "fillQty")

				registerBroker(bs, "buyer", price*fillQty*2, nil)

				// Place a bid at a compatible price.
				bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "TEST", price, fillQty)
				_, err := m.MatchLimitOrder(bidOrder)
				if err != nil {
					t.Fatalf("failed to place bid: %v", err)
				}
			}

			order = newLimitOrder("seller", domain.OrderSideAsk, "TEST", price, qty)
			_, err := m.MatchLimitOrder(order)
			if err != nil {
				t.Fatalf("failed to place ask: %v", err)
			}
		}

		// Verify the order is in a cancellable state.
		if order.Status != domain.OrderStatusPending && order.Status != domain.OrderStatusPartiallyFilled {
			t.Fatalf("expected pending or partially_filled before cancel, got %s", order.Status)
		}

		// Capture pre-cancel state.
		prevRemaining := order.RemainingQuantity
		prevTrades := make([]*domain.Trade, len(order.Trades))
		copy(prevTrades, order.Trades)
		prevFilledQty := order.FilledQuantity

		// Cancel the order.
		cancelled, err := m.CancelOrder(order.OrderID)
		if err != nil {
			t.Fatalf("expected cancellation to succeed, got error: %v", err)
		}

		// Verify cancellation state transition.
		if cancelled.Status != domain.OrderStatusCancelled {
			t.Fatalf("expected status=cancelled, got %s", cancelled.Status)
		}
		if cancelled.CancelledQuantity != prevRemaining {
			t.Fatalf("expected cancelled_quantity=%d (previous remaining), got %d",
				prevRemaining, cancelled.CancelledQuantity)
		}
		if cancelled.RemainingQuantity != 0 {
			t.Fatalf("expected remaining_quantity=0, got %d", cancelled.RemainingQuantity)
		}
		if cancelled.CancelledAt == nil {
			t.Fatalf("expected cancelled_at to be set")
		}

		// Verify quantity conservation still holds.
		checkQuantityInvariant(t, cancelled, "after cancellation")

		// Verify all previous trades are preserved.
		if cancelled.FilledQuantity != prevFilledQty {
			t.Fatalf("filled_quantity changed after cancel: was %d, now %d",
				prevFilledQty, cancelled.FilledQuantity)
		}
		if len(cancelled.Trades) != len(prevTrades) {
			t.Fatalf("trades count changed after cancel: was %d, now %d",
				len(prevTrades), len(cancelled.Trades))
		}
		for i, trade := range cancelled.Trades {
			if trade.TradeID != prevTrades[i].TradeID {
				t.Fatalf("trade[%d] changed after cancel: was %s, now %s",
					i, prevTrades[i].TradeID, trade.TradeID)
			}
		}
	})
}

// Feature: mini-stock-exchange, Property 15 (terminal state): Cancellation of terminal orders
// Validates: Requirements 6.2

func TestProperty_CancellationTerminalStateFails(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m, bs, _, _ := newTestMatcher()

		// Test cancelling a filled order.
		registerBroker(bs, "buyer", 100_000_000, nil)
		sellerHoldings := map[string]*domain.Holding{
			"TEST": {Quantity: 1000},
		}
		registerBroker(bs, "seller", 0, sellerHoldings)

		qty := rapid.Int64Range(1, 100).Draw(t, "qty")
		price := rapid.Int64Range(100, 5000).Draw(t, "price")

		// Place ask, then matching bid to get a filled order.
		askOrder := newLimitOrder("seller", domain.OrderSideAsk, "TEST", price, qty)
		_, err := m.MatchLimitOrder(askOrder)
		if err != nil {
			t.Fatalf("failed to place ask: %v", err)
		}

		bidOrder := newLimitOrder("buyer", domain.OrderSideBid, "TEST", price, qty)
		_, err = m.MatchLimitOrder(bidOrder)
		if err != nil {
			t.Fatalf("failed to place bid: %v", err)
		}

		if bidOrder.Status != domain.OrderStatusFilled {
			t.Fatalf("expected bid to be filled, got %s", bidOrder.Status)
		}

		// Cancelling a filled order should fail.
		_, err = m.CancelOrder(bidOrder.OrderID)
		if err != domain.ErrOrderNotCancellable {
			t.Fatalf("expected ErrOrderNotCancellable for filled order, got %v", err)
		}

		// Test cancelling an already-cancelled order.
		pendingBid := newLimitOrder("buyer", domain.OrderSideBid, "TEST", price, qty)
		_, err = m.MatchLimitOrder(pendingBid)
		if err != nil {
			t.Fatalf("failed to place pending bid: %v", err)
		}

		_, err = m.CancelOrder(pendingBid.OrderID)
		if err != nil {
			t.Fatalf("first cancel should succeed: %v", err)
		}

		_, err = m.CancelOrder(pendingBid.OrderID)
		if err != domain.ErrOrderNotCancellable {
			t.Fatalf("expected ErrOrderNotCancellable for already-cancelled order, got %v", err)
		}
	})
}

// Feature: mini-stock-exchange, Property 23: Quote simulation accuracy
// Validates: Requirements 14.1, 14.2

func TestProperty_QuoteSimulationAccuracy(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Choose a random side for the quote/market order.
		isBid := rapid.Bool().Draw(t, "isBid")

		// Generate random resting orders on the opposite side.
		numResting := rapid.IntRange(1, 10).Draw(t, "numResting")

		type restingSpec struct {
			price int64
			qty   int64
		}
		var restingOrders []restingSpec
		var totalLiquidity int64

		for i := 0; i < numResting; i++ {
			price := rapid.Int64Range(100, 5000).Draw(t, fmt.Sprintf("restingPrice-%d", i))
			qty := rapid.Int64Range(1, 50).Draw(t, fmt.Sprintf("restingQty-%d", i))
			restingOrders = append(restingOrders, restingSpec{price, qty})
			totalLiquidity += qty
		}

		// Quote quantity: sometimes more than liquidity, sometimes less.
		quoteQty := rapid.Int64Range(1, totalLiquidity*2).Draw(t, "quoteQty")

		var side domain.OrderSide
		if isBid {
			side = domain.OrderSideBid
		} else {
			side = domain.OrderSideAsk
		}

		// --- Build two identical matcher instances with the same book state ---

		// Matcher 1: for simulation (quote).
		m1, bs1, _, _ := newTestMatcher()
		// Matcher 2: for actual execution.
		m2, bs2, _, _ := newTestMatcher()

		if isBid {
			// Resting orders are asks. Sellers need shares.
			sellerHoldings1 := map[string]*domain.Holding{
				"TEST": {Quantity: totalLiquidity},
			}
			sellerHoldings2 := map[string]*domain.Holding{
				"TEST": {Quantity: totalLiquidity},
			}
			registerBroker(bs1, "seller", 0, sellerHoldings1)
			registerBroker(bs2, "seller", 0, sellerHoldings2)

			// Buyer needs enough cash for worst case.
			registerBroker(bs1, "buyer", 5000*quoteQty*2, nil)
			registerBroker(bs2, "buyer", 5000*quoteQty*2, nil)

			// Place identical resting asks on both books.
			for i, r := range restingOrders {
				ask1 := newLimitOrder("seller", domain.OrderSideAsk, "TEST", r.price, r.qty)
				if _, err := m1.MatchLimitOrder(ask1); err != nil {
					t.Fatalf("m1: failed to place ask %d: %v", i, err)
				}
				ask2 := newLimitOrder("seller", domain.OrderSideAsk, "TEST", r.price, r.qty)
				if _, err := m2.MatchLimitOrder(ask2); err != nil {
					t.Fatalf("m2: failed to place ask %d: %v", i, err)
				}
			}
		} else {
			// Resting orders are bids. Buyers need cash.
			registerBroker(bs1, "buyer", 5000*totalLiquidity*2, nil)
			registerBroker(bs2, "buyer", 5000*totalLiquidity*2, nil)

			// Seller needs shares for the market ask.
			sellerHoldings1 := map[string]*domain.Holding{
				"TEST": {Quantity: quoteQty},
			}
			sellerHoldings2 := map[string]*domain.Holding{
				"TEST": {Quantity: quoteQty},
			}
			registerBroker(bs1, "seller", 0, sellerHoldings1)
			registerBroker(bs2, "seller", 0, sellerHoldings2)

			// Place identical resting bids on both books.
			for i, r := range restingOrders {
				bid1 := newLimitOrder("buyer", domain.OrderSideBid, "TEST", r.price, r.qty)
				if _, err := m1.MatchLimitOrder(bid1); err != nil {
					t.Fatalf("m1: failed to place bid %d: %v", i, err)
				}
				bid2 := newLimitOrder("buyer", domain.OrderSideBid, "TEST", r.price, r.qty)
				if _, err := m2.MatchLimitOrder(bid2); err != nil {
					t.Fatalf("m2: failed to place bid %d: %v", i, err)
				}
			}
		}

		// --- Step 1: Get the quote via simulation ---
		quote := m1.SimulateMarketOrder("TEST", side, quoteQty)

		// --- Step 2: Actually execute a market order on the second matcher ---
		marketOrder := newMarketOrder("buyer", side, "TEST", quoteQty)
		if side == domain.OrderSideAsk {
			marketOrder = newMarketOrder("seller", side, "TEST", quoteQty)
		}
		_, err := m2.MatchMarketOrder(marketOrder)
		if err != nil {
			t.Fatalf("market order execution failed: %v", err)
		}

		// --- Step 3: Compare quote results with actual execution ---

		// Compute actual average price and total from the executed trades.
		var actualTotalCost int64
		var actualFilledQty int64
		for _, trade := range marketOrder.Trades {
			actualTotalCost += trade.Price * trade.Quantity
			actualFilledQty += trade.Quantity
		}

		// Verify quantity_available matches actual filled_quantity.
		if quote.QuantityAvailable != actualFilledQty {
			t.Fatalf("quote.QuantityAvailable=%d != actual filled_quantity=%d",
				quote.QuantityAvailable, actualFilledQty)
		}

		// Verify fully_fillable is correct.
		expectedFullyFillable := actualFilledQty >= quoteQty
		if quote.FullyFillable != expectedFullyFillable {
			t.Fatalf("quote.FullyFillable=%v != expected=%v (qty=%d, filled=%d)",
				quote.FullyFillable, expectedFullyFillable, quoteQty, actualFilledQty)
		}

		if actualFilledQty > 0 {
			// Verify estimated_total matches actual total cost.
			if quote.EstimatedTotal == nil {
				t.Fatalf("quote.EstimatedTotal is nil but actual total cost is %d", actualTotalCost)
			}
			if *quote.EstimatedTotal != actualTotalCost {
				t.Fatalf("quote.EstimatedTotal=%d != actual total cost=%d",
					*quote.EstimatedTotal, actualTotalCost)
			}

			// Verify estimated_average_price matches actual average price.
			expectedAvgPrice := actualTotalCost / actualFilledQty
			if quote.EstimatedAvgPrice == nil {
				t.Fatalf("quote.EstimatedAvgPrice is nil but expected %d", expectedAvgPrice)
			}
			if *quote.EstimatedAvgPrice != expectedAvgPrice {
				t.Fatalf("quote.EstimatedAvgPrice=%d != expected=%d (total=%d, qty=%d)",
					*quote.EstimatedAvgPrice, expectedAvgPrice, actualTotalCost, actualFilledQty)
			}
		} else {
			// No fills: both should be nil.
			if quote.EstimatedTotal != nil {
				t.Fatalf("quote.EstimatedTotal should be nil when no fills, got %d", *quote.EstimatedTotal)
			}
			if quote.EstimatedAvgPrice != nil {
				t.Fatalf("quote.EstimatedAvgPrice should be nil when no fills, got %d", *quote.EstimatedAvgPrice)
			}
		}
	})
}


// Unused import guard — the store import is used by newTestMatcher in matcher_test.go
// which is in the same package. We need it here for the test helpers.
var _ = (*store.BrokerStore)(nil)

// Feature: mini-stock-exchange, Property 8: Cash conservation
// Validates: Requirements 4.5

func TestProperty_CashConservation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random number of brokers (2–6).
		numBrokers := rapid.IntRange(2, 6).Draw(t, "numBrokers")

		m, bs, _, _ := newTestMatcher()

		type brokerSpec struct {
			id          string
			initialCash int64
		}
		var brokers []brokerSpec
		var totalInitialCash int64

		// Register brokers with random initial cash and holdings.
		// We need at least some brokers with shares to sell and some with cash to buy.
		for i := 0; i < numBrokers; i++ {
			id := fmt.Sprintf("broker-%d", i)
			cash := rapid.Int64Range(0, 1_000_000).Draw(t, fmt.Sprintf("cash-%d", i))
			shares := rapid.Int64Range(0, 500).Draw(t, fmt.Sprintf("shares-%d", i))

			var holdings map[string]*domain.Holding
			if shares > 0 {
				holdings = map[string]*domain.Holding{
					"AAPL": {Quantity: shares},
				}
			}

			registerBroker(bs, id, cash, holdings)
			brokers = append(brokers, brokerSpec{id: id, initialCash: cash})
			totalInitialCash += cash
		}

		// Generate and execute a random sequence of limit orders.
		numOrders := rapid.IntRange(1, 20).Draw(t, "numOrders")

		for i := 0; i < numOrders; i++ {
			brokerIdx := rapid.IntRange(0, numBrokers-1).Draw(t, fmt.Sprintf("orderBroker-%d", i))
			brokerID := brokers[brokerIdx].id
			isBid := rapid.Bool().Draw(t, fmt.Sprintf("isBid-%d", i))
			price := rapid.Int64Range(100, 5000).Draw(t, fmt.Sprintf("price-%d", i))
			qty := rapid.Int64Range(1, 50).Draw(t, fmt.Sprintf("qty-%d", i))

			if isBid {
				order := newLimitOrder(brokerID, domain.OrderSideBid, "AAPL", price, qty)
				// Ignore errors — insufficient balance is expected for random orders.
				m.MatchLimitOrder(order)
			} else {
				order := newLimitOrder(brokerID, domain.OrderSideAsk, "AAPL", price, qty)
				m.MatchLimitOrder(order)
			}
		}

		// Verify cash conservation: sum of all cash_balance must equal sum of all initial_cash.
		var totalCashNow int64
		for _, b := range brokers {
			broker, err := bs.Get(b.id)
			if err != nil {
				t.Fatalf("broker %s not found: %v", b.id, err)
			}
			totalCashNow += broker.CashBalance
		}

		if totalCashNow != totalInitialCash {
			t.Fatalf("cash conservation violated: sum(cash_balance)=%d != sum(initial_cash)=%d (diff=%d)",
				totalCashNow, totalInitialCash, totalCashNow-totalInitialCash)
		}
	})
}

// Feature: mini-stock-exchange, Property 9: Holdings conservation
// Validates: Requirements 4.5
func TestProperty_HoldingsConservation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random number of brokers (2–6) and symbols (1–3).
		numBrokers := rapid.IntRange(2, 6).Draw(t, "numBrokers")
		numSymbols := rapid.IntRange(1, 3).Draw(t, "numSymbols")

		m, bs, _, _ := newTestMatcher()

		symbols := make([]string, numSymbols)
		for i := 0; i < numSymbols; i++ {
			symbols[i] = fmt.Sprintf("SYM%d", i)
		}

		type brokerSpec struct {
			id              string
			initialHoldings map[string]int64 // symbol → initial quantity
		}
		var brokers []brokerSpec

		// Track total initial holdings per symbol across all brokers.
		totalInitialHoldings := make(map[string]int64)

		// Register brokers with random cash and random initial holdings per symbol.
		for i := 0; i < numBrokers; i++ {
			id := fmt.Sprintf("broker-%d", i)
			cash := rapid.Int64Range(0, 1_000_000).Draw(t, fmt.Sprintf("cash-%d", i))

			holdings := make(map[string]*domain.Holding)
			initialH := make(map[string]int64)

			for _, sym := range symbols {
				qty := rapid.Int64Range(0, 500).Draw(t, fmt.Sprintf("holdings-%d-%s", i, sym))
				if qty > 0 {
					holdings[sym] = &domain.Holding{Quantity: qty}
					initialH[sym] = qty
					totalInitialHoldings[sym] += qty
				}
			}

			registerBroker(bs, id, cash, holdings)
			brokers = append(brokers, brokerSpec{id: id, initialHoldings: initialH})
		}

		// Generate and execute a random sequence of limit orders across symbols.
		numOrders := rapid.IntRange(1, 20).Draw(t, "numOrders")

		for i := 0; i < numOrders; i++ {
			brokerIdx := rapid.IntRange(0, numBrokers-1).Draw(t, fmt.Sprintf("orderBroker-%d", i))
			brokerID := brokers[brokerIdx].id
			symIdx := rapid.IntRange(0, numSymbols-1).Draw(t, fmt.Sprintf("orderSym-%d", i))
			symbol := symbols[symIdx]
			isBid := rapid.Bool().Draw(t, fmt.Sprintf("isBid-%d", i))
			price := rapid.Int64Range(100, 5000).Draw(t, fmt.Sprintf("price-%d", i))
			qty := rapid.Int64Range(1, 50).Draw(t, fmt.Sprintf("qty-%d", i))

			if isBid {
				order := newLimitOrder(brokerID, domain.OrderSideBid, symbol, price, qty)
				// Ignore errors — insufficient balance/holdings is expected for random orders.
				m.MatchLimitOrder(order)
			} else {
				order := newLimitOrder(brokerID, domain.OrderSideAsk, symbol, price, qty)
				m.MatchLimitOrder(order)
			}
		}

		// Verify holdings conservation: for each symbol, sum of holdings.Quantity
		// across all brokers must equal sum of initial holdings quantities.
		for _, sym := range symbols {
			var totalHoldingsNow int64
			for _, b := range brokers {
				broker, err := bs.Get(b.id)
				if err != nil {
					t.Fatalf("broker %s not found: %v", b.id, err)
				}
				if h, ok := broker.Holdings[sym]; ok {
					totalHoldingsNow += h.Quantity
				}
			}

			expected := totalInitialHoldings[sym]
			if totalHoldingsNow != expected {
				t.Fatalf("holdings conservation violated for %s: sum(holdings.Quantity)=%d != sum(initial_holdings)=%d (diff=%d)",
					sym, totalHoldingsNow, expected, totalHoldingsNow-expected)
			}
		}
	})
}

// Feature: mini-stock-exchange, Property 10: Reservation consistency
// Validates: Requirements 2.3, 2.4, 2.8, 6.1, 7.2
func TestProperty_ReservationConsistency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numBrokers := rapid.IntRange(2, 6).Draw(t, "numBrokers")
		numSymbols := rapid.IntRange(1, 3).Draw(t, "numSymbols")

		m, bs, os, _ := newTestMatcher()

		symbols := make([]string, numSymbols)
		for i := 0; i < numSymbols; i++ {
			symbols[i] = fmt.Sprintf("SYM%d", i)
		}

		type brokerSpec struct {
			id string
		}
		var brokers []brokerSpec

		// Register brokers with random cash and holdings across all symbols.
		for i := 0; i < numBrokers; i++ {
			id := fmt.Sprintf("broker-%d", i)
			cash := rapid.Int64Range(10_000, 1_000_000).Draw(t, fmt.Sprintf("cash-%d", i))

			holdings := make(map[string]*domain.Holding)
			for _, sym := range symbols {
				qty := rapid.Int64Range(0, 500).Draw(t, fmt.Sprintf("holdings-%d-%s", i, sym))
				if qty > 0 {
					holdings[sym] = &domain.Holding{Quantity: qty}
				}
			}

			registerBroker(bs, id, cash, holdings)
			brokers = append(brokers, brokerSpec{id: id})
		}

		// Execute a random sequence of operations: place limit orders, then cancel some.
		numOrders := rapid.IntRange(1, 20).Draw(t, "numOrders")
		var placedOrderIDs []string

		for i := 0; i < numOrders; i++ {
			brokerIdx := rapid.IntRange(0, numBrokers-1).Draw(t, fmt.Sprintf("orderBroker-%d", i))
			brokerID := brokers[brokerIdx].id
			symIdx := rapid.IntRange(0, numSymbols-1).Draw(t, fmt.Sprintf("orderSym-%d", i))
			symbol := symbols[symIdx]
			isBid := rapid.Bool().Draw(t, fmt.Sprintf("isBid-%d", i))
			price := rapid.Int64Range(100, 5000).Draw(t, fmt.Sprintf("price-%d", i))
			qty := rapid.Int64Range(1, 50).Draw(t, fmt.Sprintf("qty-%d", i))

			var order *domain.Order
			if isBid {
				order = newLimitOrder(brokerID, domain.OrderSideBid, symbol, price, qty)
			} else {
				order = newLimitOrder(brokerID, domain.OrderSideAsk, symbol, price, qty)
			}

			_, err := m.MatchLimitOrder(order)
			if err == nil {
				placedOrderIDs = append(placedOrderIDs, order.OrderID)
			}
		}

		// Cancel a random subset of placed orders.
		if len(placedOrderIDs) > 0 {
			numCancels := rapid.IntRange(0, len(placedOrderIDs)).Draw(t, "numCancels")
			// Shuffle by picking random indices to cancel.
			cancelled := make(map[int]bool)
			for c := 0; c < numCancels; c++ {
				idx := rapid.IntRange(0, len(placedOrderIDs)-1).Draw(t, fmt.Sprintf("cancelIdx-%d", c))
				if cancelled[idx] {
					continue
				}
				cancelled[idx] = true
				m.CancelOrder(placedOrderIDs[idx])
			}
		}

		// Verify reservation consistency for each broker.
		for _, b := range brokers {
			broker, err := bs.Get(b.id)
			if err != nil {
				t.Fatalf("broker %s not found: %v", b.id, err)
			}

			// Collect all orders for this broker from the order store.
			// Use a large page to get all orders at once.
			allOrders, _ := os.ListByBroker(b.id, nil, 1, 10000)

			// Compute expected reserved_cash from active bid orders.
			var expectedReservedCash int64
			// Compute expected reserved_quantity per symbol from active ask orders.
			expectedReservedQty := make(map[string]int64)

			for _, order := range allOrders {
				if order.Status != domain.OrderStatusPending && order.Status != domain.OrderStatusPartiallyFilled {
					continue
				}
				if order.Type != domain.OrderTypeLimit {
					continue
				}
				if order.Side == domain.OrderSideBid {
					expectedReservedCash += order.Price * order.RemainingQuantity
				} else {
					expectedReservedQty[order.Symbol] += order.RemainingQuantity
				}
			}

			// Check reserved_cash.
			broker.Mu.Lock()
			actualReservedCash := broker.ReservedCash
			broker.Mu.Unlock()

			if actualReservedCash != expectedReservedCash {
				t.Fatalf("broker %s: reserved_cash mismatch: actual=%d expected=%d (diff=%d)",
					b.id, actualReservedCash, expectedReservedCash, actualReservedCash-expectedReservedCash)
			}

			// Check reserved_quantity per symbol.
			broker.Mu.Lock()
			for _, sym := range symbols {
				var actualReservedQty int64
				if h, ok := broker.Holdings[sym]; ok {
					actualReservedQty = h.ReservedQuantity
				}
				expected := expectedReservedQty[sym]
				if actualReservedQty != expected {
					broker.Mu.Unlock()
					t.Fatalf("broker %s, symbol %s: reserved_quantity mismatch: actual=%d expected=%d (diff=%d)",
						b.id, sym, actualReservedQty, expected, actualReservedQty-expected)
				}
			}
			broker.Mu.Unlock()
		}
	})
}
