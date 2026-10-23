// This suite checks persisted decimal handling, continuation keys, account reads,
// concurrent-transition recovery, and durable idempotency using scripted SDK responses.
package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"go.uber.org/zap"

	"instantpayments/pkg/aws"
	"instantpayments/pkg/env"
)

func TestPersistedFundsComparisonPreservesDecimalPrecision(t *testing.T) {
	for _, available := range []string{"99.99999999999999999", "100", "100.00000000000000001"} {
		t.Run(available, func(t *testing.T) {
			reserved, rejected := false, false
			payment := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
				switch target {
				case "Query":
					return 200, paymentPartition(t, "RECEIVED", "")
				case "GetItem":
					item := accountItem(t, 100, 1)
					item["Item"].(map[string]any)["availableBalance"] = map[string]any{"N": available}
					return 200, item
				case "TransactWriteItems":
					items := body["TransactItems"].([]any)
					if len(items) == 2 {
						rejected = true
						values := items[0].(map[string]any)["Update"].(map[string]any)["ExpressionAttributeValues"].(map[string]any)
						if values[":reason"].(map[string]any)["S"] != "INSUFFICIENT_FUNDS" {
							t.Fatalf("unexpected rejection: %#v", values)
						}
					} else if len(items) == 5 {
						reserved = true
					}
					return 200, map[string]any{}
				default:
					t.Fatalf("unexpected request %s", target)
					return 500, nil
				}
			})
			result, err := payment.Process("pay_1")
			if err != nil {
				t.Fatal(err)
			}
			if available == "99.99999999999999999" {
				if reserved || !rejected || result["state"] != "REJECTED" {
					t.Fatalf("expected rejection before reserving: %#v (reserved=%v rejected=%v)", result, reserved, rejected)
				}
			} else if !reserved || rejected || result["state"] != "COMPLETED" {
				t.Fatalf("sufficient balance was rejected: %#v", result)
			}
		})
	}
}

func TestPersistedPaginationRequiresContinuationKeys(t *testing.T) {
	for _, state := range []string{"", "COMPLETED"} {
		for _, forward := range []bool{false, true} {
			route, partition, value := "merchant", "paymentId", "pay_1"
			if state != "" {
				route, partition, value = "state", "aggregateState", state
			}
			t.Run(fmt.Sprintf("%s/forward=%t", route, forward), func(t *testing.T) {
				calls := 0
				payment := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
					calls++
					if target != "Query" || body["ScanIndexForward"] != forward {
						t.Fatalf("unexpected request: %s %#v", target, body)
					}
					key, ok := body["ExclusiveStartKey"].(map[string]any)
					if calls == 1 {
						if ok {
							t.Fatalf("first page must not have a continuation key: %#v", key)
						}
						return 200, map[string]any{
							"Items": []any{}, "LastEvaluatedKey": wireItem(t, map[string]any{
								"PK": "PAYMENT#pay_1", "SK": "#HEAD", "merchantId": "merchant-test", partition: value,
								"createdAtUtc": "2026-01-01T00:00:00.100000000Z",
							}),
						}
					}
					if !ok || key["PK"].(map[string]any)["S"] != "PAYMENT#pay_1" {
						t.Fatalf("continuation key was not forwarded: %#v", body)
					}
					return 200, map[string]any{"Items": []any{}}
				})
				for _, shape := range []string{
					"", `,"lastKey":null`, `,"lastKey":{}`, `,"offset":2`,
					`,"lastKey":{"PK":"PAYMENT#pay_1"}`,
					fmt.Sprintf(`,"lastKey":{"PK":"PAYMENT#","SK":"#HEAD",%q:%q,"createdAtUtc":"2026-01-01T00:00:00.100000000Z"}`, partition, value),
					fmt.Sprintf(`,"lastKey":{"PK":"ACCOUNT#acc_1","SK":"#HEAD",%q:%q,"createdAtUtc":"2026-01-01T00:00:00.100000000Z"}`, partition, value),
				} {
					payload := fmt.Sprintf(`{"merchant":"merchant-test","state":%q,"route":%q,"limit":2,"forward":%t%s}`, state, route, forward, shape)
					token := base64.RawURLEncoding.EncodeToString([]byte(payload))
					_, err := payment.List("merchant-test", state, route, token, 2, forward)
					requireDomainError(t, err, 400, "INVALID_PAGINATION_TOKEN")
				}
				if calls != 0 {
					t.Fatalf("invalid tokens made %d database calls", calls)
				}
				first, err := payment.List("merchant-test", state, route, "", 2, forward)
				if err != nil {
					t.Fatal(err)
				}
				token, ok := first["nextToken"].(string)
				if !ok || token == "" {
					t.Fatalf("missing continuation token: %#v", first)
				}
				for _, key := range []string{"merchantId", partition} {
					for _, replacement := range []string{"", "another-value"} {
						payload, err := base64.RawURLEncoding.DecodeString(token)
						if err != nil {
							t.Fatal(err)
						}
						var altered pageToken
						if err := json.Unmarshal(payload, &altered); err != nil {
							t.Fatal(err)
						}
						if replacement == "" {
							delete(altered.LastKey, key)
						} else {
							altered.LastKey[key] = replacement
						}
						invalid, err := encodePageToken(altered)
						if err != nil {
							t.Fatal(err)
						}
						_, err = payment.List("merchant-test", state, route, invalid, 2, forward)
						requireDomainError(t, err, 400, "INVALID_PAGINATION_TOKEN")
					}
				}
				if calls != 1 {
					t.Fatalf("altered native index tokens reached DynamoDB: %d calls", calls)
				}
				if _, err := payment.List("merchant-test", state, route, token, 2, forward); err != nil {
					t.Fatal(err)
				}
				if calls != 2 {
					t.Fatalf("expected two page queries, got %d", calls)
				}
				_, err = newTestPayment().List("merchant-test", state, route, token, 2, forward)
				requireDomainError(t, err, 400, "INVALID_PAGINATION_TOKEN")
			})
		}
	}
}

