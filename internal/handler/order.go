package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/service"
	"github.com/go-chi/chi/v5"
)

// OrderHandler handles HTTP requests for order endpoints.
type OrderHandler struct {
	orderSvc *service.OrderService
}

// NewOrderHandler creates a new OrderHandler.
func NewOrderHandler(orderSvc *service.OrderService) *OrderHandler {
	return &OrderHandler{orderSvc: orderSvc}
}

// submitOrderRequest is the JSON request body for POST /orders.
type submitOrderRequest struct {
	Type           string   `json:"type"`
	BrokerID       string   `json:"broker_id"`
	DocumentNumber string   `json:"document_number"`
	Side           string   `json:"side"`
	Symbol         string   `json:"symbol"`
	Price          *float64 `json:"price"`
	Quantity       int64    `json:"quantity"`
	ExpiresAt      *string  `json:"expires_at"`
}

// limitOrderResponse is the JSON response for limit orders.
// All fields are always present; nullable fields use pointers.
type limitOrderResponse struct {
	OrderID           string          `json:"order_id"`
	Type              string          `json:"type"`
	BrokerID          string          `json:"broker_id"`
	DocumentNumber    string          `json:"document_number"`
	Side              string          `json:"side"`
	Symbol            string          `json:"symbol"`
	Price             float64         `json:"price"`
	Quantity          int64           `json:"quantity"`
	FilledQuantity    int64           `json:"filled_quantity"`
	RemainingQuantity int64           `json:"remaining_quantity"`
	CancelledQuantity int64           `json:"cancelled_quantity"`
	Status            string          `json:"status"`
	ExpiresAt         string          `json:"expires_at"`
	CreatedAt         string          `json:"created_at"`
	CancelledAt       *string         `json:"cancelled_at"`
	ExpiredAt         *string         `json:"expired_at"`
	AveragePrice      *float64        `json:"average_price"`
	Trades            []tradeResponse `json:"trades"`
}

// marketOrderResponse is the JSON response for market orders.
// Omits price, expires_at, cancelled_at, expired_at entirely.
type marketOrderResponse struct {
	OrderID           string          `json:"order_id"`
	Type              string          `json:"type"`
	BrokerID          string          `json:"broker_id"`
	DocumentNumber    string          `json:"document_number"`
	Side              string          `json:"side"`
	Symbol            string          `json:"symbol"`
	Quantity          int64           `json:"quantity"`
	FilledQuantity    int64           `json:"filled_quantity"`
	RemainingQuantity int64           `json:"remaining_quantity"`
	CancelledQuantity int64           `json:"cancelled_quantity"`
	Status            string          `json:"status"`
	CreatedAt         string          `json:"created_at"`
	AveragePrice      *float64        `json:"average_price"`
	Trades            []tradeResponse `json:"trades"`
}

// tradeResponse is a single trade in the order response.
type tradeResponse struct {
	TradeID    string  `json:"trade_id"`
	Price      float64 `json:"price"`
	Quantity   int64   `json:"quantity"`
	ExecutedAt string  `json:"executed_at"`
}

// SubmitOrder handles POST /orders.
func (h *OrderHandler) SubmitOrder(w http.ResponseWriter, r *http.Request) {
	var req submitOrderRequest
	if err := ParseJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// Parse expires_at if provided.
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "validation_error", "expires_at must be a valid RFC 3339 timestamp")
			return
		}
		expiresAt = &t
	}

	order, err := h.orderSvc.SubmitOrder(service.SubmitOrderRequest{
		Type:           domain.OrderType(req.Type),
		BrokerID:       req.BrokerID,
		DocumentNumber: req.DocumentNumber,
		Side:           domain.OrderSide(req.Side),
		Symbol:         req.Symbol,
		Price:          req.Price,
		Quantity:       req.Quantity,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		mapOrderError(w, err)
		return
	}

	WriteJSON(w, http.StatusCreated, buildOrderResponse(order))
}

// GetOrder handles GET /orders/{order_id}.
func (h *OrderHandler) GetOrder(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "order_id")

	order, err := h.orderSvc.GetOrder(orderID)
	if err != nil {
		mapOrderError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, buildOrderResponse(order))
}

