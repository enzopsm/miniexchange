package domain

import "time"

// Trade represents a matched execution between a bid and an ask order.
type Trade struct {
	TradeID    string
	OrderID    string
	Price      int64 // cents
	Quantity   int64
	ExecutedAt time.Time
}