// paymentTestTransport adapts a scripted service-persistence responder to the SDK transport.
type paymentTestTransport func(*http.Request) (*http.Response, error)

// RoundTrip delegates a request to the test responder.
func (transport paymentTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// persistedPaymentService constructs a service over scripted SDK operations with
// retries disabled. Account reads are checked for eventual consistency.
func persistedPaymentService(t *testing.T, respond func(string, map[string]any) (int, any)) *Payment {
	t.Helper()
	client := dynamodb.NewFromConfig(awssdk.Config{
		Region: "eu-west-1", RetryMaxAttempts: 1,
		Credentials: credentials.NewStaticCredentialsProvider("local", "local", ""),
		HTTPClient: &http.Client{Transport: paymentTestTransport(func(request *http.Request) (*http.Response, error) {
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			target := strings.TrimPrefix(request.Header.Get("X-Amz-Target"), "DynamoDB_20120810.")
			if target == "GetItem" {
				key, ok := body["Key"].(map[string]any)
				if ok {
					sk, ok := key["SK"].(map[string]any)
					if ok {
						value, _ := sk["S"].(string)
						if strings.HasPrefix(value, "ACCOUNT#") && body["ConsistentRead"] == true {
							t.Fatal("processor account reads must remain eventual")
						}
					}
				}
			}
			status, response := respond(target, body)
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			return &http.Response{
				StatusCode: status, Header: http.Header{"Content-Type": {"application/x-amz-json-1.0"}},
				Body: io.NopCloser(strings.NewReader(string(encoded))), Request: request,
			}, nil
		})},
	}, func(options *dynamodb.Options) {
		options.BaseEndpoint = awssdk.String("http://dynamodb.test")
	})
	return NewPayment(zap.NewNop(), aws.NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"}))
}

// wireItem encodes string and numeric fixture fields in DynamoDB's JSON wire shape.
func wireItem(t *testing.T, fields map[string]any) map[string]any {
	t.Helper()
	item := make(map[string]any, len(fields))
	for name, value := range fields {
		switch value := value.(type) {
		case string:
			item[name] = map[string]any{"S": value}
		case int:
			item[name] = map[string]any{"N": fmt.Sprint(value)}
		case float64:
			item[name] = map[string]any{"N": fmt.Sprint(value)}
		default:
			t.Fatalf("unsupported fixture field %s: %T", name, value)
		}
	}
	return item
}

// paymentPartition builds a matching head and ordered history for the requested
// state, including a reserved event before expired-hold rejection.
func paymentPartition(t *testing.T, state, reason string) map[string]any {
	t.Helper()
	eventTypes := []string{"OUTBOUND_PAYMENT_CREATED"}
	if state == "FUNDS_RESERVED" || state == "COMPLETED" || reason == "RESERVATION_EXPIRED" {
		eventTypes = append(eventTypes, "FUNDS_RESERVED")
	}
	if state == "COMPLETED" || state == "REJECTED" {
		eventTypes = append(eventTypes, state)
	}
	const timestamp = "2026-01-01T00:00:00Z"
	items := []any{wireItem(t, map[string]any{
		"PK": "PAYMENT#pay_1", "SK": "#HEAD", "entityType": "PAYMENT_STREAM_HEAD",
		"aggregateState": state, "lastSequence": len(eventTypes), "correlationId": "corr_1",
		"createdAtUtc": timestamp, "updatedAtUtc": timestamp,
	})}
	for index, eventType := range eventTypes {
		fields := map[string]any{
			"PK": "PAYMENT#pay_1", "SK": fmt.Sprintf("EVENT#%019d", index+1),
			"eventType": eventType, "sequenceNumber": index + 1, "correlationId": "corr_1",
			"occurredAtUtc": timestamp,
		}
		if index == 0 {
			fields["merchantId"], fields["debtorAccountId"] = "merchant-test", "acc_usd_1"
			fields["amount"], fields["currency"] = 100, "USD"
		}
		if eventType == "REJECTED" {
			fields["reasonCode"] = reason
		}
		items = append(items, wireItem(t, fields))
	}
	return map[string]any{"Items": items}
}

// accountItem supplies a USD account response with a fixed posted balance and selected availability/version.
func accountItem(t *testing.T, available, version int) map[string]any {
	t.Helper()
	return map[string]any{"Item": wireItem(t, map[string]any{
		"PK": "ACCOUNT#acc_usd_1", "SK": "ACCOUNT#acc_usd_1", "currency": "USD",
		"currentBalance": 1000, "availableBalance": available, "version": version,
	})}
}

// canceledTransaction builds ordered cancellation reasons with failures at the selected indices.
func canceledTransaction(items int, code string, failedIndexes ...int) map[string]any {
	reasons := make([]map[string]string, items)
	for index := range reasons {
		reasons[index] = map[string]string{"Code": "None"}
	}
	for _, index := range failedIndexes {
		reasons[index]["Code"] = code
	}
	return map[string]any{"__type": "TransactionCanceledException", "CancellationReasons": reasons}
}

func TestPersistedAccountPreservesZeroAvailableBalance(t *testing.T) {
	transactions := 0
	payment := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
		switch target {
		case "GetItem":
			return 200, accountItem(t, 0, 1)
		case "Query":
			if body["KeyConditionExpression"] == "PK = :pk" {
				return 200, paymentPartition(t, "RECEIVED", "")
			}
			return 200, map[string]any{"Items": []any{}}
		case "TransactWriteItems":
			transactions++
			items := body["TransactItems"].([]any)
			if len(items) != 2 {
				t.Fatalf("zero available balance must reject without money writes, got %#v", items)
			}
			update := items[0].(map[string]any)["Update"].(map[string]any)
			values := update["ExpressionAttributeValues"].(map[string]any)
			if values[":reason"].(map[string]any)["S"] != "INSUFFICIENT_FUNDS" {
				t.Fatalf("unexpected rejection: %#v", values)
			}
			return 200, map[string]any{}
		default:
			t.Fatalf("unexpected request %s", target)
			return 500, nil
		}
	})
	account, err := payment.Account("acc_usd_1")
	if err != nil || account["availableBalance"] != float64(0) || account["currentBalance"] != float64(1000) {
		t.Fatalf("expected posted funds to remain held, got %#v, %v", account, err)
	}
	result, err := payment.Process("pay_1")
	if err != nil || result["state"] != "REJECTED" || result["reasonCode"] != "INSUFFICIENT_FUNDS" || transactions != 1 {
		t.Fatalf("expected insufficient-funds rejection, got %#v, %v (transactions=%d)", result, err, transactions)
	}
}

