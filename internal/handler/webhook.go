package handler

import (
	"errors"
	"net/http"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/service"
	"github.com/go-chi/chi/v5"
)

// WebhookHandler handles HTTP requests for webhook endpoints.
type WebhookHandler struct {
	webhookSvc *service.WebhookService
}

// NewWebhookHandler creates a new WebhookHandler.
func NewWebhookHandler(webhookSvc *service.WebhookService) *WebhookHandler {
	return &WebhookHandler{webhookSvc: webhookSvc}
}

// upsertWebhookRequest is the JSON request body for POST /webhooks.
type upsertWebhookRequest struct {
	BrokerID string   `json:"broker_id"`
	URL      string   `json:"url"`
	Events   []string `json:"events"`
}

// webhookResponse is a single webhook in the response.
type webhookResponse struct {
	WebhookID string `json:"webhook_id"`
	BrokerID  string `json:"broker_id"`
	Event     string `json:"event"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// webhookListResponse is the JSON response for POST and GET /webhooks.
type webhookListResponse struct {
	Webhooks []webhookResponse `json:"webhooks"`
}

// Upsert handles POST /webhooks.
func (h *WebhookHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	var req upsertWebhookRequest
	if err := ParseJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	webhooks, anyCreated, err := h.webhookSvc.Upsert(service.UpsertWebhookRequest{
		BrokerID: req.BrokerID,
		URL:      req.URL,
		Events:   req.Events,
	})
	if err != nil {
		mapWebhookError(w, err)
		return
	}

	status := http.StatusOK
	if anyCreated {
		status = http.StatusCreated
	}

	WriteJSON(w, status, webhookListResponse{
		Webhooks: buildWebhookResponses(webhooks),
	})
}

// List handles GET /webhooks.
func (h *WebhookHandler) List(w http.ResponseWriter, r *http.Request) {
	brokerID := r.URL.Query().Get("broker_id")
	if brokerID == "" {
		WriteError(w, http.StatusBadRequest, "validation_error", "broker_id query parameter is required")
		return
	}

	webhooks, err := h.webhookSvc.List(brokerID)
	if err != nil {
		mapWebhookError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, webhookListResponse{
		Webhooks: buildWebhookResponses(webhooks),
	})
}

// Delete handles DELETE /webhooks/{webhook_id}.
func (h *WebhookHandler) Delete(w http.ResponseWriter, r *http.Request) {
	webhookID := chi.URLParam(r, "webhook_id")

	if err := h.webhookSvc.Delete(webhookID); err != nil {
		mapWebhookError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// buildWebhookResponses converts domain webhooks to response webhooks.
func buildWebhookResponses(webhooks []*domain.Webhook) []webhookResponse {
	result := make([]webhookResponse, len(webhooks))
	for i, wh := range webhooks {
		result[i] = webhookResponse{
			WebhookID: wh.WebhookID,
			BrokerID:  wh.BrokerID,
			Event:     wh.Event,
			URL:       wh.URL,
			CreatedAt: wh.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			UpdatedAt: wh.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	return result
}

// mapWebhookError maps domain errors to HTTP responses for webhook endpoints.
func mapWebhookError(w http.ResponseWriter, err error) {
	var validationErr *domain.ValidationError
	if errors.As(err, &validationErr) {
		WriteError(w, http.StatusBadRequest, "validation_error", validationErr.Message)
		return
	}

	switch {
	case errors.Is(err, domain.ErrBrokerNotFound):
		WriteError(w, http.StatusNotFound, "broker_not_found", err.Error())
	case errors.Is(err, domain.ErrWebhookNotFound):
		WriteError(w, http.StatusNotFound, "webhook_not_found", err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
	}
}
