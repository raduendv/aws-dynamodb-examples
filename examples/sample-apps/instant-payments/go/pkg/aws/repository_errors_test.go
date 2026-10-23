// This suite checks repository pagination inputs, missing clients, malformed keys,
// SDK/decode failures, and idempotency writes through transport fixtures.
package aws

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
	"instantpayments/pkg/model"
)

func TestListPaymentPageUsesMerchantIndexesAndContinuation(t *testing.T) {
	for _, state := range []string{"", "COMPLETED"} {
		t.Run("state="+state, func(t *testing.T) {
			repository := branchRepository(t, func(target string, input map[string]any) (int, string) {
				if target != "Query" || input["Limit"] != float64(12) || input["ScanIndexForward"] != true {
					t.Fatalf("incorrect list query: %s %#v", target, input)
				}
				index := "GSI_MERCHANT_PAYMENTS"
				if state != "" {
					index = "GSI_MERCHANT_STATE_PAYMENTS"
				}
				values := input["ExpressionAttributeValues"].(map[string]any)
				start := input["ExclusiveStartKey"].(map[string]any)
				if input["IndexName"] != index || values[":merchant"].(map[string]any)["S"] != "merchant" ||
					start["PK"].(map[string]any)["S"] != "PAYMENT#previous" {
					t.Fatalf("wrong index or continuation: %#v", input)
				}
				if state != "" && (values[":state"].(map[string]any)["S"] != state ||
					input["KeyConditionExpression"] != "#merchant = :merchant AND #state = :state") {
					t.Fatalf("state partition requires both key attributes: %#v", input)
				}
				return 200, `{"Items":[{"PK":{"S":"PAYMENT#p1"},"SK":{"S":"#HEAD"},"merchantId":{"S":"merchant"},"aggregateState":{"S":"RECEIVED"},"lastSequence":{"N":"3"}}],"LastEvaluatedKey":{"PK":{"S":"PAYMENT#next"}}}`
			})
			start := map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: "PAYMENT#previous"}}
			payments, next, err := repository.ListPaymentPage(context.Background(), "merchant", state, 12, true, start)
			if err != nil || len(payments) != 1 || payments[0].PK != "p1" || payments[0].MerchantID != "merchant" || payments[0].LastSequence != 3 {
				t.Fatalf("incorrect page: %#v, %v", payments, err)
			}
			wantState := state
			if wantState == "" {
				wantState = "RECEIVED"
			}
			if payments[0].Status != wantState || next["PK"].(*types.AttributeValueMemberS).Value != "PAYMENT#next" {
				t.Fatalf("lost state/continuation: %#v %#v", payments[0], next)
			}
		})
	}
	t.Run("list wrapper defaults", func(t *testing.T) {
		repository := branchRepository(t, func(_ string, input map[string]any) (int, string) {
			if input["Limit"] != float64(100) || input["ScanIndexForward"] != false {
				t.Fatalf("wrong wrapper defaults: %#v", input)
			}
			return 200, `{"Items":[{"PK":{"S":"PAYMENT#p1"},"SK":{"S":"#HEAD"}}]}`
		})
		payments, err := repository.ListPayments(context.Background(), "merchant", "")
		if err != nil || len(payments) != 1 || payments[0].MerchantID != "merchant" {
			t.Fatalf("unexpected list: %#v, %v", payments, err)
		}
	})
}

func TestRepositoryWithoutClientReturnsErrors(t *testing.T) {
	repository := NewRepository(zap.NewNop(), nil, &env.Env{})
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"put payment":     func() error { return repository.PutPayment(ctx, &model.Payment{}) },
		"create payment":  func() error { return repository.CreatePayment(ctx, &model.Payment{}, &model.PaymentEvent{}, nil) },
		"list payments":   func() error { _, err := repository.ListPayments(ctx, "merchant", ""); return err },
		"put idempotency": func() error { return repository.PutIdempotencyRecord(ctx, &model.IdempotencyRecord{}) },
		"get account":     func() error { _, err := repository.GetAccount(ctx, "a1", "a1"); return err },
		"put account":     func() error { return repository.PutAccount(ctx, &model.Account{}) },
		"get payment":     func() error { _, err := repository.GetPayment(ctx, "p1"); return err },
		"get idempotency": func() error { _, err := repository.GetIdempotencyRecord(ctx, "key"); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil || !strings.Contains(err.Error(), "client is not configured") {
				t.Fatalf("missing client not reported: %v", err)
			}
		})
	}
}

