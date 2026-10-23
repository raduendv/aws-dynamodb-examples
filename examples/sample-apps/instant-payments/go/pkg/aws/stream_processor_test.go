// This suite verifies stream discovery, iterator recovery, ordered dispatch,
// bounded retries, logging, and lifecycle behavior without DynamoDB Local.
package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams"
	streamtypes "github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"instantpayments/pkg/env"
	"instantpayments/pkg/workerpool"
	workerpooljob "instantpayments/pkg/workerpool/job"
)

// streamOutcome supplies a classified business error without importing the service.
// The service imports this package, so concrete service errors are tested at the worker boundary.
type streamOutcome struct{ expected bool }

// Error supplies stable text that is deliberately unrelated to classification.
func (e streamOutcome) Error() string { return "controlled stream outcome" }

// ExpectedOutcome marks whether the worker result is a routine business outcome.
func (e streamOutcome) ExpectedOutcome() bool { return e.expected }

func TestProcessRecordClassifiesRetryLogsWithoutChangingRetryPolicy(t *testing.T) {
	for _, expected := range []bool{true, false} {
		t.Run(fmt.Sprintf("expected=%v", expected), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			jobs := make(chan workerpooljob.Job)
			processor := testProcessor(ctx, nil, jobs)
			core, logs := observer.New(zap.DebugLevel)
			processor.log = zap.New(core)
			done := make(chan error, 1)
			go func() {
				done <- processor.processRecord(streamtypes.Shard{}, streamtypes.Record{})
			}()
			for attempt := 0; attempt < maxProcessRetries; attempt++ {
				select {
				case queued := <-jobs:
					queued.(*workerpooljob.IdentifyJob).Result <- fmt.Errorf("wrapped: %w", streamOutcome{expected: expected})
				case <-ctx.Done():
					t.Fatal("stream retry was not dispatched")
				}
			}
			if err := <-done; !errors.Is(err, errStreamRecordSkipped) {
				t.Fatalf("poison skip must be distinct from success: %v", err)
			}
			level := zap.WarnLevel
			if expected {
				level = zap.DebugLevel
			}
			retries := logs.FilterMessage("retrying failed stream record").All()
			if len(retries) != maxProcessRetries-1 {
				t.Fatalf("retry logs = %d, want %d", len(retries), maxProcessRetries-1)
			}
			for _, entry := range retries {
				if entry.Level != level {
					t.Errorf("retry level = %s, want %s", entry.Level, level)
				}
			}
			skips := logs.FilterMessage("skipping poison stream record after bounded retries").All()
			if len(skips) != 1 || skips[0].Level != zap.ErrorLevel {
				t.Fatalf("exhausted poison retry must remain an error: %#v", skips)
			}
		})
	}
}

// streamHookLifecycle records poller lifecycle hooks for explicit test execution.
type streamHookLifecycle struct {
	hooks []fx.Hook
}

// Append captures a hook without starting application components.
func (lifecycle *streamHookLifecycle) Append(hook fx.Hook) {
	lifecycle.hooks = append(lifecycle.hooks, hook)
}

// streamResponder supplies the HTTP status and JSON body for a Streams request.
type streamResponder func(*http.Request) (int, string)

// streamRoundTripperFunc adapts a Streams request fixture to http.RoundTripper.
type streamRoundTripperFunc func(*http.Request) (*http.Response, error)

// RoundTrip delegates one Streams request to the fixture.
func (function streamRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

// testStreamClient disables SDK retries and supplies fixture responses without network access.
func testStreamClient(t *testing.T, responder streamResponder) *dynamodbstreams.Client {
	t.Helper()
	configuration := awssdk.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("key", "secret", ""),
		HTTPClient: &http.Client{Transport: streamRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
			status, body := responder(request)
			return &http.Response{
				StatusCode: status,
				Header:     http.Header{"Content-Type": []string{"application/x-amz-json-1.0"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    request,
			}, nil
		})},
	}
	return dynamodbstreams.NewFromConfig(configuration, func(options *dynamodbstreams.Options) {
		options.BaseEndpoint = awssdk.String("http://streams.test")
		options.RetryMaxAttempts = 1
	})
}

// requestTarget matches the SDK operation suffix in a Streams target header.
func requestTarget(request *http.Request, operation string) bool {
	return strings.HasSuffix(request.Header.Get("X-Amz-Target"), "."+operation)
}