func TestProcessReloadsAfterConcurrentUpdates(t *testing.T) {
	for _, testCase := range []struct {
		name, initial, latest, expected, reason string
		items                                   int
		failedIndexes                           []int
		code                                    string
	}{
		{"reserve account version", "RECEIVED", "RECEIVED", "REJECTED", "INSUFFICIENT_FUNDS", 5, []int{2}, "ConditionalCheckFailed"},
		{"reserve head advanced", "RECEIVED", "COMPLETED", "COMPLETED", "", 5, []int{3}, "ConditionalCheckFailed"},
		{"complete account version", "FUNDS_RESERVED", "FUNDS_RESERVED", "COMPLETED", "", 6, []int{0}, "ConditionalCheckFailed"},
		{"complete head advanced", "FUNDS_RESERVED", "COMPLETED", "COMPLETED", "", 6, []int{4}, "ConditionalCheckFailed"},
		{"complete consumed by winner", "FUNDS_RESERVED", "COMPLETED", "COMPLETED", "", 6, []int{1, 4}, "ConditionalCheckFailed"},
		{"reject head advanced", "RECEIVED", "REJECTED", "REJECTED", "INSUFFICIENT_FUNDS", 2, []int{0}, "ConditionalCheckFailed"},
		{"transaction conflict", "FUNDS_RESERVED", "COMPLETED", "COMPLETED", "", 6, []int{0}, "TransactionConflict"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state, reason, reads, writes := testCase.initial, "", 0, 0
			payment := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
				switch target {
				case "Query":
					reads++
					return 200, paymentPartition(t, state, reason)
				case "GetItem":
					available := 1000
					if testCase.items == 2 || (writes > 0 && testCase.reason == "INSUFFICIENT_FUNDS") {
						available = 0
					}
					return 200, accountItem(t, available, writes+1)
				case "TransactWriteItems":
					writes++
					items := body["TransactItems"].([]any)
					if writes == 1 {
						if len(items) != testCase.items {
							t.Fatalf("expected %d transaction items, got %d", testCase.items, len(items))
						}
						state, reason = testCase.latest, testCase.reason
						return 400, canceledTransaction(len(items), testCase.code, testCase.failedIndexes...)
					}
					if writes > 2 {
						t.Fatal("unexpected extra transaction")
					}
					state, reason = testCase.expected, testCase.reason
					return 200, map[string]any{}
				default:
					t.Fatalf("unexpected request %s", target)
					return 500, nil
				}
			})
			result, err := payment.Process("pay_1")
			if err != nil || result["state"] != testCase.expected || reads != 2 {
				t.Fatalf("expected convergence to %s after reloading, got %#v, %v (reads=%d)", testCase.expected, result, err, reads)
			}
			if testCase.reason != "" && result["reasonCode"] != testCase.reason {
				t.Fatalf("unexpected business reason %#v", result)
			}
		})
	}
}

