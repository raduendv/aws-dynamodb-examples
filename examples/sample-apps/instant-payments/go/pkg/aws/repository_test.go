// This suite verifies repository wire shapes, read consistency, key codecs,
// decimal preservation, timestamps, and transaction ordering using HTTP fixtures.
package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
	"instantpayments/pkg/model"
)

// roundTripperFunc adapts a request fixture to the SDK HTTP transport.
type roundTripperFunc func(*http.Request) (*http.Response, error)

// RoundTrip delegates one SDK request to the fixture.
func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

// testDynamoClient returns a dummy-credential client whose successful responses come
// entirely from responder, with no network access.
func testDynamoClient(t *testing.T, responder func(*http.Request) string) *dynamodb.Client {
	t.Helper()
	configuration := awssdk.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("key", "secret", ""),
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(responder(request))), Request: request}, nil
		})},
	}
	return dynamodb.NewFromConfig(configuration, func(options *dynamodb.Options) {
		options.BaseEndpoint = awssdk.String("http://dynamodb.test")
	})
}

func TestNewRepositoryAndCreateIdempotencyRecord(t *testing.T) {
	repository := NewRepository(zap.NewNop(), nil, &env.Env{TableName: "payments-test"})
	if repository.tableName != "payments-test" {
		t.Fatalf("expected table name to be preserved, got %q", repository.tableName)
	}
	record, err := repository.CreateIdempotencyRecord("key-123", "hash-456", 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if record.PK != "key-123" || record.SK != model.IdempotencySK || record.RequestHash != "hash-456" {
		t.Fatalf("unexpected idempotency record %#v", record)
	}
	if record.ExpiresAt.Sub(record.CreatedAt) != 30*24*time.Hour {
		t.Fatalf("unexpected idempotency retention %s", record.ExpiresAt.Sub(record.CreatedAt))
	}
}

func TestCreatePaymentUsesAtomicOrderedTransaction(t *testing.T) {
	var requestBody map[string]any
	client := testDynamoClient(t, func(request *http.Request) string {
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode transaction request: %v", err)
		}
		return `{}`
	})
	repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
	payment := &model.Payment{Item: model.Item{EntityType: "PAYMENT_STREAM_HEAD"}, PaymentKeys: model.PaymentKeys{PK: "pay_1", SK: "#HEAD"}}
	event := &model.PaymentEvent{Item: model.Item{EntityType: "PAYMENT_EVENT"}, PaymentEventKeys: model.PaymentEventKeys{PK: "pay_1", SK: 1}}
	record := &model.IdempotencyRecord{
		Item: model.Item{EntityType: "IDEMPOTENCY"}, IdempotencyKeys: model.IdempotencyKeys{PK: "key", SK: "IDEMPOTENCY"},
		Response: map[string]any{
			"paymentId": "pay_1", "state": "RECEIVED", "correlationId": "corr_1", "createdAtUtc": "2026-01-01T00:00:00Z",
		},
	}
	if err := repository.CreatePayment(context.Background(), payment, event, record); err != nil {
		t.Fatal(err)
	}
	items, ok := requestBody["TransactItems"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("expected three transaction writes in request, got %#v", requestBody)
	}
	expected := []struct {
		pk, sk string
	}{
		{"PAYMENT#pay_1", "#HEAD"},
		{"PAYMENT#pay_1", "EVENT#0000000000000000001"},
		{"IDEMPOTENCY#key", "IDEMPOTENCY"},
	}
	for index, transactionItem := range items {
		write, ok := transactionItem.(map[string]any)["Put"].(map[string]any)
		if !ok {
			t.Fatalf("transaction item %d is not Put: %#v", index, transactionItem)
		}
		item := write["Item"].(map[string]any)
		pk := item["PK"].(map[string]any)["S"]
		sk := item["SK"].(map[string]any)["S"]
		if pk != expected[index].pk || sk != expected[index].sk {
			t.Fatalf("transaction item %d keys = %v/%v", index, pk, sk)
		}
	}
	lastPut := items[2].(map[string]any)["Put"].(map[string]any)
	head := items[0].(map[string]any)["Put"].(map[string]any)["Item"].(map[string]any)
	if reason := head["reasonCode"]; !reflect.DeepEqual(reason, map[string]any{"NULL": true}) {
		t.Fatalf("new head must persist a NULL reasonCode, got %#v", reason)
	}
	eventItem := items[1].(map[string]any)["Put"].(map[string]any)["Item"].(map[string]any)
	if _, ok := eventItem["occurredAt"]; !ok {
		t.Fatal("event timestamp must use occurredAt")
	}
	if _, ok := eventItem["occurredAtUtc"]; ok {
		t.Fatal("new events must not write the legacy timestamp attribute")
	}
	if lastPut["ConditionExpression"] != "attribute_not_exists(PK)" {
		t.Fatalf("expected conditional idempotency write, got %#v", lastPut)
	}
	idempotencyItem := lastPut["Item"].(map[string]any)
	if _, ok := idempotencyItem["responseSnapshot"].(map[string]any)["M"]; !ok {
		t.Fatalf("expected responseSnapshot to be stored as a DynamoDB map, got %#v", idempotencyItem["responseSnapshot"])
	}
}

