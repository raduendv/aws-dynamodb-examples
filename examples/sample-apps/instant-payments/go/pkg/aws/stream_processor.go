package aws

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"github.com/aws/smithy-go"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
	"instantpayments/pkg/model"
	"instantpayments/pkg/workerpool"
	"instantpayments/pkg/workerpool/job"
)

// StreamProcessor polls DynamoDB Streams and dispatches records to workers.
type StreamProcessor struct {
	ctx context.Context
	// acknowledgmentCtx stays live during shutdown while admitted worker jobs drain.
	acknowledgmentCtx context.Context
	log               *zap.Logger
	client            *dynamodbstreams.Client
	tableName         string
	iterator          types.ShardIteratorType
	enabled           bool
	dispatcher        *workerpool.WorkerPool
	done              chan struct{}

	// These timing fields are overridable by package tests; zero initialDelay uses
	// the production startup delay.
	initialDelay time.Duration
	retryDelay   time.Duration
	shards       map[string]*streamShard
	checkpoints  map[string]string
	retries      *retryCountLRU
}

const (
	// maxProcessRetries keeps a transiently failing record from blocking its shard indefinitely.
	maxProcessRetries = 3
	// streamRecordRetryDelay is a modest linear backoff that gives a transient downstream fault time to clear.
	streamRecordRetryDelay = 100 * time.Millisecond
	// streamRecordLimit stays below the Streams API maximum to bound response size per round.
	streamRecordLimit = 100
	// maxGetRecordsRoundsPerShard bounds work per shard per tick so large shards cannot monopolize polling.
	maxGetRecordsRoundsPerShard = 512
	// streamInitialPollDelay lets application startup and worker registration settle before the first read.
	streamInitialPollDelay = 2 * time.Second
	// streamPollInterval balances event latency against idle API traffic.
	streamPollInterval = time.Second
	// maxRetryCountEntries bounds process-local poison-record bookkeeping in long-running deployments.
	maxRetryCountEntries = 1024
	// streamResultBuffer lets a worker acknowledge even after a forced poller exit.
	streamResultBuffer = 1
	// maxShardsPerDescribePage matches the DynamoDB Streams DescribeStream page limit.
	maxShardsPerDescribePage = 100
)

// errStreamRecordSkipped allows iterator progress without claiming business success.
var errStreamRecordSkipped = errors.New("poison stream record skipped")

// streamShard retains a shard's live iterator independently from its successful checkpoint.
type streamShard struct {
	streamARN *string
	shard     types.Shard
	iterator  *string
	complete  bool
}

// retryCountEntry associates a namespaced entity key with its accumulated failures.
type retryCountEntry struct {
	key   string
	count int
}

// retryCountLRU bounds per-entity failure counters and evicts the least recently used key.
type retryCountLRU struct {
	entries map[string]*list.Element
	order   *list.List
}

// newRetryCountLRU creates empty, bounded, process-local retry accounting.
func newRetryCountLRU() *retryCountLRU {
	return &retryCountLRU{entries: make(map[string]*list.Element), order: list.New()}
}

// increment records a failure, refreshes recency, and evicts the oldest entry at capacity.
func (lru *retryCountLRU) increment(key string) int {
	if lru == nil || key == "" {
		return 0
	}
	if element, ok := lru.entries[key]; ok {
		lru.order.MoveToFront(element)
		entry := element.Value.(*retryCountEntry)
		entry.count++
		return entry.count
	}
	element := lru.order.PushFront(&retryCountEntry{key: key, count: 1})
	lru.entries[key] = element
	if lru.order.Len() > maxRetryCountEntries {
		oldest := lru.order.Back()
		delete(lru.entries, oldest.Value.(*retryCountEntry).key)
		lru.order.Remove(oldest)
	}
	return 1
}

// remove forgets a record's accumulated failures after successful processing.
func (lru *retryCountLRU) remove(key string) {
	if element, ok := lru.entries[key]; ok {
		delete(lru.entries, key)
		lru.order.Remove(element)
	}
}

// Start discovers shards on every poll tick and processes each shard serially.
func (sw *StreamProcessor) Start() {
	if !sw.enabled {
		sw.log.Info("DynamoDB Streams processing is disabled")
		return
	}
	sw.initialize()
	initialDelay := sw.initialDelay
	if initialDelay == 0 {
		initialDelay = streamInitialPollDelay
	}
	runStreamPoller(sw.ctx, initialDelay, streamPollInterval, sw.pollOnce)
}

// runStreamPoller schedules serial polling until intake is canceled.
func runStreamPoller(ctx context.Context, initialDelay, interval time.Duration, poll func()) {
	if !waitForStreamContext(ctx, initialDelay) {
		return
	}
	poll()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}

