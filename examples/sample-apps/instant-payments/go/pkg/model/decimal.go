package model

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// DecimalString normalizes a JSON decimal without binary floating-point rounding.
// DynamoDB's numeric exponent range bounds expansion of scientific notation.
func DecimalString(value string) (string, error) {
	if !json.Valid([]byte(value)) || value == "" || strings.TrimSpace(value) != value || (value[0] != '-' && (value[0] < '0' || value[0] > '9')) {
		return "", fmt.Errorf("invalid decimal")
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	mantissa, exponentText, scientific := strings.Cut(strings.ToLower(value), "e")
	exponent := 0
	if scientific {
		var err error
		exponent, err = strconv.Atoi(exponentText)
		if err != nil || exponent < -decimalExpansionLimit || exponent > decimalExpansionLimit {
			return "", fmt.Errorf("decimal exponent exceeds supported range")
		}
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := whole + fraction
	point := len(whole) + exponent
	if point <= 0 {
		value = "0." + strings.Repeat("0", -point) + digits
	} else if point >= len(digits) {
		value = digits + strings.Repeat("0", point-len(digits))
	} else {
		value = digits[:point] + "." + digits[point:]
	}
	whole, fraction, _ = strings.Cut(value, ".")
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	fraction = strings.TrimRight(fraction, "0")
	value = whole
	if fraction != "" {
		value += "." + fraction
	}
	if negative && value != "0" {
		value = "-" + value
	}
	return value, nil
}

// decimalExpansionLimit exceeds DynamoDB's exponent range while bounding allocation.
const decimalExpansionLimit = 400

// AmountDecimal uses a preserved decimal when present, otherwise the shortest
// decimal representation of a programmatically supplied floating-point amount.
func AmountDecimal(amount float64, exact string) (string, error) {
	if exact == "" {
		exact = strconv.FormatFloat(amount, 'f', -1, 64)
	}
	return DecimalString(exact)
}

// DecimalJSON preserves stored precision as a JSON number. Exactly representable
// decimal encodings retain the existing float64 result for Go callers.
func DecimalJSON(value float64, exact string) (any, error) {
	normalized, err := AmountDecimal(value, exact)
	if err != nil {
		return nil, err
	}
	approximate, err := AmountDecimal(value, "")
	if err != nil {
		return nil, err
	}
	if normalized == approximate {
		return value, nil
	}
	return json.Number(normalized), nil
}
