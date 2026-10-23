// This suite checks durable create recovery, replay corruption, read failures,
// reservation release, and list/batch mapping through isolated service fixtures.
package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"instantpayments/pkg/env"
	"instantpayments/pkg/model"
)

// recoveryFailure supplies a nonretryable SDK access-denied response.
func recoveryFailure() map[string]any {
	return map[string]any{"__type": "AccessDeniedException", "message": "fixture denied"}
}

// recoveryBinding wraps the selected hash and wire-format snapshot in an idempotency GetItem response.
func recoveryBinding(t *testing.T, hash string, snapshot map[string]any) map[string]any {
	t.Helper()
	item := wireItem(t, map[string]any{
		"PK": "IDEMPOTENCY#recovery", "SK": "IDEMPOTENCY", "requestHash": hash,
	})
	item["responseSnapshot"] = map[string]any{"M": snapshot}
	return map[string]any{"Item": item}
}

// recoverySnapshot returns a stable winning creation response in DynamoDB attribute form.
func recoverySnapshot(t *testing.T) map[string]any {
	t.Helper()
	return wireItem(t, map[string]any{
		"paymentId": "pay_winner", "state": "RECEIVED",
		"correlationId": "corr_winner", "createdAtUtc": "2026-01-01T00:00:00Z",
	})
}

func TestCreateDurableTransactionRecovery(t *testing.T) {
	for _, name := range []string{
		"same request wins race", "different request wins race", "winner lookup fails",
		"no winner exists", "winner snapshot malformed", "commit succeeded but response lost",
	} {
		t.Run(name, func(t *testing.T) {
			input := validInput("recovery")
			hash, err := requestHash(input)
			if err != nil {
				t.Fatal(err)
			}
			reads, transactions := 0, 0
			var committedBinding map[string]any
			service := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
				switch target {
				case "GetItem":
					reads++
					if body["ConsistentRead"] != true {
						t.Fatal("idempotency recovery must use a strongly consistent read")
					}
					if reads == 1 {
						return 200, map[string]any{}
					}
					switch name {
					case "winner lookup fails":
						return 400, recoveryFailure()
					case "no winner exists":
						return 200, map[string]any{}
					case "different request wins race":
						return 200, recoveryBinding(t, "another-request-hash", recoverySnapshot(t))
					case "winner snapshot malformed":
						return 200, recoveryBinding(t, hash, wireItem(t, map[string]any{"createdAtUtc": "not-a-time"}))
					case "commit succeeded but response lost":
						return 200, map[string]any{"Item": committedBinding}
					default:
						return 200, recoveryBinding(t, hash, recoverySnapshot(t))
					}
				case "TransactWriteItems":
					transactions++
					if name == "commit succeeded but response lost" {
						items := body["TransactItems"].([]any)
						committedBinding = items[2].(map[string]any)["Put"].(map[string]any)["Item"].(map[string]any)
						return 500, map[string]any{"__type": "InternalServerError", "message": "response lost after commit"}
					}
					return 400, canceledTransaction(3, "ConditionalCheckFailed", 2)
				default:
					t.Fatalf("unexpected operation %s", target)
					return 500, nil
				}
			})
			output, status, err := service.Create(input)
			if reads != 2 || transactions != 1 {
				t.Fatalf("expected one transaction and recovery read, got reads=%d transactions=%d", reads, transactions)
			}
			switch name {
			case "different request wins race":
				requireDomainError(t, err, 409, "IDEMPOTENCY_CONFLICT")
			case "winner lookup fails", "no winner exists", "winner snapshot malformed":
				expected := map[string]string{
					"winner lookup fails": "lookup idempotency record", "no winner exists": "create payment transaction:",
					"winner snapshot malformed": "decode idempotency response",
				}[name]
				if err == nil || !strings.Contains(err.Error(), expected) || output != nil || status != 0 {
					t.Fatalf("expected %q failure without output, got %#v %d %v", expected, output, status, err)
				}
			default:
				if err != nil || status != 200 || output == nil || output.State != "RECEIVED" || output.CorrelationID == nil {
					t.Fatalf("expected recovered immutable create response, got %#v %d %v", output, status, err)
				}
				if name == "same request wins race" {
					if output.PaymentID != "pay_winner" || *output.CorrelationID != "corr_winner" ||
						output.CreatedAtUTC.Format(time.RFC3339Nano) != "2026-01-01T00:00:00Z" {
						t.Fatalf("did not return winner's response: %#v", output)
					}
				} else {
					snapshot := committedBinding["responseSnapshot"].(map[string]any)["M"].(map[string]any)
					if output.PaymentID != snapshot["paymentId"].(map[string]any)["S"] ||
						*output.CorrelationID != snapshot["correlationId"].(map[string]any)["S"] {
						t.Fatalf("recovery did not return committed transaction response: %#v", output)
					}
				}
			}
			if len(service.payments) != 0 || len(service.idempotency) != 0 {
				t.Fatal("a failed transaction or recovered winner must not publish the losing payment in memory")
			}
		})
	}
}

