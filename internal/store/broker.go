package store

import (
	"sync"

	"github.com/efreitasn/miniexchange/internal/domain"
)

// BrokerStore is a thread-safe in-memory store for brokers,
// keyed by broker_id.
type BrokerStore struct {
	mu      sync.RWMutex
	brokers map[string]*domain.Broker
}

// NewBrokerStore creates an empty BrokerStore.
func NewBrokerStore() *BrokerStore {
	return &BrokerStore{
		brokers: make(map[string]*domain.Broker),
	}
}

// Create adds a broker to the store. It returns
// domain.ErrBrokerAlreadyExists if a broker with the same ID
// already exists.
func (s *BrokerStore) Create(b *domain.Broker) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.brokers[b.BrokerID]; exists {
		return domain.ErrBrokerAlreadyExists
	}
	s.brokers[b.BrokerID] = b
	return nil
}

// Get retrieves a broker by ID. It returns
// domain.ErrBrokerNotFound if the broker does not exist.
func (s *BrokerStore) Get(id string) (*domain.Broker, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.brokers[id]
	if !ok {
		return nil, domain.ErrBrokerNotFound
	}
	return b, nil
}

// Exists returns true if a broker with the given ID exists.
func (s *BrokerStore) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.brokers[id]
	return ok
}
