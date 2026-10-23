// This suite checks table, TTL, stream, and seed bootstrap behavior through
// scripted SDK HTTP responses without contacting DynamoDB.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"instantpayments/pkg/aws"
	"instantpayments/pkg/env"
)

// startupReply specifies one expected SDK operation, its response, and optional input assertions.
type startupReply struct {
	operation string
	status    int
	body      string
	check     func(*testing.T, map[string]any)
}

// startupTransport adapts a scripted responder to the SDK HTTP transport.
type startupTransport func(*http.Request) (*http.Response, error)

// RoundTrip delegates one request to the scripted responder.
func (transport startupTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// startupClient consumes replies in order and fails on unexpected or missing calls.
// SDK retries are disabled so each reply corresponds to one startup operation.
func startupClient(t *testing.T, replies ...startupReply) *dynamodb.Client {
	t.Helper()
	calls := 0
	t.Cleanup(func() {
		if calls != len(replies) {
			t.Errorf("startup made %d requests, want %d", calls, len(replies))
		}
	})
	return dynamodb.NewFromConfig(awssdk.Config{
		Region: "eu-west-1", RetryMaxAttempts: 1,
		Credentials: credentials.NewStaticCredentialsProvider("local", "local", ""),
		HTTPClient: &http.Client{Transport: startupTransport(func(request *http.Request) (*http.Response, error) {
			if calls >= len(replies) {
				t.Fatalf("unexpected request %s", request.Header.Get("X-Amz-Target"))
			}
			reply := replies[calls]
			calls++
			if got := request.Header.Get("X-Amz-Target"); got != "DynamoDB_20120810."+reply.operation {
				t.Fatalf("request %d = %q, want %s", calls, got, reply.operation)
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if reply.check != nil {
				reply.check(t, body)
			}
			status := reply.status
			if status == 0 {
				status = http.StatusOK
			}
			return &http.Response{
				StatusCode: status, Header: http.Header{"Content-Type": {"application/x-amz-json-1.0"}},
				Body: io.NopCloser(strings.NewReader(reply.body)), Request: request,
			}, nil
		})},
	}, func(options *dynamodb.Options) { options.BaseEndpoint = awssdk.String("http://startup.test") })
}

// activeStartupTable represents a ready table with the required stream view.
const activeStartupTable = `{"Table":{"TableName":"payments","TableStatus":"ACTIVE","LatestStreamArn":"arn:stream","StreamSpecification":{"StreamEnabled":true,"StreamViewType":"NEW_IMAGE"}}}`

func TestInitializeDynamoDBProvisioningPaths(t *testing.T) {
	for _, test := range []struct {
		name      string
		provision bool
		replies   []startupReply
	}{
		{"existing managed", false, []startupReply{{operation: "DescribeTable", body: activeStartupTable}}},
		{"creating managed", false, []startupReply{
			{operation: "DescribeTable", body: `{"Table":{"TableStatus":"CREATING"}}`},
			{operation: "DescribeTable", body: activeStartupTable},
		}},
		{"existing provisioned", true, []startupReply{
			{operation: "DescribeTable", body: activeStartupTable},
			{operation: "DescribeTimeToLive", body: `{"TimeToLiveDescription":{"AttributeName":"ttl","TimeToLiveStatus":"ENABLED"}}`},
			{operation: "DescribeTable", body: activeStartupTable},
		}},
		{"creating provisioned", true, []startupReply{
			{operation: "DescribeTable", body: `{"Table":{"TableStatus":"CREATING"}}`},
			{operation: "DescribeTable", body: activeStartupTable},
			{operation: "DescribeTimeToLive", body: `{"TimeToLiveDescription":{"AttributeName":"ttl","TimeToLiveStatus":"ENABLING"}}`},
			{operation: "DescribeTable", body: activeStartupTable},
		}},
		{"create table", true, []startupReply{
			{operation: "DescribeTable", status: 400, body: `{"__type":"ResourceNotFoundException"}`},
			{operation: "CreateTable", body: `{"TableDescription":{"TableStatus":"CREATING"}}`, check: func(t *testing.T, body map[string]any) {
				if body["TableName"] != "payments" || body["BillingMode"] != "PAY_PER_REQUEST" ||
					!reflect.DeepEqual(body["KeySchema"], []any{
						map[string]any{"AttributeName": "PK", "KeyType": "HASH"},
						map[string]any{"AttributeName": "SK", "KeyType": "RANGE"},
					}) || len(body["GlobalSecondaryIndexes"].([]any)) != 2 {
					t.Fatalf("incorrect table creation schema %#v", body)
				}
			}},
			{operation: "DescribeTable", body: activeStartupTable},
			{operation: "DescribeTimeToLive", body: `{"TimeToLiveDescription":{"AttributeName":"ttl","TimeToLiveStatus":"ENABLED"}}`},
			{operation: "DescribeTable", body: activeStartupTable},
		}},
		{"concurrent create", true, []startupReply{
			{operation: "DescribeTable", status: 400, body: `{"__type":"ResourceNotFoundException"}`},
			{operation: "CreateTable", status: 400, body: `{"__type":"ResourceInUseException"}`},
			{operation: "DescribeTable", body: activeStartupTable},
			{operation: "DescribeTimeToLive", body: `{"TimeToLiveDescription":{"AttributeName":"ttl","TimeToLiveStatus":"ENABLED"}}`},
			{operation: "DescribeTable", body: activeStartupTable},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			initializeDynamoDB(startupClient(t, test.replies...), zap.NewNop(), &env.Env{TableName: "payments", CreateResources: test.provision})
		})
	}
}

func TestEnableDynamoDBTTLAndStream(t *testing.T) {
	t.Run("TTL", func(t *testing.T) {
		client := startupClient(t,
			startupReply{operation: "DescribeTimeToLive", body: `{"TimeToLiveDescription":{"TimeToLiveStatus":"DISABLED"}}`},
			startupReply{operation: "UpdateTimeToLive", body: `{}`, check: func(t *testing.T, body map[string]any) {
				if body["TableName"] != "payments" || !reflect.DeepEqual(body["TimeToLiveSpecification"], map[string]any{"AttributeName": "ttl", "Enabled": true}) {
					t.Fatalf("incorrect TTL enablement %#v", body)
				}
			}},
		)
		ensureDynamoDBTTL(client, zap.NewNop(), "payments")
	})
	t.Run("stream", func(t *testing.T) {
		client := startupClient(t,
			startupReply{operation: "DescribeTable", body: `{"Table":{"TableStatus":"ACTIVE"}}`},
			startupReply{operation: "UpdateTable", body: `{}`, check: func(t *testing.T, body map[string]any) {
				if body["TableName"] != "payments" || !reflect.DeepEqual(body["StreamSpecification"], map[string]any{"StreamEnabled": true, "StreamViewType": "NEW_IMAGE"}) {
					t.Fatalf("incorrect stream enablement %#v", body)
				}
			}},
			startupReply{operation: "DescribeTable", body: activeStartupTable},
		)
		ensureDynamoDBStream(client, zap.NewNop(), "payments")
	})
}

func TestExistingTTLRequiresExpectedAttribute(t *testing.T) {
	for _, body := range []string{
		`{"TimeToLiveDescription":{"AttributeName":"wrong","TimeToLiveStatus":"ENABLED"}}`,
		`{"TimeToLiveDescription":{"AttributeName":"wrong","TimeToLiveStatus":"ENABLING"}}`,
		`{"TimeToLiveDescription":{"TimeToLiveStatus":"ENABLED"}}`,
		`{}`,
	} {
		t.Run(body, func(t *testing.T) {
			logger := zap.NewNop().WithOptions(zap.WithFatalHook(zapcore.WriteThenPanic))
			defer func() {
				if recover() == nil {
					t.Error("incompatible TTL policy must stop startup")
				}
			}()
			client := startupClient(t, startupReply{operation: "DescribeTimeToLive", body: body})
			ensureDynamoDBTTL(client, logger, "payments")
		})
	}
}

// Concurrent bootstrap instances can both observe DISABLED. Only a fresh read
// proving the intended attribute is enabled makes the failed update harmless.
func TestConcurrentTTLEnablement(t *testing.T) {
	for _, status := range []string{"ENABLED", "ENABLING"} {
		for _, code := range []string{"ValidationException", "ResourceInUseException"} {
			t.Run(status+"/"+code, func(t *testing.T) {
				client := startupClient(t,
					startupReply{operation: "DescribeTimeToLive", body: `{"TimeToLiveDescription":{"TimeToLiveStatus":"DISABLED"}}`},
					startupReply{operation: "UpdateTimeToLive", status: 400, body: `{"__type":"` + code + `","message":"already enabled"}`},
					startupReply{operation: "DescribeTimeToLive", body: `{"TimeToLiveDescription":{"AttributeName":"ttl","TimeToLiveStatus":"` + status + `"}}`},
				)
				ensureDynamoDBTTL(client, zap.NewNop(), "payments")
			})
		}
	}
	for _, body := range []string{
		`{"TimeToLiveDescription":{"AttributeName":"wrong","TimeToLiveStatus":"ENABLED"}}`,
		`{"TimeToLiveDescription":{"AttributeName":"ttl","TimeToLiveStatus":"DISABLED"}}`,
		`{}`,
		`{"__type":"AccessDeniedException"}`,
	} {
		t.Run(body, func(t *testing.T) {
			status := 200
			if strings.Contains(body, "__type") {
				status = 400
			}
			client := startupClient(t,
				startupReply{operation: "DescribeTimeToLive", body: `{"TimeToLiveDescription":{"TimeToLiveStatus":"DISABLED"}}`},
				startupReply{operation: "UpdateTimeToLive", status: 400, body: `{"__type":"ValidationException"}`},
				startupReply{operation: "DescribeTimeToLive", status: status, body: body},
			)
			logger := zap.NewNop().WithOptions(zap.WithFatalHook(zapcore.WriteThenPanic))
			defer func() {
				if recover() == nil {
					t.Error("unverified TTL enablement did not fail startup")
				}
			}()
			ensureDynamoDBTTL(client, logger, "payments")
		})
	}
}
func TestStartupFailuresAreFatal(t *testing.T) {
	failed := func(operation string) startupReply {
		return startupReply{operation: operation, status: 400, body: `{"__type":"AccessDeniedException","message":"denied"}`}
	}
	missing := startupReply{operation: "DescribeTable", status: 400, body: `{"__type":"ResourceNotFoundException"}`}
	for _, test := range []struct {
		name    string
		replies []startupReply
		run     func(*dynamodb.Client, *zap.Logger)
	}{
		{"describe table", []startupReply{failed("DescribeTable")}, func(c *dynamodb.Client, l *zap.Logger) {
			initializeDynamoDB(c, l, &env.Env{TableName: "payments"})
		}},
		{"missing managed table", []startupReply{missing}, func(c *dynamodb.Client, l *zap.Logger) {
			initializeDynamoDB(c, l, &env.Env{TableName: "payments"})
		}},
		{"unexpected table status", []startupReply{{operation: "DescribeTable", body: `{"Table":{"TableStatus":"DELETING"}}`}}, func(c *dynamodb.Client, l *zap.Logger) {
			initializeDynamoDB(c, l, &env.Env{TableName: "payments"})
		}},
		{"create", []startupReply{missing, failed("CreateTable")}, func(c *dynamodb.Client, l *zap.Logger) {
			initializeDynamoDB(c, l, &env.Env{TableName: "payments", CreateResources: true})
		}},
		{"creating waiter", []startupReply{
			{operation: "DescribeTable", body: `{"Table":{"TableStatus":"CREATING"}}`}, failed("DescribeTable"),
		}, func(c *dynamodb.Client, l *zap.Logger) {
			initializeDynamoDB(c, l, &env.Env{TableName: "payments"})
		}},
		{"created waiter", []startupReply{
			missing, {operation: "CreateTable", body: `{"TableDescription":{"TableStatus":"CREATING"}}`}, failed("DescribeTable"),
		}, func(c *dynamodb.Client, l *zap.Logger) {
			initializeDynamoDB(c, l, &env.Env{TableName: "payments", CreateResources: true})
		}},
		{"concurrent waiter", []startupReply{
			missing, {operation: "CreateTable", status: 400, body: `{"__type":"ResourceInUseException"}`}, failed("DescribeTable"),
		}, func(c *dynamodb.Client, l *zap.Logger) {
			initializeDynamoDB(c, l, &env.Env{TableName: "payments", CreateResources: true})
		}},
		{"describe TTL", []startupReply{failed("DescribeTimeToLive")}, func(c *dynamodb.Client, l *zap.Logger) {
			ensureDynamoDBTTL(c, l, "payments")
		}},
		{"update TTL", []startupReply{
			{operation: "DescribeTimeToLive", body: `{"TimeToLiveDescription":{"TimeToLiveStatus":"DISABLED"}}`}, failed("UpdateTimeToLive"),
		}, func(c *dynamodb.Client, l *zap.Logger) {
			ensureDynamoDBTTL(c, l, "payments")
		}},
		{"describe stream", []startupReply{failed("DescribeTable")}, func(c *dynamodb.Client, l *zap.Logger) {
			ensureDynamoDBStream(c, l, "payments")
		}},
		{"enable stream", []startupReply{{operation: "DescribeTable", body: `{"Table":{}}`}, failed("UpdateTable")}, func(c *dynamodb.Client, l *zap.Logger) {
			ensureDynamoDBStream(c, l, "payments")
		}},
		{"wait stream", []startupReply{
			{operation: "DescribeTable", body: `{"Table":{}}`}, {operation: "UpdateTable", body: `{}`}, failed("DescribeTable"),
		}, func(c *dynamodb.Client, l *zap.Logger) {
			ensureDynamoDBStream(c, l, "payments")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := startupClient(t, test.replies...)
			logger := zap.NewNop().WithOptions(zap.WithFatalHook(zapcore.WriteThenPanic))
			defer func() {
				if recover() == nil {
					t.Error("startup failure did not stop initialization")
				}
			}()
			test.run(client, logger)
		})
	}
}

func TestSeedAccountsCreatesMissingAndPreservesExisting(t *testing.T) {
	seedAccounts(zap.NewNop(), nil, &env.Env{CreateResources: false})
	balances := []string{"10000", "5000", "500", "100", "0"}
	replies := make([]startupReply, 0, 10)
	for index := 0; index < 10; index++ {
		index := index
		replies = append(replies, startupReply{operation: "PutItem", body: `{}`, check: func(t *testing.T, body map[string]any) {
			item := body["Item"].(map[string]any)
			currency := "USD"
			if index >= 5 {
				currency = "EUR"
			}
			if item["currentBalance"].(map[string]any)["N"] != balances[index%5] ||
				item["availableBalance"].(map[string]any)["N"] != balances[index%5] ||
				item["currency"].(map[string]any)["S"] != currency ||
				body["ConditionExpression"] != "attribute_not_exists(PK)" {
				t.Fatalf("incorrect seed account %#v", body)
			}
		}})
	}
	client := startupClient(t, replies...)
	seedAccounts(zap.NewNop(), aws.NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments"}), &env.Env{CreateResources: true})

	for index := range replies {
		replies[index] = startupReply{operation: "PutItem", status: 400, body: `{"__type":"ConditionalCheckFailedException"}`}
	}
	client = startupClient(t, replies...)
	seedAccounts(zap.NewNop(), aws.NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments"}), &env.Env{CreateResources: true})
}

func TestSeedAccountsPropagatesStorageFailure(t *testing.T) {
	client := startupClient(t, startupReply{operation: "PutItem", status: 400, body: `{"__type":"AccessDeniedException"}`})
	logger := zap.NewNop().WithOptions(zap.WithFatalHook(zapcore.WriteThenPanic))
	defer func() {
		if recover() == nil {
			t.Error("seeding failure did not stop startup")
		}
	}()
	seedAccounts(logger, aws.NewRepository(logger, client, &env.Env{TableName: "payments"}), &env.Env{CreateResources: true})
}