func TestCreateDurableInitialLookup(t *testing.T) {
	for _, name := range []string{"lookup error", "conflicting hash", "malformed snapshot", "snapshot missing payment ID"} {
		t.Run(name, func(t *testing.T) {
			input := validInput("recovery")
			hash, err := requestHash(input)
			if err != nil {
				t.Fatal(err)
			}
			transactions := 0
			service := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
				if target == "TransactWriteItems" {
					transactions++
					return 200, map[string]any{}
				}
				if target != "GetItem" {
					t.Fatalf("unexpected operation %s", target)
				}
				switch name {
				case "lookup error":
					return 400, recoveryFailure()
				case "conflicting hash":
					return 200, recoveryBinding(t, "different", recoverySnapshot(t))
				case "malformed snapshot":
					return 200, recoveryBinding(t, hash, wireItem(t, map[string]any{"paymentId": 42}))
				default:
					return 200, recoveryBinding(t, hash, wireItem(t, map[string]any{"state": "RECEIVED"}))
				}
			})
			output, status, err := service.Create(input)
			switch name {
			case "snapshot missing payment ID":
				if err != nil || status != 201 || output == nil || output.PaymentID == "" || transactions != 1 {
					t.Fatalf("incomplete snapshot should not replay: %#v %d %v", output, status, err)
				}
			case "conflicting hash":
				requireDomainError(t, err, 409, "IDEMPOTENCY_CONFLICT")
			default:
				prefix := "load idempotency record:"
				if name == "malformed snapshot" {
					prefix = "decode idempotency response:"
				}
				if err == nil || !strings.HasPrefix(err.Error(), prefix) || output != nil || status != 0 {
					t.Fatalf("expected %s, got %#v %d %v", prefix, output, status, err)
				}
			}
			if name != "snapshot missing payment ID" && transactions != 0 {
				t.Fatal("lookup failures must prevent a create transaction")
			}
		})
	}
}