// initialize supplies process-local defaults for polling and retry bookkeeping.
func (sw *StreamProcessor) initialize() {
	if sw.log == nil {
		sw.log = zap.NewNop()
	}
	if sw.shards == nil {
		sw.shards = make(map[string]*streamShard)
	}
	if sw.checkpoints == nil {
		sw.checkpoints = make(map[string]string)
	}
	if sw.retries == nil {
		sw.retries = newRetryCountLRU()
	}
}

// waitForStreamContext reports whether a delay elapsed before cancellation.
func waitForStreamContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// awaitStreamShutdown waits for the poller without exceeding either shutdown deadline.
func awaitStreamShutdown(ctx context.Context, done <-chan struct{}, maxWait time.Duration) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	if err := shutdownCtx.Err(); err != nil {
		return fmt.Errorf("wait for streams worker shutdown: %w", err)
	}
	select {
	case <-done:
		if err := shutdownCtx.Err(); err != nil {
			return fmt.Errorf("wait for streams worker shutdown: %w", err)
		}
		return nil
	case <-shutdownCtx.Done():
		return fmt.Errorf("wait for streams worker shutdown: %w", shutdownCtx.Err())
	}
}

// pollOnce refreshes discovery and drains each shard serially until intake stops.
func (sw *StreamProcessor) pollOnce() {
	if sw.ctx.Err() != nil {
		return
	}
	streams, err := sw.listStreams()
	if err != nil {
		if sw.ctx.Err() == nil {
			sw.log.Error("failed to discover DynamoDB streams", zap.Error(err))
		}
		return
	}
	if err := sw.discoverShards(streams); err != nil {
		if sw.ctx.Err() == nil {
			sw.log.Error("failed to discover stream shards", zap.Error(err))
		}
		return
	}
	for key, shard := range sw.shards {
		if sw.ctx.Err() != nil {
			return
		}
		sw.pollShard(key, shard)
	}
}

// listStreams retrieves all stream pages for the configured table or returns the SDK error.
func (sw *StreamProcessor) listStreams() ([]types.Stream, error) {
	var streams []types.Stream
	var lastEvaluatedStreamArn *string
	for {
		res, err := sw.client.ListStreams(sw.ctx, &dynamodbstreams.ListStreamsInput{
			TableName:               aws.String(sw.tableName),
			ExclusiveStartStreamArn: lastEvaluatedStreamArn,
		})
		if err != nil {
			return nil, err
		}
		streams = append(streams, res.Streams...)
		if res.LastEvaluatedStreamArn == nil {
			return streams, nil
		}
		lastEvaluatedStreamArn = res.LastEvaluatedStreamArn
	}
}

// discoverShards pages active streams and prunes checkpoints for shards no longer present.
func (sw *StreamProcessor) discoverShards(streams []types.Stream) error {
	sw.initialize()
	discovered := make(map[string]*streamShard)
	for _, stream := range streams {
		var exclusiveStartShardID *string
		for {
			desc, err := sw.client.DescribeStream(sw.ctx, &dynamodbstreams.DescribeStreamInput{
				StreamArn:             stream.StreamArn,
				ExclusiveStartShardId: exclusiveStartShardID,
				Limit:                 aws.Int32(maxShardsPerDescribePage),
			})
			if err != nil {
				return err
			}
			if desc.StreamDescription != nil {
				for _, shard := range desc.StreamDescription.Shards {
					key := streamShardCheckpointKey(stream.StreamArn, shard.ShardId)
					state := sw.shards[key]
					if state == nil {
						state = &streamShard{streamARN: stream.StreamArn, shard: shard}
					} else {
						state.shard = shard
					}
					discovered[key] = state
				}
			}
			if desc.StreamDescription == nil || desc.StreamDescription.LastEvaluatedShardId == nil {
				break
			}
			exclusiveStartShardID = desc.StreamDescription.LastEvaluatedShardId
		}
	}
	for key := range sw.shards {
		if _, active := discovered[key]; !active {
			delete(sw.checkpoints, key)
		}
	}
	sw.shards = discovered
	return nil
}

// streamShardCheckpointKey keeps identical shard IDs from different stream generations separate.
func streamShardCheckpointKey(streamARN, shardID *string) string {
	return aws.ToString(streamARN) + "#" + aws.ToString(shardID)
}

