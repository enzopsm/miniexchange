package domain

import (
	"fmt"
	"math"
)

// DollarsToCents converts a float64 dollar amount to int64 cents.
// It validates that the input has at most 2 decimal places and returns
// an error if more precision is provided. Uses math.Round after
// multiplying by 100 to handle floating-point representation issues.
func DollarsToCents(f float64) (int64, error) {
	// Multiply by 1000 to check for a third decimal place.
	// Round to avoid floating-point artifacts (e.g., 1.10 * 1000 = 1099.9999...).
	scaled := math.Round(f * 1000)
	if math.Mod(scaled, 10) != 0 {
		return 0, fmt.Errorf("monetary values must have at most 2 decimal places")
	}

	cents := math.Round(f * 100)
	return int64(cents), nil
}

// CentsToDollars converts an int64 cents value to a float64 dollar amount.
func CentsToDollars(c int64) float64 {
	return float64(c) / 100.0
}
