package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/service"
	"github.com/go-chi/chi/v5"
)

// BrokerHandler handles HTTP requests for broker endpoints.
type BrokerHandler struct {
	brokerSvc *service.BrokerService
	orderSvc  *service.OrderService
}

// NewBrokerHandler creates a new BrokerHandler.
func NewBrokerHandler(brokerSvc *service.BrokerService, orderSvc *service.OrderService) *BrokerHandler {
	return &BrokerHandler{
		brokerSvc: brokerSvc,
		orderSvc:  orderSvc,
	}
}

// registerBrokerRequest is the JSON request body for POST /brokers.
type registerBrokerRequest struct {
	BrokerID        string         `json:"broker_id"`
	InitialCash     float64        `json:"initial_cash"`
	InitialHoldings []holdingInput `json:"initial_holdings"`
}

// holdingInput is a single holding in the registration request.
type holdingInput struct {
	Symbol   string `json:"symbol"`
	Quantity int64  `json:"quantity"`
}

// brokerResponse is the JSON response for POST /brokers (201 Created).
type brokerResponse struct {
	BrokerID    string            `json:"broker_id"`
	CashBalance float64           `json:"cash_balance"`
	Holdings    []holdingResponse `json:"holdings"`
	CreatedAt   string            `json:"created_at"`
}

// holdingResponse is a single holding in the broker response.
type holdingResponse struct {
	Symbol   string `json:"symbol"`
	Quantity int64  `json:"quantity"`
}

// balanceResponse is the JSON response for GET /brokers/{broker_id}/balance.
type balanceResponse struct {
	BrokerID      string                   `json:"broker_id"`
	CashBalance   float64                  `json:"cash_balance"`
	ReservedCash  float64                  `json:"reserved_cash"`
	AvailableCash float64                  `json:"available_cash"`
	Holdings      []holdingBalanceResponse `json:"holdings"`
	UpdatedAt     string                   `json:"updated_at"`
}

// holdingBalanceResponse is a single holding in the balance response.
type holdingBalanceResponse struct {
	Symbol            string `json:"symbol"`
	Quantity          int64  `json:"quantity"`
	ReservedQuantity  int64  `json:"reserved_quantity"`
	AvailableQuantity int64  `json:"available_quantity"`
}

// orderSummaryResponse is a single order in the order listing (summary view, no trades).
type orderSummaryResponse struct {
	OrderID           string  `json:"order_id"`
	Type              string  `json:"type"`
	DocumentNumber    string  `json:"document_number"`
	Symbol            string  `json:"symbol"`
	Side              string  `json:"side"`
	Price             *float64 `json:"price,omitempty"`
	Quantity          int64   `json:"quantity"`
	FilledQuantity    int64   `json:"filled_quantity"`
	RemainingQuantity int64   `json:"remaining_quantity"`
	CancelledQuantity int64   `json:"cancelled_quantity"`
	Status            string  `json:"status"`
	AveragePrice      *float64 `json:"average_price"`
	CreatedAt         string  `json:"created_at"`
}

// orderListResponse is the JSON response for GET /brokers/{broker_id}/orders.
type orderListResponse struct {
	Orders []orderSummaryResponse `json:"orders"`
	Total  int                    `json:"total"`
	Page   int                    `json:"page"`
	Limit  int                    `json:"limit"`
}