// requestBody decodes an SDK request body and fails the test on malformed JSON.
func requestBody(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s input: %v", request.Header.Get("X-Amz-Target"), err)
	}
	return decoded
}

// testProcessor returns isolated poller state with caller-controlled dispatch and cancellation.
func testProcessor(ctx context.Context, client *dynamodbstreams.Client, jobs chan workerpooljob.Job) *StreamProcessor {
	return &StreamProcessor{
		ctx:         ctx,
		log:         zap.NewNop(),
		client:      client,
		tableName:   "payments",
		iterator:    streamtypes.ShardIteratorTypeLatest,
		enabled:     true,
		dispatcher:  &workerpool.WorkerPool{JobPool: jobs},
		shards:      make(map[string]*streamShard),
		checkpoints: make(map[string]string),
		retries:     newRetryCountLRU(),
	}
}

// ackStreamJobs acknowledges dispatched records successfully until its returned stop
// function is called. It isolates poller tests from business-worker execution.
func ackStreamJobs(jobs <-chan workerpooljob.Job) func() {
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		for {
			select {
			case <-stop:
				return
			case queued := <-jobs:
				queued.(*workerpooljob.IdentifyJob).Result <- nil
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}

func TestDescribeStreamPagesAndPrunesStaleShardCheckpoints(t *testing.T) {
	describeCalls := 0
	client := testStreamClient(t, func(request *http.Request) (int, string) {
		if !requestTarget(request, "DescribeStream") {
			t.Fatalf("unexpected operation %s", request.Header.Get("X-Amz-Target"))
		}
		input := requestBody(t, request)
		if input["Limit"] != float64(maxShardsPerDescribePage) {
			t.Fatalf("DescribeStream limit = %v", input["Limit"])
		}
		describeCalls++
		if describeCalls == 1 {
			if _, exists := input["ExclusiveStartShardId"]; exists {
				t.Fatalf("first page included start shard: %#v", input)
			}
			return 200, `{"StreamDescription":{"Shards":[{"ShardId":"shard-1"}],"LastEvaluatedShardId":"shard-1"}}`
		}
		if input["ExclusiveStartShardId"] != "shard-1" {
			t.Fatalf("second page start shard = %v", input["ExclusiveStartShardId"])
		}
		return 200, `{"StreamDescription":{"Shards":[{"ShardId":"shard-2"}]}}`
	})
	processor := testProcessor(context.Background(), client, make(chan workerpooljob.Job, 1))
	streamArn := awssdk.String("arn:stream")
	retainedKey := streamShardCheckpointKey(streamArn, awssdk.String("shard-1"))
	staleKey := streamShardCheckpointKey(streamArn, awssdk.String("stale"))
	processor.shards[retainedKey] = &streamShard{streamARN: streamArn, shard: streamtypes.Shard{ShardId: awssdk.String("shard-1")}, iterator: awssdk.String("keep")}
	processor.checkpoints[retainedKey] = "100"
	processor.shards[staleKey] = &streamShard{streamARN: streamArn, shard: streamtypes.Shard{ShardId: awssdk.String("stale")}}
	processor.checkpoints[staleKey] = "old"

	if err := processor.discoverShards([]streamtypes.Stream{{StreamArn: streamArn}}); err != nil {
		t.Fatal(err)
	}
	if describeCalls != 2 || processor.shards[retainedKey].iterator == nil || awssdk.ToString(processor.shards[retainedKey].iterator) != "keep" {
		t.Fatalf("discovery did not page/preserve state: calls=%d shards=%#v", describeCalls, processor.shards)
	}
	if _, exists := processor.checkpoints[staleKey]; exists {
		t.Fatal("stale shard checkpoint was not pruned")
	}
	if _, exists := processor.shards[streamShardCheckpointKey(streamArn, awssdk.String("shard-2"))]; !exists {
		t.Fatal("second-page shard was not discovered")
	}
}

func TestDiscoveryRunsAgainOnEveryPoll(t *testing.T) {
	listCalls, describeCalls := 0, 0
	client := testStreamClient(t, func(request *http.Request) (int, string) {
		switch {
		case requestTarget(request, "ListStreams"):
			listCalls++
			return 200, `{"Streams":[{"StreamArn":"arn:stream"}]}`
		case requestTarget(request, "DescribeStream"):
			describeCalls++
			return 200, `{"StreamDescription":{"Shards":[]}}`
		default:
			t.Fatalf("unexpected operation %s", request.Header.Get("X-Amz-Target"))
			return 500, `{}`
		}
	})
	processor := testProcessor(context.Background(), client, make(chan workerpooljob.Job, 1))
	processor.pollOnce()
	processor.pollOnce()
	if listCalls != 2 || describeCalls != 2 {
		t.Fatalf("discovery calls list=%d describe=%d, want 2 each", listCalls, describeCalls)
	}
}

func TestExpiredIteratorRenewsAfterLastSuccessfulSequence(t *testing.T) {
	var iteratorInput map[string]any
	getRecordsCalls := 0
	client := testStreamClient(t, func(request *http.Request) (int, string) {
		switch {
		case requestTarget(request, "GetRecords"):
			getRecordsCalls++
			if getRecordsCalls == 1 {
				return 400, `{"__type":"ExpiredIteratorException","message":"expired"}`
			}
			return 200, `{"Records":[],"NextShardIterator":"next"}`
		case requestTarget(request, "GetShardIterator"):
			iteratorInput = requestBody(t, request)
			return 200, `{"ShardIterator":"renewed"}`
		default:
			t.Fatalf("unexpected operation %s", request.Header.Get("X-Amz-Target"))
			return 500, `{}`
		}
	})
	processor := testProcessor(context.Background(), client, make(chan workerpooljob.Job, 1))
	key := "arn:stream#shard"
	shard := &streamShard{streamARN: awssdk.String("arn:stream"), shard: streamtypes.Shard{ShardId: awssdk.String("shard")}, iterator: awssdk.String("expired")}
	processor.shards[key] = shard
	processor.checkpoints[key] = "987"
	processor.pollShard(key, shard)
	processor.pollShard(key, shard)
	if iteratorInput["ShardIteratorType"] != string(streamtypes.ShardIteratorTypeAfterSequenceNumber) || iteratorInput["SequenceNumber"] != "987" {
		t.Fatalf("renewal did not resume after checkpoint: %#v", iteratorInput)
	}
	if getRecordsCalls != 2 {
		t.Fatalf("GetRecords calls = %d, want expired request and resumed request", getRecordsCalls)
	}
}

func TestTrimmedIteratorFallsBackToConfiguredIterator(t *testing.T) {
	var iteratorInput map[string]any
	client := testStreamClient(t, func(request *http.Request) (int, string) {
		switch {
		case requestTarget(request, "GetRecords"):
			return 400, `{"__type":"TrimmedDataAccessException","message":"trimmed"}`
		case requestTarget(request, "GetShardIterator"):
			iteratorInput = requestBody(t, request)
			return 200, `{"ShardIterator":"fresh"}`
		default:
			t.Fatalf("unexpected operation %s", request.Header.Get("X-Amz-Target"))
			return 500, `{}`
		}
	})
	processor := testProcessor(context.Background(), client, make(chan workerpooljob.Job, 1))
	processor.iterator = streamtypes.ShardIteratorTypeTrimHorizon
	key := "arn:stream#shard"
	shard := &streamShard{streamARN: awssdk.String("arn:stream"), shard: streamtypes.Shard{ShardId: awssdk.String("shard")}, iterator: awssdk.String("trimmed")}
	processor.checkpoints[key] = "123"
	processor.pollShard(key, shard)
	if iteratorInput["ShardIteratorType"] != string(streamtypes.ShardIteratorTypeTrimHorizon) {
		t.Fatalf("trimmed data did not use configured fallback: %#v", iteratorInput)
	}
	if _, exists := iteratorInput["SequenceNumber"]; exists {
		t.Fatalf("fallback unexpectedly included sequence number: %#v", iteratorInput)
	}
}

func TestSequenceCheckpointAdvancesOnlyAfterAcknowledgedRecords(t *testing.T) {
	calls := 0
	client := testStreamClient(t, func(request *http.Request) (int, string) {
		if !requestTarget(request, "GetRecords") {
			t.Fatalf("unexpected operation %s", request.Header.Get("X-Amz-Target"))
		}
		calls++
		if calls == 1 {
			return 200, `{"Records":[{"eventID":"first","dynamodb":{"SequenceNumber":"10"}},{"eventID":"second","dynamodb":{"SequenceNumber":"11"}}],"NextShardIterator":"next"}`
		}
		return 200, `{"Records":[],"NextShardIterator":"next-next"}`
	})
	jobs := make(chan workerpooljob.Job)
	processor := testProcessor(context.Background(), client, jobs)
	key := "arn:stream#shard"
	shard := &streamShard{streamARN: awssdk.String("arn:stream"), shard: streamtypes.Shard{ShardId: awssdk.String("shard")}, iterator: awssdk.String("start")}
	done := make(chan struct{})
	go func() {
		processor.pollShard(key, shard)
		close(done)
	}()
	first := <-jobs
	if _, exists := processor.checkpoints[key]; exists {
		t.Fatal("checkpoint advanced before the first acknowledgment")
	}
	first.(*workerpooljob.IdentifyJob).Result <- nil
	second := <-jobs
	if got := processor.checkpoints[key]; got != "10" {
		t.Fatalf("checkpoint after first acknowledgment = %q, want 10", got)
	}
	second.(*workerpooljob.IdentifyJob).Result <- nil
	<-done
	if got := processor.checkpoints[key]; got != "11" {
		t.Fatalf("last-success checkpoint = %q, want 11", got)
	}
	if awssdk.ToString(shard.iterator) != "next-next" {
		t.Fatalf("iterator was not advanced: %q", awssdk.ToString(shard.iterator))
	}
}

func TestPollShardStopsAtPerTickRoundLimit(t *testing.T) {
	rounds := 0
	client := testStreamClient(t, func(request *http.Request) (int, string) {
		if !requestTarget(request, "GetRecords") {
			t.Fatalf("unexpected operation %s", request.Header.Get("X-Amz-Target"))
		}
		rounds++
		return 200, `{"Records":[{"eventID":"event","dynamodb":{"SequenceNumber":"1"}}],"NextShardIterator":"next"}`
	})
	jobs := make(chan workerpooljob.Job, maxGetRecordsRoundsPerShard)
	stopAcker := ackStreamJobs(jobs)
	defer stopAcker()
	processor := testProcessor(context.Background(), client, jobs)
	key := "arn:stream#shard"
	shard := &streamShard{streamARN: awssdk.String("arn:stream"), shard: streamtypes.Shard{ShardId: awssdk.String("shard")}, iterator: awssdk.String("start")}
	processor.pollShard(key, shard)
	if rounds != maxGetRecordsRoundsPerShard {
		t.Fatalf("GetRecords rounds = %d, want %d", rounds, maxGetRecordsRoundsPerShard)
	}
}

func TestPoisonSkipAdvancesIteratorButRenewalUsesOnlySuccessfulCheckpoint(t *testing.T) {
	for _, test := range []struct {
		name           string
		previous       string
		laterSuccess   bool
		wantCheckpoint string
	}{
		{name: "no successful record yet"},
		{name: "last success survives poison", previous: "9", wantCheckpoint: "9"},
		{name: "later success advances checkpoint", previous: "9", laterSuccess: true, wantCheckpoint: "11"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var renewal map[string]any
			client := testStreamClient(t, func(request *http.Request) (int, string) {
				if requestTarget(request, "GetShardIterator") {
					renewal = requestBody(t, request)
					return 200, `{"ShardIterator":"renewed"}`
				}
				input := requestBody(t, request)
				switch input["ShardIterator"] {
				case "start":
					records := `{"eventID":"poison","dynamodb":{"SequenceNumber":"10"}}`
					if test.laterSuccess {
						records += `,{"eventID":"success","dynamodb":{"SequenceNumber":"11"}}`
					}
					return 200, `{"Records":[` + records + `],"NextShardIterator":"after-poison"}`
				case "after-poison":
					return 200, `{"Records":[],"NextShardIterator":"expired"}`
				case "expired":
					return 400, `{"__type":"ExpiredIteratorException","message":"expired"}`
				default:
					t.Errorf("unexpected request: %#v", input)
					return 500, `{}`
				}
			})
			jobs := make(chan workerpooljob.Job)
			processor := testProcessor(context.Background(), client, jobs)
			key := "arn:stream#shard"
			if test.previous != "" {
				processor.checkpoints[key] = test.previous
			}
			jobCount := maxProcessRetries
			if test.laterSuccess {
				jobCount++
			}
			acknowledged := make(chan struct{})
			go func() {
				defer close(acknowledged)
				for n := 0; n < jobCount; n++ {
					queued := (<-jobs).(*workerpooljob.IdentifyJob)
					var err error
					if awssdk.ToString(queued.Record.EventID) == "poison" {
						err = errors.New("poison")
					}
					queued.Complete(err)
				}
			}()
			shard := &streamShard{
				streamARN: awssdk.String("arn:stream"),
				shard:     streamtypes.Shard{ShardId: awssdk.String("shard")},
				iterator:  awssdk.String("start"),
			}
			processor.pollShard(key, shard)
			<-acknowledged
			if got := processor.checkpoints[key]; got != test.wantCheckpoint {
				t.Fatalf("successful checkpoint = %q, want %q", got, test.wantCheckpoint)
			}
			if awssdk.ToString(shard.iterator) != "expired" {
				t.Fatalf("poison blocked iterator progress: %v", shard.iterator)
			}
			processor.pollShard(key, shard)
			if test.wantCheckpoint == "" {
				if renewal["ShardIteratorType"] != string(streamtypes.ShardIteratorTypeLatest) || renewal["SequenceNumber"] != nil {
					t.Fatalf("renewal without a success must use configuration: %#v", renewal)
				}
			} else if renewal["ShardIteratorType"] != string(streamtypes.ShardIteratorTypeAfterSequenceNumber) || renewal["SequenceNumber"] != test.wantCheckpoint {
				t.Fatalf("renewal used a poison sequence: %#v", renewal)
			}
		})
	}
}

func TestProcessRecordUsesNamespacedBoundedRetryCounters(t *testing.T) {
	for _, test := range []struct {
		name    string
		record  streamtypes.Record
		wantKey string
	}{
		{
			name: "payment namespace is bare payment id",
			record: streamtypes.Record{EventName: streamtypes.OperationTypeInsert, Dynamodb: &streamtypes.StreamRecord{NewImage: map[string]streamtypes.AttributeValue{
				"entityType": &streamtypes.AttributeValueMemberS{Value: "PAYMENT_EVENT"},
				"eventType":  &streamtypes.AttributeValueMemberS{Value: "OUTBOUND_PAYMENT_CREATED"},
				"paymentId":  &streamtypes.AttributeValueMemberS{Value: "pay-1"},
			}}},
			wantKey: "pay-1",
		},
		{
			name: "release namespace includes account and reservation",
			record: streamtypes.Record{EventName: streamtypes.OperationTypeRemove, Dynamodb: &streamtypes.StreamRecord{Keys: map[string]streamtypes.AttributeValue{
				"PK": &streamtypes.AttributeValueMemberS{Value: "ACCOUNT#acc-1"},
				"SK": &streamtypes.AttributeValueMemberS{Value: "RESERVATION_TEMP#res-1"},
			}}},
			wantKey: "release:acc-1#res-1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			jobs := make(chan workerpooljob.Job)
			processor := testProcessor(context.Background(), nil, jobs)
			done := make(chan error, 1)
			go func() {
				done <- processor.processRecord(streamtypes.Shard{ShardId: awssdk.String("shard")}, test.record)
			}()
			for attempt := 0; attempt < maxProcessRetries; attempt++ {
				job := <-jobs
				job.(*workerpooljob.IdentifyJob).Result <- errors.New("downstream failure")
			}
			if err := <-done; !errors.Is(err, errStreamRecordSkipped) {
				t.Fatalf("poison skip must be distinct from success: %v", err)
			}
			if got := processor.retries.entries[test.wantKey].Value.(*retryCountEntry).count; got != maxProcessRetries {
				t.Fatalf("failure count = %d, want %d", got, maxProcessRetries)
			}
			if got := streamRecordRetryKey(test.record); got != test.wantKey {
				t.Fatalf("retry key = %q, want %q", got, test.wantKey)
			}
		})
	}
}

