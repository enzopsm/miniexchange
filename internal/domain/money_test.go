package domain

import (
	"math"
	"testing"
)

func TestDollarsToCents(t *testing.T) {
	tests := []struct {
		name    string
		input   float64
		want    int64
		wantErr bool
	}{
		{"zero", 0.0, 0, false},
		{"whole dollars", 100.0, 10000, false},
		{"one decimal place", 1.5, 150, false},
		{"two decimal places", 148.50, 14850, false},
		{"small amount", 0.01, 1, false},
		{"large amount", 1000000.00, 100000000, false},
		{"negative value", -50.25, -5025, false},
		{"three decimal places", 1.234, 0, true},
		{"many decimal places", 0.001, 0, true},
		{"trailing precision issue 0.10", 0.10, 10, false},
		{"trailing precision issue 0.20", 0.20, 20, false},
		{"1.10 precision", 1.10, 110, false},
		{"99.99", 99.99, 9999, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DollarsToCents(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("DollarsToCents(%v) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("DollarsToCents(%v) unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("DollarsToCents(%v) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCentsToDollars(t *testing.T) {
	tests := []struct {
		name  string
		input int64
		want  float64
	}{
		{"zero", 0, 0.0},
		{"one cent", 1, 0.01},
		{"one dollar", 100, 1.0},
		{"typical amount", 14850, 148.50},
		{"large amount", 100000000, 1000000.00},
		{"negative", -5025, -50.25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CentsToDollars(tt.input)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("CentsToDollars(%d) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
