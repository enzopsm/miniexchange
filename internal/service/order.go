package service

import (
	"fmt"
	"regexp"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/engine"
	"github.com/efreitasn/miniexchange/internal/store"
)

var (
	documentNumberRegex = regexp.MustCompile(`^[a-zA-Z0-9]{1,32}$`)
	orderSymbolRegex    = regexp.MustCompile(`^[A-Z]{1,10}$`)
)

// ValidOrderStatuses lists all valid order status values for validation.
var ValidOrderStatuses = map[domain.OrderStatus]bool{
	domain.OrderStatusPending:         true,
	domain.OrderStatusPartiallyFilled: true,
	domain.OrderStatusFilled:          true,
	domain.OrderStatusCancelled:       true,
	domain.OrderStatusExpired:         true,
}

// SubmitOrderRequest represents the input for order submission.
type SubmitOrderRequest struct {
	Type           domain.OrderType
	BrokerID       string
	DocumentNumber string
	Side           domain.OrderSide
	Symbol         string
	Price          *float64   // required for limit, must be nil for market
	Quantity       int64
	ExpiresAt      *time.Time // required for limit, must be nil for market
}

// OrderService handles order submission, retrieval, cancellation, and listing.
type OrderService struct {
	matcher     *engine.Matcher
	expiry      *engine.ExpiryManager
	brokerStore *store.BrokerStore
	orderStore  *store.OrderStore
	tradeStore  *store.TradeStore
	webhookSvc  *WebhookService
	symbols     *domain.SymbolRegistry
}

// NewOrderService creates a new OrderService with the given dependencies.
func NewOrderService(
	matcher *engine.Matcher,
	expiry *engine.ExpiryManager,
	brokerStore *store.BrokerStore,
	orderStore *store.OrderStore,
	tradeStore *store.TradeStore,
	webhookSvc *WebhookService,
	symbols *domain.SymbolRegistry,
) *OrderService {
	return &OrderService{
		matcher:     matcher,
		expiry:      expiry,
		brokerStore: brokerStore,
		orderStore:  orderStore,
		tradeStore:  tradeStore,
		webhookSvc:  webhookSvc,
		symbols:     symbols,
	}
}

// SubmitOrder validates the request, creates the order, runs the matching
// engine, and dispatches webhooks for any trades executed.
func (s *OrderService) SubmitOrder(req SubmitOrderRequest) (*domain.Order, error) {
	// Validate order type.
	if req.Type != domain.OrderTypeLimit && req.Type != domain.OrderTypeMarket {
		return nil, &domain.ValidationError{
			Message: fmt.Sprintf("Unknown order type: %s. Must be one of: limit, market", req.Type),
		}
	}

	// Validate common fields.
	if !brokerIDRegex.MatchString(req.BrokerID) {
		return nil, &domain.ValidationError{
			Message: "broker_id must match ^[a-zA-Z0-9_-]{1,64}$",
		}
	}
	if !documentNumberRegex.MatchString(req.DocumentNumber) {
		return nil, &domain.ValidationError{
			Message: "document_number must match ^[a-zA-Z0-9]{1,32}$",
		}
	}
	if req.Side != domain.OrderSideBid && req.Side != domain.OrderSideAsk {
		return nil, &domain.ValidationError{
			Message: "side must be 'bid' or 'ask'",
		}
	}
	if !orderSymbolRegex.MatchString(req.Symbol) {
		return nil, &domain.ValidationError{
			Message: "symbol must match ^[A-Z]{1,10}$",
		}
	}
	if req.Quantity <= 0 {
		return nil, &domain.ValidationError{
			Message: "quantity must be a positive integer",
		}
	}

	// Type-specific validation.
	if req.Type == domain.OrderTypeLimit {
		return s.submitLimitOrder(req)
	}
	return s.submitMarketOrder(req)
}

func (s *OrderService) submitLimitOrder(req SubmitOrderRequest) (*domain.Order, error) {
	// Validate price.
	if req.Price == nil {
		return nil, &domain.ValidationError{
			Message: "price is required for limit orders",
		}
	}
	if *req.Price <= 0 {
		return nil, &domain.ValidationError{
			Message: "price must be greater than 0",
		}
	}
	priceCents, err := domain.DollarsToCents(*req.Price)
	if err != nil {
		return nil, &domain.ValidationError{
			Message: "price must have at most 2 decimal places",
		}
	}

	// Validate expires_at.
	if req.ExpiresAt == nil {
		return nil, &domain.ValidationError{
			Message: "expires_at is required for limit orders",
		}
	}
	if !req.ExpiresAt.After(time.Now()) {
		return nil, &domain.ValidationError{
			Message: "expires_at must be a future timestamp",
		}
	}

	// Validate broker exists.
	if !s.brokerStore.Exists(req.BrokerID) {
		return nil, domain.ErrBrokerNotFound
	}

	order := &domain.Order{
		Type:           domain.OrderTypeLimit,
		BrokerID:       req.BrokerID,
		DocumentNumber: req.DocumentNumber,
		Side:           req.Side,
		Symbol:         req.Symbol,
		Price:          priceCents,
		Quantity:       req.Quantity,
		ExpiresAt:      req.ExpiresAt,
	}

	trades, err := s.matcher.MatchLimitOrder(order)
	if err != nil {
		return nil, err
	}

	// If the order rests on the book (pending or partially_filled), add to expiry manager.
	if order.Status == domain.OrderStatusPending || order.Status == domain.OrderStatusPartiallyFilled {
		s.expiry.Add(order)
	}

	// Dispatch webhooks for trades (outside the lock, fire-and-forget).
	s.dispatchTradeWebhooks(trades, order)

	return order, nil
}