func TestProcessRecordCancellationUnblocksDispatchAcknowledgmentAndRetryWait(t *testing.T) {
	for _, phase := range []string{"dispatch", "acknowledgment", "retry wait"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			jobs := make(chan workerpooljob.Job)
			processor := testProcessor(ctx, nil, jobs)
			processor.retryDelay = time.Hour
			done := make(chan error, 1)
			go func() { done <- processor.processRecord(streamtypes.Shard{}, streamtypes.Record{}) }()
			if phase != "dispatch" {
				queued := <-jobs
				if phase == "retry wait" {
					queued.(*workerpooljob.IdentifyJob).Result <- errors.New("transient failure")
				}
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("processRecord error = %v, want context canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled record processing did not return")
			}
		})
	}
}

func TestRetryCounterLRUEvictsOldestAtCapacity(t *testing.T) {
	lru := newRetryCountLRU()
	for i := 0; i < maxRetryCountEntries; i++ {
		lru.increment(string(rune(i + 1)))
	}
	lru.increment(string(rune(1))) // Touch the oldest entry so the next key evicts the second.
	lru.increment("overflow")
	if len(lru.entries) != maxRetryCountEntries {
		t.Fatalf("retry store size = %d, want %d", len(lru.entries), maxRetryCountEntries)
	}
	if _, exists := lru.entries[string(rune(1))]; !exists {
		t.Fatal("recently accessed entry was evicted")
	}
	if _, exists := lru.entries[string(rune(2))]; exists {
		t.Fatal("least recently used entry was retained")
	}
}

