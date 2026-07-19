package domain_test

import (
	"errors"
	"math"
	"testing"

	"walletservice/internal/domain"
)

func TestParseMoney(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		amount   string
		currency string
		minor    int64
		wantErr  error
	}{
		{name: "whole dollars", amount: "12", currency: "USD", minor: 1200},
		{name: "cents", amount: "12.34", currency: "usd", minor: 1234},
		{name: "single decimal", amount: "12.3", currency: "EUR", minor: 1230},
		{name: "zero exponent", amount: "12", currency: "JPY", minor: 12},
		{name: "zero", amount: "0.00", currency: "USD", wantErr: domain.ErrInvalidAmount},
		{name: "negative", amount: "-1.00", currency: "USD", wantErr: domain.ErrInvalidAmount},
		{name: "too precise", amount: "1.001", currency: "USD", wantErr: domain.ErrInvalidAmount},
		{name: "empty fraction", amount: "1.", currency: "USD", wantErr: domain.ErrInvalidAmount},
		{name: "fractional yen", amount: "1.0", currency: "JPY", wantErr: domain.ErrInvalidAmount},
		{name: "unsupported currency", amount: "1.00", currency: "BTC", wantErr: domain.ErrInvalidCurrency},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			money, err := domain.ParseMoney(test.amount, test.currency)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("expected %v, got %v", test.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if money.Minor != test.minor {
				t.Fatalf("expected %d minor units, got %d", test.minor, money.Minor)
			}
		})
	}
}

func TestAddMinorRejectsOverflow(t *testing.T) {
	t.Parallel()
	_, err := domain.AddMinor(math.MaxInt64-5, 10)
	if !errors.Is(err, domain.ErrAmountOverflow) {
		t.Fatalf("expected amount overflow, got %v", err)
	}
}

func TestFormatMinor(t *testing.T) {
	t.Parallel()
	if got := domain.FormatMinor(5, "USD"); got != "0.05" {
		t.Fatalf("expected 0.05, got %s", got)
	}
	if got := domain.FormatMinor(1200, "USD"); got != "12.00" {
		t.Fatalf("expected 12.00, got %s", got)
	}
}

func TestNewMoneyValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		minor    int64
		currency string
		want     domain.Money
		wantErr  error
	}{
		{name: "normalizes currency", minor: 1, currency: " usd ", want: domain.Money{Minor: 1, Currency: "USD"}},
		{name: "zero", minor: 0, currency: "USD", wantErr: domain.ErrInvalidAmount},
		{name: "negative", minor: -1, currency: "USD", wantErr: domain.ErrInvalidAmount},
		{name: "unsupported currency", minor: 1, currency: "CAD", wantErr: domain.ErrInvalidCurrency},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			money, err := domain.NewMoney(test.minor, test.currency)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected error %v, got %v", test.wantErr, err)
			}
			if test.wantErr == nil && money != test.want {
				t.Fatalf("expected %+v, got %+v", test.want, money)
			}
		})
	}
}

func TestParseMoneyRejectsOutOfRangeValue(t *testing.T) {
	t.Parallel()
	_, err := domain.ParseMoney("999999999999999999999999999.99", "USD")
	if !errors.Is(err, domain.ErrAmountOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
}