func TestProcessBoundsConcurrentUpdateRetries(t *testing.T) {
	reads, writes := 0, 0
	payment := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
		switch target {
		case "Query":
			reads++
			return 200, paymentPartition(t, "RECEIVED", "")
		case "GetItem":
			return 200, accountItem(t, 1000, writes+1)
		case "TransactWriteItems":
			writes++
			return 400, canceledTransaction(5, "ConditionalCheckFailed", 3)
		default:
			t.Fatalf("unexpected request %s", target)
			return 500, nil
		}
	})
	_, err := payment.Process("pay_1")
	if !errors.Is(err, aws.ErrConcurrentUpdate) || reads != 3 || writes != 3 {
		t.Fatalf("expected three bounded retries and original conflict, got reads=%d writes=%d err=%v", reads, writes, err)
	}
}

func TestProcessReservationConditionFailureRejectsWithoutDebit(t *testing.T) {
	state, reason, writes := "FUNDS_RESERVED", "", 0
	payment := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
		switch target {
		case "Query":
			return 200, paymentPartition(t, state, reason)
		case "GetItem":
			return 200, accountItem(t, 900, 2)
		case "TransactWriteItems":
			writes++
			items := body["TransactItems"].([]any)
			if writes == 1 {
				if len(items) != 6 {
					t.Fatalf("expected completion attempt, got %#v", items)
				}
				reservation := items[1].(map[string]any)["Update"].(map[string]any)
				if reservation["ConditionExpression"] != "#status = :active AND expiresAt > :now" {
					t.Fatalf("missing stale-hold guard: %#v", reservation)
				}
				return 400, canceledTransaction(6, "ConditionalCheckFailed", 1)
			}
			if writes != 2 || len(items) != 2 {
				t.Fatalf("expiry must only write rejection head/event, got %#v", items)
			}
			update := items[0].(map[string]any)["Update"].(map[string]any)
			values := update["ExpressionAttributeValues"].(map[string]any)
			if values[":expected"].(map[string]any)["S"] != "FUNDS_RESERVED" ||
				values[":reason"].(map[string]any)["S"] != "RESERVATION_EXPIRED" ||
				values[":sequence"].(map[string]any)["N"] != "2" {
				t.Fatalf("unexpected expiry rejection guard/values %#v", values)
			}
			state, reason = "REJECTED", "RESERVATION_EXPIRED"
			return 200, map[string]any{}
		default:
			t.Fatalf("unexpected request %s", target)
			return 500, nil
		}
	})
	for attempt := 0; attempt < 2; attempt++ {
		result, err := payment.Process("pay_1")
		if err != nil || result["state"] != "REJECTED" || result["reasonCode"] != "RESERVATION_EXPIRED" {
			t.Fatalf("expected terminal expired-hold rejection, got %#v, %v", result, err)
		}
	}
	if writes != 2 {
		t.Fatalf("terminal retry must not mutate state, got %d transactions", writes)
	}
	if _, err := payment.Get("pay_1"); err != nil {
		t.Fatalf("expired-hold history must replay: %v", err)
	}
}

