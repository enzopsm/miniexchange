package service

import (
	"testing"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/store"
)

func newTestBrokerService() *BrokerService {
	return NewBrokerService(store.NewBrokerStore(), domain.NewSymbolRegistry())
}

func TestRegister_Success_CashOnly(t *testing.T) {
	svc := newTestBrokerService()

	broker, err := svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-1",
		InitialCash: 1000.50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if broker.BrokerID != "broker-1" {
		t.Errorf("got broker_id %q, want %q", broker.BrokerID, "broker-1")
	}
	if broker.CashBalance != 100050 {
		t.Errorf("got cash_balance %d, want %d", broker.CashBalance, 100050)
	}
	if len(broker.Holdings) != 0 {
		t.Errorf("got %d holdings, want 0", len(broker.Holdings))
	}
}

func TestRegister_Success_WithHoldings(t *testing.T) {
	svc := newTestBrokerService()

	broker, err := svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-2",
		InitialCash: 500000.00,
		InitialHoldings: []HoldingInput{
			{Symbol: "AAPL", Quantity: 100},
			{Symbol: "GOOG", Quantity: 50},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if broker.CashBalance != 50000000 {
		t.Errorf("got cash_balance %d, want %d", broker.CashBalance, 50000000)
	}
	if len(broker.Holdings) != 2 {
		t.Errorf("got %d holdings, want 2", len(broker.Holdings))
	}
	if broker.Holdings["AAPL"].Quantity != 100 {
		t.Errorf("got AAPL quantity %d, want 100", broker.Holdings["AAPL"].Quantity)
	}
	if broker.Holdings["GOOG"].Quantity != 50 {
		t.Errorf("got GOOG quantity %d, want 50", broker.Holdings["GOOG"].Quantity)
	}
}

func TestRegister_Success_ZeroCash(t *testing.T) {
	svc := newTestBrokerService()

	broker, err := svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-zero",
		InitialCash: 0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if broker.CashBalance != 0 {
		t.Errorf("got cash_balance %d, want 0", broker.CashBalance)
	}
}

func TestRegister_Success_SymbolsRegistered(t *testing.T) {
	bs := store.NewBrokerStore()
	sr := domain.NewSymbolRegistry()
	svc := NewBrokerService(bs, sr)

	_, err := svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-sym",
		InitialCash: 100.00,
		InitialHoldings: []HoldingInput{
			{Symbol: "TSLA", Quantity: 10},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sr.Exists("TSLA") {
		t.Error("expected TSLA to be registered in symbol registry")
	}
	if sr.Exists("AAPL") {
		t.Error("expected AAPL to NOT be registered in symbol registry")
	}
}

func TestRegister_DuplicateBroker(t *testing.T) {
	svc := newTestBrokerService()

	_, err := svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-dup",
		InitialCash: 100.00,
	})
	if err != nil {
		t.Fatalf("unexpected error on first register: %v", err)
	}

	_, err = svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-dup",
		InitialCash: 200.00,
	})
	if err != domain.ErrBrokerAlreadyExists {
		t.Errorf("got error %v, want ErrBrokerAlreadyExists", err)
	}
}

func TestRegister_InvalidBrokerID(t *testing.T) {
	tests := []struct {
		name     string
		brokerID string
	}{
		{"empty", ""},
		{"spaces", "broker 1"},
		{"special chars", "broker@1"},
		{"too long", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, // 65 chars
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestBrokerService()
			_, err := svc.Register(RegisterBrokerRequest{
				BrokerID:    tt.brokerID,
				InitialCash: 100.00,
			})
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if _, ok := err.(*domain.ValidationError); !ok {
				t.Errorf("expected *ValidationError, got %T: %v", err, err)
			}
		})
	}
}