// pollShard bounds each polling round, preserving successful checkpoints separately
// from iterator progress so poison skips never become renewal checkpoints.
func (sw *StreamProcessor) pollShard(key string, shard *streamShard) {
	if shard.complete || sw.ctx.Err() != nil {
		return
	}
	if shard.iterator == nil {
		if !sw.openShardIterator(key, shard, sw.iterator, nil) {
			return
		}
	}
	for round := 0; round < maxGetRecordsRoundsPerShard; round++ {
		if sw.ctx.Err() != nil || shard.iterator == nil {
			return
		}
		result, err := sw.client.GetRecords(sw.ctx, &dynamodbstreams.GetRecordsInput{
			ShardIterator: shard.iterator,
			Limit:         aws.Int32(streamRecordLimit),
		})
		if err != nil {
			switch streamIteratorErrorKind(err) {
			case "expired":
				sequence := sw.checkpoints[key]
				iteratorType := sw.iterator
				var sequenceNumber *string
				if sequence != "" {
					iteratorType = types.ShardIteratorTypeAfterSequenceNumber
					sequenceNumber = aws.String(sequence)
				}
				sw.openShardIterator(key, shard, iteratorType, sequenceNumber)
			case "trimmed":
				sw.openShardIterator(key, shard, sw.iterator, nil)
			default:
				if sw.ctx.Err() == nil {
					sw.log.Error("failed to get stream records", zap.String("shardId", aws.ToString(shard.shard.ShardId)), zap.Error(err))
				}
			}
			return
		}
		for _, record := range result.Records {
			if err := sw.processRecord(shard.shard, record); err != nil {
				if errors.Is(err, errStreamRecordSkipped) {
					continue
				}
				if sw.ctx.Err() == nil {
					sw.log.Error("stream record processing stopped", zap.String("shardId", aws.ToString(shard.shard.ShardId)), zap.Error(err))
				}
				return
			}
			if record.Dynamodb != nil && record.Dynamodb.SequenceNumber != nil {
				sw.checkpoints[key] = aws.ToString(record.Dynamodb.SequenceNumber)
			}
		}
		shard.iterator = result.NextShardIterator
		if shard.iterator == nil {
			shard.complete = true
			sw.log.Info("shard closed", zap.String("shardId", aws.ToString(shard.shard.ShardId)))
			return
		}
		if len(result.Records) == 0 {
			return
		}
	}
}

// openShardIterator renews a shard position, falling back to configuration when its checkpoint was trimmed.
func (sw *StreamProcessor) openShardIterator(key string, shard *streamShard, iteratorType types.ShardIteratorType, sequence *string) bool {
	input := &dynamodbstreams.GetShardIteratorInput{
		StreamArn:         shard.streamARN,
		ShardId:           shard.shard.ShardId,
		ShardIteratorType: iteratorType,
		SequenceNumber:    sequence,
	}
	result, err := sw.client.GetShardIterator(sw.ctx, input)
	if err != nil {
		if iteratorType == types.ShardIteratorTypeAfterSequenceNumber && streamIteratorErrorKind(err) == "trimmed" {
			return sw.openShardIterator(key, shard, sw.iterator, nil)
		}
		if sw.ctx.Err() == nil {
			sw.log.Error("failed to get shard iterator", zap.String("shardId", aws.ToString(shard.shard.ShardId)), zap.Error(err))
		}
		return false
	}
	shard.iterator = result.ShardIterator
	return shard.iterator != nil
}

// streamIteratorErrorKind classifies recoverable SDK iterator errors through wrapping.
func streamIteratorErrorKind(err error) string {
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "ExpiredIteratorException":
			return "expired"
		case "TrimmedDataAccessException":
			return "trimmed"
		}
	}
	return ""
}

// processRecord waits for business acknowledgment with bounded retries. Poison skips
// return errStreamRecordSkipped, not success; stopping intake does not abandon an
// already admitted job's acknowledgment during the shutdown grace period.
func (sw *StreamProcessor) processRecord(shard types.Shard, record types.Record) error {
	sw.initialize()
	acknowledgmentCtx := sw.acknowledgmentCtx
	if acknowledgmentCtx == nil {
		acknowledgmentCtx = sw.ctx
	}
	retryKey := streamRecordRetryKey(record)
	for attempt := 1; attempt <= maxProcessRetries; attempt++ {
		result := make(chan error, streamResultBuffer)
		streamJob := &job.IdentifyJob{Record: record, Result: result}
		if err := sw.dispatcher.DispatchContext(sw.ctx, streamJob); err != nil {
			return err
		}

		select {
		case err := <-result:
			if err == nil {
				sw.retries.remove(retryKey)
				return nil
			}
			if sw.ctx.Err() != nil {
				return sw.ctx.Err()
			}
			count := sw.retries.increment(retryKey)
			if count >= maxProcessRetries {
				sw.log.Error("skipping poison stream record after bounded retries",
					zap.String("shardId", aws.ToString(shard.ShardId)),
					zap.String("retryKey", retryKey),
					zap.Int("attempts", count),
					zap.Error(err))
				return errStreamRecordSkipped
			}
			level := sw.log.Warn
			if job.IsExpectedOutcome(err) {
				level = sw.log.Debug
			}
			level("retrying failed stream record",
				zap.String("shardId", aws.ToString(shard.ShardId)),
				zap.String("retryKey", retryKey),
				zap.Int("attempt", count),
				zap.Error(err))
		case <-acknowledgmentCtx.Done():
			return acknowledgmentCtx.Err()
		}
		if attempt < maxProcessRetries && sw.retryDelay > 0 && !waitForStreamContext(sw.ctx, time.Duration(attempt)*sw.retryDelay) {
			return sw.ctx.Err()
		}
	}
	return errStreamRecordSkipped
}

