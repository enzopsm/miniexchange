package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/store"
	"github.com/google/uuid"
)

// Valid webhook event types.
var validWebhookEvents = map[string]bool{
	"trade.executed":  true,
	"order.expired":   true,
	"order.cancelled": true,
}

// UpsertWebhookRequest represents the input for webhook registration.
type UpsertWebhookRequest struct {
	BrokerID string
	URL      string
	Events   []string
}

// WebhookService handles webhook CRUD and event dispatch.
type WebhookService struct {
	store       *store.WebhookStore
	brokerStore *store.BrokerStore
	client      *http.Client
}

// NewWebhookService creates a new WebhookService with the given dependencies.
func NewWebhookService(
	webhookStore *store.WebhookStore,
	brokerStore *store.BrokerStore,
	webhookTimeout time.Duration,
) *WebhookService {
	return &WebhookService{
		store:       webhookStore,
		brokerStore: brokerStore,
		client: &http.Client{
			Timeout: webhookTimeout,
		},
	}
}

// Upsert validates the request and creates or updates webhook subscriptions.
// Returns the resulting webhooks, whether any new subscriptions were created, and any error.
func (s *WebhookService) Upsert(req UpsertWebhookRequest) ([]*domain.Webhook, bool, error) {
	// Validate broker exists.
	if !s.brokerStore.Exists(req.BrokerID) {
		return nil, false, domain.ErrBrokerNotFound
	}

	// Validate URL.
	if req.URL == "" {
		return nil, false, &domain.ValidationError{Message: "url is required"}
	}
	if len(req.URL) > 2048 {
		return nil, false, &domain.ValidationError{Message: "url must be at most 2048 characters"}
	}
	parsed, err := url.ParseRequestURI(req.URL)
	if err != nil || !parsed.IsAbs() {
		return nil, false, &domain.ValidationError{Message: "url must be a valid absolute URL"}
	}
	if parsed.Scheme != "https" {
		return nil, false, &domain.ValidationError{Message: "url must use https scheme"}
	}

	// Validate events.
	if len(req.Events) == 0 {
		return nil, false, &domain.ValidationError{Message: "events must be a non-empty array"}
	}

	// Deduplicate events while preserving order and validating.
	seen := make(map[string]bool, len(req.Events))
	dedupedEvents := make([]string, 0, len(req.Events))
	for _, event := range req.Events {
		if !validWebhookEvents[event] {
			return nil, false, &domain.ValidationError{
				Message: "Unknown event type: " + event + ". Must be one of: trade.executed, order.expired, order.cancelled",
			}
		}
		if !seen[event] {
			seen[event] = true
			dedupedEvents = append(dedupedEvents, event)
		}
	}

	// Upsert each (broker_id, event) pair.
	now := time.Now().UTC().Truncate(time.Second)
	anyCreated := false
	webhooks := make([]*domain.Webhook, 0, len(dedupedEvents))

	for _, event := range dedupedEvents {
		w := &domain.Webhook{
			WebhookID: uuid.New().String(),
			BrokerID:  req.BrokerID,
			Event:     event,
			URL:       req.URL,
			CreatedAt: now,
			UpdatedAt: now,
		}

		created := s.store.Upsert(w)
		if created {
			anyCreated = true
			webhooks = append(webhooks, w)
		} else {
			// Fetch the existing webhook to return it.
			existing := s.store.GetByBrokerEvent(req.BrokerID, event)
			if existing != nil {
				webhooks = append(webhooks, existing)
			}
		}
	}

	return webhooks, anyCreated, nil
}

// List validates the broker exists and returns all webhook subscriptions.
func (s *WebhookService) List(brokerID string) ([]*domain.Webhook, error) {
	if !s.brokerStore.Exists(brokerID) {
		return nil, domain.ErrBrokerNotFound
	}
	return s.store.ListByBroker(brokerID), nil
}

// Delete removes a webhook subscription by ID.
func (s *WebhookService) Delete(webhookID string) error {
	return s.store.Delete(webhookID)
}

// tradeExecutedPayload is the JSON payload for trade.executed webhooks.
type tradeExecutedPayload struct {
	Event     string                 `json:"event"`
	Timestamp string                 `json:"timestamp"`
	Data      tradeExecutedData      `json:"data"`
}