func TestPaymentReplayRejectsCorruption(t *testing.T) {
	for _, test := range []struct {
		name, state, reason, expected string
		events                        []string
		change                        func(*model.Payment)
	}{
		{name: "missing event", state: "RECEIVED", events: []string{"OUTBOUND_PAYMENT_CREATED"}, expected: "head sequence", change: func(p *model.Payment) { p.LastSequence++ }},
		{name: "wrong sequence", state: "RECEIVED", events: []string{"OUTBOUND_PAYMENT_CREATED"}, expected: "does not match", change: func(p *model.Payment) { p.Events[0].SequenceNumber++ }},
		{name: "wrong correlation", state: "RECEIVED", events: []string{"OUTBOUND_PAYMENT_CREATED"}, expected: "does not match", change: func(p *model.Payment) { p.Events[0].CorrelationID = "different" }},
		{name: "duplicate creation", state: "RECEIVED", events: []string{"OUTBOUND_PAYMENT_CREATED", "OUTBOUND_PAYMENT_CREATED"}, expected: "creation event after"},
		{name: "reserve before creation", state: "FUNDS_RESERVED", events: []string{"FUNDS_RESERVED"}, expected: "invalid reservation"},
		{name: "complete before reserve", state: "COMPLETED", events: []string{"OUTBOUND_PAYMENT_CREATED", "COMPLETED"}, expected: "invalid completion"},
		{name: "reject before creation", state: "REJECTED", events: []string{"REJECTED"}, expected: "invalid rejection"},
		{name: "reject reserved without expiration", state: "REJECTED", events: []string{"OUTBOUND_PAYMENT_CREATED", "FUNDS_RESERVED", "REJECTED"}, reason: "INSUFFICIENT_FUNDS", expected: "invalid rejection"},
		{name: "unknown event", state: "RECEIVED", events: []string{"UNRECOGNIZED"}, expected: "unknown event type"},
		{name: "head disagrees", state: "COMPLETED", events: []string{"OUTBOUND_PAYMENT_CREATED"}, expected: "does not match head state"},
	} {
		t.Run(test.name, func(t *testing.T) {
			head := &model.Payment{
				PaymentKeys:   model.PaymentKeys{PK: "pay_replay", SK: model.PaymentHeadSK},
				CorrelationID: "corr_replay", Status: test.state, LastSequence: int64(len(test.events)),
			}
			for index, eventType := range test.events {
				head.Events = append(head.Events, model.PaymentEvent{
					PaymentEventKeys: model.PaymentEventKeys{PK: "pay_replay", SK: model.EventKey(index + 1)},
					SequenceNumber:   int64(index + 1), CorrelationID: head.CorrelationID,
					EventType: model.PaymentEventType(eventType), ReasonCode: test.reason,
				})
			}
			if test.change != nil {
				test.change(head)
			}
			row, err := paymentFromModel(head)
			if err == nil || row != nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("expected corruption %q, got %#v %v", test.expected, row, err)
			}
		})
	}
}

func TestLoadPaymentReplayRecoveryIsBounded(t *testing.T) {
	for _, name := range []string{"recovers coherent stream", "persistent corruption", "canceled while recovering", "repository failure", "missing payment"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads := 0
			service := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
				if target != "Query" {
					t.Fatalf("unexpected operation %s", target)
				}
				reads++
				if name == "repository failure" {
					return 400, recoveryFailure()
				}
				if name == "missing payment" {
					return 200, map[string]any{}
				}
				partition := paymentPartition(t, "COMPLETED", "")
				if name != "recovers coherent stream" || reads == 1 {
					partition["Items"] = partition["Items"].([]any)[:2]
				}
				if name == "canceled while recovering" {
					cancel()
				}
				return 200, partition
			})
			row, err := service.loadPaymentContext(ctx, "pay_1")
			switch name {
			case "recovers coherent stream":
				if err != nil || row == nil || row.State != "COMPLETED" || row.LastSequence != 3 || reads != 2 || len(service.payments) != 0 {
					t.Fatalf("expected coherent retry without shared persistent cache, got %#v %v reads=%d", row, err, reads)
				}
			case "persistent corruption":
				if err == nil || !strings.Contains(err.Error(), "after five reads") || reads != 5 {
					t.Fatalf("expected bounded replay failure, got %v reads=%d", err, reads)
				}
			case "canceled while recovering":
				if !errors.Is(err, context.Canceled) || reads != 1 {
					t.Fatalf("expected cancellation after first corrupt read, got %v reads=%d", err, reads)
				}
			case "repository failure":
				if err == nil || !strings.Contains(err.Error(), "AccessDenied") || reads != 1 {
					t.Fatalf("repository errors must not be retried as replay corruption: %v reads=%d", err, reads)
				}
			case "missing payment":
				if row != nil || err != nil || reads != 1 {
					t.Fatalf("expected absent payment, got %#v %v reads=%d", row, err, reads)
				}
			}
			if name != "recovers coherent stream" && len(service.payments) != 0 {
				t.Fatal("an incomplete or failed read must not populate the payment cache")
			}
		})
	}
}

