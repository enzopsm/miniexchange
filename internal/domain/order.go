package domain

import "time"

// OrderType distinguishes limit orders from market orders.
type OrderType string

const (
	OrderTypeLimit  OrderType = "limit"
	OrderTypeMarket OrderType = "market"
)

// OrderSide indicates whether an order is a bid (buy) or ask (sell).
type OrderSide string

const (
	OrderSideBid OrderSide = "bid"
	OrderSideAsk OrderSide = "ask"
)

// OrderStatus represents the lifecycle state of an order.
type OrderStatus string

const (
	OrderStatusPending         OrderStatus = "pending"
	OrderStatusPartiallyFilled OrderStatus = "partially_filled"
	OrderStatusFilled          OrderStatus = "filled"
	OrderStatusCancelled       OrderStatus = "cancelled"
	OrderStatusExpired         OrderStatus = "expired"
)

// Order represents a bid or ask instruction submitted by a broker.
type Order struct {
	OrderID           string
	Type              OrderType
	BrokerID          string
	DocumentNumber    string
	Side              OrderSide
	Symbol            string
	Price             int64 // cents, 0 for market orders
	Quantity          int64
	FilledQuantity    int64
	RemainingQuantity int64
	CancelledQuantity int64
	Status            OrderStatus
	ExpiresAt         *time.Time // nil for market orders
	CreatedAt         time.Time
	CancelledAt       *time.Time
	ExpiredAt         *time.Time
	Trades            []*Trade
}

// AveragePrice computes the volume-weighted average execution price
// as sum(trade.price × trade.quantity) / filled_quantity using integer
// arithmetic. Returns (price, true) when trades exist, or (0, false)
// when no trades have been executed.
func (o *Order) AveragePrice() (int64, bool) {
	if len(o.Trades) == 0 || o.FilledQuantity == 0 {
		return 0, false
	}
	var total int64
	for _, t := range o.Trades {
		total += t.Price * t.Quantity
	}
	return total / o.FilledQuantity, true
}