func TestCreateUsesDurableIdempotencyAfterTTLDeletion(t *testing.T) {
	creates := 0
	var binding map[string]any
	payment := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
		switch target {
		case "GetItem":
			if binding != nil {
				return 200, map[string]any{"Item": binding}
			}
			return 200, map[string]any{}
		case "TransactWriteItems":
			creates++
			items := body["TransactItems"].([]any)
			binding = items[2].(map[string]any)["Put"].(map[string]any)["Item"].(map[string]any)
			return 200, map[string]any{}
		default:
			t.Fatalf("unexpected request %s", target)
			return 500, nil
		}
	})
	input := validInput("expired-key")
	first, status, err := payment.Create(input)
	if err != nil || status != 201 {
		t.Fatalf("initial create failed: %#v %d %v", first, status, err)
	}
	replay, status, err := payment.Create(input)
	if err != nil || status != 200 || replay.PaymentID != first.PaymentID ||
		replay.State != first.State || replay.CreatedAtUTC != first.CreatedAtUTC ||
		*replay.CorrelationID != *first.CorrelationID || creates != 1 {
		t.Fatalf("existing binding should replay its immutable snapshot, got %#v %d %v", replay, status, err)
	}
	binding = nil
	second, status, err := payment.Create(input)
	if err != nil || status != 201 || second.PaymentID == first.PaymentID {
		t.Fatalf("removed binding should permit a new payment, got %#v %d %v", second, status, err)
	}
	binding = nil
	input.Amount++
	third, status, err := payment.Create(input)
	if err != nil || status != 201 || third.PaymentID == second.PaymentID || creates != 3 {
		t.Fatalf("removed binding should permit changed intent, got %#v %d %v (creates=%d)", third, status, err, creates)
	}
}