func TestReleaseReservationMemoryLifecycle(t *testing.T) {
	service := newTestPayment()
	acct := service.accounts["acc_usd_1"]
	acct.AvailableBalance = 875
	acct.Rows = []map[string]any{
		{"reservationId": "res_consumed", "status": "CONSUMED", "amount": float64(100)},
		{"reservationId": "res_active", "status": "ACTIVE", "amount": float64(125)},
		{"reservationId": "res_bad", "status": "ACTIVE", "amount": "25"},
	}
	for _, call := range [][2]string{
		{"missing-account", "res_active"}, {"acc_usd_1", "missing-reservation"},
		{"acc_usd_1", "res_consumed"},
	} {
		if err := service.ReleaseReservation(context.Background(), call[0], call[1]); err != nil {
			t.Fatal(err)
		}
	}
	if acct.AvailableBalance != 875 {
		t.Fatal("missing or consumed reservations must not restore funds")
	}
	for i := 0; i < 2; i++ {
		if err := service.ReleaseReservation(context.Background(), "acc_usd_1", "res_active"); err != nil {
			t.Fatal(err)
		}
	}
	if acct.AvailableBalance != 1000 || acct.CurrentBalance != 10000 || acct.Rows[1]["status"] != "RELEASED" {
		t.Fatalf("release must restore available funds exactly once without debit: %#v", acct)
	}
	if err := service.ReleaseReservation(context.Background(), "acc_usd_1", "res_bad"); err == nil || !strings.Contains(err.Error(), "invalid amount") {
		t.Fatalf("expected malformed reservation error, got %v", err)
	}
	if acct.AvailableBalance != 1000 || acct.Rows[2]["status"] != "ACTIVE" {
		t.Fatal("malformed reservation must not mutate funds or status")
	}
}

func TestReleaseReservationDelegatesToRepository(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			requests := 0
			service := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
				requests++
				if target != "GetItem" || body["ConsistentRead"] != true {
					t.Fatalf("unexpected reservation read: %s %#v", target, body)
				}
				key := body["Key"].(map[string]any)
				if key["PK"].(map[string]any)["S"] != "ACCOUNT#acc_usd_1" ||
					key["SK"].(map[string]any)["S"] != "RESERVATION#res_test" {
					t.Fatalf("incorrect delegated identifiers: %#v", key)
				}
				if fail {
					return 400, recoveryFailure()
				}
				return 200, map[string]any{}
			})
			err := service.ReleaseReservation(context.Background(), "acc_usd_1", "res_test")
			if requests != 1 || (fail && (err == nil || !strings.Contains(err.Error(), "AccessDenied"))) || (!fail && err != nil) {
				t.Fatalf("repository release result not propagated: requests=%d err=%v", requests, err)
			}
		})
	}
}

