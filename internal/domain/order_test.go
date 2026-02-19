package domain

import (
	"testing"
	"time"
)

func TestOrder_AveragePrice_SingleTrade(t *testing.T) {
	o := &Order{
		FilledQuantity: 100,
		Trades: []*Trade{
			{Price: 15000, Quantity: 100, ExecutedAt: time.Now()},
		},
	}
	avg, ok := o.AveragePrice()
	if !ok {
		t.Fatal("AveragePrice() returned false, want true")
	}
	if avg != 15000 {
		t.Errorf("AveragePrice() = %d, want 15000", avg)
	}
}

func TestOrder_AveragePrice_MultipleTrades(t *testing.T) {
	// 700 @ 14800 + 300 @ 14900 = 10360000 + 4470000 = 14830000 / 1000 = 14830
	o := &Order{
		FilledQuantity: 1000,
		Trades: []*Trade{
			{Price: 14800, Quantity: 700, ExecutedAt: time.Now()},
			{Price: 14900, Quantity: 300, ExecutedAt: time.Now()},
		},
	}
	avg, ok := o.AveragePrice()
	if !ok {
		t.Fatal("AveragePrice() returned false, want true")
	}
	if avg != 14830 {
		t.Errorf("AveragePrice() = %d, want 14830", avg)
	}
}

func TestOrder_AveragePrice_NoTrades(t *testing.T) {
	o := &Order{
		FilledQuantity: 0,
		Trades:         []*Trade{},
	}
	_, ok := o.AveragePrice()
	if ok {
		t.Error("AveragePrice() returned true, want false for no trades")
	}
}

func TestOrder_AveragePrice_NilTrades(t *testing.T) {
	o := &Order{
		FilledQuantity: 0,
		Trades:         nil,
	}
	_, ok := o.AveragePrice()
	if ok {
		t.Error("AveragePrice() returned true, want false for nil trades")
	}
}
