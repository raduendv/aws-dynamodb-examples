package aws

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams"
	streamtypes "github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
	"instantpayments/pkg/workerpool"
	workerpooljob "instantpayments/pkg/workerpool/job"
)

type streamHookLifecycle struct {
	hooks []fx.Hook
}

func (lifecycle *streamHookLifecycle) Append(hook fx.Hook) {
	lifecycle.hooks = append(lifecycle.hooks, hook)
}

func testStreamClient(t *testing.T, responder func(*http.Request) (int, string)) *dynamodbstreams.Client {
	t.Helper()
	configuration := awssdk.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("key", "secret", ""),
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			status, body := responder(request)
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/x-amz-json-1.0"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})},
	}
	return dynamodbstreams.NewFromConfig(configuration, func(options *dynamodbstreams.Options) {
		options.BaseEndpoint = awssdk.String("http://streams.test")
	})
}

func TestStreamProcessorReturnsWhenNoStreamsExist(t *testing.T) {
	client := testStreamClient(t, func(request *http.Request) (int, string) {
		if !strings.HasSuffix(request.Header.Get("X-Amz-Target"), ".ListStreams") {
			t.Fatalf("unexpected request %q", request.Header.Get("X-Amz-Target"))
		}
		return http.StatusOK, `{"Streams":[]}`
	})
	processor := &StreamProcessor{ctx: context.Background(), log: zap.NewNop(), client: client, tableName: "payments", dispatcher: &workerpool.WorkerPool{JobPool: make(chan workerpooljob.Job, 1)}}
	processor.Start()
}

func TestStreamProcessorStopsAfterListError(t *testing.T) {
	client := testStreamClient(t, func(*http.Request) (int, string) {
		return http.StatusInternalServerError, `{"__type":"InternalServerError","message":"boom"}`
	})
	processor := &StreamProcessor{ctx: context.Background(), log: zap.NewNop(), client: client, tableName: "payments", dispatcher: &workerpool.WorkerPool{JobPool: make(chan workerpooljob.Job, 1)}}
	processor.Start()
}

func TestStreamProcessorHandlesPaginationAndDescribeError(t *testing.T) {
	listCalls := 0
	client := testStreamClient(t, func(request *http.Request) (int, string) {
		switch {
		case strings.HasSuffix(request.Header.Get("X-Amz-Target"), ".ListStreams"):
			listCalls++
			if listCalls == 1 {
				return http.StatusOK, `{"Streams":[{"StreamArn":"arn:first"}],"LastEvaluatedStreamArn":"arn:next"}`
			}
			return http.StatusOK, `{"Streams":[{"StreamArn":"arn:second"}]}`
		case strings.HasSuffix(request.Header.Get("X-Amz-Target"), ".DescribeStream"):
			return http.StatusInternalServerError, `{"__type":"InternalServerError","message":"boom"}`
		default:
			t.Fatalf("unexpected request %q", request.Header.Get("X-Amz-Target"))
			return http.StatusInternalServerError, `{}`
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	processor := &StreamProcessor{ctx: ctx, log: zap.NewNop(), client: client, tableName: "payments", dispatcher: &workerpool.WorkerPool{JobPool: make(chan workerpooljob.Job, 1)}}
	done := make(chan struct{})
	go func() { processor.Start(); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream processor did not stop after cancellation")
	}
	if listCalls != 2 {
		t.Fatalf("expected paginated ListStreams calls, got %d", listCalls)
	}
}

func TestProcessShardReadsUntilClosed(t *testing.T) {
	client := testStreamClient(t, func(request *http.Request) (int, string) {
		switch {
		case strings.HasSuffix(request.Header.Get("X-Amz-Target"), ".GetShardIterator"):
			return http.StatusOK, `{"ShardIterator":"iterator"}`
		case strings.HasSuffix(request.Header.Get("X-Amz-Target"), ".GetRecords"):
			return http.StatusOK, `{"Records":[]}`
		default:
			t.Fatalf("unexpected request %q", request.Header.Get("X-Amz-Target"))
			return http.StatusInternalServerError, `{}`
		}
	})
	processor := &StreamProcessor{ctx: context.Background(), log: zap.NewNop(), client: client, dispatcher: &workerpool.WorkerPool{JobPool: make(chan workerpooljob.Job, 1)}}
	processor.processShard(awssdk.String("arn:stream"), streamtypes.Shard{ShardId: awssdk.String("shard")})
}

func TestNewStreamsWorkerRegistersLifecycle(t *testing.T) {
	lifecycle := &streamHookLifecycle{}
	client := testStreamClient(t, func(*http.Request) (int, string) { return http.StatusOK, `{"Streams":[]}` })
	processor := NewStreamsWorker(lifecycle, client, &env.Env{TableName: "payments"}, zap.NewNop(), &workerpool.WorkerPool{JobPool: make(chan workerpooljob.Job, 1)})
	if processor == nil || len(lifecycle.hooks) != 1 {
		t.Fatalf("unexpected stream worker setup %#v hooks=%d", processor, len(lifecycle.hooks))
	}
	hook := lifecycle.hooks[0]
	if err := hook.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := hook.OnStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
}
