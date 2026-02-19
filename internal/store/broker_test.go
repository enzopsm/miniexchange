package store

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
)

func newTestBroker(id string) *domain.Broker {
	return &domain.Broker{
		BrokerID:    id,
		CashBalance: 100000, // $1000.00
		Holdings:    make(map[string]*domain.Holding),
		CreatedAt:   time.Now(),
	}
}

func TestBrokerStore_Create(t *testing.T) {
	s := NewBrokerStore()
	b := newTestBroker("broker-1")

	if err := s.Create(b); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Duplicate should fail.
	if err := s.Create(b); err != domain.ErrBrokerAlreadyExists {
		t.Fatalf("expected ErrBrokerAlreadyExists, got %v", err)
	}
}

func TestBrokerStore_Get(t *testing.T) {
	s := NewBrokerStore()
	b := newTestBroker("broker-1")
	_ = s.Create(b)

	got, err := s.Get("broker-1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.BrokerID != "broker-1" {
		t.Fatalf("expected broker-1, got %s", got.BrokerID)
	}
	if got.CashBalance != 100000 {
		t.Fatalf("expected cash 100000, got %d", got.CashBalance)
	}

	// Non-existent broker.
	_, err = s.Get("no-such-broker")
	if err != domain.ErrBrokerNotFound {
		t.Fatalf("expected ErrBrokerNotFound, got %v", err)
	}
}

func TestBrokerStore_Exists(t *testing.T) {
	s := NewBrokerStore()
	b := newTestBroker("broker-1")
	_ = s.Create(b)

	if !s.Exists("broker-1") {
		t.Fatal("expected broker-1 to exist")
	}
	if s.Exists("no-such-broker") {
		t.Fatal("expected no-such-broker to not exist")
	}
}

func TestBrokerStore_ConcurrentAccess(t *testing.T) {
	s := NewBrokerStore()
	var wg sync.WaitGroup

	// Concurrently create distinct brokers.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_ = s.Create(newTestBroker(id))
		}(fmt.Sprintf("broker-%d", i))
	}
	wg.Wait()

	// All 100 should exist.
	for i := 0; i < 100; i++ {
		if !s.Exists(fmt.Sprintf("broker-%d", i)) {
			t.Fatalf("broker-%d should exist", i)
		}
	}

	// Concurrent reads while creating more brokers.
	for i := 100; i < 200; i++ {
		wg.Add(2)
		go func(id string) {
			defer wg.Done()
			_ = s.Create(newTestBroker(id))
		}(fmt.Sprintf("broker-%d", i))
		go func(id string) {
			defer wg.Done()
			s.Exists(id)
		}(fmt.Sprintf("broker-%d", i-100))
	}
	wg.Wait()
}