type tradeExecutedData struct {
	TradeID               string  `json:"trade_id"`
	BrokerID              string  `json:"broker_id"`
	OrderID               string  `json:"order_id"`
	Symbol                string  `json:"symbol"`
	Side                  string  `json:"side"`
	TradePrice            float64 `json:"trade_price"`
	TradeQuantity         int64   `json:"trade_quantity"`
	OrderStatus           string  `json:"order_status"`
	OrderFilledQuantity   int64   `json:"order_filled_quantity"`
	OrderRemainingQuantity int64  `json:"order_remaining_quantity"`
}

// orderEventPayload is the JSON payload for order.expired and order.cancelled webhooks.
type orderEventPayload struct {
	Event     string         `json:"event"`
	Timestamp string         `json:"timestamp"`
	Data      orderEventData `json:"data"`
}

type orderEventData struct {
	BrokerID          string  `json:"broker_id"`
	OrderID           string  `json:"order_id"`
	Symbol            string  `json:"symbol"`
	Side              string  `json:"side"`
	Price             float64 `json:"price"`
	Quantity          int64   `json:"quantity"`
	FilledQuantity    int64   `json:"filled_quantity"`
	CancelledQuantity int64   `json:"cancelled_quantity"`
	RemainingQuantity int64   `json:"remaining_quantity"`
	Status            string  `json:"status"`
}

// DispatchTradeExecuted dispatches a trade.executed webhook notification
// to the specified broker. Fire-and-forget — errors are silently ignored.
func (s *WebhookService) DispatchTradeExecuted(brokerID string, trade *domain.Trade, order *domain.Order) {
	wh := s.store.GetByBrokerEvent(brokerID, "trade.executed")
	if wh == nil {
		return
	}

	payload := tradeExecutedPayload{
		Event:     "trade.executed",
		Timestamp: trade.ExecutedAt.UTC().Truncate(time.Second).Format(time.RFC3339),
		Data: tradeExecutedData{
			TradeID:                trade.TradeID,
			BrokerID:               brokerID,
			OrderID:                order.OrderID,
			Symbol:                 order.Symbol,
			Side:                   string(order.Side),
			TradePrice:             domain.CentsToDollars(trade.Price),
			TradeQuantity:          trade.Quantity,
			OrderStatus:            string(order.Status),
			OrderFilledQuantity:    order.FilledQuantity,
			OrderRemainingQuantity: order.RemainingQuantity,
		},
	}

	go s.deliver(wh, "trade.executed", payload)
}

// DispatchOrderExpired dispatches an order.expired webhook notification
// to the order's broker. Fire-and-forget.
func (s *WebhookService) DispatchOrderExpired(order *domain.Order) {
	wh := s.store.GetByBrokerEvent(order.BrokerID, "order.expired")
	if wh == nil {
		return
	}

	payload := s.buildOrderEventPayload("order.expired", order)
	go s.deliver(wh, "order.expired", payload)
}

// DispatchOrderCancelled dispatches an order.cancelled webhook notification
// to the order's broker. Fire-and-forget.
func (s *WebhookService) DispatchOrderCancelled(order *domain.Order) {
	wh := s.store.GetByBrokerEvent(order.BrokerID, "order.cancelled")
	if wh == nil {
		return
	}

	payload := s.buildOrderEventPayload("order.cancelled", order)
	go s.deliver(wh, "order.cancelled", payload)
}

// buildOrderEventPayload creates the JSON payload for order.expired and order.cancelled events.
func (s *WebhookService) buildOrderEventPayload(event string, order *domain.Order) orderEventPayload {
	return orderEventPayload{
		Event:     event,
		Timestamp: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339),
		Data: orderEventData{
			BrokerID:          order.BrokerID,
			OrderID:           order.OrderID,
			Symbol:            order.Symbol,
			Side:              string(order.Side),
			Price:             domain.CentsToDollars(order.Price),
			Quantity:          order.Quantity,
			FilledQuantity:    order.FilledQuantity,
			CancelledQuantity: order.CancelledQuantity,
			RemainingQuantity: order.RemainingQuantity,
			Status:            string(order.Status),
		},
	}
}

// deliver sends the webhook payload via HTTP POST with the required headers.
// Errors are silently ignored (fire-and-forget).
func (s *WebhookService) deliver(wh *domain.Webhook, eventType string, payload interface{}) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	req, err := http.NewRequest(http.MethodPost, wh.URL, bytes.NewReader(body))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Delivery-Id", uuid.New().String())
	req.Header.Set("X-Webhook-Id", wh.WebhookID)
	req.Header.Set("X-Event-Type", eventType)

	resp, err := s.client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
