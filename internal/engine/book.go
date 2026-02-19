package engine

import (
	"sync"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/google/btree"
)

// OrderBookEntry represents a single order resting on the book.
type OrderBookEntry struct {
	Price     int64
	CreatedAt time.Time
	OrderID   string
	Order     *domain.Order
}

// PriceLevel represents an aggregated price level in the order book.
type PriceLevel struct {
	Price         int64
	TotalQuantity int64
	OrderCount    int
}

// bidLess defines ordering for the bid side: price descending, then
// created_at ascending, then order_id ascending. This means Min()
// returns the best bid (highest price, earliest time).
func bidLess(a, b OrderBookEntry) bool {
	if a.Price != b.Price {
		return a.Price > b.Price
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.OrderID < b.OrderID
}

// askLess defines ordering for the ask side: price ascending, then
// created_at ascending, then order_id ascending. Min() returns the
// best ask (lowest price, earliest time).
func askLess(a, b OrderBookEntry) bool {
	if a.Price != b.Price {
		return a.Price < b.Price
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.OrderID < b.OrderID
}

// OrderBook maintains the bid and ask sides for a single symbol using
// B-trees with a secondary index for O(log n) removal by order ID.
type OrderBook struct {
	symbol string
	mu     sync.RWMutex
	bids   *btree.BTreeG[OrderBookEntry]
	asks   *btree.BTreeG[OrderBookEntry]
	index  map[string]OrderBookEntry // order_id → entry
}

// NewOrderBook creates an order book for the given symbol.
func NewOrderBook(symbol string) *OrderBook {
	const degree = 32
	return &OrderBook{
		symbol: symbol,
		bids:   btree.NewG[OrderBookEntry](degree, bidLess),
		asks:   btree.NewG[OrderBookEntry](degree, askLess),
		index:  make(map[string]OrderBookEntry),
	}
}
// RLock acquires the read lock on the order book.
func (ob *OrderBook) RLock() {
	ob.mu.RLock()
}

// RUnlock releases the read lock on the order book.
func (ob *OrderBook) RUnlock() {
	ob.mu.RUnlock()
}

// InsertBid adds an entry to the bid side of the book.
func (ob *OrderBook) InsertBid(entry OrderBookEntry) {
	ob.bids.ReplaceOrInsert(entry)
	ob.index[entry.OrderID] = entry
}

// InsertAsk adds an entry to the ask side of the book.
func (ob *OrderBook) InsertAsk(entry OrderBookEntry) {
	ob.asks.ReplaceOrInsert(entry)
	ob.index[entry.OrderID] = entry
}

// Remove deletes an order from the book by order ID using the
// secondary index. It tries both sides since the caller may not
// know which side the order is on.
func (ob *OrderBook) Remove(orderID string) {
	entry, ok := ob.index[orderID]
	if !ok {
		return
	}
	delete(ob.index, orderID)
	// Try both sides — Delete is a no-op if the entry isn't found.
	ob.bids.Delete(entry)
	ob.asks.Delete(entry)
}

// BestBid returns the highest-priority bid (highest price, earliest time).
func (ob *OrderBook) BestBid() (OrderBookEntry, bool) {
	return ob.bids.Min()
}

// BestAsk returns the highest-priority ask (lowest price, earliest time).
func (ob *OrderBook) BestAsk() (OrderBookEntry, bool) {
	return ob.asks.Min()
}

// TopBids returns up to n aggregated price levels from the bid side,
// ordered by price descending.
func (ob *OrderBook) TopBids(n int) []PriceLevel {
	return topLevels(ob.bids, n)
}

// TopAsks returns up to n aggregated price levels from the ask side,
// ordered by price ascending.
func (ob *OrderBook) TopAsks(n int) []PriceLevel {
	return topLevels(ob.asks, n)
}

// topLevels iterates the B-tree in order and aggregates entries into
// at most n price levels.
func topLevels(tree *btree.BTreeG[OrderBookEntry], n int) []PriceLevel {
	if n <= 0 {
		return nil
	}
	levels := make([]PriceLevel, 0, n)
	tree.Ascend(func(entry OrderBookEntry) bool {
		if len(levels) > 0 && levels[len(levels)-1].Price == entry.Price {
			levels[len(levels)-1].TotalQuantity += entry.Order.RemainingQuantity
			levels[len(levels)-1].OrderCount++
			return true
		}
		if len(levels) >= n {
			return false
		}
		levels = append(levels, PriceLevel{
			Price:         entry.Price,
			TotalQuantity: entry.Order.RemainingQuantity,
			OrderCount:    1,
		})
		return true
	})
	return levels
}

// WalkAsks iterates asks in order (lowest price first). The callback
// returns true to continue, false to stop. Used for market buy simulation.
func (ob *OrderBook) WalkAsks(fn func(OrderBookEntry) bool) {
	ob.asks.Ascend(fn)
}

// WalkBids iterates bids in order (highest price first). The callback
// returns true to continue, false to stop. Used for market sell simulation.
func (ob *OrderBook) WalkBids(fn func(OrderBookEntry) bool) {
	ob.bids.Ascend(fn)
}

// BidCount returns the number of individual bid orders on the book.
func (ob *OrderBook) BidCount() int {
	return ob.bids.Len()
}

// AskCount returns the number of individual ask orders on the book.
func (ob *OrderBook) AskCount() int {
	return ob.asks.Len()
}

// BookManager is a thread-safe map of symbol → OrderBook.
type BookManager struct {
	mu    sync.RWMutex
	books map[string]*OrderBook
}

// NewBookManager creates a new BookManager.
func NewBookManager() *BookManager {
	return &BookManager{
		books: make(map[string]*OrderBook),
	}
}

// GetOrCreate returns the order book for the given symbol, creating
// one if it doesn't already exist.
func (bm *BookManager) GetOrCreate(symbol string) *OrderBook {
	bm.mu.RLock()
	book, ok := bm.books[symbol]
	bm.mu.RUnlock()
	if ok {
		return book
	}

	bm.mu.Lock()
	defer bm.mu.Unlock()
	// Double-check after acquiring write lock.
	if book, ok = bm.books[symbol]; ok {
		return book
	}
	book = NewOrderBook(symbol)
	bm.books[symbol] = book
	return book
}
