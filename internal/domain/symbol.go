package domain

import "sync"

// SymbolRegistry tracks known stock symbols in a thread-safe manner.
// Symbols are implicitly registered when they appear in any order
// submission or in a broker's initial_holdings.
type SymbolRegistry struct {
	mu      sync.RWMutex
	symbols map[string]bool
}

// NewSymbolRegistry creates an empty SymbolRegistry.
func NewSymbolRegistry() *SymbolRegistry {
	return &SymbolRegistry{
		symbols: make(map[string]bool),
	}
}

// Register adds a symbol to the registry. Safe for concurrent use.
func (r *SymbolRegistry) Register(symbol string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.symbols[symbol] = true
}

// Exists returns true if the symbol has been registered. Safe for concurrent use.
func (r *SymbolRegistry) Exists(symbol string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.symbols[symbol]
}
