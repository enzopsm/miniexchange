package service

import (
	"fmt"
	"regexp"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/store"
)

var (
	brokerIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	symbolRegex   = regexp.MustCompile(`^[A-Z]{1,10}$`)
)

// RegisterBrokerRequest represents the input for broker registration.
type RegisterBrokerRequest struct {
	BrokerID        string
	InitialCash     float64
	InitialHoldings []HoldingInput
}

// HoldingInput represents a single holding in a registration request.
type HoldingInput struct {
	Symbol   string
	Quantity int64
}

// BalanceResponse represents the response for the broker balance endpoint.
type BalanceResponse struct {
	BrokerID      string
	CashBalance   int64
	ReservedCash  int64
	AvailableCash int64
	Holdings      []HoldingBalance
	UpdatedAt     time.Time
}

// HoldingBalance represents a single holding in the balance response.
type HoldingBalance struct {
	Symbol            string
	Quantity          int64
	ReservedQuantity  int64
	AvailableQuantity int64
}

// BrokerService handles broker registration and balance queries.
type BrokerService struct {
	store   *store.BrokerStore
	symbols *domain.SymbolRegistry
}

// NewBrokerService creates a new BrokerService.
func NewBrokerService(store *store.BrokerStore, symbols *domain.SymbolRegistry) *BrokerService {
	return &BrokerService{
		store:   store,
		symbols: symbols,
	}
}

// Register validates the request, creates a broker, and registers symbols.
func (s *BrokerService) Register(req RegisterBrokerRequest) (*domain.Broker, error) {
	// Validate broker_id
	if !brokerIDRegex.MatchString(req.BrokerID) {
		return nil, &domain.ValidationError{
			Message: "broker_id must match ^[a-zA-Z0-9_-]{1,64}$",
		}
	}

	// Validate initial_cash >= 0
	if req.InitialCash < 0 {
		return nil, &domain.ValidationError{
			Message: "initial_cash must be >= 0",
		}
	}

	// Convert initial_cash to cents (validates <= 2 decimal places)
	cashCents, err := domain.DollarsToCents(req.InitialCash)
	if err != nil {
		return nil, &domain.ValidationError{
			Message: "initial_cash must have at most 2 decimal places",
		}
	}

	// Validate holdings
	seen := make(map[string]bool)
	for _, h := range req.InitialHoldings {
		if !symbolRegex.MatchString(h.Symbol) {
			return nil, &domain.ValidationError{
				Message: fmt.Sprintf("holding symbol must match ^[A-Z]{1,10}$, got %q", h.Symbol),
			}
		}
		if h.Quantity <= 0 {
			return nil, &domain.ValidationError{
				Message: fmt.Sprintf("holding quantity must be > 0 for symbol %s", h.Symbol),
			}
		}
		if seen[h.Symbol] {
			return nil, &domain.ValidationError{
				Message: fmt.Sprintf("duplicate symbol in initial_holdings: %s", h.Symbol),
			}
		}
		seen[h.Symbol] = true
	}

	// Build holdings map
	holdings := make(map[string]*domain.Holding)
	for _, h := range req.InitialHoldings {
		holdings[h.Symbol] = &domain.Holding{
			Quantity:         h.Quantity,
			ReservedQuantity: 0,
		}
	}

	broker := &domain.Broker{
		BrokerID:     req.BrokerID,
		CashBalance:  cashCents,
		ReservedCash: 0,
		Holdings:     holdings,
		CreatedAt:    time.Now(),
	}

	// Attempt to create (returns ErrBrokerAlreadyExists if duplicate)
	if err := s.store.Create(broker); err != nil {
		return nil, err
	}

	// Register symbols from holdings
	for symbol := range holdings {
		s.symbols.Register(symbol)
	}

	return broker, nil
}

// GetBalance retrieves the broker's current balance including reservations.
func (s *BrokerService) GetBalance(brokerID string) (*BalanceResponse, error) {
	broker, err := s.store.Get(brokerID)
	if err != nil {
		return nil, err
	}

	broker.Mu.Lock()
	defer broker.Mu.Unlock()

	holdings := make([]HoldingBalance, 0, len(broker.Holdings))
	for symbol, h := range broker.Holdings {
		holdings = append(holdings, HoldingBalance{
			Symbol:            symbol,
			Quantity:          h.Quantity,
			ReservedQuantity:  h.ReservedQuantity,
			AvailableQuantity: h.Quantity - h.ReservedQuantity,
		})
	}

	return &BalanceResponse{
		BrokerID:      broker.BrokerID,
		CashBalance:   broker.CashBalance,
		ReservedCash:  broker.ReservedCash,
		AvailableCash: broker.CashBalance - broker.ReservedCash,
		Holdings:      holdings,
		UpdatedAt:     broker.CreatedAt,
	}, nil
}