func TestGetPaymentReadsHeadAndEventsInOneConsistentPartitionQuery(t *testing.T) {
	var requestBody map[string]any
	client := testDynamoClient(t, func(request *http.Request) string {
		if !strings.HasSuffix(request.Header.Get("X-Amz-Target"), ".Query") {
			t.Fatalf("expected Query request, got %q", request.Header.Get("X-Amz-Target"))
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode payment query: %v", err)
		}
		return `{"Items":[
			{"PK":{"S":"PAYMENT#pay_1"},"SK":{"S":"#HEAD"},"entityType":{"S":"PAYMENT_STREAM_HEAD"},"correlationId":{"S":"corr_1"},"merchantId":{"S":"merchant"},"amount":{"N":"10.5"},"currency":{"S":"USD"},"aggregateState":{"S":"RECEIVED"},"createdAtUtc":{"S":"2026-01-01T00:00:00Z"},"updatedAtUtc":{"S":"2026-01-01T00:00:00Z"},"lastSequence":{"N":"1"}},
			{"PK":{"S":"PAYMENT#pay_1"},"SK":{"S":"EVENT#0000000000000000001"},"entityType":{"S":"PAYMENT_EVENT"},"eventType":{"S":"OUTBOUND_PAYMENT_CREATED"},"sequenceNumber":{"N":"1"},"correlationId":{"S":"corr_1"},"occurredAtUtc":{"S":"2026-01-01T00:00:00Z"},"paymentId":{"S":"pay_1"}}
		]}`
	})
	repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
	payment, err := repository.GetPayment(context.Background(), "pay_1")
	if err != nil {
		t.Fatal(err)
	}
	if payment == nil || payment.CorrelationID != "corr_1" || len(payment.Events) != 1 || payment.Events[0].EventType != "OUTBOUND_PAYMENT_CREATED" || payment.Events[0].OccurredAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("unexpected reconstructed payment: %#v", payment)
	}
	if payment.PK != "pay_1" || payment.Events[0].PK != "pay_1" || payment.Events[0].SK != 1 {
		t.Fatalf("expected logical decoded keys, got %#v", payment)
	}
	values := requestBody["ExpressionAttributeValues"].(map[string]any)
	if values[":pk"].(map[string]any)["S"] != "PAYMENT#pay_1" {
		t.Fatalf("unexpected encoded partition key %#v", values)
	}
	if requestBody["ConsistentRead"] != true || requestBody["KeyConditionExpression"] != "PK = :pk" {
		t.Fatalf("expected strongly-consistent payment partition query, got %#v", requestBody)
	}
}

