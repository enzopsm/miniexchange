package engine

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/store"
)

// WebhookDispatcher is an interface for dispatching webhook notifications
// from the engine layer without depending on the service layer directly.
type WebhookDispatcher interface {
	DispatchOrderExpired(order *domain.Order)
}

// ExpiryManager tracks active limit orders sorted by expires_at and
// periodically expires orders whose expiration time has passed.
type ExpiryManager struct {
	interval     time.Duration
	books        *BookManager
	orderStore   *store.OrderStore
	brokerStore  *store.BrokerStore
	webhookSvc   WebhookDispatcher
	activeOrders []*domain.Order // sorted by expires_at ASC
	mu           sync.Mutex      // protects activeOrders slice
}

// NewExpiryManager creates a new ExpiryManager with the given dependencies.
func NewExpiryManager(
	interval time.Duration,
	books *BookManager,
	orderStore *store.OrderStore,
	brokerStore *store.BrokerStore,
	webhookSvc WebhookDispatcher,
) *ExpiryManager {
	return &ExpiryManager{
		interval:     interval,
		books:        books,
		orderStore:   orderStore,
		brokerStore:  brokerStore,
		webhookSvc:   webhookSvc,
		activeOrders: make([]*domain.Order, 0),
	}
}

// Add inserts an order into the sorted activeOrders slice, maintaining
// expires_at ASC order. Only call this for limit orders that rest on the book.
func (e *ExpiryManager) Add(order *domain.Order) {
	if order.ExpiresAt == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	expiresAt := *order.ExpiresAt
	// Binary search for the insertion point.
	idx := sort.Search(len(e.activeOrders), func(i int) bool {
		return e.activeOrders[i].ExpiresAt.After(expiresAt)
	})
	// Insert at idx.
	e.activeOrders = append(e.activeOrders, nil)
	copy(e.activeOrders[idx+1:], e.activeOrders[idx:])
	e.activeOrders[idx] = order
}

// Remove deletes an order from the activeOrders slice by order ID.
func (e *ExpiryManager) Remove(orderID string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i, o := range e.activeOrders {
		if o.OrderID == orderID {
			e.activeOrders = append(e.activeOrders[:i], e.activeOrders[i+1:]...)
			return
		}
	}
}

// Start launches a background goroutine that ticks at the configured
// interval and expires orders. It stops when ctx is cancelled.
func (e *ExpiryManager) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				e.tick(t)
			}
		}
	}()
}

// tick iterates from the front of the sorted activeOrders slice and
// expires all orders where expires_at <= now.
func (e *ExpiryManager) tick(now time.Time) {
	// Collect orders to expire under the expiry manager lock.
	e.mu.Lock()
	var toExpire []*domain.Order
	cutoff := 0
	for cutoff < len(e.activeOrders) {
		o := e.activeOrders[cutoff]
		if o.ExpiresAt == nil || o.ExpiresAt.After(now) {
			break
		}
		toExpire = append(toExpire, o)
		cutoff++
	}
	// Remove expired orders from the front of the slice.
	if cutoff > 0 {
		e.activeOrders = e.activeOrders[cutoff:]
	}
	e.mu.Unlock()

	// Process each expired order.
	for _, order := range toExpire {
		e.expireOrder(order)
	}
}

// expireOrder handles the expiration of a single order: acquires the
// per-symbol write lock, re-checks status, transitions to expired,
// releases reservation, removes from book, and fires webhook.
func (e *ExpiryManager) expireOrder(order *domain.Order) {
	// Step 1: Acquire per-symbol write lock.
	book := e.books.GetOrCreate(order.Symbol)
	book.mu.Lock()

	// Step 2: Re-check status (may have been filled/cancelled since last check).
	switch order.Status {
	case domain.OrderStatusPending, domain.OrderStatusPartiallyFilled:
		// Still eligible for expiration.
	default:
		book.mu.Unlock()
		return
	}

	// Step 3: Set status=expired, cancelled_quantity=remaining_quantity,
	// remaining_quantity=0, expired_at=expires_at.
	order.CancelledQuantity = order.RemainingQuantity
	order.RemainingQuantity = 0
	order.Status = domain.OrderStatusExpired
	order.ExpiredAt = order.ExpiresAt

	// Step 4: Remove from book.
	book.Remove(order.OrderID)

	// Step 5: Release reservation.
	broker, err := e.brokerStore.Get(order.BrokerID)
	if err == nil {
		broker.Mu.Lock()
		if order.Side == domain.OrderSideBid {
			// Release reserved cash: price × cancelled_quantity.
			broker.ReservedCash -= order.Price * order.CancelledQuantity
		} else {
			// Release reserved shares.
			if h, ok := broker.Holdings[order.Symbol]; ok {
				h.ReservedQuantity -= order.CancelledQuantity
			}
		}
		broker.Mu.Unlock()
	}

	// Release per-symbol lock before webhook dispatch to avoid blocking
	// the matching engine on network I/O.
	book.mu.Unlock()

	// Step 6: Fire webhook (outside lock).
	if e.webhookSvc != nil {
		e.webhookSvc.DispatchOrderExpired(order)
	}
}

// ActiveOrderCount returns the number of orders currently tracked for
// expiration. Useful for testing.
func (e *ExpiryManager) ActiveOrderCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.activeOrders)
}
