package domain

import (
	"errors"
	"testing"
)

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{Message: "initial_cash must be >= 0"}
	if err.Error() != "initial_cash must be >= 0" {
		t.Errorf("Error() = %q, want %q", err.Error(), "initial_cash must be >= 0")
	}
}

func TestValidationError_ImplementsError(t *testing.T) {
	var err error = &ValidationError{Message: "test"}
	if err == nil {
		t.Error("ValidationError should implement error interface")
	}
}

func TestSentinelErrors_AreDistinct(t *testing.T) {
	errs := []error{
		ErrBrokerAlreadyExists,
		ErrBrokerNotFound,
		ErrOrderNotFound,
		ErrOrderNotCancellable,
		ErrInsufficientBalance,
		ErrInsufficientHoldings,
		ErrNoLiquidity,
		ErrSymbolNotFound,
		ErrWebhookNotFound,
	}
	for i := 0; i < len(errs); i++ {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Errorf("sentinel errors %d and %d should be distinct", i, j)
			}
		}
	}
}