func TestRepositoryTypedLookupKeys(t *testing.T) {
	requests := 0
	client := testDynamoClient(t, func(request *http.Request) string {
		var body struct {
			Key map[string]struct{ S string }
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		pk, sk := "ACCOUNT#acc_1", "ACCOUNT#acc_1"
		if requests == 1 {
			pk, sk = "IDEMPOTENCY#raw#key", model.IdempotencySK
		}
		requests++
		if body.Key["PK"].S != pk || body.Key["SK"].S != sk {
			t.Fatalf("lookup key = %#v, want %s/%s", body.Key, pk, sk)
		}
		return `{"Item":{}}`
	})
	repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
	if _, err := repository.GetAccount(context.Background(), "acc_1", "acc_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetIdempotencyRecord(context.Background(), "raw#key"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetAccount(context.Background(), "", "acc_1"); err == nil {
		t.Fatal("expected empty logical account key to fail")
	}
	if requests != 2 {
		t.Fatalf("expected only two valid lookups, got %d", requests)
	}
}

func TestAccountReadsRemainEventual(t *testing.T) {
	for _, update := range []bool{false, true} {
		t.Run(fmt.Sprintf("forUpdate=%v", update), func(t *testing.T) {
			calls := 0
			client := testDynamoClient(t, func(request *http.Request) string {
				calls++
				if !strings.HasSuffix(request.Header.Get("X-Amz-Target"), ".GetItem") {
					t.Fatal("account read must use GetItem")
				}
				var input map[string]any
				if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
					t.Fatal(err)
				}
				if input["ConsistentRead"] == true {
					t.Fatal("account reads must remain eventual, including update paths")
				}
				key := input["Key"].(map[string]any)
				if key["PK"].(map[string]any)["S"] != "ACCOUNT#acc_1" ||
					key["SK"].(map[string]any)["S"] != "ACCOUNT#acc_1" {
					t.Fatalf("unexpected account keys: %#v", key)
				}
				return `{"Item":{"PK":{"S":"ACCOUNT#acc_1"},"SK":{"S":"ACCOUNT#acc_1"},"version":{"N":"7"}}}`
			})
			repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
			read := repository.GetAccount
			if update {
				read = repository.GetAccountForUpdate
			}
			account, err := read(context.Background(), "acc_1", "acc_1")
			if err != nil || account == nil || account.Version != 7 || calls != 1 {
				t.Fatalf("account=%#v err=%v calls=%d", account, err, calls)
			}
		})
	}
}

func TestRepositorySurfacesMalformedStoredKeys(t *testing.T) {
	for _, test := range []struct {
		name, response string
		read           func(*Repository) error
	}{
		{"account", `{"Item":{"PK":{"S":"PAYMENT#acc_1"},"SK":{"S":"ACCOUNT#acc_1"}}}`, func(r *Repository) error {
			_, err := r.GetAccount(context.Background(), "acc_1", "acc_1")
			return err
		}},
		{"idempotency", `{"Item":{"PK":{"S":"IDEMPOTENCY#"},"SK":{"S":"IDEMPOTENCY"}}}`, func(r *Repository) error {
			_, err := r.GetIdempotencyRecord(context.Background(), "key")
			return err
		}},
		{"event", `{"Items":[{"PK":{"S":"PAYMENT#pay_1"},"SK":{"S":"EVENT#1"}}]}`, func(r *Repository) error {
			_, err := r.GetPayment(context.Background(), "pay_1")
			return err
		}},
		{"reservation", `{"Responses":{"payments-test":[{"PK":{"S":"ACCOUNT#acc_1"},"SK":{"S":"RESERVATION#"}}]}}`, func(r *Repository) error {
			_, err := r.GetReservations(context.Background(), "acc_1", []string{"res_1"})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := testDynamoClient(t, func(*http.Request) string { return test.response })
			repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
			if err := test.read(repository); err == nil {
				t.Fatal("malformed stored key was silently accepted")
			}
		})
	}
}