func TestListDurableProjectionMapping(t *testing.T) {
	for _, include := range []bool{false, true} {
		t.Run(fmt.Sprintf("include=%t", include), func(t *testing.T) {
			state := ""
			if include {
				state = "REJECTED"
			}
			queries := 0
			service := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
				queries++
				if target != "Query" || body["Limit"] != float64(1) || body["ScanIndexForward"] != true {
					t.Fatalf("unexpected list request %s %#v", target, body)
				}
				fields := map[string]any{
					"PK": "PAYMENT#pay_projected", "SK": "#HEAD",
					"createdAtUtc": "2026-01-01T00:00:00Z", "updatedAtUtc": "2026-01-02T00:00:00Z",
					"lastSequence": 2, "correlationId": "corr_projection", "amount": 12.5, "currency": "USD",
					"reasonCode": "INSUFFICIENT_FUNDS",
				}
				if !include {
					fields["aggregateState"], fields["merchantId"] = "REJECTED", "merchant-test"
				}
				partition, index := "paymentId", "GSI_MERCHANT_PAYMENTS"
				partitionValue := "pay_projected"
				if include {
					partition, index = "aggregateState", "GSI_MERCHANT_STATE_PAYMENTS"
					partitionValue = "REJECTED"
				}
				if body["IndexName"] != index {
					t.Fatalf("wrong index: %#v", body)
				}
				key := wireItem(t, map[string]any{
					"PK": "PAYMENT#pay_projected", "SK": "#HEAD", "createdAtUtc": fields["createdAtUtc"],
					"merchantId": "merchant-test", partition: partitionValue,
				})
				if queries == 2 {
					if !reflect.DeepEqual(body["ExclusiveStartKey"], key) {
						t.Fatalf("continuation key was not preserved: %#v", body)
					}
					return 200, map[string]any{}
				}
				return 200, map[string]any{"Items": []any{wireItem(t, fields)}, "LastEvaluatedKey": key}
			})
			page, err := service.List("merchant-test", state, "/payments", "", 1, true)
			if err != nil {
				t.Fatal(err)
			}
			items := page["items"].([]any)
			expected := map[string]any{
				"paymentId": "pay_projected", "state": "REJECTED", "version": int64(2), "merchantId": "merchant-test",
				"correlationId": "corr_projection", "amount": 12.5, "currency": "USD",
				"createdAtUtc": "2026-01-01T00:00:00Z", "updatedAtUtc": "2026-01-02T00:00:00Z", "reasonCode": "INSUFFICIENT_FUNDS",
			}
			if len(items) != 1 || !reflect.DeepEqual(items[0], expected) {
				t.Fatalf("projection was not mapped to public fields: %#v", items)
			}
			next, err := service.List("merchant-test", state, "/payments", page["nextToken"].(string), 1, true)
			if err != nil || len(next["items"].([]any)) != 0 || queries != 2 {
				t.Fatalf("continuation failed: %#v %v", next, err)
			}
		})
	}
}

func TestMemoryPaymentFailuresAndFacade(t *testing.T) {
	service := NewPaymentWithEnv(zap.NewNop(), nil, nil)
	if service.String() != "payments=0" {
		t.Fatal(service.String())
	}
	if _, err := service.InitiateOutbound(nil); err == nil || err.Error() != "required payment field is missing or blank" {
		t.Fatalf("facade must preserve validation error: %v", err)
	}
	created, err := service.InitiateOutbound(validInput("facade"))
	if err != nil || created.PaymentID == "" || service.String() != "payments=1" {
		t.Fatalf("facade create failed: %#v %v", created, err)
	}
	service.idempotency["orphan"] = "missing-payment"
	_, _, err = service.Create(validInput("orphan"))
	requireDomainError(t, err, 409, "IDEMPOTENCY_CONFLICT")
	_, err = service.Process("missing-payment")
	requireDomainError(t, err, 404, "PAYMENT_NOT_FOUND")
	input := validInput("missing-account")
	input.DebtorAccountID = "missing-account"
	created, err = service.InitiateOutbound(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Process(created.PaymentID)
	if err != nil || result["state"] != "REJECTED" || result["reasonCode"] != "ACCOUNT_NOT_FOUND" {
		t.Fatalf("missing debtor must be a business rejection, got %#v %v", result, err)
	}
	row := service.payments[created.PaymentID]
	row.State = "FUNDS_RESERVED"
	_, err = service.Process(created.PaymentID)
	requireDomainError(t, err, 404, "ACCOUNT_NOT_FOUND")

	input = validInput("currency-mismatch")
	input.Currency, input.Amount = "EUR", 12.5
	created, err = service.InitiateOutbound(input)
	if err != nil {
		t.Fatal(err)
	}
	before := *service.accounts[input.DebtorAccountID]
	result, err = service.Process(created.PaymentID)
	if err != nil || result["state"] != "REJECTED" || result["reasonCode"] != "CURRENCY_MISMATCH" {
		t.Fatalf("currency mismatch must reject: %#v %v", result, err)
	}
	if !reflect.DeepEqual(before, *service.accounts[input.DebtorAccountID]) {
		t.Fatal("currency rejection must not mutate account")
	}
	got, err := service.Get(created.PaymentID)
	if err != nil || got["reasonCode"] != "CURRENCY_MISMATCH" {
		t.Fatalf("rejection reason missing from payment read: %#v %v", got, err)
	}
	if !service.mu.TryLock() {
		t.Fatal("completed calls must release service lock")
	}
	if service.mu.TryLock() {
		t.Fatal("TryLock must fail while another caller owns lock")
	}
	service.mu.Unlock()
	settings := NewPaymentWithEnv(zap.NewNop(), nil, &env.Env{IdempotencyTTL: 60, ReservationTimeout: 30})
	if settings.idempotencyTTL != time.Minute || settings.reservationTimeout != 30*time.Second {
		t.Fatal("explicit workflow timeouts were not applied")
	}
}

func TestMemoryListLimitAndTokenBoundaries(t *testing.T) {
	service := newTestPayment()
	for i := 0; i < 3; i++ {
		if _, _, err := service.Create(validInput(fmt.Sprintf("list-%d", i))); err != nil {
			t.Fatal(err)
		}
	}

	page, err := service.List("merchant-test", "", "/payments", "", 1, false)
	if err != nil || len(page["items"].([]any)) != 1 || page["nextToken"] == nil {
		t.Fatalf("reverse list failed: %#v %v", page, err)
	}
	for _, limit := range []int{0, 101} {
		page, err := service.List("merchant-test", "", "/payments", "", limit, false)
		if err != nil || len(page["items"].([]any)) != 3 {
			t.Fatalf("limit normalization failed: %#v %v", page, err)
		}
	}
	tooFar, err := encodePageToken(pageToken{Merchant: "merchant-test", Route: "/payments", Limit: 1, Offset: 99})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"!", base64.RawURLEncoding.EncodeToString([]byte("{")), tooFar} {
		_, err := service.List("merchant-test", "", "/payments", token, 1, false)
		requireDomainError(t, err, 400, "INVALID_PAGINATION_TOKEN")
	}
}

