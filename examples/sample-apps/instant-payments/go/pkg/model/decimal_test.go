// This suite verifies exact decimal normalization and JSON encoding without float64 rounding.
package model

import (
	"encoding/json"
	"testing"
)

func TestDecimalNormalization(t *testing.T) {
	for input, want := range map[string]string{
		"100.00": "100", "1e2": "100", "1.2500E-2": "0.0125", "-0.00": "0",
		"0.000001000": "0.000001", "1.0000000000000001": "1.0000000000000001",
		"-12.50": "-12.5", "1e-130": "0." + stringZeros(129) + "1",
	} {
		t.Run(input, func(t *testing.T) {
			got, err := DecimalString(input)
			if err != nil || got != want {
				t.Fatalf("normalized %q: %q %v, want %q", input, got, err, want)
			}
		})
	}
	for _, input := range []string{"", "null", `"1"`, "01", "NaN", "1e999999999", "1e-999999999", "[]", "1 2"} {
		if _, err := DecimalString(input); err == nil {
			t.Fatalf("accepted invalid decimal %q", input)
		}
	}
}

// stringZeros constructs the expected exponent expansion without a huge test literal.
func stringZeros(count int) string {
	bytes := make([]byte, count)
	for index := range bytes {
		bytes[index] = '0'
	}
	return string(bytes)
}

func TestDecimalJSONPreservesStoredNumber(t *testing.T) {
	value, err := DecimalJSON(1, "1.0000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != "1.0000000000000001" {
		t.Fatalf("exact JSON: %s %v", encoded, err)
	}
	if _, err := DecimalJSON(1, "not-a-number"); err == nil {
		t.Fatal("invalid stored number must fail explicitly")
	}
}