func TestRepositoryGetsEmptyAccountAndPutsAccount(t *testing.T) {
	var targets []string
	client := testDynamoClient(t, func(request *http.Request) string {
		targets = append(targets, request.Header.Get("X-Amz-Target"))
		if strings.HasSuffix(request.Header.Get("X-Amz-Target"), ".GetItem") {
			return `{"Item":{}}`
		}
		return `{}`
	})
	repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})

	account, err := repository.GetAccount(context.Background(), "missing", "missing")
	if err != nil || account != nil {
		t.Fatalf("expected empty account, got %#v, %v", account, err)
	}
	newAccount := &model.Account{AccountKeys: model.AccountKeys{PK: "alice", SK: "alice"}, CurrentBalance: 100, AvailableBalance: 100, Currency: "USD", Status: model.AccountStatusActive}
	if err := repository.PutAccount(context.Background(), newAccount); err != nil {
		t.Fatal(err)
	}
	if newAccount.CreatedAt.IsZero() || newAccount.UpdatedAt.IsZero() {
		t.Fatalf("expected repository to set timestamps %#v", newAccount)
	}
	if len(targets) != 2 || !strings.HasSuffix(targets[0], ".GetItem") || !strings.HasSuffix(targets[1], ".PutItem") {
		t.Fatalf("unexpected DynamoDB targets %v", targets)
	}
}

func TestAccountWritesPreserveExplicitBalances(t *testing.T) {
	for _, seed := range []bool{false, true} {
		for _, balances := range []struct {
			current, available float64
		}{
			{0, 0}, {100.25, 0}, {100.25, 99.75}, {0, 0.25},
		} {
			t.Run(fmt.Sprintf("seed=%t/current=%v/available=%v", seed, balances.current, balances.available), func(t *testing.T) {
				calls := 0
				client := testDynamoClient(t, func(request *http.Request) string {
					calls++
					var body struct {
						Item                map[string]struct{ N string }
						ConditionExpression string
					}
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if got := body.Item["currentBalance"].N; got != fmt.Sprint(balances.current) {
						t.Fatalf("current balance = %q, want %v", got, balances.current)
					}
					if got := body.Item["availableBalance"].N; got != fmt.Sprint(balances.available) {
						t.Fatalf("available balance = %q, want %v", got, balances.available)
					}
					for _, field := range []string{"name", "balance", "Name", "Balance"} {
						if _, exists := body.Item[field]; exists {
							t.Fatalf("legacy field %q was persisted", field)
						}
					}
					if seed && body.ConditionExpression != "attribute_not_exists(PK)" {
						t.Fatal("seed write lost its overwrite guard")
					}
					return `{}`
				})
				repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
				account := &model.Account{
					AccountKeys:    model.AccountKeys{PK: "acc_1", SK: "acc_1"},
					CurrentBalance: balances.current, AvailableBalance: balances.available,
				}
				var err error
				if seed {
					err = repository.PutSeedAccount(context.Background(), account)
				} else {
					err = repository.PutAccount(context.Background(), account)
				}
				if err != nil {
					t.Fatal(err)
				}
				if calls != 1 || account.CurrentBalance != balances.current || account.AvailableBalance != balances.available {
					t.Fatalf("account write changed balances: %#v (calls=%d)", account, calls)
				}
			})
		}
	}
}

func TestRepositoryUnmarshalsAccountAndMissingPayment(t *testing.T) {
	client := testDynamoClient(t, func(*http.Request) string {
		return `{"Item":{}}`
	})
	repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
	account, err := repository.GetAccount(context.Background(), "alice", "alice")
	if err != nil || account != nil {
		t.Fatalf("expected missing account, got %#v, %v", account, err)
	}
	payment, err := repository.GetPayment(context.Background(), "payment")
	if err != nil || payment != nil {
		t.Fatalf("expected missing payment, got %#v, %v", payment, err)
	}
}