func TestRegister_NegativeCash(t *testing.T) {
	svc := newTestBrokerService()

	_, err := svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-neg",
		InitialCash: -1.00,
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestRegister_CashTooManyDecimals(t *testing.T) {
	svc := newTestBrokerService()

	_, err := svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-dec",
		InitialCash: 100.123,
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestRegister_InvalidSymbol(t *testing.T) {
	tests := []struct {
		name   string
		symbol string
	}{
		{"lowercase", "aapl"},
		{"too long", "AAAAAAAAAAAA"}, // 12 chars
		{"empty", ""},
		{"numbers", "AAPL1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestBrokerService()
			_, err := svc.Register(RegisterBrokerRequest{
				BrokerID:    "broker-sym",
				InitialCash: 100.00,
				InitialHoldings: []HoldingInput{
					{Symbol: tt.symbol, Quantity: 10},
				},
			})
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if _, ok := err.(*domain.ValidationError); !ok {
				t.Errorf("expected *ValidationError, got %T: %v", err, err)
			}
		})
	}
}

func TestRegister_ZeroQuantity(t *testing.T) {
	svc := newTestBrokerService()

	_, err := svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-zq",
		InitialCash: 100.00,
		InitialHoldings: []HoldingInput{
			{Symbol: "AAPL", Quantity: 0},
		},
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestRegister_NegativeQuantity(t *testing.T) {
	svc := newTestBrokerService()

	_, err := svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-nq",
		InitialCash: 100.00,
		InitialHoldings: []HoldingInput{
			{Symbol: "AAPL", Quantity: -5},
		},
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestRegister_DuplicateSymbol(t *testing.T) {
	svc := newTestBrokerService()

	_, err := svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-ds",
		InitialCash: 100.00,
		InitialHoldings: []HoldingInput{
			{Symbol: "AAPL", Quantity: 10},
			{Symbol: "AAPL", Quantity: 20},
		},
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := err.(*domain.ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestGetBalance_Success(t *testing.T) {
	svc := newTestBrokerService()

	_, err := svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-bal",
		InitialCash: 1000.00,
		InitialHoldings: []HoldingInput{
			{Symbol: "AAPL", Quantity: 100},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bal, err := svc.GetBalance("broker-bal")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bal.BrokerID != "broker-bal" {
		t.Errorf("got broker_id %q, want %q", bal.BrokerID, "broker-bal")
	}
	if bal.CashBalance != 100000 {
		t.Errorf("got cash_balance %d, want %d", bal.CashBalance, 100000)
	}
	if bal.ReservedCash != 0 {
		t.Errorf("got reserved_cash %d, want 0", bal.ReservedCash)
	}
	if bal.AvailableCash != 100000 {
		t.Errorf("got available_cash %d, want %d", bal.AvailableCash, 100000)
	}
	if len(bal.Holdings) != 1 {
		t.Fatalf("got %d holdings, want 1", len(bal.Holdings))
	}
	h := bal.Holdings[0]
	if h.Symbol != "AAPL" {
		t.Errorf("got symbol %q, want %q", h.Symbol, "AAPL")
	}
	if h.Quantity != 100 {
		t.Errorf("got quantity %d, want 100", h.Quantity)
	}
	if h.ReservedQuantity != 0 {
		t.Errorf("got reserved_quantity %d, want 0", h.ReservedQuantity)
	}
	if h.AvailableQuantity != 100 {
		t.Errorf("got available_quantity %d, want 100", h.AvailableQuantity)
	}
}

func TestGetBalance_BrokerNotFound(t *testing.T) {
	svc := newTestBrokerService()

	_, err := svc.GetBalance("nonexistent")
	if err != domain.ErrBrokerNotFound {
		t.Errorf("got error %v, want ErrBrokerNotFound", err)
	}
}

func TestGetBalance_EmptyHoldings(t *testing.T) {
	svc := newTestBrokerService()

	_, err := svc.Register(RegisterBrokerRequest{
		BrokerID:    "broker-empty",
		InitialCash: 500.00,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bal, err := svc.GetBalance("broker-empty")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bal.Holdings) != 0 {
		t.Errorf("got %d holdings, want 0", len(bal.Holdings))
	}
	if bal.CashBalance != 50000 {
		t.Errorf("got cash_balance %d, want %d", bal.CashBalance, 50000)
	}
}