func TestRepositoryPropagatesAWSAndDecodeFailures(t *testing.T) {
	for _, decode := range []bool{false, true} {
		t.Run(map[bool]string{false: "AWS error", true: "decode error"}[decode], func(t *testing.T) {
			for _, operation := range []string{"account", "idempotency", "payment", "payment event", "list", "reservations", "batch"} {
				t.Run(operation, func(t *testing.T) {
					repository := branchRepository(t, func(target string, _ map[string]any) (int, string) {
						if operation == "reservations" && target == "GetItem" {
							return 200, `{"Item":` + accountForRelease + `}`
						}
						if !decode {
							return 400, `{"__type":"ResourceNotFoundException","message":"missing table"}`
						}
						switch operation {
						case "account":
							return 200, `{"Item":{"version":{"N":"not-a-number"}}}`
						case "idempotency":
							return 200, `{"Item":{"PK":{"S":"wrong-prefix"}}}`
						case "payment":
							return 200, `{"Items":[{"PK":{"S":"PAYMENT#p1"},"SK":{"S":"#HEAD"},"amount":{"N":"not-a-number"}}]}`
						case "payment event":
							return 200, `{"Items":[{"PK":{"S":"PAYMENT#p1"},"SK":{"S":"EVENT#bad"}}]}`
						case "list":
							return 200, `{"Items":[{"PK":{"S":"PAYMENT#p1"},"SK":{"S":"#HEAD"},"lastSequence":{"N":"bad"}}]}`
						case "reservations":
							return 200, `{"Items":[{"amount":{"N":"not-a-number"}}]}`
						case "batch":
							return 200, `{"Responses":{"payments-test":[{"amount":{"N":"not-a-number"}}]}}`
						default:
							t.Fatal("unexpected operation")
							return 500, `{}`
						}
					})
					ctx := context.Background()
					var err error
					switch operation {
					case "account":
						_, err = repository.GetAccount(ctx, "a1", "a1")
					case "idempotency":
						_, err = repository.GetIdempotencyRecord(ctx, "key")
					case "payment", "payment event":
						_, err = repository.GetPayment(ctx, "p1")
					case "list":
						_, err = repository.ListPayments(ctx, "merchant", "")
					case "reservations":
						_, _, err = repository.GetAccountReservations(ctx, "a1")
					case "batch":
						_, err = repository.GetReservations(ctx, "a1", []string{"res_p1"})
					}
					if err == nil {
						t.Fatal("expected error")
					}
					if !decode {
						var missing *types.ResourceNotFoundException
						if !errors.As(err, &missing) {
							t.Fatalf("AWS error identity not preserved: %v", err)
						}
					}
				})
			}
		})
	}
}

func TestRepositoryValidatesKeysBeforeAWSCalls(t *testing.T) {
	repository := branchRepository(t, func(target string, _ map[string]any) (int, string) {
		t.Fatalf("invalid key should not reach AWS: %s", target)
		return 500, `{}`
	})
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"put payment":    func() error { return repository.PutPayment(ctx, &model.Payment{}) },
		"create payment": func() error { return repository.CreatePayment(ctx, &model.Payment{}, &model.PaymentEvent{}, nil) },
		"create event": func() error {
			return repository.CreatePayment(ctx, &model.Payment{PaymentKeys: model.PaymentKeys{PK: "p1"}}, &model.PaymentEvent{}, nil)
		},
		"create idempotency": func() error {
			return repository.CreatePayment(ctx, &model.Payment{PaymentKeys: model.PaymentKeys{PK: "p1"}},
				&model.PaymentEvent{PaymentEventKeys: model.PaymentEventKeys{PK: "p1", SK: 1}}, &model.IdempotencyRecord{})
		},
		"put idempotency":      func() error { return repository.PutIdempotencyRecord(ctx, &model.IdempotencyRecord{}) },
		"account":              func() error { _, err := repository.GetAccount(ctx, "", "a1"); return err },
		"account reservations": func() error { _, _, err := repository.GetAccountReservations(ctx, ""); return err },
		"batch reservation":    func() error { _, err := repository.GetReservations(ctx, "a1", []string{""}); return err },
		"put account":          func() error { return repository.PutAccount(ctx, &model.Account{}) },
		"seed account":         func() error { return repository.PutSeedAccount(ctx, &model.Account{}) },
		"get payment":          func() error { _, err := repository.GetPayment(ctx, ""); return err },
		"get idempotency":      func() error { _, err := repository.GetIdempotencyRecord(ctx, ""); return err },
		"reserve account":      func() error { return repository.ReservePayment(ctx, &model.Payment{}, &model.Account{}, time.Now()) },
		"reserve event": func() error {
			return repository.ReservePayment(ctx, &model.Payment{DebtorAccountID: "a1"}, &model.Account{}, time.Now())
		},
		"reserve sequence": func() error {
			return repository.ReservePayment(ctx, &model.Payment{PaymentKeys: model.PaymentKeys{PK: "p1"}, DebtorAccountID: "a1", LastSequence: -1}, &model.Account{}, time.Now())
		},
		"complete event": func() error {
			return repository.CompletePayment(ctx, &model.Payment{}, &model.Account{CurrentBalanceExact: "10"}, time.Now())
		},
		"complete ledger": func() error {
			return repository.CompletePayment(ctx, &model.Payment{PaymentKeys: model.PaymentKeys{PK: "p1"}, DebtorAccountID: "a1"}, &model.Account{CurrentBalanceExact: "10"}, time.Now())
		},
		"reject event":        func() error { return repository.RejectPayment(ctx, &model.Payment{}, "DECLINED") },
		"release account":     func() error { return repository.ReleaseReservation(ctx, "", "res_p1") },
		"release reservation": func() error { return repository.ReleaseReservation(ctx, "a1", "") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("invalid key not rejected")
			}
		})
	}
}

func TestPutIdempotencyRecordPersistsBinding(t *testing.T) {
	repository := branchRepository(t, func(target string, input map[string]any) (int, string) {
		if target != "PutItem" {
			t.Fatalf("unexpected target %s", target)
		}
		item := input["Item"].(map[string]any)
		if item["PK"].(map[string]any)["S"] != "IDEMPOTENCY#key" || item["SK"].(map[string]any)["S"] != model.IdempotencySK ||
			item["requestHash"].(map[string]any)["S"] != "request-hash" {
			t.Fatalf("binding lost: %#v", item)
		}
		return 200, `{}`
	})
	record, err := repository.CreateIdempotencyRecord("key", "request-hash", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.PutIdempotencyRecord(context.Background(), record); err != nil {
		t.Fatal(err)
	}
}