func TestReservationReadsPreserveAmountUnits(t *testing.T) {
	for _, amount := range []string{"100", "10.5"} {
		t.Run(amount, func(t *testing.T) {
			item := `{"reservationId":{"S":"res_1"},"paymentId":{"S":"pay_1"},"amount":{"N":"` + amount + `"},"status":{"S":"CONSUMED"},"createdAtUtc":{"S":"2026-01-01T00:00:00Z"}}`
			client := testDynamoClient(t, func(request *http.Request) string {
				switch request.Header.Get("X-Amz-Target") {
				case "DynamoDB_20120810.GetItem":
					return `{"Item":{"PK":{"S":"ACCOUNT#acc_usd_1"},"SK":{"S":"ACCOUNT#acc_usd_1"},"currency":{"S":"USD"}}}`
				case "DynamoDB_20120810.Query":
					return `{"Items":[` + item + `]}`
				case "DynamoDB_20120810.BatchGetItem":
					return `{"Responses":{"payments-test":[` + item + `]}}`
				default:
					t.Fatalf("unexpected DynamoDB target %q", request.Header.Get("X-Amz-Target"))
					return ""
				}

			})
			repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
			var expected float64
			if err := json.Unmarshal([]byte(amount), &expected); err != nil {
				t.Fatal(err)
			}
			account, reservations, err := repository.GetAccountReservations(context.Background(), "acc_usd_1")
			if err != nil {
				t.Fatal(err)
			}
			if account == nil || len(reservations) != 1 || reservations[0]["amount"] != expected {
				t.Fatalf("account reservation amount should be %v, got %#v", expected, reservations)
			}
			batch, err := repository.GetReservations(context.Background(), "acc_usd_1", []string{"res_1"})
			if err != nil {
				t.Fatal(err)
			}
			if batch["res_1"]["amount"] != expected {
				t.Fatalf("batch reservation amount should be %v, got %#v", expected, batch)
			}
		})
	}
}