func (s *OrderService) submitMarketOrder(req SubmitOrderRequest) (*domain.Order, error) {
	// Market orders must NOT include price or expires_at.
	if req.Price != nil {
		return nil, &domain.ValidationError{
			Message: "market orders must not include price",
		}
	}
	if req.ExpiresAt != nil {
		return nil, &domain.ValidationError{
			Message: "market orders must not include expires_at",
		}
	}

	// Validate broker exists.
	if !s.brokerStore.Exists(req.BrokerID) {
		return nil, domain.ErrBrokerNotFound
	}

	order := &domain.Order{
		Type:           domain.OrderTypeMarket,
		BrokerID:       req.BrokerID,
		DocumentNumber: req.DocumentNumber,
		Side:           req.Side,
		Symbol:         req.Symbol,
		Quantity:       req.Quantity,
	}

	trades, err := s.matcher.MatchMarketOrder(order)
	if err != nil {
		return nil, err
	}

	// Dispatch webhooks for trades.
	s.dispatchTradeWebhooks(trades, order)

	return order, nil
}

// dispatchTradeWebhooks dispatches trade.executed webhooks for each trade
// to both the buyer and seller brokers. Skips dispatch if webhookSvc is nil.
//
// The trades slice contains the incoming order's trade records. For each
// trade, we also need to notify the resting order's broker. We find the
// resting order by looking up the counterpart trade (same TradeID, different
// OrderID) in the trade store.
func (s *OrderService) dispatchTradeWebhooks(trades []*domain.Trade, incomingOrder *domain.Order) {
	if s.webhookSvc == nil || len(trades) == 0 {
		return
	}

	// Build a set of TradeIDs to find counterpart orders.
	// The trade store has both sides appended per symbol.
	allTrades := s.tradeStore.GetBySymbol(incomingOrder.Symbol)

	// Index counterpart trades by TradeID for O(1) lookup.
	counterparts := make(map[string]*domain.Trade, len(trades))
	for _, t := range allTrades {
		if t.OrderID != incomingOrder.OrderID {
			counterparts[t.TradeID] = t
		}
	}

	for _, trade := range trades {
		// Dispatch to the incoming order's broker.
		s.webhookSvc.DispatchTradeExecuted(incomingOrder.BrokerID, trade, incomingOrder)

		// Dispatch to the resting order's broker.
		if ct, ok := counterparts[trade.TradeID]; ok {
			restingOrder, err := s.orderStore.Get(ct.OrderID)
			if err == nil {
				s.webhookSvc.DispatchTradeExecuted(restingOrder.BrokerID, ct, restingOrder)
			}
		}
	}
}

// GetOrder retrieves an order by ID with all its trades.
func (s *OrderService) GetOrder(orderID string) (*domain.Order, error) {
	return s.orderStore.Get(orderID)
}

// CancelOrder cancels a pending or partially filled order.
func (s *OrderService) CancelOrder(orderID string) (*domain.Order, error) {
	order, err := s.matcher.CancelOrder(orderID)
	if err != nil {
		return nil, err
	}

	// Remove from expiry manager.
	s.expiry.Remove(orderID)

	// Dispatch order.cancelled webhook.
	if s.webhookSvc != nil {
		s.webhookSvc.DispatchOrderCancelled(order)
	}

	return order, nil
}

// ListOrders returns a paginated list of orders for a broker with optional
// status filtering.
func (s *OrderService) ListOrders(brokerID string, status *domain.OrderStatus, page, limit int) ([]*domain.Order, int, error) {
	// Validate broker exists.
	if !s.brokerStore.Exists(brokerID) {
		return nil, 0, domain.ErrBrokerNotFound
	}

	// Validate status if provided.
	if status != nil {
		if !ValidOrderStatuses[*status] {
			return nil, 0, &domain.ValidationError{
				Message: fmt.Sprintf("Invalid status filter: '%s'. Must be one of: pending, partially_filled, filled, cancelled, expired", *status),
			}
		}
	}

	// Validate pagination.
	if page < 1 {
		return nil, 0, &domain.ValidationError{
			Message: "page must be >= 1",
		}
	}
	if limit < 1 || limit > 100 {
		return nil, 0, &domain.ValidationError{
			Message: "limit must be between 1 and 100",
		}
	}

	orders, total := s.orderStore.ListByBroker(brokerID, status, page, limit)
	return orders, total, nil
}