// Register handles POST /brokers.
func (h *BrokerHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerBrokerRequest
	if err := ParseJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// Map to service request.
	holdings := make([]service.HoldingInput, len(req.InitialHoldings))
	for i, h := range req.InitialHoldings {
		holdings[i] = service.HoldingInput{
			Symbol:   h.Symbol,
			Quantity: h.Quantity,
		}
	}

	broker, err := h.brokerSvc.Register(service.RegisterBrokerRequest{
		BrokerID:        req.BrokerID,
		InitialCash:     req.InitialCash,
		InitialHoldings: holdings,
	})
	if err != nil {
		mapBrokerError(w, err)
		return
	}

	// Build response with cents→dollars conversion.
	respHoldings := make([]holdingResponse, 0, len(broker.Holdings))
	for symbol, holding := range broker.Holdings {
		respHoldings = append(respHoldings, holdingResponse{
			Symbol:   symbol,
			Quantity: holding.Quantity,
		})
	}

	WriteJSON(w, http.StatusCreated, brokerResponse{
		BrokerID:    broker.BrokerID,
		CashBalance: domain.CentsToDollars(broker.CashBalance),
		Holdings:    respHoldings,
		CreatedAt:   broker.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// GetBalance handles GET /brokers/{broker_id}/balance.
func (h *BrokerHandler) GetBalance(w http.ResponseWriter, r *http.Request) {
	brokerID := chi.URLParam(r, "broker_id")

	balance, err := h.brokerSvc.GetBalance(brokerID)
	if err != nil {
		mapBrokerError(w, err)
		return
	}

	holdings := make([]holdingBalanceResponse, len(balance.Holdings))
	for i, h := range balance.Holdings {
		holdings[i] = holdingBalanceResponse{
			Symbol:            h.Symbol,
			Quantity:          h.Quantity,
			ReservedQuantity:  h.ReservedQuantity,
			AvailableQuantity: h.AvailableQuantity,
		}
	}

	WriteJSON(w, http.StatusOK, balanceResponse{
		BrokerID:      balance.BrokerID,
		CashBalance:   domain.CentsToDollars(balance.CashBalance),
		ReservedCash:  domain.CentsToDollars(balance.ReservedCash),
		AvailableCash: domain.CentsToDollars(balance.AvailableCash),
		Holdings:      holdings,
		UpdatedAt:     balance.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// ListOrders handles GET /brokers/{broker_id}/orders.
func (h *BrokerHandler) ListOrders(w http.ResponseWriter, r *http.Request) {
	brokerID := chi.URLParam(r, "broker_id")

	// Parse query params.
	var statusFilter *domain.OrderStatus
	if s := r.URL.Query().Get("status"); s != "" {
		status := domain.OrderStatus(s)
		statusFilter = &status
	}

	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		var err error
		page, err = strconv.Atoi(p)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "validation_error", "page must be a valid integer")
			return
		}
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		var err error
		limit, err = strconv.Atoi(l)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "validation_error", "limit must be a valid integer")
			return
		}
	}

	orders, total, err := h.orderSvc.ListOrders(brokerID, statusFilter, page, limit)
	if err != nil {
		mapBrokerError(w, err)
		return
	}

	// Build summary responses.
	summaries := make([]orderSummaryResponse, len(orders))
	for i, o := range orders {
		summary := orderSummaryResponse{
			OrderID:           o.OrderID,
			Type:              string(o.Type),
			DocumentNumber:    o.DocumentNumber,
			Symbol:            o.Symbol,
			Side:              string(o.Side),
			Quantity:          o.Quantity,
			FilledQuantity:    o.FilledQuantity,
			RemainingQuantity: o.RemainingQuantity,
			CancelledQuantity: o.CancelledQuantity,
			Status:            string(o.Status),
			CreatedAt:         o.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}

		// Conditional price field: limit orders include price, market orders omit it.
		if o.Type == domain.OrderTypeLimit {
			p := domain.CentsToDollars(o.Price)
			summary.Price = &p
		}

		// average_price: present for all orders, null when no fills.
		if avg, ok := o.AveragePrice(); ok {
			a := domain.CentsToDollars(avg)
			summary.AveragePrice = &a
		}

		summaries[i] = summary
	}

	WriteJSON(w, http.StatusOK, orderListResponse{
		Orders: summaries,
		Total:  total,
		Page:   page,
		Limit:  limit,
	})
}

// mapBrokerError maps domain errors to HTTP responses for broker endpoints.
func mapBrokerError(w http.ResponseWriter, err error) {
	var validationErr *domain.ValidationError
	if errors.As(err, &validationErr) {
		WriteError(w, http.StatusBadRequest, "validation_error", validationErr.Message)
		return
	}

	switch {
	case errors.Is(err, domain.ErrBrokerAlreadyExists):
		WriteError(w, http.StatusConflict, "broker_already_exists", err.Error())
	case errors.Is(err, domain.ErrBrokerNotFound):
		WriteError(w, http.StatusNotFound, "broker_not_found", err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
	}
}