func TestPaymentReadFailuresPreserveRepositoryErrors(t *testing.T) {
	for _, name := range []string{"get", "account", "account reservations", "batch", "list", "reserved account"} {
		t.Run(name, func(t *testing.T) {
			service := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
				if name == "account reservations" && target == "GetItem" {
					return 200, accountItem(t, 900, 2)
				}
				if name == "reserved account" && target == "Query" {
					return 200, paymentPartition(t, "FUNDS_RESERVED", "")
				}
				return 400, recoveryFailure()
			})
			var result map[string]any
			var err error
			switch name {
			case "get":
				result, err = service.Get("pay_1")
			case "account", "account reservations":
				result, err = service.Account("acc_usd_1")
			case "batch":
				result, err = service.Batch("acc_usd_1", []string{"res_1"})
			case "list":
				result, err = service.List("merchant-test", "", "/payments", "", 50, true)
			case "reserved account":
				result, err = service.Process("pay_1")
			}
			if result != nil || err == nil || !strings.Contains(err.Error(), "AccessDenied") {
				t.Fatalf("repository error must propagate without a partial result: %#v %v", result, err)
			}
			if !service.mu.TryLock() {
				t.Fatal("failed read retained service mutex")
			}
			service.mu.Unlock()
		})
	}
}