// streamRecordRetryKey isolates payment creation, timer release, and unclassified record failures.
func streamRecordRetryKey(record types.Record) string {
	if record.Dynamodb == nil {
		return "record:" + aws.ToString(record.EventID)
	}
	if record.EventName == types.OperationTypeInsert {
		image := record.Dynamodb.NewImage
		entityType, _ := streamStringAttribute(image["entityType"])
		eventType, _ := streamStringAttribute(image["eventType"])
		paymentID, _ := streamStringAttribute(image["paymentId"])
		if entityType == "PAYMENT_EVENT" && eventType == "OUTBOUND_PAYMENT_CREATED" && paymentID != "" {
			return paymentID
		}
	}
	if record.EventName == types.OperationTypeRemove {
		sk, _ := streamStringAttribute(record.Dynamodb.Keys["SK"])
		if strings.HasPrefix(sk, model.ReservationTimerPrefix) {
			pk, _ := streamStringAttribute(record.Dynamodb.Keys["PK"])
			return "release:" + strings.TrimPrefix(pk, "ACCOUNT#") + "#" + strings.TrimPrefix(sk, model.ReservationTimerPrefix)
		}
	}
	return "record:" + aws.ToString(record.EventID)
}

// streamStringAttribute extracts a nonempty string attribute without coercion.
func streamStringAttribute(value types.AttributeValue) (string, bool) {
	attribute, ok := value.(*types.AttributeValueMemberS)
	if !ok || attribute.Value == "" {
		return "", false
	}
	return attribute.Value, true
}

// NewStreamsWorker registers the stream poller with the Fx lifecycle.
func NewStreamsWorker(
	lc fx.Lifecycle,
	client *dynamodbstreams.Client,
	env *env.Env,
	log *zap.Logger,
	dispatcher *workerpool.WorkerPool,
) *StreamProcessor {
	ctx, cancel := context.WithCancel(context.Background())
	acknowledgmentCtx, cancelAcknowledgment := context.WithCancel(context.Background())
	iterator := types.ShardIteratorTypeLatest
	if env.StreamIteratorType == "TRIM_HORIZON" {
		iterator = types.ShardIteratorTypeTrimHorizon
	}
	sw := &StreamProcessor{
		ctx:               ctx,
		acknowledgmentCtx: acknowledgmentCtx,
		log:               log,
		client:            client,
		tableName:         env.TableName,
		iterator:          iterator,
		enabled:           env.StreamsEnabled,
		dispatcher:        dispatcher,
		done:              make(chan struct{}),
		initialDelay:      streamInitialPollDelay,
		retryDelay:        streamRecordRetryDelay,
		shards:            make(map[string]*streamShard),
		checkpoints:       make(map[string]string),
		retries:           newRetryCountLRU(),
	}

	if !sw.enabled {
		cancel()
		cancelAcknowledgment()
		return sw
	}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			log.Info("starting streams worker")
			go func() {
				defer close(sw.done)
				sw.Start()
			}()
			log.Info("started streams worker")
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			log.Info("stopping streams worker")
			// Fx stops the poller before its worker dependency. Coordinate both
			// here so separate hooks cannot restart the shutdown grace period.
			shutdownCtx, shutdownCancel := context.WithTimeout(stopCtx, workerpool.ShutdownGracePeriod)
			defer shutdownCancel()
			defer cancelAcknowledgment()
			cancel()
			poolErr := dispatcher.Shutdown(shutdownCtx)
			if poolErr != nil {
				cancelAcknowledgment()
			}
			pollerErr := awaitStreamShutdown(shutdownCtx, sw.done, workerpool.ShutdownGracePeriod)
			if err := errors.Join(poolErr, pollerErr); err != nil {
				return err
			}
			log.Info("stopped streams worker")
			return nil
		},
	})

	return sw
}

// ConfigureStreams is the Fx invocation for conditional stream-worker wiring.
// It deliberately checks the setting before constructing a worker so HTTP-only
// configurations do not instantiate the component or register lifecycle hooks.
func ConfigureStreams(
	lc fx.Lifecycle,
	client *dynamodbstreams.Client,
	settings *env.Env,
	log *zap.Logger,
	dispatcher *workerpool.WorkerPool,
) {
	if settings == nil || !settings.StreamsEnabled {
		return
	}
	NewStreamsWorker(lc, client, settings, log, dispatcher)
}
