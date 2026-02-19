package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/store"
)

func newTestWebhookService() (*WebhookService, *store.BrokerStore) {
	bs := store.NewBrokerStore()
	ws := store.NewWebhookStore()
	svc := NewWebhookService(ws, bs, 5*time.Second)
	return svc, bs
}

func registerBroker(t *testing.T, bs *store.BrokerStore, id string) {
	t.Helper()
	err := bs.Create(&domain.Broker{
		BrokerID:    id,
		CashBalance: 100000,
		Holdings:    make(map[string]*domain.Holding),
		CreatedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to create broker: %v", err)
	}
}

// --- Upsert tests ---

func TestUpsert_Success_NewSubscriptions(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	webhooks, created, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "https://example.com/hooks",
		Events:   []string{"trade.executed", "order.expired"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Error("expected created=true for new subscriptions")
	}
	if len(webhooks) != 2 {
		t.Fatalf("got %d webhooks, want 2", len(webhooks))
	}
	if webhooks[0].Event != "trade.executed" {
		t.Errorf("got event %q, want %q", webhooks[0].Event, "trade.executed")
	}
	if webhooks[1].Event != "order.expired" {
		t.Errorf("got event %q, want %q", webhooks[1].Event, "order.expired")
	}
	if webhooks[0].URL != "https://example.com/hooks" {
		t.Errorf("got URL %q, want %q", webhooks[0].URL, "https://example.com/hooks")
	}
}

func TestUpsert_Success_UpdateExistingURL(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	// Create initial subscription.
	_, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "https://example.com/old",
		Events:   []string{"trade.executed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Update URL.
	webhooks, created, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "https://example.com/new",
		Events:   []string{"trade.executed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created {
		t.Error("expected created=false for URL update")
	}
	if len(webhooks) != 1 {
		t.Fatalf("got %d webhooks, want 1", len(webhooks))
	}
	if webhooks[0].URL != "https://example.com/new" {
		t.Errorf("got URL %q, want %q", webhooks[0].URL, "https://example.com/new")
	}
}

func TestUpsert_Success_IdempotentSameURL(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	webhooks1, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "https://example.com/hooks",
		Events:   []string{"trade.executed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	webhooks2, created, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "https://example.com/hooks",
		Events:   []string{"trade.executed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created {
		t.Error("expected created=false for idempotent re-registration")
	}
	if webhooks1[0].WebhookID != webhooks2[0].WebhookID {
		t.Error("webhook_id should be stable across idempotent re-registrations")
	}
}

func TestUpsert_Success_MixNewAndExisting(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	// Create one subscription.
	_, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "https://example.com/hooks",
		Events:   []string{"trade.executed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Upsert with one existing and one new.
	webhooks, created, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "https://example.com/hooks",
		Events:   []string{"trade.executed", "order.cancelled"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Error("expected created=true when at least one new subscription")
	}
	if len(webhooks) != 2 {
		t.Fatalf("got %d webhooks, want 2", len(webhooks))
	}
}

func TestUpsert_Success_DeduplicateEvents(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	webhooks, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "https://example.com/hooks",
		Events:   []string{"trade.executed", "trade.executed", "trade.executed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(webhooks) != 1 {
		t.Fatalf("got %d webhooks, want 1 (duplicates should be deduplicated)", len(webhooks))
	}
}

func TestUpsert_BrokerNotFound(t *testing.T) {
	svc, _ := newTestWebhookService()

	_, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "nonexistent",
		URL:      "https://example.com/hooks",
		Events:   []string{"trade.executed"},
	})
	if err != domain.ErrBrokerNotFound {
		t.Errorf("got error %v, want ErrBrokerNotFound", err)
	}
}

func TestUpsert_EmptyURL(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	_, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "",
		Events:   []string{"trade.executed"},
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestUpsert_HTTPSchemeRejected(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	_, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "http://example.com/hooks",
		Events:   []string{"trade.executed"},
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	ve, ok := err.(*domain.ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Message != "url must use https scheme" {
		t.Errorf("got message %q, want %q", ve.Message, "url must use https scheme")
	}
}

func TestUpsert_URLTooLong(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	longURL := "https://example.com/" + string(make([]byte, 2049))
	_, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      longURL,
		Events:   []string{"trade.executed"},
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestUpsert_InvalidURL(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	_, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "not-a-url",
		Events:   []string{"trade.executed"},
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestUpsert_EmptyEvents(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	_, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "https://example.com/hooks",
		Events:   []string{},
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	ve, ok := err.(*domain.ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Message != "events must be a non-empty array" {
		t.Errorf("got message %q, want %q", ve.Message, "events must be a non-empty array")
	}
}

func TestUpsert_InvalidEventType(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	_, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "https://example.com/hooks",
		Events:   []string{"trade.matched"},
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	ve, ok := err.(*domain.ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	expected := "Unknown event type: trade.matched. Must be one of: trade.executed, order.expired, order.cancelled"
	if ve.Message != expected {
		t.Errorf("got message %q, want %q", ve.Message, expected)
	}
}

// --- List tests ---

func TestList_Success(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	_, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "https://example.com/hooks",
		Events:   []string{"trade.executed", "order.expired"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	webhooks, err := svc.List("broker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(webhooks) != 2 {
		t.Fatalf("got %d webhooks, want 2", len(webhooks))
	}
}

func TestList_EmptyResult(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	webhooks, err := svc.List("broker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(webhooks) != 0 {
		t.Fatalf("got %d webhooks, want 0", len(webhooks))
	}
}

func TestList_BrokerNotFound(t *testing.T) {
	svc, _ := newTestWebhookService()

	_, err := svc.List("nonexistent")
	if err != domain.ErrBrokerNotFound {
		t.Errorf("got error %v, want ErrBrokerNotFound", err)
	}
}

// --- Delete tests ---

func TestDelete_Success(t *testing.T) {
	svc, bs := newTestWebhookService()
	registerBroker(t, bs, "broker-1")

	webhooks, _, err := svc.Upsert(UpsertWebhookRequest{
		BrokerID: "broker-1",
		URL:      "https://example.com/hooks",
		Events:   []string{"trade.executed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = svc.Delete(webhooks[0].WebhookID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it's gone.
	list, err := svc.List("broker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("got %d webhooks after delete, want 0", len(list))
	}
}

func TestDelete_NotFound(t *testing.T) {
	svc, _ := newTestWebhookService()

	err := svc.Delete("nonexistent-id")
	if err != domain.ErrWebhookNotFound {
		t.Errorf("got error %v, want ErrWebhookNotFound", err)
	}
}

// --- Dispatch tests ---

func TestDispatchTradeExecuted_SendsCorrectPayload(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]interface{}
	var headers []http.Header

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		json.Unmarshal(body, &payload)
		mu.Lock()
		received = append(received, payload)
		headers = append(headers, r.Header.Clone())
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	bs := store.NewBrokerStore()
	ws := store.NewWebhookStore()
	svc := &WebhookService{
		store:       ws,
		brokerStore: bs,
		client:      server.Client(),
	}

	registerBroker(t, bs, "broker-1")

	// Register webhook — use the test server URL (which is https).
	ws.Upsert(&domain.Webhook{
		WebhookID: "wh-1",
		BrokerID:  "broker-1",
		Event:     "trade.executed",
		URL:       server.URL + "/hooks",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	trade := &domain.Trade{
		TradeID:    "trd-1",
		OrderID:    "ord-1",
		Price:      14800,
		Quantity:   500,
		ExecutedAt: time.Date(2026, 2, 16, 16, 29, 0, 0, time.UTC),
	}
	order := &domain.Order{
		OrderID:           "ord-1",
		BrokerID:          "broker-1",
		Symbol:            "AAPL",
		Side:              domain.OrderSideBid,
		Status:            domain.OrderStatusPartiallyFilled,
		FilledQuantity:    500,
		RemainingQuantity: 500,
	}

	svc.DispatchTradeExecuted("broker-1", trade, order)

	// Wait for goroutine to complete.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("got %d requests, want 1", len(received))
	}

	payload := received[0]
	if payload["event"] != "trade.executed" {
		t.Errorf("got event %v, want trade.executed", payload["event"])
	}

	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be a map")
	}
	if data["trade_id"] != "trd-1" {
		t.Errorf("got trade_id %v, want trd-1", data["trade_id"])
	}
	if data["broker_id"] != "broker-1" {
		t.Errorf("got broker_id %v, want broker-1", data["broker_id"])
	}
	if data["trade_price"] != 148.0 {
		t.Errorf("got trade_price %v, want 148.0", data["trade_price"])
	}
	if data["trade_quantity"] != float64(500) {
		t.Errorf("got trade_quantity %v, want 500", data["trade_quantity"])
	}

	h := headers[0]
	if h.Get("X-Webhook-Id") != "wh-1" {
		t.Errorf("got X-Webhook-Id %q, want %q", h.Get("X-Webhook-Id"), "wh-1")
	}
	if h.Get("X-Event-Type") != "trade.executed" {
		t.Errorf("got X-Event-Type %q, want %q", h.Get("X-Event-Type"), "trade.executed")
	}
	if h.Get("X-Delivery-Id") == "" {
		t.Error("expected X-Delivery-Id header to be set")
	}
	if h.Get("Content-Type") != "application/json" {
		t.Errorf("got Content-Type %q, want %q", h.Get("Content-Type"), "application/json")
	}
}

func TestDispatchOrderExpired_SendsCorrectPayload(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]interface{}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		json.Unmarshal(body, &payload)
		mu.Lock()
		received = append(received, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	bs := store.NewBrokerStore()
	ws := store.NewWebhookStore()
	svc := &WebhookService{
		store:       ws,
		brokerStore: bs,
		client:      server.Client(),
	}

	registerBroker(t, bs, "broker-1")

	ws.Upsert(&domain.Webhook{
		WebhookID: "wh-2",
		BrokerID:  "broker-1",
		Event:     "order.expired",
		URL:       server.URL + "/hooks",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	order := &domain.Order{
		OrderID:           "ord-1",
		BrokerID:          "broker-1",
		Symbol:            "AAPL",
		Side:              domain.OrderSideBid,
		Price:             15000,
		Quantity:          1000,
		FilledQuantity:    500,
		CancelledQuantity: 500,
		RemainingQuantity: 0,
		Status:            domain.OrderStatusExpired,
	}

	svc.DispatchOrderExpired(order)
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("got %d requests, want 1", len(received))
	}

	payload := received[0]
	if payload["event"] != "order.expired" {
		t.Errorf("got event %v, want order.expired", payload["event"])
	}

	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be a map")
	}
	if data["price"] != 150.0 {
		t.Errorf("got price %v, want 150.0", data["price"])
	}
	if data["status"] != "expired" {
		t.Errorf("got status %v, want expired", data["status"])
	}
}

func TestDispatchOrderCancelled_SendsCorrectPayload(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]interface{}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		json.Unmarshal(body, &payload)
		mu.Lock()
		received = append(received, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	bs := store.NewBrokerStore()
	ws := store.NewWebhookStore()
	svc := &WebhookService{
		store:       ws,
		brokerStore: bs,
		client:      server.Client(),
	}

	registerBroker(t, bs, "broker-1")

	ws.Upsert(&domain.Webhook{
		WebhookID: "wh-3",
		BrokerID:  "broker-1",
		Event:     "order.cancelled",
		URL:       server.URL + "/hooks",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	order := &domain.Order{
		OrderID:           "ord-1",
		BrokerID:          "broker-1",
		Symbol:            "AAPL",
		Side:              domain.OrderSideAsk,
		Price:             15500,
		Quantity:          1000,
		FilledQuantity:    0,
		CancelledQuantity: 1000,
		RemainingQuantity: 0,
		Status:            domain.OrderStatusCancelled,
	}

	svc.DispatchOrderCancelled(order)
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("got %d requests, want 1", len(received))
	}

	payload := received[0]
	if payload["event"] != "order.cancelled" {
		t.Errorf("got event %v, want order.cancelled", payload["event"])
	}

	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be a map")
	}
	if data["status"] != "cancelled" {
		t.Errorf("got status %v, want cancelled", data["status"])
	}
}

func TestDispatch_NoSubscription_NoRequest(t *testing.T) {
	requestCount := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	bs := store.NewBrokerStore()
	ws := store.NewWebhookStore()
	svc := &WebhookService{
		store:       ws,
		brokerStore: bs,
		client:      server.Client(),
	}

	registerBroker(t, bs, "broker-1")

	// No subscriptions registered — dispatch should be a no-op.
	trade := &domain.Trade{
		TradeID:    "trd-1",
		OrderID:    "ord-1",
		Price:      14800,
		Quantity:   500,
		ExecutedAt: time.Now(),
	}
	order := &domain.Order{
		OrderID:  "ord-1",
		BrokerID: "broker-1",
		Symbol:   "AAPL",
		Side:     domain.OrderSideBid,
		Status:   domain.OrderStatusFilled,
	}

	svc.DispatchTradeExecuted("broker-1", trade, order)
	svc.DispatchOrderExpired(order)
	svc.DispatchOrderCancelled(order)

	time.Sleep(100 * time.Millisecond)

	if requestCount != 0 {
		t.Errorf("got %d requests, want 0 (no subscriptions)", requestCount)
	}
}

func TestDispatch_ServerError_SilentlyIgnored(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	bs := store.NewBrokerStore()
	ws := store.NewWebhookStore()
	svc := &WebhookService{
		store:       ws,
		brokerStore: bs,
		client:      server.Client(),
	}

	registerBroker(t, bs, "broker-1")

	ws.Upsert(&domain.Webhook{
		WebhookID: "wh-err",
		BrokerID:  "broker-1",
		Event:     "trade.executed",
		URL:       server.URL + "/hooks",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	trade := &domain.Trade{
		TradeID:    "trd-1",
		OrderID:    "ord-1",
		Price:      14800,
		Quantity:   500,
		ExecutedAt: time.Now(),
	}
	order := &domain.Order{
		OrderID:  "ord-1",
		BrokerID: "broker-1",
		Symbol:   "AAPL",
		Side:     domain.OrderSideBid,
		Status:   domain.OrderStatusFilled,
	}

	// Should not panic or return error — fire-and-forget.
	svc.DispatchTradeExecuted("broker-1", trade, order)
	time.Sleep(100 * time.Millisecond)
}