// CancelOrder handles DELETE /orders/{order_id}.
func (h *OrderHandler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "order_id")

	order, err := h.orderSvc.CancelOrder(orderID)
	if err != nil {
		mapOrderError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, buildOrderResponse(order))
}

// buildOrderResponse constructs the appropriate response type based on order type.
// Market orders omit price, expires_at, cancelled_at, expired_at.
// Limit orders always include them (null when not set).
func buildOrderResponse(o *domain.Order) any {
	trades := buildTradeResponses(o.Trades)

	var avgPrice *float64
	if avg, ok := o.AveragePrice(); ok {
		v := domain.CentsToDollars(avg)
		avgPrice = &v
	}

	if o.Type == domain.OrderTypeMarket {
		return marketOrderResponse{
			OrderID:           o.OrderID,
			Type:              string(o.Type),
			BrokerID:          o.BrokerID,
			DocumentNumber:    o.DocumentNumber,
			Side:              string(o.Side),
			Symbol:            o.Symbol,
			Quantity:          o.Quantity,
			FilledQuantity:    o.FilledQuantity,
			RemainingQuantity: o.RemainingQuantity,
			CancelledQuantity: o.CancelledQuantity,
			Status:            string(o.Status),
			CreatedAt:         o.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			AveragePrice:      avgPrice,
			Trades:            trades,
		}
	}

	// Limit order: always include price, expires_at, cancelled_at, expired_at.
	resp := limitOrderResponse{
		OrderID:           o.OrderID,
		Type:              string(o.Type),
		BrokerID:          o.BrokerID,
		DocumentNumber:    o.DocumentNumber,
		Side:              string(o.Side),
		Symbol:            o.Symbol,
		Price:             domain.CentsToDollars(o.Price),
		Quantity:          o.Quantity,
		FilledQuantity:    o.FilledQuantity,
		RemainingQuantity: o.RemainingQuantity,
		CancelledQuantity: o.CancelledQuantity,
		Status:            string(o.Status),
		ExpiresAt:         o.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
		CreatedAt:         o.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		AveragePrice:      avgPrice,
		Trades:            trades,
	}

	if o.CancelledAt != nil {
		s := o.CancelledAt.UTC().Format("2006-01-02T15:04:05Z")
		resp.CancelledAt = &s
	}
	if o.ExpiredAt != nil {
		s := o.ExpiredAt.UTC().Format("2006-01-02T15:04:05Z")
		resp.ExpiredAt = &s
	}

	return resp
}

// buildTradeResponses converts domain trades to response trades.
func buildTradeResponses(trades []*domain.Trade) []tradeResponse {
	result := make([]tradeResponse, len(trades))
	for i, t := range trades {
		result[i] = tradeResponse{
			TradeID:    t.TradeID,
			Price:      domain.CentsToDollars(t.Price),
			Quantity:   t.Quantity,
			ExecutedAt: t.ExecutedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	return result
}

// mapOrderError maps domain errors to HTTP responses for order endpoints.
func mapOrderError(w http.ResponseWriter, err error) {
	var validationErr *domain.ValidationError
	if errors.As(err, &validationErr) {
		WriteError(w, http.StatusBadRequest, "validation_error", validationErr.Message)
		return
	}

	switch {
	case errors.Is(err, domain.ErrBrokerNotFound):
		WriteError(w, http.StatusNotFound, "broker_not_found", err.Error())
	case errors.Is(err, domain.ErrOrderNotFound):
		WriteError(w, http.StatusNotFound, "order_not_found", err.Error())
	case errors.Is(err, domain.ErrOrderNotCancellable):
		WriteError(w, http.StatusConflict, "order_not_cancellable", err.Error())
	case errors.Is(err, domain.ErrInsufficientBalance):
		WriteError(w, http.StatusConflict, "insufficient_balance", err.Error())
	case errors.Is(err, domain.ErrInsufficientHoldings):
		WriteError(w, http.StatusConflict, "insufficient_holdings", err.Error())
	case errors.Is(err, domain.ErrNoLiquidity):
		WriteError(w, http.StatusConflict, "no_liquidity", err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
	}
}