// shutdownPaymentProcessor lets lifecycle tests hold business work across intake cancellation.
type shutdownPaymentProcessor struct {
	run func(context.Context, string) (map[string]any, error)
}

// ProcessContext delegates creation processing to the test's controlled business boundary.
func (processor shutdownPaymentProcessor) ProcessContext(ctx context.Context, paymentID string) (map[string]any, error) {
	return processor.run(ctx, paymentID)
}

// ReleaseReservation supplies the unused timer-release half of the worker contract.
func (shutdownPaymentProcessor) ReleaseReservation(context.Context, string, string) error {
	return nil
}

func TestStreamShutdownCoordinatesWorkerGraceBeforeReverseOrderPoolHook(t *testing.T) {
	for _, forced := range []bool{false, true} {
		t.Run(map[bool]string{false: "business success during grace", true: "deadline forces business cancellation"}[forced], func(t *testing.T) {
			started := make(chan context.Context, 1)
			release, finished := make(chan struct{}), make(chan error, 1)
			processor := shutdownPaymentProcessor{run: func(ctx context.Context, _ string) (map[string]any, error) {
				started <- ctx
				select {
				case <-release:
					finished <- nil
					return nil, nil
				case <-ctx.Done():
					finished <- ctx.Err()
					return nil, ctx.Err()
				}
			}}
			client := testStreamClient(t, func(request *http.Request) (int, string) {
				switch {
				case requestTarget(request, "ListStreams"):
					return 200, `{"Streams":[{"StreamArn":"arn:stream"}]}`
				case requestTarget(request, "DescribeStream"):
					return 200, `{"StreamDescription":{"Shards":[{"ShardId":"shard"}]}}`
				case requestTarget(request, "GetShardIterator"):
					return 200, `{"ShardIterator":"start"}`
				case requestTarget(request, "GetRecords"):
					return 200, `{"Records":[{"eventID":"creation","eventName":"INSERT","dynamodb":{"SequenceNumber":"10","Keys":{"PK":{"S":"PAYMENT#pay_1"},"SK":{"S":"EVENT#0000000000000000001"}},"NewImage":{"entityType":{"S":"PAYMENT_EVENT"},"eventType":{"S":"OUTBOUND_PAYMENT_CREATED"},"paymentId":{"S":"pay_1"}}}}],"NextShardIterator":"next"}`
				default:
					t.Errorf("unexpected stream operation: %s", request.Header.Get("X-Amz-Target"))
					return 500, `{}`
				}
			})
			lifecycle := &streamHookLifecycle{}
			settings := &env.Env{StreamsEnabled: true, WorkerPoolSize: 1, JobPoolSize: 1}
			pool := workerpool.NewDispatcher(lifecycle, settings, zap.NewNop(), nil, processor)
			poller := NewStreamsWorker(lifecycle, client, settings, zap.NewNop(), pool)
			poller.initialDelay = time.Millisecond
			for _, hook := range lifecycle.hooks {
				if err := hook.OnStart(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			var businessCtx context.Context
			select {
			case businessCtx = <-started:
			case <-time.After(time.Second):
				t.Fatal("stream business processing did not start")
			}
			limit := time.Second
			if forced {
				limit = 40 * time.Millisecond
			}
			stopCtx, cancel := context.WithTimeout(context.Background(), limit)
			defer cancel()
			stopped := make(chan error, 1)
			go func() { stopped <- lifecycle.hooks[1].OnStop(stopCtx) }()
			<-poller.ctx.Done()
			if err := businessCtx.Err(); err != nil {
				t.Fatalf("intake cancellation abandoned admitted work: %v", err)
			}
			if !forced {
				close(release)
			}
			err := <-stopped
			if forced {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("forced stream shutdown must report timeout: %v", err)
				}
			} else if err != nil {
				t.Fatalf("stream grace did not complete: %v", err)
			}
			select {
			case err := <-finished:
				if forced && !errors.Is(err, context.Canceled) {
					t.Fatalf("timeout did not cancel business context: %v", err)
				}
				if !forced && err != nil {
					t.Fatalf("graceful business operation failed: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("business operation did not finish")
			}
			select {
			case <-poller.done:
			case <-time.After(time.Second):
				t.Fatal("poller acknowledgment did not finish")
			}
			if got := poller.checkpoints["arn:stream#shard"]; !forced && got != "10" {
				t.Fatalf("graceful success was not acknowledged: checkpoint=%q", got)
			}
			// Fx invokes the worker dependency only after the stream hook. This
			// second hook must not begin a separate grace period after a timeout.
			poolStopCtx, poolStopCancel := context.WithTimeout(context.Background(), time.Second)
			defer poolStopCancel()
			if err := lifecycle.hooks[0].OnStop(poolStopCtx); !forced && err != nil {
				t.Fatalf("reverse-order worker stop failed: %v", err)
			}
		})
	}
}

func TestStreamProcessorLifecycleHonorsDisabledAndShutdown(t *testing.T) {
	lifecycle := &streamHookLifecycle{}
	disabled := NewStreamsWorker(lifecycle, nil, &env.Env{StreamIteratorType: "TRIM_HORIZON"}, zap.NewNop(), nil)
	if len(lifecycle.hooks) != 0 || disabled.ctx.Err() != context.Canceled || disabled.iterator != streamtypes.ShardIteratorTypeTrimHorizon {
		t.Fatalf("disabled worker should not register hooks: %#v", disabled)
	}
	disabled.Start()

	client := testStreamClient(t, func(request *http.Request) (int, string) {
		if requestTarget(request, "ListStreams") {
			return 200, `{"Streams":[]}`
		}
		t.Fatalf("unexpected operation %s", request.Header.Get("X-Amz-Target"))
		return 500, `{}`
	})
	pool := workerpool.NewDispatcher(&streamHookLifecycle{}, &env.Env{WorkerPoolSize: 1, JobPoolSize: 1}, zap.NewNop(), nil, nil)
	enabled := NewStreamsWorker(lifecycle, client, &env.Env{StreamsEnabled: true}, zap.NewNop(), pool)
	if enabled.initialDelay != streamInitialPollDelay || enabled.iterator != streamtypes.ShardIteratorTypeLatest {
		t.Fatalf("unexpected configured defaults: delay=%v iterator=%s", enabled.initialDelay, enabled.iterator)
	}
	if err := lifecycle.hooks[0].OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.hooks[0].OnStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-enabled.done:
	default:
		t.Fatal("shutdown returned before poller exited")
	}
}

func TestStreamProcessorShutdownUsesBoundedContext(t *testing.T) {
	lifecycle := &streamHookLifecycle{}
	client := testStreamClient(t, func(request *http.Request) (int, string) {
		return 200, `{"Streams":[]}`
	})
	pool := workerpool.NewDispatcher(&streamHookLifecycle{}, &env.Env{WorkerPoolSize: 1, JobPoolSize: 1}, zap.NewNop(), nil, nil)
	NewStreamsWorker(lifecycle, client, &env.Env{StreamsEnabled: true}, zap.NewNop(), pool)
	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lifecycle.hooks[0].OnStop(stopCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown did not honor canceled parent context: %v", err)
	}
}

func TestConfigureStreamsDoesNotConstructOrRegisterWhenDisabled(t *testing.T) {
	lifecycle := &streamHookLifecycle{}
	ConfigureStreams(lifecycle, nil, &env.Env{StreamsEnabled: false}, nil, nil)
	if len(lifecycle.hooks) != 0 {
		t.Fatalf("disabled stream configuration registered %d lifecycle hooks", len(lifecycle.hooks))
	}
}

func TestPollShardStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := testStreamClient(t, func(request *http.Request) (int, string) {
		if requestTarget(request, "GetRecords") {
			cancel()
			return 200, `{"Records":[],"NextShardIterator":"next"}`
		}
		t.Fatalf("unexpected operation %s", request.Header.Get("X-Amz-Target"))
		return 500, `{}`
	})
	processor := testProcessor(ctx, client, make(chan workerpooljob.Job, 1))
	shard := &streamShard{streamARN: awssdk.String("arn:stream"), shard: streamtypes.Shard{ShardId: awssdk.String("shard")}, iterator: awssdk.String("iterator")}
	processor.pollShard("key", shard)
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("fake request did not cancel context")
	}
}

