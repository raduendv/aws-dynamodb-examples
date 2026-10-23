// This suite exercises repository retry, conditional-write, and failure branches
// using scripted DynamoDB responses rather than a running database.
package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
	"instantpayments/pkg/model"
)

// branchRepository passes decoded operations to responder with SDK retries disabled.
func branchRepository(t *testing.T, responder func(string, map[string]any) (int, string)) *Repository {
	t.Helper()
	client := dynamodb.NewFromConfig(awssdk.Config{
		Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("key", "secret", ""),
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			var input map[string]any
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			target := strings.TrimPrefix(request.Header.Get("X-Amz-Target"), "DynamoDB_20120810.")
			status, body := responder(target, input)
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/x-amz-json-1.0"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})},
	}, func(options *dynamodb.Options) {
		options.BaseEndpoint = awssdk.String("http://dynamodb.test")
		options.RetryMaxAttempts = 1
	})
	return NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
}

const (
	// activeReservation is the active hold used by release transaction fixtures.
	activeReservation = `{"reservationId":{"S":"res_p1"},"paymentId":{"S":"p1"},"amount":{"N":"12.5"},"status":{"S":"ACTIVE"}}`
	// accountForRelease provides the account identity and optimistic-lock version.
	accountForRelease = `{"PK":{"S":"ACCOUNT#a1"},"SK":{"S":"ACCOUNT#a1"},"version":{"N":"7"}}`
)

// transactionCanceled encodes ordered cancellation reasons for transaction guards.
func transactionCanceled(codes ...string) string {
	reasons := make([]map[string]string, len(codes))
	for i, code := range codes {
		reasons[i] = map[string]string{"Code": code}
	}
	body, _ := json.Marshal(map[string]any{"__type": "TransactionCanceledException", "Message": "transaction canceled", "CancellationReasons": reasons})
	return string(body)
}

func TestReleaseReservationRetriesAndIdempotency(t *testing.T) {
	for _, test := range []struct {
		name      string
		failures  int
		codes     []string
		wantCalls int
		wantErr   string
		cancel    bool
	}{
		{name: "success", wantCalls: 1},
		{name: "already released concurrently", failures: 1, codes: []string{"ConditionalCheckFailed", "None"}, wantCalls: 1},
		{name: "account version refreshed", failures: 2, codes: []string{"None", "ConditionalCheckFailed"}, wantCalls: 3},
		{name: "transaction conflict", failures: 1, codes: []string{"TransactionConflict"}, wantCalls: 2},
		{name: "retries exhausted", failures: 3, codes: []string{"None", "ConditionalCheckFailed"}, wantCalls: 3, wantErr: "retries exhausted"},
		{name: "nonretryable", failures: 1, codes: []string{"ValidationError"}, wantCalls: 1, wantErr: "release reservation:"},
		{name: "cancel backoff", failures: 1, codes: []string{"TransactionConflict"}, wantCalls: 1, cancel: true, wantErr: "context canceled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writes, reads := 0, 0
			repository := branchRepository(t, func(target string, input map[string]any) (int, string) {
				switch target {
				case "GetItem":
					key := input["Key"].(map[string]any)
					if key["SK"].(map[string]any)["S"] == "RESERVATION#res_p1" {
						if input["ConsistentRead"] != true {
							t.Fatal("release must strongly read the reservation")
						}
						return 200, `{"Item":` + activeReservation + `}`
					}
					if input["ConsistentRead"] == true {
						t.Fatal("release must eventually read the account")
					}
					reads++
					return 200, fmt.Sprintf(`{"Item":{"PK":{"S":"ACCOUNT#a1"},"SK":{"S":"ACCOUNT#a1"},"version":{"N":"%d"}}}`, reads+6)
				case "TransactWriteItems":
					writes++
					items := input["TransactItems"].([]any)
					if len(items) != 2 {
						t.Fatalf("release must atomically update reservation and account: %#v", items)
					}
					reservation := items[0].(map[string]any)["Update"].(map[string]any)
					account := items[1].(map[string]any)["Update"].(map[string]any)
					values := account["ExpressionAttributeValues"].(map[string]any)
					if reservation["ConditionExpression"] != "#status = :active" || account["ConditionExpression"] != "version = :version" ||
						values[":amount"].(map[string]any)["N"] != "12.5" ||
						values[":version"].(map[string]any)["N"] != fmt.Sprint(reads+6) {
						t.Fatalf("incorrect release transaction: %#v", items)
					}
					if writes <= test.failures {
						if test.cancel {
							cancel()
						}
						return 400, transactionCanceled(test.codes...)
					}
					return 200, `{}`
				default:
					t.Fatalf("unexpected target %s", target)
					return 500, `{}`
				}
			})
			err := repository.ReleaseReservation(ctx, "a1", "res_p1")
			if test.wantErr == "" && err != nil || test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("unexpected release error %v (want %q)", err, test.wantErr)
			}
			if writes != test.wantCalls || reads != writes {
				t.Fatalf("writes=%d account reads=%d, want %d of each", writes, reads, test.wantCalls)
			}
			if test.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation identity lost: %v", err)
			}
		})
	}
}

