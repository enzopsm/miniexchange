package engine

import (
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
)

// helper to create an OrderBookEntry with a minimal Order.
func makeEntry(price int64, createdAt time.Time, orderID string, remaining int64) OrderBookEntry {
	return OrderBookEntry{
		Price:     price,
		CreatedAt: createdAt,
		OrderID:   orderID,
		Order: &domain.Order{
			OrderID:           orderID,
			Price:             price,
			RemainingQuantity: remaining,
			CreatedAt:         createdAt,
		},
	}
}

var baseTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func TestBidLess_PriceDescending(t *testing.T) {
	a := makeEntry(200, baseTime, "a", 1)
	b := makeEntry(100, baseTime, "b", 1)
	// Higher price should come first (be "less" in bid ordering).
	if !bidLess(a, b) {
		t.Error("expected higher price to be less on bid side")
	}
	if bidLess(b, a) {
		t.Error("expected lower price to not be less on bid side")
	}
}

func TestBidLess_TimeAscending(t *testing.T) {
	a := makeEntry(100, baseTime, "a", 1)
	b := makeEntry(100, baseTime.Add(time.Second), "b", 1)
	if !bidLess(a, b) {
		t.Error("expected earlier time to be less on bid side at same price")
	}
	if bidLess(b, a) {
		t.Error("expected later time to not be less on bid side at same price")
	}
}

func TestBidLess_OrderIDAscending(t *testing.T) {
	a := makeEntry(100, baseTime, "a", 1)
	b := makeEntry(100, baseTime, "b", 1)
	if !bidLess(a, b) {
		t.Error("expected smaller order_id to be less on bid side at same price and time")
	}
	if bidLess(b, a) {
		t.Error("expected larger order_id to not be less on bid side at same price and time")
	}
}

func TestAskLess_PriceAscending(t *testing.T) {
	a := makeEntry(100, baseTime, "a", 1)
	b := makeEntry(200, baseTime, "b", 1)
	if !askLess(a, b) {
		t.Error("expected lower price to be less on ask side")
	}
	if askLess(b, a) {
		t.Error("expected higher price to not be less on ask side")
	}
}

func TestAskLess_TimeAscending(t *testing.T) {
	a := makeEntry(100, baseTime, "a", 1)
	b := makeEntry(100, baseTime.Add(time.Second), "b", 1)
	if !askLess(a, b) {
		t.Error("expected earlier time to be less on ask side at same price")
	}
}

func TestAskLess_OrderIDAscending(t *testing.T) {
	a := makeEntry(100, baseTime, "a", 1)
	b := makeEntry(100, baseTime, "b", 1)
	if !askLess(a, b) {
		t.Error("expected smaller order_id to be less on ask side at same price and time")
	}
}

func TestOrderBook_InsertAndBestBid(t *testing.T) {
	ob := NewOrderBook("AAPL")
	e1 := makeEntry(100, baseTime, "o1", 10)
	e2 := makeEntry(200, baseTime, "o2", 5)
	ob.InsertBid(e1)
	ob.InsertBid(e2)

	best, ok := ob.BestBid()
	if !ok {
		t.Fatal("expected best bid to exist")
	}
	if best.OrderID != "o2" {
		t.Errorf("expected best bid o2 (price 200), got %s (price %d)", best.OrderID, best.Price)
	}
}

func TestOrderBook_InsertAndBestAsk(t *testing.T) {
	ob := NewOrderBook("AAPL")
	e1 := makeEntry(200, baseTime, "o1", 10)
	e2 := makeEntry(100, baseTime, "o2", 5)
	ob.InsertAsk(e1)
	ob.InsertAsk(e2)

	best, ok := ob.BestAsk()
	if !ok {
		t.Fatal("expected best ask to exist")
	}
	if best.OrderID != "o2" {
		t.Errorf("expected best ask o2 (price 100), got %s (price %d)", best.OrderID, best.Price)
	}
}

func TestOrderBook_EmptyBestBid(t *testing.T) {
	ob := NewOrderBook("AAPL")
	_, ok := ob.BestBid()
	if ok {
		t.Error("expected no best bid on empty book")
	}
}

func TestOrderBook_EmptyBestAsk(t *testing.T) {
	ob := NewOrderBook("AAPL")
	_, ok := ob.BestAsk()
	if ok {
		t.Error("expected no best ask on empty book")
	}
}

func TestOrderBook_Remove(t *testing.T) {
	ob := NewOrderBook("AAPL")
	e1 := makeEntry(100, baseTime, "o1", 10)
	e2 := makeEntry(200, baseTime, "o2", 5)
	ob.InsertBid(e1)
	ob.InsertBid(e2)

	ob.Remove("o2")
	best, ok := ob.BestBid()
	if !ok {
		t.Fatal("expected best bid after removing o2")
	}
	if best.OrderID != "o1" {
		t.Errorf("expected best bid o1 after removing o2, got %s", best.OrderID)
	}
	if ob.BidCount() != 1 {
		t.Errorf("expected bid count 1, got %d", ob.BidCount())
	}
}

