package store

import (
	"sync"

	"github.com/efreitasn/miniexchange/internal/domain"
)

// TradeStore is a thread-safe in-memory store for trades,
// keyed by symbol. Trades are append-only and chronological.
type TradeStore struct {
	mu     sync.RWMutex
	trades map[string][]*domain.Trade // symbol → trades (chronological)
}

// NewTradeStore creates an empty TradeStore.
func NewTradeStore() *TradeStore {
	return &TradeStore{
		trades: make(map[string][]*domain.Trade),
	}
}

// Append adds a trade to the symbol's chronological list.
func (s *TradeStore) Append(symbol string, t *domain.Trade) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.trades[symbol] = append(s.trades[symbol], t)
}

// GetBySymbol returns all trades for a symbol in chronological order.
// Returns an empty slice if no trades exist for the symbol.
func (s *TradeStore) GetBySymbol(symbol string) []*domain.Trade {
	s.mu.RLock()
	defer s.mu.RUnlock()

	trades := s.trades[symbol]
	if trades == nil {
		return []*domain.Trade{}
	}

	// Return a copy to avoid callers mutating the internal slice.
	result := make([]*domain.Trade, len(trades))
	copy(result, trades)
	return result
}
