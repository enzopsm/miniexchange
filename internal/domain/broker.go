package domain

import (
	"sync"
	"time"
)

// Holding represents a broker's position in a single stock symbol.
type Holding struct {
	Quantity         int64
	ReservedQuantity int64
}

// Broker represents a registered participant on the exchange.
type Broker struct {
	BrokerID     string
	CashBalance  int64              // total cash in cents
	ReservedCash int64              // cash locked by active bid orders
	Holdings     map[string]*Holding // symbol → holding
	CreatedAt    time.Time
	Mu           sync.Mutex // per-broker lock for balance mutations
}

// AvailableCash returns the broker's unreserved cash balance.
func (b *Broker) AvailableCash() int64 {
	return b.CashBalance - b.ReservedCash
}

// AvailableQuantity returns the unreserved quantity for the given symbol,
// or 0 if the broker has no holding in that symbol.
func (b *Broker) AvailableQuantity(symbol string) int64 {
	h, ok := b.Holdings[symbol]
	if !ok {
		return 0
	}
	return h.Quantity - h.ReservedQuantity
}
