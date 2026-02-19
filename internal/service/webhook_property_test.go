package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/store"
	"pgregory.net/rapid"
)

// Feature: mini-stock-exchange, Property 21: Webhook upsert idempotency
// Validates: Requirements 12.1, 12.3

// TestProperty_WebhookUpsertIdempotency verifies that for any webhook registration
// with the same (broker_id, event) pair and same URL, re-registering is idempotent —
// the webhook_id remains stable and the subscription is unchanged. Changing the URL
// updates the subscription without changing the webhook_id.
func TestProperty_WebhookUpsertIdempotency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		bs := store.NewBrokerStore()
		ws := store.NewWebhookStore()
		svc := NewWebhookService(ws, bs, 5*time.Second)

		// Register a broker.
		brokerID := fmt.Sprintf("broker-%d", rapid.IntRange(1, 9999).Draw(t, "brokerSuffix"))
		err := bs.Create(&domain.Broker{
			BrokerID:    brokerID,
			CashBalance: 100000,
			Holdings:    make(map[string]*domain.Holding),
			CreatedAt:   time.Now(),
		})
		if err != nil {
			t.Fatalf("failed to create broker: %v", err)
		}

		// Pick a random event type.
		eventTypes := []string{"trade.executed", "order.expired", "order.cancelled"}
		eventIdx := rapid.IntRange(0, len(eventTypes)-1).Draw(t, "eventIdx")
		event := eventTypes[eventIdx]

		// Generate two distinct random URLs.
		urlSuffix1 := rapid.IntRange(1, 99999).Draw(t, "urlSuffix1")
		url1 := fmt.Sprintf("https://example.com/hook/%d", urlSuffix1)

		urlSuffix2 := rapid.IntRange(1, 99999).Draw(t, "urlSuffix2")
		// Ensure url2 is different from url1.
		url2 := fmt.Sprintf("https://other.example.com/hook/%d", urlSuffix2)

		// --- Step 1: Initial registration ---
		webhooks1, created1, err := svc.Upsert(UpsertWebhookRequest{
			BrokerID: brokerID,
			URL:      url1,
			Events:   []string{event},
		})
		if err != nil {
			t.Fatalf("initial upsert failed: %v", err)
		}
		if !created1 {
			t.Fatal("expected created=true for initial registration")
		}
		if len(webhooks1) != 1 {
			t.Fatalf("expected 1 webhook, got %d", len(webhooks1))
		}
		originalID := webhooks1[0].WebhookID
		if webhooks1[0].URL != url1 {
			t.Fatalf("expected URL %q, got %q", url1, webhooks1[0].URL)
		}

		// --- Step 2: Re-register with same (broker_id, event) and same URL ---
		// This should be idempotent: webhook_id stable, created=false.
		numRepeats := rapid.IntRange(1, 5).Draw(t, "numRepeats")
		for i := 0; i < numRepeats; i++ {
			webhooks2, created2, err := svc.Upsert(UpsertWebhookRequest{
				BrokerID: brokerID,
				URL:      url1,
				Events:   []string{event},
			})
			if err != nil {
				t.Fatalf("idempotent upsert %d failed: %v", i, err)
			}
			if created2 {
				t.Fatalf("repeat %d: expected created=false for idempotent re-registration", i)
			}
			if len(webhooks2) != 1 {
				t.Fatalf("repeat %d: expected 1 webhook, got %d", i, len(webhooks2))
			}
			if webhooks2[0].WebhookID != originalID {
				t.Fatalf("repeat %d: webhook_id changed from %q to %q", i, originalID, webhooks2[0].WebhookID)
			}
			if webhooks2[0].URL != url1 {
				t.Fatalf("repeat %d: URL changed from %q to %q", i, url1, webhooks2[0].URL)
			}
		}

		// --- Step 3: Re-register with same (broker_id, event) but different URL ---
		// webhook_id should remain stable, URL should be updated.
		webhooks3, created3, err := svc.Upsert(UpsertWebhookRequest{
			BrokerID: brokerID,
			URL:      url2,
			Events:   []string{event},
		})
		if err != nil {
			t.Fatalf("URL update upsert failed: %v", err)
		}
		if created3 {
			t.Fatal("expected created=false when updating URL")
		}
		if len(webhooks3) != 1 {
			t.Fatalf("expected 1 webhook, got %d", len(webhooks3))
		}
		if webhooks3[0].WebhookID != originalID {
			t.Fatalf("webhook_id changed after URL update: %q -> %q", originalID, webhooks3[0].WebhookID)
		}
		if webhooks3[0].URL != url2 {
			t.Fatalf("expected updated URL %q, got %q", url2, webhooks3[0].URL)
		}

		// --- Step 4: Verify idempotency with the new URL ---
		webhooks4, created4, err := svc.Upsert(UpsertWebhookRequest{
			BrokerID: brokerID,
			URL:      url2,
			Events:   []string{event},
		})
		if err != nil {
			t.Fatalf("post-update idempotent upsert failed: %v", err)
		}
		if created4 {
			t.Fatal("expected created=false for idempotent re-registration after URL update")
		}
		if webhooks4[0].WebhookID != originalID {
			t.Fatalf("webhook_id not stable after URL update idempotent check: %q -> %q",
				originalID, webhooks4[0].WebhookID)
		}
		if webhooks4[0].URL != url2 {
			t.Fatalf("URL should remain %q, got %q", url2, webhooks4[0].URL)
		}
	})
}
