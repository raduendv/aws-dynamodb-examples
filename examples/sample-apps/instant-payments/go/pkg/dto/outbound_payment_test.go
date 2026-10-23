// This suite checks outbound-payment timestamp round trips and exact numeric request decoding.
package dto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOutboundAmountJSONContract(t *testing.T) {
	for _, amount := range []string{`"1"`, `true`, `{}`, `[]`} {
		var input OutboundPaymentInput
		if err := json.Unmarshal([]byte(`{"amount":`+amount+`}`), &input); err == nil {
			t.Fatalf("accepted nonnumeric amount %s", amount)
		}
	}
	for _, amount := range []string{"1.0000000000000001", "1.0000000000000002", "1.0e2"} {
		var input OutboundPaymentInput
		if err := json.Unmarshal([]byte(`{"amount":`+amount+`}`), &input); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var output map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &output); err != nil {
			t.Fatal(err)
		}
		want := amount
		if amount == "1.0e2" {
			want = "100"
		}
		if string(output["amount"]) != want {
			t.Fatalf("amount changed: %s -> %s", amount, output["amount"])
		}
	}
}

func TestOutboundPaymentTimestampRoundTrip(t *testing.T) {
	output := OutboundPaymentOutput{PaymentID: "pay_1", State: "RECEIVED", CreatedAtUTC: time.Date(2026, 1, 1, 0, 0, 0, 100_000_000, time.UTC)}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"createdAtUtc":"2026-01-01T00:00:00.100Z"`) ||
		strings.Count(string(encoded), `"createdAtUtc"`) != 1 || !strings.Contains(string(encoded), `"paymentId":"pay_1"`) {
		t.Fatalf("incorrect response %s", encoded)
	}
	var decoded OutboundPaymentOutput
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.CreatedAtUTC.Equal(output.CreatedAtUTC) || decoded.PaymentID != output.PaymentID {
		t.Fatalf("response round trip changed values: %#v", decoded)
	}
}