func TestOrderBook_RemoveNonExistent(t *testing.T) {
	ob := NewOrderBook("AAPL")
	ob.Remove("nonexistent") // should not panic
}

func TestOrderBook_RemoveFromAskSide(t *testing.T) {
	ob := NewOrderBook("AAPL")
	e1 := makeEntry(100, baseTime, "o1", 10)
	ob.InsertAsk(e1)
	ob.Remove("o1")
	if ob.AskCount() != 0 {
		t.Errorf("expected ask count 0 after removal, got %d", ob.AskCount())
	}
}

func TestOrderBook_BidCount_AskCount(t *testing.T) {
	ob := NewOrderBook("AAPL")
	if ob.BidCount() != 0 || ob.AskCount() != 0 {
		t.Error("expected empty book to have 0 counts")
	}
	ob.InsertBid(makeEntry(100, baseTime, "b1", 1))
	ob.InsertBid(makeEntry(200, baseTime, "b2", 1))
	ob.InsertAsk(makeEntry(300, baseTime, "a1", 1))
	if ob.BidCount() != 2 {
		t.Errorf("expected bid count 2, got %d", ob.BidCount())
	}
	if ob.AskCount() != 1 {
		t.Errorf("expected ask count 1, got %d", ob.AskCount())
	}
}

func TestOrderBook_TopBids(t *testing.T) {
	ob := NewOrderBook("AAPL")
	// Insert 3 bids at 2 price levels: 200 (2 orders) and 100 (1 order).
	ob.InsertBid(makeEntry(200, baseTime, "b1", 10))
	ob.InsertBid(makeEntry(200, baseTime.Add(time.Second), "b2", 5))
	ob.InsertBid(makeEntry(100, baseTime, "b3", 20))

	levels := ob.TopBids(5)
	if len(levels) != 2 {
		t.Fatalf("expected 2 price levels, got %d", len(levels))
	}
	// First level: price 200 (highest), total qty 15, 2 orders.
	if levels[0].Price != 200 || levels[0].TotalQuantity != 15 || levels[0].OrderCount != 2 {
		t.Errorf("level 0: got price=%d qty=%d count=%d", levels[0].Price, levels[0].TotalQuantity, levels[0].OrderCount)
	}
	// Second level: price 100, total qty 20, 1 order.
	if levels[1].Price != 100 || levels[1].TotalQuantity != 20 || levels[1].OrderCount != 1 {
		t.Errorf("level 1: got price=%d qty=%d count=%d", levels[1].Price, levels[1].TotalQuantity, levels[1].OrderCount)
	}
}

func TestOrderBook_TopBids_LimitN(t *testing.T) {
	ob := NewOrderBook("AAPL")
	ob.InsertBid(makeEntry(300, baseTime, "b1", 1))
	ob.InsertBid(makeEntry(200, baseTime, "b2", 1))
	ob.InsertBid(makeEntry(100, baseTime, "b3", 1))

	levels := ob.TopBids(2)
	if len(levels) != 2 {
		t.Fatalf("expected 2 levels, got %d", len(levels))
	}
	if levels[0].Price != 300 || levels[1].Price != 200 {
		t.Errorf("expected prices [300, 200], got [%d, %d]", levels[0].Price, levels[1].Price)
	}
}

func TestOrderBook_TopAsks(t *testing.T) {
	ob := NewOrderBook("AAPL")
	ob.InsertAsk(makeEntry(100, baseTime, "a1", 10))
	ob.InsertAsk(makeEntry(100, baseTime.Add(time.Second), "a2", 5))
	ob.InsertAsk(makeEntry(200, baseTime, "a3", 20))

	levels := ob.TopAsks(5)
	if len(levels) != 2 {
		t.Fatalf("expected 2 price levels, got %d", len(levels))
	}
	if levels[0].Price != 100 || levels[0].TotalQuantity != 15 || levels[0].OrderCount != 2 {
		t.Errorf("level 0: got price=%d qty=%d count=%d", levels[0].Price, levels[0].TotalQuantity, levels[0].OrderCount)
	}
	if levels[1].Price != 200 || levels[1].TotalQuantity != 20 || levels[1].OrderCount != 1 {
		t.Errorf("level 1: got price=%d qty=%d count=%d", levels[1].Price, levels[1].TotalQuantity, levels[1].OrderCount)
	}
}

func TestOrderBook_TopBids_Empty(t *testing.T) {
	ob := NewOrderBook("AAPL")
	levels := ob.TopBids(10)
	if len(levels) != 0 {
		t.Errorf("expected 0 levels on empty book, got %d", len(levels))
	}
}

func TestOrderBook_TopAsks_ZeroN(t *testing.T) {
	ob := NewOrderBook("AAPL")
	ob.InsertAsk(makeEntry(100, baseTime, "a1", 10))
	levels := ob.TopAsks(0)
	if levels != nil {
		t.Errorf("expected nil for n=0, got %v", levels)
	}
}

