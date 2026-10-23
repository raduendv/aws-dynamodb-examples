// This suite verifies whole-call deadlines and cancellation reaching both SDK transports.
package aws

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams"
	"instantpayments/pkg/env"
)

func TestConfiguredClientsBoundWholeCalls(t *testing.T) {
	for _, streams := range []bool{false, true} {
		t.Run(map[bool]string{false: "DynamoDB", true: "Streams"}[streams], func(t *testing.T) {
			cfg, err := Config(&env.Env{
				AWSRegion: "eu-west-1", DynamoDBEndpoint: "http://localhost:8000", DynamoDBClientType: "high-level",
			})
			if err != nil {
				t.Fatal(err)
			}
			cfg.HTTPClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				deadline, ok := request.Context().Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Error("configured SDK call is missing its five-second deadline")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
			})}
			if streams {
				_, err = DynamoDBStreams(cfg).ListStreams(context.Background(), &dynamodbstreams.ListStreamsInput{})
			} else {
				_, err = DynamoDB(cfg).ListTables(context.Background(), &dynamodb.ListTablesInput{})
			}
			if err != nil {
				t.Fatalf("configured client call failed: %v", err)
			}
			// Exercise the installed middleware with a short deadline while retries are enabled.
			cfg.APIOptions[len(cfg.APIOptions)-1] = withCallTimeout(20 * time.Millisecond)
			cfg.HTTPClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, request.Context().Err()
			})}
			start := time.Now()
			if streams {
				_, err = DynamoDBStreams(cfg).ListStreams(context.Background(), &dynamodbstreams.ListStreamsInput{})
			} else {
				_, err = DynamoDB(cfg).ListTables(context.Background(), &dynamodb.ListTablesInput{})
			}
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
				t.Fatalf("SDK retry loop escaped its call deadline: %v (elapsed %v)", err, time.Since(start))
			}
		})
	}
}

func TestConfiguredCallCancellationReachesTransport(t *testing.T) {
	cfg, err := Config(&env.Env{
		AWSRegion: "eu-west-1", DynamoDBEndpoint: "http://localhost:8000", DynamoDBClientType: "high-level",
	})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	cfg.HTTPClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := DynamoDB(cfg).GetItem(ctx, &dynamodb.GetItemInput{
			TableName: awssdk.String("payments-test"),
			Key:       map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: "ACCOUNT#acc_1"}},
		})
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("SDK call returned before reaching the transport: %v", err)
	case <-ctx.Done():
		t.Fatal("transport was not called")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled SDK request, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SDK request ignored cancellation")
	}
}