func TestGetAccountReservationsReadsAllPages(t *testing.T) {
	queryCalls := 0
	client := testDynamoClient(t, func(request *http.Request) string {
		if strings.HasSuffix(request.Header.Get("X-Amz-Target"), ".GetItem") {
			return `{"Item":{"PK":{"S":"ACCOUNT#acc_1"},"SK":{"S":"ACCOUNT#acc_1"}}}`
		}
		queryCalls++
		var body struct {
			ExclusiveStartKey map[string]struct{ S string }
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if queryCalls == 1 {
			if len(body.ExclusiveStartKey) != 0 {
				t.Fatalf("unexpected first-page start key %#v", body.ExclusiveStartKey)
			}
			return `{"Items":[{"reservationId":{"S":"res_1"},"amount":{"N":"100"}}],
				"LastEvaluatedKey":{"PK":{"S":"ACCOUNT#acc_1"},"SK":{"S":"RESERVATION#res_1"}}}`
		}
		if queryCalls != 2 || body.ExclusiveStartKey["PK"].S != "ACCOUNT#acc_1" ||
			body.ExclusiveStartKey["SK"].S != "RESERVATION#res_1" {
			t.Fatalf("unexpected continuation request %d %#v", queryCalls, body)
		}
		return `{"Items":[{"reservationId":{"S":"res_2"},"amount":{"N":"10.5"}}]}`
	})
	repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
	_, reservations, err := repository.GetAccountReservations(context.Background(), "acc_1")
	if err != nil {
		t.Fatal(err)
	}
	if queryCalls != 2 || len(reservations) != 2 ||
		reservations[0]["reservationId"] != "res_1" || reservations[1]["reservationId"] != "res_2" {
		t.Fatalf("expected both reservation pages, got %#v (queries=%d)", reservations, queryCalls)
	}
}

func TestDecimalBalanceAfter(t *testing.T) {
	for _, test := range []struct{ balance, amount, want string }{
		{"0.3", "0.1", "0.2"},
		{"1", "0.01", "0.99"},
		{"100.00000000000000001", "0.1", "99.90000000000000001"},
		{"3e-7", "1e-7", "0.0000002"},
		{"1e20", "0.1", "99999999999999999999.9"},
		{"0.1", "0.1", "0"},
		{"-0.3", "0.1", "-0.4"},
	} {
		t.Run(test.balance+"-"+test.amount, func(t *testing.T) {
			got, err := decimalBalanceAfter(test.balance, test.amount)
			if err != nil || string(got) != test.want {
				t.Fatalf("got %q, %v; want %q", got, err, test.want)
			}
		})
	}
	for _, test := range []struct{ balance, amount string }{
		{"", "0.1"}, {"NaN", "0.1"}, {"1", "+Inf"}, {"1/3", "0.1"},
	} {
		if _, err := decimalBalanceAfter(test.balance, test.amount); err == nil {
			t.Fatalf("expected invalid decimal error for %#v", test)
		}
	}
}

func TestCompletePaymentPreservesStoredDecimalBalance(t *testing.T) {
	client := testDynamoClient(t, func(request *http.Request) string {
		if strings.HasSuffix(request.Header.Get("X-Amz-Target"), ".GetItem") {
			return `{"Item":{"currentBalance":{"N":"100.00000000000000001"},"version":{"N":"1"}}}`
		}
		var body struct {
			TransactItems []struct {
				Put struct {
					Item map[string]struct{ N string }
				}
			}
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if got := body.TransactItems[3].Put.Item["balanceAfter"].N; got != "99.90000000000000001" {
			t.Fatalf("ledger lost decimal precision: %q", got)
		}
		return `{}`
	})
	repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
	account, err := repository.GetAccountForUpdate(context.Background(), "acc_1", "acc_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CompletePayment(context.Background(), &model.Payment{
		PaymentKeys: model.PaymentKeys{PK: "pay_1"}, Amount: 0.1, DebtorAccountID: "acc_1", CreatedAt: time.Now(),
	}, account, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestPutPaymentPreservesRejectionReason(t *testing.T) {
	for _, reason := range []string{"", "INSUFFICIENT_FUNDS", "RESERVATION_EXPIRED"} {
		t.Run(reason, func(t *testing.T) {
			client := testDynamoClient(t, func(request *http.Request) string {
				var body struct {
					Item map[string]struct {
						S    string
						NULL bool
					}
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				value, exists := body.Item["reasonCode"]
				if !exists || value.S != reason || value.NULL != (reason == "") {
					t.Fatalf("unexpected reason projection: %#v", body.Item)
				}
				return `{}`
			})
			repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
			if err := repository.PutPayment(context.Background(), &model.Payment{
				PaymentKeys: model.PaymentKeys{PK: "pay_1", SK: "#HEAD"}, Status: "REJECTED", ReasonCode: reason,
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPaymentHeadWritesUseFixedWidthUTCTimestamps(t *testing.T) {
	for _, test := range []struct{ timestamp, want string }{
		{"2026-01-01T00:00:00Z", "2026-01-01T00:00:00.000000000Z"},
		{"2026-01-01T00:00:00.001Z", "2026-01-01T00:00:00.001000000Z"},
		{"2026-01-01T00:00:00.001001Z", "2026-01-01T00:00:00.001001000Z"},
		{"2026-01-01T00:00:00.1Z", "2026-01-01T00:00:00.100000000Z"},
		{"2026-01-01T00:00:00.11Z", "2026-01-01T00:00:00.110000000Z"},
		{"2026-01-01T01:00:00.100000001+01:00", "2026-01-01T00:00:00.100000001Z"},
	} {
		t.Run(test.timestamp, func(t *testing.T) {
			created, err := time.Parse(time.RFC3339Nano, test.timestamp)
			if err != nil {
				t.Fatal(err)
			}
			client := testDynamoClient(t, func(request *http.Request) string {
				var body struct {
					Item          map[string]struct{ S string }
					TransactItems []struct {
						Put struct {
							Item map[string]struct{ S string }
						}
					}
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				item := body.Item
				if len(body.TransactItems) != 0 {
					item = body.TransactItems[0].Put.Item
				}
				if got := item["createdAtUtc"].S; got != test.want {
					t.Fatalf("timestamp = %q, want %q", got, test.want)
				}
				return `{}`
			})
			repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
			payment := &model.Payment{PaymentKeys: model.PaymentKeys{PK: "pay_1", SK: "#HEAD"}, CreatedAt: created}
			if err := repository.PutPayment(context.Background(), payment); err != nil {
				t.Fatal(err)
			}
			event := &model.PaymentEvent{PaymentEventKeys: model.PaymentEventKeys{PK: payment.PK, SK: 1}}
			record := &model.IdempotencyRecord{IdempotencyKeys: model.IdempotencyKeys{PK: "key", SK: model.IdempotencySK}}
			if err := repository.CreatePayment(context.Background(), payment, event, record); err != nil {
				t.Fatal(err)
			}
		})
	}
}
