package domain

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

var currencyExponents = map[string]int{
	"EUR": 2,
	"GBP": 2,
	"JPY": 0,
	"USD": 2,
}

type Money struct {
	Minor    int64
	Currency string
}

func NewMoney(minor int64, currency string) (Money, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if _, ok := currencyExponents[currency]; !ok {
		return Money{}, ErrInvalidCurrency
	}
	if minor <= 0 {
		return Money{}, ErrInvalidAmount
	}
	return Money{Minor: minor, Currency: currency}, nil
}

func NormalizeCurrency(currency string) (string, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if _, ok := currencyExponents[currency]; !ok {
		return "", ErrInvalidCurrency
	}
	return currency, nil
}

func ParseMoney(amount, currency string) (Money, error) {
	currency, err := NormalizeCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	amount = strings.TrimSpace(amount)
	if amount == "" || strings.HasPrefix(amount, "-") || strings.HasPrefix(amount, "+") {
		return Money{}, ErrInvalidAmount
	}

	parts := strings.Split(amount, ".")
	if len(parts) > 2 || parts[0] == "" {
		return Money{}, ErrInvalidAmount
	}
	exponent := currencyExponents[currency]
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" {
			return Money{}, ErrInvalidAmount
		}
	}
	if len(fraction) > exponent || (exponent == 0 && len(parts) == 2) {
		return Money{}, ErrInvalidAmount
	}
	if !digitsOnly(parts[0]) || !digitsOnly(fraction) {
		return Money{}, ErrInvalidAmount
	}
	fraction += strings.Repeat("0", exponent-len(fraction))
	combined := strings.TrimLeft(parts[0]+fraction, "0")
	if combined == "" {
		return Money{}, ErrInvalidAmount
	}
	minor, err := strconv.ParseInt(combined, 10, 64)
	if err != nil || minor <= 0 {
		return Money{}, ErrAmountOverflow
	}
	return Money{Minor: minor, Currency: currency}, nil
}

func FormatMinor(minor int64, currency string) string {
	exponent := currencyExponents[currency]
	if exponent == 0 {
		return strconv.FormatInt(minor, 10)
	}
	negative := minor < 0
	if negative {
		minor = -minor
	}
	digits := strconv.FormatInt(minor, 10)
	if len(digits) <= exponent {
		digits = strings.Repeat("0", exponent-len(digits)+1) + digits
	}
	formatted := digits[:len(digits)-exponent] + "." + digits[len(digits)-exponent:]
	if negative {
		return "-" + formatted
	}
	return formatted
}

func AddMinor(balance, amount int64) (int64, error) {
	if amount <= 0 {
		return 0, ErrInvalidAmount
	}
	if balance > math.MaxInt64-amount {
		return 0, ErrAmountOverflow
	}
	return balance + amount, nil
}

func digitsOnly(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (m Money) String() string {
	return fmt.Sprintf("%s %s", FormatMinor(m.Minor, m.Currency), m.Currency)
}
