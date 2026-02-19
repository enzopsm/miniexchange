package domain

import (
	"math"
	"testing"

	"pgregory.net/rapid"
)

// Feature: mini-stock-exchange, Property 24: Monetary value round-trip
// Validates: Requirements 1.5, 17.2

func TestProperty_MonetaryRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a cent value in a reasonable monetary range.
		// This ensures the float64 representation has at most 2 decimal places.
		cents := rapid.Int64Range(-99_999_999_99, 99_999_999_99).Draw(t, "cents")

		// Convert cents → dollars → cents. This must round-trip exactly.
		dollars := CentsToDollars(cents)
		gotCents, err := DollarsToCents(dollars)
		if err != nil {
			t.Fatalf("DollarsToCents(%v) returned error for value derived from %d cents: %v", dollars, cents, err)
		}
		if gotCents != cents {
			t.Fatalf("round-trip failed: cents=%d → dollars=%v → cents=%d", cents, dollars, gotCents)
		}
	})
}

func TestProperty_DollarsToCentsRejectsExcessPrecision(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a value that has a meaningful third decimal place.
		// We build it as: whole.XX_Y where Y ∈ [1..9] (the offending digit).
		whole := rapid.Int64Range(-999_999, 999_999).Draw(t, "whole")
		d1 := rapid.IntRange(0, 9).Draw(t, "d1")
		d2 := rapid.IntRange(0, 9).Draw(t, "d2")
		d3 := rapid.IntRange(1, 9).Draw(t, "d3") // must be non-zero

		// Construct the float: whole + 0.XY_Z
		sign := 1.0
		absWhole := whole
		if whole < 0 {
			sign = -1.0
			absWhole = -whole
		}
		f := sign * (float64(absWhole) + float64(d1)*0.1 + float64(d2)*0.01 + float64(d3)*0.001)

		// Verify the third decimal digit is actually non-zero after float representation.
		// Due to floating-point, some constructed values may lose the third digit.
		scaled := math.Round(f * 1000)
		if math.Mod(math.Abs(scaled), 10) == 0 {
			t.Skip("floating-point collapsed the third decimal digit")
		}

		_, err := DollarsToCents(f)
		if err == nil {
			t.Fatalf("DollarsToCents(%v) should reject value with >2 decimal places", f)
		}
	})
}
