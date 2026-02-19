package domain

import (
	"sync"
	"testing"
)

func TestSymbolRegistry_RegisterAndExists(t *testing.T) {
	r := NewSymbolRegistry()

	if r.Exists("AAPL") {
		t.Error("Exists(AAPL) = true before registration")
	}

	r.Register("AAPL")

	if !r.Exists("AAPL") {
		t.Error("Exists(AAPL) = false after registration")
	}
	if r.Exists("GOOG") {
		t.Error("Exists(GOOG) = true, should be false")
	}
}

func TestSymbolRegistry_ConcurrentAccess(t *testing.T) {
	r := NewSymbolRegistry()
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(sym string) {
			defer wg.Done()
			r.Register(sym)
		}("SYM")
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Exists("SYM")
		}()
	}

	wg.Wait()

	if !r.Exists("SYM") {
		t.Error("Exists(SYM) = false after concurrent registration")
	}
}
