package domain

import (
	"testing"
	"time"
)

func TestBroker_AvailableCash(t *testing.T) {
	b := &Broker{
		BrokerID:     "b1",
		CashBalance:  100000, // $1000.00
		ReservedCash: 30000,  // $300.00
		Holdings:     make(map[string]*Holding),
		CreatedAt:    time.Now(),
	}
	if got := b.AvailableCash(); got != 70000 {
		t.Errorf("AvailableCash() = %d, want 70000", got)
	}
}

func TestBroker_AvailableCash_NoReservation(t *testing.T) {
	b := &Broker{
		CashBalance:  50000,
		ReservedCash: 0,
		Holdings:     make(map[string]*Holding),
	}
	if got := b.AvailableCash(); got != 50000 {
		t.Errorf("AvailableCash() = %d, want 50000", got)
	}
}

func TestBroker_AvailableQuantity(t *testing.T) {
	b := &Broker{
		Holdings: map[string]*Holding{
			"AAPL": {Quantity: 500, ReservedQuantity: 200},
			"GOOG": {Quantity: 100, ReservedQuantity: 0},
		},
	}

	if got := b.AvailableQuantity("AAPL"); got != 300 {
		t.Errorf("AvailableQuantity(AAPL) = %d, want 300", got)
	}
	if got := b.AvailableQuantity("GOOG"); got != 100 {
		t.Errorf("AvailableQuantity(GOOG) = %d, want 100", got)
	}
}

func TestBroker_AvailableQuantity_NoHolding(t *testing.T) {
	b := &Broker{
		Holdings: make(map[string]*Holding),
	}
	if got := b.AvailableQuantity("MSFT"); got != 0 {
		t.Errorf("AvailableQuantity(MSFT) = %d, want 0", got)
	}
}
