//go:build integration

// This suite verifies real GSI pagination across timestamp precision boundaries
// and equal-timestamp payment-ID ties in both query directions.
package service

import (
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"

	"instantpayments/pkg/model"
)

func TestDynamoDBMerchantTimestampBoundaryPagination(t *testing.T) {
	ctx, _, repository, _ := dynamoDBIntegrationRepository(t)
	timestamps := []string{
		"2026-01-01T00:00:00Z",
		"2026-01-01T00:00:00.001Z",
		"2026-01-01T00:00:00.001001Z",
		"2026-01-01T01:00:00.001001001+01:00",
		"2026-01-01T00:00:01Z",
	}
	for index := len(timestamps) - 1; index >= 0; index-- {
		instant, err := time.Parse(time.RFC3339Nano, timestamps[index])
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.PutPayment(ctx, &model.Payment{
			PaymentKeys: model.PaymentKeys{PK: model.PaymentKey(fmt.Sprintf("pay_%d", index)), SK: model.PaymentHeadSK},
			MerchantID:  "boundary-merchant", Status: "RECEIVED", LastSequence: 1,
			CreatedAt: instant, UpdatedAt: instant,
		}); err != nil {
			t.Fatal(err)
		}
	}
	payment := NewPayment(zap.NewNop(), repository)
	for _, state := range []string{"", "RECEIVED"} {
		for {
			rows, _, err := repository.ListPaymentPage(ctx, "boundary-merchant", state, 100, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) == len(timestamps) {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(20 * time.Millisecond):
			}
		}
		for _, forward := range []bool{true, false} {
			t.Run(fmt.Sprintf("state=%s/forward=%t", state, forward), func(t *testing.T) {
				scope := "merchant"
				if state != "" {
					scope = "merchant-state"
				}
				token := ""
				for index := range timestamps {
					page, err := payment.List("boundary-merchant", state, scope, token, 1, forward)
					if err != nil {
						t.Fatal(err)
					}
					wantIndex := index
					if !forward {
						wantIndex = len(timestamps) - 1 - index
					}
					items := page["items"].([]any)
					if len(items) != 1 || items[0].(map[string]any)["paymentId"] != fmt.Sprintf("pay_%d", wantIndex) {
						t.Fatalf("timestamp-boundary page %d: %#v", index, page)
					}
					token, _ = page["nextToken"].(string)
					if index < len(timestamps)-1 && token == "" {
						t.Fatal("missing timestamp-boundary continuation token")
					}
				}
				if token != "" {
					page, err := payment.List("boundary-merchant", state, scope, token, 1, forward)
					if err != nil {
						t.Fatal(err)
					}
					if len(page["items"].([]any)) != 0 || page["nextToken"] != nil {
						t.Fatalf("pagination did not terminate after the final payment: %#v", page)
					}
				}
			})
		}
	}
}

// Equal timestamps must still page deterministically by the second RANGE key.
// This uses actual Local indexes, not a transport fixture that merely echoes keys.
func TestDynamoDBMerchantPaymentIDTieBreaker(t *testing.T) {
	ctx, _, repository, _ := dynamoDBIntegrationRepository(t)
	instant := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"pay_c", "pay_a", "pay_b"} {
		if err := repository.PutPayment(ctx, &model.Payment{
			PaymentKeys: model.PaymentKeys{PK: model.PaymentKey(id), SK: model.PaymentHeadSK},
			MerchantID:  "tie-merchant", Status: "RECEIVED", LastSequence: 1,
			CreatedAt: instant, UpdatedAt: instant,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for {
		rows, _, err := repository.ListPaymentPage(ctx, "tie-merchant", "", 100, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 3 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	payment := NewPayment(zap.NewNop(), repository)
	for _, forward := range []bool{true, false} {
		t.Run(fmt.Sprintf("forward=%t", forward), func(t *testing.T) {
			token := ""
			for index := 0; index < 3; index++ {
				page, err := payment.List("tie-merchant", "", "merchant", token, 1, forward)
				if err != nil {
					t.Fatal(err)
				}
				letter := 'a' + rune(index)
				if !forward {
					letter = 'c' - rune(index)
				}
				items := page["items"].([]any)
				if len(items) != 1 || items[0].(map[string]any)["paymentId"] != fmt.Sprintf("pay_%c", letter) {
					t.Fatalf("equal-timestamp page %d: %#v", index, page)
				}
				token, _ = page["nextToken"].(string)
				if index < 2 && token == "" {
					t.Fatal("missing multi-key continuation token")
				}
			}
		})
	}
}
