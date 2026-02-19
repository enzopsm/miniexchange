package store

import (
	"sync"

	"github.com/efreitasn/miniexchange/internal/domain"
)

// OrderStore is a thread-safe in-memory store for orders,
// with a primary index by order_id and a secondary index by broker_id.
type OrderStore struct {
	mu           sync.RWMutex
	orders       map[string]*domain.Order
	brokerOrders map[string][]*domain.Order // broker_id → orders (append-only)
}

// NewOrderStore creates an empty OrderStore.
func NewOrderStore() *OrderStore {
	return &OrderStore{
		orders:       make(map[string]*domain.Order),
		brokerOrders: make(map[string][]*domain.Order),
	}
}

// Create adds an order to the store and appends it to the
// broker's secondary index.
func (s *OrderStore) Create(o *domain.Order) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.orders[o.OrderID] = o
	s.brokerOrders[o.BrokerID] = append(s.brokerOrders[o.BrokerID], o)
}

// Get retrieves an order by ID. It returns
// domain.ErrOrderNotFound if the order does not exist.
func (s *OrderStore) Get(id string) (*domain.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	o, ok := s.orders[id]
	if !ok {
		return nil, domain.ErrOrderNotFound
	}
	return o, nil
}

// ListByBroker returns orders for a broker in reverse chronological order
// (newest first). If status is non-nil, only orders matching that status
// are included. Pagination is 1-based. Returns the matching orders for the
// requested page and the total count of matching orders (before pagination).
func (s *OrderStore) ListByBroker(brokerID string, status *domain.OrderStatus, page, limit int) ([]*domain.Order, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := s.brokerOrders[brokerID]

	// Filter by status if provided, collecting in reverse order.
	filtered := make([]*domain.Order, 0)
	for i := len(all) - 1; i >= 0; i-- {
		if status != nil && all[i].Status != *status {
			continue
		}
		filtered = append(filtered, all[i])
	}

	total := len(filtered)

	// Apply pagination.
	start := (page - 1) * limit
	if start >= total {
		return []*domain.Order{}, total
	}
	end := start + limit
	if end > total {
		end = total
	}

	return filtered[start:end], total
}
