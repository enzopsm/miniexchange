package store

import (
	"sync"

	"github.com/efreitasn/miniexchange/internal/domain"
)

// WebhookStore is a thread-safe in-memory store for webhooks.
// Primary index: webhook_id → webhook.
// Secondary index: broker_id → event → webhook.
type WebhookStore struct {
	mu       sync.RWMutex
	webhooks map[string]*domain.Webhook            // webhook_id → webhook
	byBroker map[string]map[string]*domain.Webhook // broker_id → event → webhook
}

// NewWebhookStore creates an empty WebhookStore.
func NewWebhookStore() *WebhookStore {
	return &WebhookStore{
		webhooks: make(map[string]*domain.Webhook),
		byBroker: make(map[string]map[string]*domain.Webhook),
	}
}

// Upsert inserts or updates a webhook subscription keyed by (broker_id, event).
// If a subscription already exists for that broker+event pair, the URL and
// UpdatedAt are updated (the webhook_id remains stable). If the existing URL
// matches, it is a no-op. Returns true if a new subscription was created.
func (s *WebhookStore) Upsert(w *domain.Webhook) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if a subscription already exists for this broker+event.
	if events, ok := s.byBroker[w.BrokerID]; ok {
		if existing, ok := events[w.Event]; ok {
			// Update URL and UpdatedAt if the URL changed.
			if existing.URL != w.URL {
				existing.URL = w.URL
				existing.UpdatedAt = w.UpdatedAt
			}
			return false
		}
	}

	// New subscription — add to both indexes.
	s.webhooks[w.WebhookID] = w

	if s.byBroker[w.BrokerID] == nil {
		s.byBroker[w.BrokerID] = make(map[string]*domain.Webhook)
	}
	s.byBroker[w.BrokerID][w.Event] = w

	return true
}

// Get retrieves a webhook by ID. It returns
// domain.ErrWebhookNotFound if the webhook does not exist.
func (s *WebhookStore) Get(id string) (*domain.Webhook, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w, ok := s.webhooks[id]
	if !ok {
		return nil, domain.ErrWebhookNotFound
	}
	return w, nil
}

// ListByBroker returns all webhooks for a broker.
// Returns an empty slice if the broker has no subscriptions.
func (s *WebhookStore) ListByBroker(brokerID string) []*domain.Webhook {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.byBroker[brokerID]
	if len(events) == 0 {
		return []*domain.Webhook{}
	}

	result := make([]*domain.Webhook, 0, len(events))
	for _, w := range events {
		result = append(result, w)
	}
	return result
}

// Delete removes a webhook by ID. It returns
// domain.ErrWebhookNotFound if the webhook does not exist.
// Both the primary and secondary indexes are cleaned up.
func (s *WebhookStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.webhooks[id]
	if !ok {
		return domain.ErrWebhookNotFound
	}

	// Remove from primary index.
	delete(s.webhooks, id)

	// Remove from secondary index.
	if events, ok := s.byBroker[w.BrokerID]; ok {
		delete(events, w.Event)
		if len(events) == 0 {
			delete(s.byBroker, w.BrokerID)
		}
	}

	return nil
}

// GetByBrokerEvent returns the webhook for a specific broker+event pair,
// or nil if no subscription exists.
func (s *WebhookStore) GetByBrokerEvent(brokerID, event string) *domain.Webhook {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.byBroker[brokerID]
	if events == nil {
		return nil
	}
	return events[event]
}