func TestReleaseReservationReadFailuresAndNoops(t *testing.T) {
	for _, test := range []struct {
		name, reservation, account, wantErr string
		status, wantReads                   int
	}{
		{name: "missing reservation", reservation: `{}`, status: 200, wantReads: 1},
		{name: "inactive reservation", reservation: `{"Item":{"status":{"S":"RELEASED"}}}`, status: 200, wantReads: 1},
		{name: "malformed reservation", reservation: `{"Item":{"amount":{"N":"invalid"}}}`, status: 200, wantReads: 1, wantErr: "invalid"},
		{name: "reservation read error", reservation: `{"__type":"ResourceNotFoundException","message":"missing table"}`, status: 400, wantReads: 1, wantErr: "ResourceNotFoundException"},
		{name: "missing account", reservation: `{"Item":` + activeReservation + `}`, account: `{}`, status: 200, wantReads: 2, wantErr: "does not exist"},
		{name: "invalid account", reservation: `{"Item":` + activeReservation + `}`, account: `{"Item":{"version":{"N":"bad"}}}`, status: 200, wantReads: 2, wantErr: "bad"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reads := 0
			repository := branchRepository(t, func(target string, _ map[string]any) (int, string) {
				if target != "GetItem" {
					t.Fatalf("no writes expected, got %s", target)
				}
				reads++
				if reads == 1 {
					return test.status, test.reservation
				}
				return 200, test.account
			})
			err := repository.ReleaseReservation(context.Background(), "a1", "res_p1")
			if test.wantErr == "" && err != nil || test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("got error %v, want %q", err, test.wantErr)
			}
			if reads != test.wantReads {
				t.Fatalf("reads=%d want %d", reads, test.wantReads)
			}
		})
	}
}

