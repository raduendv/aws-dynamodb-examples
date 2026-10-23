// This suite verifies cancellation during lock acquisition, settlement I/O,
// retry waits, and worker shutdown using controlled blocking transports.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	streamtypes "github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"instantpayments/pkg/aws"
	"instantpayments/pkg/env"
	"instantpayments/pkg/workerpool"
	"instantpayments/pkg/workerpool/job"
)

// lockWaitingContext signals the first cancellation check during lock acquisition.
type lockWaitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

// Done announces the first check before returning the underlying cancellation channel.
func (ctx *lockWaitingContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func TestProcessContextCancelsWhileWaitingForLock(t *testing.T) {
	payment := newTestPayment()
	payment.mu.Lock()
	defer payment.mu.Unlock()
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancellation", true: "deadline"}[deadline], func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			if deadline {
				cancel()
				parent, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
			}
			defer cancel()
			ctx := &lockWaitingContext{Context: parent, waiting: make(chan struct{})}
			done := make(chan error, 1)
			go func() {
				_, err := payment.ProcessContext(ctx, "pay_1")
				done <- err
			}()
			select {
			case <-ctx.waiting:
			case <-time.After(time.Second):
				t.Fatal("processing did not reach cancellable lock acquisition")
			}
			if !deadline {
				cancel()
			}
			want := context.Canceled
			if deadline {
				want = context.DeadlineExceeded
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatalf("expected %v, got %v", want, err)
				}
			case <-time.After(time.Second):
				t.Fatal("processing waited for the lock after its context ended")
			}
		})
	}
}

// blockingPaymentService blocks the selected settlement stage until its request
// context ends and signals when that stage is reached.
func blockingPaymentService(t *testing.T, stage string) (*Payment, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	transactions := 0
	client := dynamodb.NewFromConfig(awssdk.Config{
		Region: "eu-west-1", RetryMaxAttempts: 1,
		Credentials: credentials.NewStaticCredentialsProvider("local", "local", ""),
		HTTPClient: &http.Client{Transport: paymentTestTransport(func(request *http.Request) (*http.Response, error) {
			target := strings.TrimPrefix(request.Header.Get("X-Amz-Target"), "DynamoDB_20120810.")
			var response any
			block := false
			switch target {
			case "Query":
				block = stage == "load payment"
				response = paymentPartition(t, "RECEIVED", "")
			case "GetItem":
				block = stage == "load account"
				available := 1000
				if stage == "reject" {
					available = 0
				}
				response = accountItem(t, available, transactions+1)
			case "TransactWriteItems":
				transactions++
				block = stage == "reserve" || stage == "reject" || (stage == "complete" && transactions == 2)
				response = map[string]any{}
			default:
				t.Errorf("unexpected request %s", target)
			}
			if block {
				close(started)
				<-request.Context().Done()
				return nil, request.Context().Err()
			}
			payload, err := json.Marshal(response)
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(string(payload))), Request: request,
			}, nil
		})},
	}, func(options *dynamodb.Options) {
		options.BaseEndpoint = awssdk.String("http://dynamodb.test")
	})
	return NewPayment(zap.NewNop(), aws.NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"})), started
}

func TestProcessContextCancelsEverySettlementStage(t *testing.T) {
	for _, stage := range []string{"load payment", "load account", "reserve", "complete", "reject"} {
		t.Run(stage, func(t *testing.T) {
			payment, started := blockingPaymentService(t, stage)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := payment.ProcessContext(ctx, "pay_1")
				done <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("processing never reached the blocking request")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("expected cancellation, got %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("processing did not cancel promptly")
			}
			if !payment.mu.TryLock() {
				t.Fatal("canceled processing retained the payment mutex")
			}
			payment.mu.Unlock()
		})
	}
}

func TestPaymentRetryWaitHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForRetry(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled retry wait, got %v", err)
	}
	if _, err := newTestPayment().ProcessContext(ctx, "pay_1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled processing before lookup, got %v", err)
	}
}

// paymentWorkerLifecycle captures worker hooks for cancellation tests.
type paymentWorkerLifecycle struct{ hooks []fx.Hook }