func TestDurableBatchDeduplicatesAndPreservesOrder(t *testing.T) {
	requests := 0
	service := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
		requests++
		if target != "BatchGetItem" {
			t.Fatalf("batch should not require an account read: %s", target)
		}
		keys := body["RequestItems"].(map[string]any)["payments-test"].(map[string]any)["Keys"].([]any)
		if len(keys) != 3 {
			t.Fatalf("expected three distinct reservation keys, got %#v", keys)
		}
		items := []any{}
		for _, id := range []string{"res_second", "res_first"} {
			items = append(items, wireItem(t, map[string]any{
				"PK": "ACCOUNT#acc_usd_1", "SK": "RESERVATION#" + id, "reservationId": id,
				"paymentId": "pay_1", "amount": 12.5, "status": "ACTIVE", "createdAtUtc": "2026-01-01T00:00:00Z",
			}))
		}
		return 200, map[string]any{"Responses": map[string]any{"payments-test": items}}
	})
	result, err := service.Batch("acc_usd_1", []string{"res_first", "missing", "res_second", "res_first"})
	if err != nil {
		t.Fatal(err)
	}
	rows := result["reservations"].([]any)
	if requests != 1 || len(rows) != 2 ||
		rows[0].(map[string]any)["reservationId"] != "res_first" ||
		rows[1].(map[string]any)["reservationId"] != "res_second" ||
		!reflect.DeepEqual(result["missingReservationIds"], []string{"missing"}) {
		t.Fatalf("batch did not preserve unique input order and missing IDs: %#v", result)
	}
}

func TestDurableAccountReadsAccountOnlyOnce(t *testing.T) {
	reads, queries := 0, 0
	payment := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
		switch target {
		case "GetItem":
			reads++
			return 200, accountItem(t, 900, 2)
		case "Query":
			queries++
			values := body["ExpressionAttributeValues"].(map[string]any)
			if values[":prefix"].(map[string]any)["S"] != "RESERVATION#" {
				t.Fatalf("account overview must not query timers or ledger rows: %#v", body)
			}
			return 200, map[string]any{"Items": []any{}}
		default:
			t.Fatalf("unexpected operation %s", target)
			return 500, nil
		}
	})
	result, err := payment.Account("acc_usd_1")
	if err != nil || reads != 1 || queries != 1 || result["availableBalance"] != float64(900) {
		t.Fatalf("account read contract: %#v, %v, GetItem=%d Query=%d", result, err, reads, queries)
	}
}

func TestDurableAccountNotFound(t *testing.T) {
	service := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
		if target != "GetItem" {
			t.Fatalf("unexpected operation %s", target)
		}
		return 200, map[string]any{}
	})
	result, err := service.Account("missing")
	requireDomainError(t, err, 404, "ACCOUNT_NOT_FOUND")
	if result != nil {
		t.Fatal("absent account must not return partial data")
	}
}

func TestDurableListRejectsUnsupportedContinuationKey(t *testing.T) {
	service := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
		if target != "Query" {
			t.Fatalf("unexpected operation %s", target)
		}
		return 200, map[string]any{
			"LastEvaluatedKey": wireItem(t, map[string]any{"PK": "PAYMENT#pay_1", "SK": "#HEAD", "createdAtUtc": 42, "merchantId": "merchant-test"}),
		}
	})
	result, err := service.List("merchant-test", "", "/payments", "", 50, true)
	if result != nil || err == nil || !strings.Contains(err.Error(), "unsupported pagination key") {
		t.Fatalf("unsupported continuation key must not be encoded: %#v %v", result, err)
	}
}

func TestReservationExpiryRejectionFailureDoesNotComplete(t *testing.T) {
	writes := 0
	service := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
		switch target {
		case "Query":
			return 200, paymentPartition(t, "FUNDS_RESERVED", "")
		case "GetItem":
			return 200, accountItem(t, 900, 2)
		case "TransactWriteItems":
			writes++
			if writes == 1 {
				return 400, canceledTransaction(6, "ConditionalCheckFailed", 1)
			}
			return 400, recoveryFailure()
		default:
			t.Fatalf("unexpected operation %s", target)
			return 500, nil
		}
	})
	result, err := service.Process("pay_1")
	if result != nil || err == nil || !strings.Contains(err.Error(), "reject payment:") || writes != 2 {
		t.Fatalf("failed expiry rejection must be reported: %#v %v writes=%d", result, err, writes)
	}
	tracked, err := service.Get("pay_1")
	if err != nil || tracked["state"] != "FUNDS_RESERVED" || tracked["version"] != 2 {
		t.Fatalf("failed rejection must not publish completion: %#v %v", tracked, err)
	}
}