func TestGetReservationsRetriesUnprocessedKeys(t *testing.T) {
	unprocessed := `"UnprocessedKeys":{"payments-test":{"Keys":[{"PK":{"S":"ACCOUNT#a1"},"SK":{"S":"RESERVATION#res_p2"}}]}}`
	t.Run("accumulates responses and retries only remaining keys", func(t *testing.T) {
		calls := 0
		repository := branchRepository(t, func(target string, input map[string]any) (int, string) {
			if target != "BatchGetItem" {
				t.Fatalf("unexpected target %s", target)
			}
			calls++
			keys := input["RequestItems"].(map[string]any)["payments-test"].(map[string]any)["Keys"].([]any)
			if calls == 1 {
				if len(keys) != 2 {
					t.Fatalf("expected two initial keys: %#v", keys)
				}
				return 200, `{"Responses":{"payments-test":[` + activeReservation + `]},` + unprocessed + `}`
			}
			if len(keys) != 1 || keys[0].(map[string]any)["SK"].(map[string]any)["S"] != "RESERVATION#res_p2" {
				t.Fatalf("must retry only unprocessed keys: %#v", keys)
			}
			return 200, `{"Responses":{"payments-test":[{"reservationId":{"S":"res_p2"},"amount":{"N":"7"}}]}}`
		})
		found, err := repository.GetReservations(context.Background(), "a1", []string{"res_p1", "res_p2"})
		if err != nil || calls != 2 || len(found) != 2 || found["res_p1"]["amount"] != 12.5 || found["res_p2"]["amount"] != float64(7) {
			t.Fatalf("responses not accumulated: %#v calls=%d err=%v", found, calls, err)
		}
	})
	for _, cancelCall := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancelCall), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			repository := branchRepository(t, func(string, map[string]any) (int, string) {
				calls++
				if cancelCall {
					cancel()
				}
				return 200, `{` + unprocessed + `}`
			})
			found, err := repository.GetReservations(ctx, "a1", []string{"res_p2"})
			if found != nil || err == nil {
				t.Fatalf("expected failure without partial results: %#v, %v", found, err)
			}
			if cancelCall {
				if calls != 1 || !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation not propagated: calls=%d err=%v", calls, err)
				}
			} else if calls != 5 || !strings.Contains(err.Error(), "after 5 attempts") {
				t.Fatalf("retry limit not enforced: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestReservationTransactions(t *testing.T) {
	payment := &model.Payment{
		PaymentKeys:     model.PaymentKeys{PK: "p1", SK: model.PaymentHeadSK},
		DebtorAccountID: "a1", MerchantID: "merchant", Amount: 12.5, LastSequence: 2, Status: "FUNDS_RESERVED",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	account := &model.Account{Version: 7, CurrentBalanceExact: "100"}
	for _, operation := range []string{"reserve", "complete", "reject"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			repository := branchRepository(t, func(target string, input map[string]any) (int, string) {
				calls++
				if target != "TransactWriteItems" {
					t.Fatalf("unexpected %s", target)
				}
				items := input["TransactItems"].([]any)
				var headIndex, eventIndex, count int
				switch operation {
				case "reserve":
					headIndex, eventIndex, count = 3, 4, 5
					reservation := items[0].(map[string]any)["Put"].(map[string]any)["Item"].(map[string]any)
					timer := items[1].(map[string]any)["Put"].(map[string]any)["Item"].(map[string]any)
					if reservation["status"].(map[string]any)["S"] != "ACTIVE" || timer["ttl"] == nil {
						t.Fatalf("missing durable reservation or expiry timer: %#v", items)
					}
				case "complete":
					headIndex, eventIndex, count = 4, 5, 6
				case "reject":
					headIndex, eventIndex, count = 0, 1, 2
				}
				if len(items) != count {
					t.Fatalf("expected %d atomic items, got %d", count, len(items))
				}
				head := items[headIndex].(map[string]any)["Update"].(map[string]any)
				values := head["ExpressionAttributeValues"].(map[string]any)
				event := items[eventIndex].(map[string]any)["Put"].(map[string]any)["Item"].(map[string]any)
				if values[":sequence"].(map[string]any)["N"] != "2" || values[":next"].(map[string]any)["N"] != "3" ||
					event["sequenceNumber"].(map[string]any)["N"] != "3" {
					t.Fatalf("incorrect event ordering: %#v", items)
				}
				if operation == "reject" && (values[":reason"].(map[string]any)["S"] != "INSUFFICIENT_FUNDS" || event["reasonCode"].(map[string]any)["S"] != "INSUFFICIENT_FUNDS") {
					t.Fatalf("rejection reason lost: %#v", items)
				}
				return 200, `{}`
			})
			var err error
			switch operation {
			case "reserve":
				err = repository.ReservePayment(context.Background(), payment, account, time.Now().Add(time.Minute))
			case "complete":
				err = repository.CompletePayment(context.Background(), payment, account, time.Now())
			case "reject":
				err = repository.RejectPayment(context.Background(), payment, "INSUFFICIENT_FUNDS")
			}
			if err != nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestSettlementFailureClassification(t *testing.T) {
	payment := &model.Payment{PaymentKeys: model.PaymentKeys{PK: "p1"}, DebtorAccountID: "a1", Amount: 1, CreatedAt: time.Now().UTC()}
	account := &model.Account{CurrentBalanceExact: "100"}
	for _, test := range []struct {
		name, operation string
		codes           []string
		want            error
		conflict        bool
	}{
		{name: "reserve stale account", operation: "reserve", codes: []string{"None", "None", "ConditionalCheckFailed"}, want: ErrConcurrentUpdate},
		{name: "reject stale head", operation: "reject", codes: []string{"ConditionalCheckFailed"}, want: ErrConcurrentUpdate},
		{name: "complete stale account", operation: "complete", codes: []string{"ConditionalCheckFailed"}, want: ErrConcurrentUpdate},
		{name: "complete expired reservation", operation: "complete", codes: []string{"None", "ConditionalCheckFailed"}, want: ErrReservationExpired},
		{name: "complete conflict", operation: "complete", codes: []string{"TransactionConflict"}, conflict: true},
		{name: "complete unclassified", operation: "complete", codes: []string{"ValidationError"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := branchRepository(t, func(string, map[string]any) (int, string) {
				return 400, transactionCanceled(test.codes...)
			})
			var err error
			switch test.operation {
			case "reserve":
				err = repository.ReservePayment(context.Background(), payment, account, time.Now())
			case "reject":
				err = repository.RejectPayment(context.Background(), payment, "DECLINED")
			case "complete":
				err = repository.CompletePayment(context.Background(), payment, account, time.Now())
			}
			if err == nil || test.want != nil && !errors.Is(err, test.want) || IsTransactionConflict(err) != test.conflict {
				t.Fatalf("wrong classification: %v", err)
			}
			var canceled *types.TransactionCanceledException
			if !errors.As(err, &canceled) {
				t.Fatalf("original DynamoDB error lost: %v", err)
			}
		})
	}
	repository := NewRepository(zap.NewNop(), nil, &env.Env{})
	if err := repository.CompletePayment(context.Background(), payment, &model.Account{CurrentBalanceExact: "bad"}, time.Now()); err == nil || !strings.Contains(err.Error(), "calculate ledger balance") {
		t.Fatalf("invalid decimal should fail before AWS: %v", err)
	}
}
