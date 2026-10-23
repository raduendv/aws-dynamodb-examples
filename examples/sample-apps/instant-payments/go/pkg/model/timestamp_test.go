// This suite checks grouped UTC timestamp formatting and independent index-projection slices.
package model

import (
	"testing"
	"time"
)

func TestFormatTimestamp(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"},
		{"2026-01-01T00:00:00.1Z", "2026-01-01T00:00:00.100Z"},
		{"2026-01-01T00:00:00.123Z", "2026-01-01T00:00:00.123Z"},
		{"2026-01-01T00:00:00.1234Z", "2026-01-01T00:00:00.123400Z"},
		{"2026-01-01T00:00:00.123456Z", "2026-01-01T00:00:00.123456Z"},
		{"2026-01-01T00:00:00.1234567Z", "2026-01-01T00:00:00.123456700Z"},
		{"2026-01-01T01:00:00.123456789+01:00", "2026-01-01T00:00:00.123456789Z"},
	} {
		value, err := time.Parse(time.RFC3339Nano, test.input)
		if err != nil {
			t.Fatal(err)
		}
		if got := FormatTimestamp(value); got != test.want {
			t.Errorf("%s: got %s, want %s", test.input, got, test.want)
		}
	}
}

func TestMerchantStateProjectionReturnsIndependentSlices(t *testing.T) {
	projection := MerchantStateProjection()
	projection[0] = "changed"
	if MerchantStateProjection()[0] != "paymentId" {
		t.Fatal("caller mutated the shared projection contract")
	}
}