func TestOrderBook_WalkAsks(t *testing.T) {
	ob := NewOrderBook("AAPL")
	ob.InsertAsk(makeEntry(300, baseTime, "a3", 1))
	ob.InsertAsk(makeEntry(100, baseTime, "a1", 1))
	ob.InsertAsk(makeEntry(200, baseTime, "a2", 1))

	var prices []int64
	ob.WalkAsks(func(e OrderBookEntry) bool {
		prices = append(prices, e.Price)
		return true
	})
	if len(prices) != 3 || prices[0] != 100 || prices[1] != 200 || prices[2] != 300 {
		t.Errorf("expected asks in ascending price order [100,200,300], got %v", prices)
	}
}

func TestOrderBook_WalkAsks_StopEarly(t *testing.T) {
	ob := NewOrderBook("AAPL")
	ob.InsertAsk(makeEntry(100, baseTime, "a1", 1))
	ob.InsertAsk(makeEntry(200, baseTime, "a2", 1))
	ob.InsertAsk(makeEntry(300, baseTime, "a3", 1))

	var prices []int64
	ob.WalkAsks(func(e OrderBookEntry) bool {
		prices = append(prices, e.Price)
		return len(prices) < 2
	})
	if len(prices) != 2 {
		t.Errorf("expected walk to stop after 2 entries, got %d", len(prices))
	}
}

func TestOrderBook_WalkBids(t *testing.T) {
	ob := NewOrderBook("AAPL")
	ob.InsertBid(makeEntry(100, baseTime, "b1", 1))
	ob.InsertBid(makeEntry(300, baseTime, "b3", 1))
	ob.InsertBid(makeEntry(200, baseTime, "b2", 1))

	var prices []int64
	ob.WalkBids(func(e OrderBookEntry) bool {
		prices = append(prices, e.Price)
		return true
	})
	if len(prices) != 3 || prices[0] != 300 || prices[1] != 200 || prices[2] != 100 {
		t.Errorf("expected bids in descending price order [300,200,100], got %v", prices)
	}
}

func TestOrderBook_WalkBids_StopEarly(t *testing.T) {
	ob := NewOrderBook("AAPL")
	ob.InsertBid(makeEntry(300, baseTime, "b1", 1))
	ob.InsertBid(makeEntry(200, baseTime, "b2", 1))
	ob.InsertBid(makeEntry(100, baseTime, "b3", 1))

	var prices []int64
	ob.WalkBids(func(e OrderBookEntry) bool {
		prices = append(prices, e.Price)
		return len(prices) < 1
	})
	if len(prices) != 1 || prices[0] != 300 {
		t.Errorf("expected walk to stop after 1 entry with price 300, got %v", prices)
	}
}

func TestOrderBook_BidTimePriority(t *testing.T) {
	ob := NewOrderBook("AAPL")
	// Same price, different times — earlier should be best.
	e1 := makeEntry(100, baseTime.Add(time.Second), "o1", 1)
	e2 := makeEntry(100, baseTime, "o2", 1)
	ob.InsertBid(e1)
	ob.InsertBid(e2)

	best, _ := ob.BestBid()
	if best.OrderID != "o2" {
		t.Errorf("expected o2 (earlier time) as best bid, got %s", best.OrderID)
	}
}

func TestOrderBook_AskTimePriority(t *testing.T) {
	ob := NewOrderBook("AAPL")
	e1 := makeEntry(100, baseTime.Add(time.Second), "o1", 1)
	e2 := makeEntry(100, baseTime, "o2", 1)
	ob.InsertAsk(e1)
	ob.InsertAsk(e2)

	best, _ := ob.BestAsk()
	if best.OrderID != "o2" {
		t.Errorf("expected o2 (earlier time) as best ask, got %s", best.OrderID)
	}
}

// BookManager tests

func TestBookManager_GetOrCreate(t *testing.T) {
	bm := NewBookManager()
	book1 := bm.GetOrCreate("AAPL")
	if book1 == nil {
		t.Fatal("expected non-nil book")
	}
	if book1.symbol != "AAPL" {
		t.Errorf("expected symbol AAPL, got %s", book1.symbol)
	}

	// Same symbol returns same book.
	book2 := bm.GetOrCreate("AAPL")
	if book1 != book2 {
		t.Error("expected same book instance for same symbol")
	}

	// Different symbol returns different book.
	book3 := bm.GetOrCreate("GOOG")
	if book1 == book3 {
		t.Error("expected different book for different symbol")
	}
}

func TestBookManager_GetOrCreate_Concurrent(t *testing.T) {
	bm := NewBookManager()
	const goroutines = 50
	results := make(chan *OrderBook, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			results <- bm.GetOrCreate("AAPL")
		}()
	}

	var first *OrderBook
	for i := 0; i < goroutines; i++ {
		book := <-results
		if first == nil {
			first = book
		} else if book != first {
			t.Error("expected all goroutines to get the same book instance")
		}
	}
}