// Append records a lifecycle hook for explicit invocation.
func (lifecycle *paymentWorkerLifecycle) Append(hook fx.Hook) {
	lifecycle.hooks = append(lifecycle.hooks, hook)
}

// paymentCreationJob supplies a valid creation event and a buffered result acknowledgment.
func paymentCreationJob(result chan error) *job.IdentifyJob {
	return &job.IdentifyJob{
		Result: result,
		Record: streamtypes.Record{
			EventName: streamtypes.OperationTypeInsert,
			Dynamodb: &streamtypes.StreamRecord{
				Keys: map[string]streamtypes.AttributeValue{
					"PK": &streamtypes.AttributeValueMemberS{Value: "PAYMENT#pay_1"},
					"SK": &streamtypes.AttributeValueMemberS{Value: "EVENT#0000000000000000001"},
				},
				NewImage: map[string]streamtypes.AttributeValue{
					"entityType": &streamtypes.AttributeValueMemberS{Value: "PAYMENT_EVENT"},
					"eventType":  &streamtypes.AttributeValueMemberS{Value: "OUTBOUND_PAYMENT_CREATED"},
					"paymentId":  &streamtypes.AttributeValueMemberS{Value: "pay_1"},
				},
			},
		},
	}
}

func TestWorkerShutdownAllowsInFlightPaymentRequestToCompleteDuringGrace(t *testing.T) {
	started, release := make(chan context.Context, 1), make(chan struct{})
	client := dynamodb.NewFromConfig(awssdk.Config{
		Region: "eu-west-1", RetryMaxAttempts: 1,
		Credentials: credentials.NewStaticCredentialsProvider("local", "local", ""),
		HTTPClient: &http.Client{Transport: paymentTestTransport(func(request *http.Request) (*http.Response, error) {
			started <- request.Context()
			select {
			case <-release:
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
			payload, err := json.Marshal(paymentPartition(t, "COMPLETED", ""))
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(string(payload))), Request: request,
			}, nil
		})},
	}, func(options *dynamodb.Options) {
		options.BaseEndpoint = awssdk.String("http://dynamodb.test")
	})
	payment := NewPayment(zap.NewNop(), aws.NewRepository(zap.NewNop(), client, &env.Env{TableName: "payments-test"}))
	lifecycle := &paymentWorkerLifecycle{}
	pool := workerpool.NewDispatcher(lifecycle, &env.Env{WorkerPoolSize: 1, JobPoolSize: 1}, zap.NewNop(), nil, payment)
	result := make(chan error, 1)
	pool.Dispatch(paymentCreationJob(result))
	var requestCtx context.Context
	select {
	case requestCtx = <-started:
	case <-time.After(time.Second):
		t.Fatal("payment did not reach its request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// The context's Done observation signals that Shutdown has closed admission
	// and reached its cancellable grace wait, before the transport is released.
	stopCtx := &lockWaitingContext{Context: ctx, waiting: make(chan struct{})}
	stopped := make(chan error, 1)
	go func() { stopped <- lifecycle.hooks[0].OnStop(stopCtx) }()
	<-stopCtx.waiting
	if err := requestCtx.Err(); err != nil {
		t.Fatalf("grace period canceled the payment request: %v", err)
	}
	close(release)
	if err := <-stopped; err != nil {
		t.Fatalf("payment did not drain during grace: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("payment request failed during grace: %v", err)
	}
}

func TestWorkerShutdownCancelsInFlightPaymentRequest(t *testing.T) {
	payment, started := blockingPaymentService(t, "load payment")
	lifecycle := &paymentWorkerLifecycle{}
	pool := workerpool.NewDispatcher(lifecycle, &env.Env{WorkerPoolSize: 1, JobPoolSize: 1}, zap.NewNop(), nil, payment)
	result := make(chan error, 1)
	pool.Dispatch(paymentCreationJob(result))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker never reached the blocking request")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := lifecycle.hooks[0].OnStop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("forced worker shutdown must report its timeout: %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("job must report cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not acknowledge the canceled job")
	}
}
