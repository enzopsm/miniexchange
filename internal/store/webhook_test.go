package store

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
)

func newTestWebhook(id, brokerID, event, url string) *domain.Webhook {
	now := time.Now()
	return &domain.Webhook{
		WebhookID: id,
		BrokerID:  brokerID,
		Event:     event,
		URL:       url,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestWebhookStore_Upsert_NewSubscription(t *testing.T) {
	s := NewWebhookStore()
	w := newTestWebhook("wh-1", "broker-1", "trade.executed", "https://example.com/hook")

	created := s.Upsert(w)
	if !created {
		t.Fatal("expected Upsert to return true for new subscription")
	}

	got, err := s.Get("wh-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.WebhookID != "wh-1" {
		t.Fatalf("expected webhook ID wh-1, got %s", got.WebhookID)
	}
}

func TestWebhookStore_Upsert_UpdateURL(t *testing.T) {
	s := NewWebhookStore()
	w := newTestWebhook("wh-1", "broker-1", "trade.executed", "https://example.com/old")
	s.Upsert(w)

	// Upsert with same broker+event but different URL.
	w2 := newTestWebhook("wh-2", "broker-1", "trade.executed", "https://example.com/new")
	w2.UpdatedAt = time.Now().Add(time.Second)
	created := s.Upsert(w2)
	if created {
		t.Fatal("expected Upsert to return false when updating existing subscription")
	}

	// The original webhook_id should be stable.
	got, err := s.Get("wh-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.URL != "https://example.com/new" {
		t.Fatalf("expected URL to be updated, got %s", got.URL)
	}

	// The new webhook_id should NOT be in the store.
	_, err = s.Get("wh-2")
	if err != domain.ErrWebhookNotFound {
		t.Fatalf("expected ErrWebhookNotFound for wh-2, got %v", err)
	}
}

func TestWebhookStore_Upsert_SameURL_Idempotent(t *testing.T) {
	s := NewWebhookStore()
	w := newTestWebhook("wh-1", "broker-1", "trade.executed", "https://example.com/hook")
	s.Upsert(w)

	// Re-register with same URL — should be a no-op.
	w2 := newTestWebhook("wh-2", "broker-1", "trade.executed", "https://example.com/hook")
	created := s.Upsert(w2)
	if created {
		t.Fatal("expected Upsert to return false for idempotent re-registration")
	}

	got, _ := s.Get("wh-1")
	if got.URL != "https://example.com/hook" {
		t.Fatalf("expected URL unchanged, got %s", got.URL)
	}
}

func TestWebhookStore_Upsert_DifferentEvents(t *testing.T) {
	s := NewWebhookStore()
	w1 := newTestWebhook("wh-1", "broker-1", "trade.executed", "https://example.com/trades")
	w2 := newTestWebhook("wh-2", "broker-1", "order.expired", "https://example.com/expired")

	c1 := s.Upsert(w1)
	c2 := s.Upsert(w2)
	if !c1 || !c2 {
		t.Fatal("expected both to be new subscriptions")
	}

	list := s.ListByBroker("broker-1")
	if len(list) != 2 {
		t.Fatalf("expected 2 webhooks, got %d", len(list))
	}
}

func TestWebhookStore_Get_NotFound(t *testing.T) {
	s := NewWebhookStore()

	_, err := s.Get("nonexistent")
	if err != domain.ErrWebhookNotFound {
		t.Fatalf("expected ErrWebhookNotFound, got %v", err)
	}
}

func TestWebhookStore_ListByBroker_Empty(t *testing.T) {
	s := NewWebhookStore()

	list := s.ListByBroker("broker-1")
	if list == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 webhooks, got %d", len(list))
	}
}

func TestWebhookStore_Delete(t *testing.T) {
	s := NewWebhookStore()
	w := newTestWebhook("wh-1", "broker-1", "trade.executed", "https://example.com/hook")
	s.Upsert(w)

	err := s.Delete("wh-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Primary index should be cleaned up.
	_, err = s.Get("wh-1")
	if err != domain.ErrWebhookNotFound {
		t.Fatalf("expected ErrWebhookNotFound after delete, got %v", err)
	}

	// Secondary index should be cleaned up.
	got := s.GetByBrokerEvent("broker-1", "trade.executed")
	if got != nil {
		t.Fatal("expected nil from GetByBrokerEvent after delete")
	}

	// ListByBroker should return empty.
	list := s.ListByBroker("broker-1")
	if len(list) != 0 {
		t.Fatalf("expected 0 webhooks after delete, got %d", len(list))
	}
}

func TestWebhookStore_Delete_NotFound(t *testing.T) {
	s := NewWebhookStore()

	err := s.Delete("nonexistent")
	if err != domain.ErrWebhookNotFound {
		t.Fatalf("expected ErrWebhookNotFound, got %v", err)
	}
}

func TestWebhookStore_Delete_PartialCleanup(t *testing.T) {
	s := NewWebhookStore()
	w1 := newTestWebhook("wh-1", "broker-1", "trade.executed", "https://example.com/trades")
	w2 := newTestWebhook("wh-2", "broker-1", "order.expired", "https://example.com/expired")
	s.Upsert(w1)
	s.Upsert(w2)

	// Delete one — the other should remain.
	s.Delete("wh-1")

	list := s.ListByBroker("broker-1")
	if len(list) != 1 {
		t.Fatalf("expected 1 webhook remaining, got %d", len(list))
	}
	if list[0].WebhookID != "wh-2" {
		t.Fatalf("expected wh-2 to remain, got %s", list[0].WebhookID)
	}
}

func TestWebhookStore_GetByBrokerEvent(t *testing.T) {
	s := NewWebhookStore()
	w := newTestWebhook("wh-1", "broker-1", "trade.executed", "https://example.com/hook")
	s.Upsert(w)

	got := s.GetByBrokerEvent("broker-1", "trade.executed")
	if got == nil {
		t.Fatal("expected webhook, got nil")
	}
	if got.WebhookID != "wh-1" {
		t.Fatalf("expected wh-1, got %s", got.WebhookID)
	}
}

func TestWebhookStore_GetByBrokerEvent_NotFound(t *testing.T) {
	s := NewWebhookStore()

	got := s.GetByBrokerEvent("broker-1", "trade.executed")
	if got != nil {
		t.Fatal("expected nil for nonexistent broker+event")
	}

	// Add a webhook for a different event.
	w := newTestWebhook("wh-1", "broker-1", "order.expired", "https://example.com/hook")
	s.Upsert(w)

	got = s.GetByBrokerEvent("broker-1", "trade.executed")
	if got != nil {
		t.Fatal("expected nil for different event")
	}
}

func TestWebhookStore_ConcurrentAccess(t *testing.T) {
	s := NewWebhookStore()
	var wg sync.WaitGroup

	// Concurrent upserts for different brokers.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := newTestWebhook(
				fmt.Sprintf("wh-%d", i),
				fmt.Sprintf("broker-%d", i),
				"trade.executed",
				fmt.Sprintf("https://example.com/hook/%d", i),
			)
			s.Upsert(w)
		}(i)
	}
	wg.Wait()

	// Concurrent reads + deletes.
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			s.ListByBroker(fmt.Sprintf("broker-%d", i))
		}(i)
		go func(i int) {
			defer wg.Done()
			s.Get(fmt.Sprintf("wh-%d", i))
		}(i)
	}
	wg.Wait()
}