func TestStreamPollingConstantsMatchContract(t *testing.T) {
	if streamInitialPollDelay != 2*time.Second || streamPollInterval != time.Second || streamRecordLimit != 100 ||
		maxGetRecordsRoundsPerShard != 512 || maxProcessRetries != 3 || maxRetryCountEntries != 1024 ||
		workerpool.ShutdownGracePeriod != 10*time.Second || maxShardsPerDescribePage != 100 {
		t.Fatal("stream polling tuning constants differ from the contract")
	}
}

func TestStreamPollerWaitsForInitialDelayAndTick(t *testing.T) {
	const initialDelay = 120 * time.Millisecond
	const interval = 150 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	polls := make(chan time.Time, 3)
	started := time.Now()
	go runStreamPoller(ctx, initialDelay, interval, func() {
		polls <- time.Now()
	})

	select {
	case calledAt := <-polls:
		t.Fatalf("poll ran before startup delay: elapsed %v", calledAt.Sub(started))
	case <-time.After(initialDelay / 2):
	}
	var first time.Time
	select {
	case first = <-polls:
	case <-time.After(time.Second):
		t.Fatal("initial poll did not run after startup delay")
	}
	if elapsed := first.Sub(started); elapsed < initialDelay {
		t.Fatalf("initial poll ran after %v, before %v startup delay", elapsed, initialDelay)
	}

	select {
	case calledAt := <-polls:
		t.Fatalf("next empty poll ran too early: %v after first poll", calledAt.Sub(first))
	case <-time.After(interval / 2):
	}
	select {
	case second := <-polls:
		if elapsed := second.Sub(first); elapsed < interval {
			t.Fatalf("next poll ran after %v, before %v interval", elapsed, interval)
		}
	case <-time.After(time.Second):
		t.Fatal("next poll did not run on the scheduled tick")
	}
	cancel()
}

func TestAwaitStreamShutdownHonorsCallerAndConfiguredDeadlines(t *testing.T) {
	t.Run("caller deadline wins", func(t *testing.T) {
		const callerLimit = 60 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), callerLimit)
		defer cancel()
		started := time.Now()
		err := awaitStreamShutdown(ctx, make(chan struct{}), time.Second)
		elapsed := time.Since(started)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown error = %v, want caller deadline", err)
		}
		if elapsed < callerLimit || elapsed > 500*time.Millisecond {
			t.Fatalf("caller-bounded shutdown took %v", elapsed)
		}
	})

	t.Run("configured limit wins", func(t *testing.T) {
		const maxWait = 60 * time.Millisecond
		started := time.Now()
		err := awaitStreamShutdown(context.Background(), make(chan struct{}), maxWait)
		elapsed := time.Since(started)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown error = %v, want configured timeout", err)
		}
		if elapsed < maxWait || elapsed > 500*time.Millisecond {
			t.Fatalf("configured-bounded shutdown took %v", elapsed)
		}
	})

	t.Run("completed worker returns immediately", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		if err := awaitStreamShutdown(context.Background(), done, 10*time.Second); err != nil {
			t.Fatalf("completed worker shutdown failed: %v", err)
		}
	})
}
