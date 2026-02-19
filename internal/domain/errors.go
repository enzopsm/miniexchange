package domain

import "errors"

// Sentinel errors for domain-level error handling.
// The handler layer maps these to HTTP status codes.
var (
	ErrBrokerAlreadyExists  = errors.New("broker_already_exists")
	ErrBrokerNotFound       = errors.New("broker_not_found")
	ErrOrderNotFound        = errors.New("order_not_found")
	ErrOrderNotCancellable  = errors.New("order_not_cancellable")
	ErrInsufficientBalance  = errors.New("insufficient_balance")
	ErrInsufficientHoldings = errors.New("insufficient_holdings")
	ErrNoLiquidity          = errors.New("no_liquidity")
	ErrSymbolNotFound       = errors.New("symbol_not_found")
	ErrWebhookNotFound      = errors.New("webhook_not_found")
)

// ValidationError represents a request validation failure.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}
