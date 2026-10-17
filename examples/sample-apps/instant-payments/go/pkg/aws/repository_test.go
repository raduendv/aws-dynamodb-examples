package aws

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
	"instantpayments/pkg/model"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

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
	record, err := repository.CreateIdempotencyRecord("key-123", "hash-456")
	if err != nil {
		t.Fatal(err)
	}
	if record.PK != "IDEMPOTENCY#key-123" || record.SK != "IDEMPOTENCY#key-123" || record.RequestHash != "hash-456" {
		t.Fatalf("unexpected idempotency record %#v", record)
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

	account, err := repository.GetAccount(context.Background(), "ACCOUNT#missing", "ACCOUNT#missing")
	if err != nil || account != nil {
		t.Fatalf("expected empty account, got %#v, %v", account, err)
	}
	newAccount := &model.Account{Item: model.Item{PK: "ACCOUNT#alice", SK: "ACCOUNT#alice"}, Name: "Alice", Balance: 100, Currency: "USD", Status: model.AccountStatusActive}
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

func TestRepositoryUnmarshalsAccountAndMissingPayment(t *testing.T) {
	client := testDynamoClient(t, func(*http.Request) string {
		return `{"Item":{}}`
	})
	repository := NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})
	account, err := repository.GetAccount(context.Background(), "ACCOUNT#alice", "ACCOUNT#alice")
	if err != nil || account != nil {
		t.Fatalf("expected missing account, got %#v, %v", account, err)
	}
	payment, err := repository.GetPayment(context.Background(), "payment")
	if err != nil || payment != nil {
		t.Fatalf("expected missing payment, got %#v, %v", payment, err)
	}
}
