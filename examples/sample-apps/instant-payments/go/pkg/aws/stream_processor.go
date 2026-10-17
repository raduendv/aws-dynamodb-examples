package aws

import (
	"context"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
	"instantpayments/pkg/workerpool"
	"instantpayments/pkg/workerpool/job"
)

// StreamProcessor reads DynamoDB Streams and dispatches records to workers.
type StreamProcessor struct {
	ctx        context.Context
	log        *zap.Logger
	client     *dynamodbstreams.Client
	tableName  string
	dispatcher *workerpool.WorkerPool
}

// Start lists streams, starts shard processors, and waits for cancellation.
func (sw *StreamProcessor) Start() {
	var wg sync.WaitGroup

	streams, err := sw.listStreams()
	if err != nil {
		sw.log.Error("failed to list streams", zap.Error(err))

		return
	}
	if len(streams) == 0 {
		sw.log.Warn("no DynamoDB streams found; stream processor stopped", zap.String("tableName", sw.tableName))

		return
	}

	shardCount := sw.processStreams(streams, &wg)
	sw.log.Info("stream processors started", zap.Int("streamCount", len(streams)), zap.Int("shardCount", shardCount))

	// Wait for cancellation
	<-sw.ctx.Done()
	wg.Wait()
}

func (sw *StreamProcessor) listStreams() ([]types.Stream, error) {
	var streams []types.Stream
	var lastEvaluatedStreamArn *string

	sw.log.Info("fetching streams", zap.String("tableName", sw.tableName))
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

func (sw *StreamProcessor) processStreams(streams []types.Stream, wg *sync.WaitGroup) int {
	sw.log.Info("fetched streams", zap.Any("streams", streams))
	shardCount := 0

	for _, stream := range streams {
		sw.log.Info("processing stream", zap.String("streamArn", aws.ToString(stream.StreamArn)))
		desc, err := sw.client.DescribeStream(sw.ctx, &dynamodbstreams.DescribeStreamInput{
			StreamArn: stream.StreamArn,
		})
		if err != nil {
			sw.log.Error("failed to describe stream", zap.Error(err))

			continue
		}

		sw.log.Info("stream described", zap.Int("shardCount", len(desc.StreamDescription.Shards)))

		for _, shard := range desc.StreamDescription.Shards {
			shardCount++
			sw.log.Info("starting shard processor", zap.String("shardId", aws.ToString(shard.ShardId)))
			wg.Add(1)
			go func(streamArn *string, shard types.Shard) {
				defer wg.Done()
				sw.processShard(streamArn, shard)
			}(stream.StreamArn, shard)
		}
	}

	return shardCount
}

func (sw *StreamProcessor) processShard(streamArn *string, shard types.Shard) {
	sw.log.Info("processing shard", zap.String("shardId", aws.ToString(shard.ShardId)))

	// Use TrimHorizon to read all records, or Latest for only new ones
	iteratorType := types.ShardIteratorTypeTrimHorizon

	getIter, err := sw.client.GetShardIterator(sw.ctx, &dynamodbstreams.GetShardIteratorInput{
		StreamArn:         streamArn,
		ShardId:           shard.ShardId,
		ShardIteratorType: iteratorType,
	})
	if err != nil {
		sw.log.Error("failed to get shard iterator", zap.Error(err))

		return
	}
	shardIterator := getIter.ShardIterator

	for {
		select {
		case <-sw.ctx.Done():
			sw.log.Info("shard processing cancelled", zap.String("shardId", aws.ToString(shard.ShardId)))
			return
		default:
		}

		if shardIterator == nil {

			return
		}

		records, err := sw.client.GetRecords(sw.ctx, &dynamodbstreams.GetRecordsInput{
			ShardIterator: shardIterator,
		})
		if err != nil {
			sw.log.Error("failed to get records", zap.Error(err))

			return
		}

		dispatched := 0
		for _, record := range records.Records {
			sw.log.Info("fetched record", zap.Any("record", record))
			select {
			case sw.dispatcher.JobPool <- &job.IdentifyJob{Record: record}:
				dispatched++
			case <-sw.ctx.Done():

				return
			default:
				sw.log.Warn("job pool full, dropping record", zap.String("shardId", aws.ToString(shard.ShardId)))
			}
		}

		if dispatched > 0 {
			sw.log.Info("dispatched stream records", zap.Int("count", dispatched), zap.String("shardId", aws.ToString(shard.ShardId)))
		}

		if len(records.Records) == 0 {
			time.Sleep(time.Second) // avoid tight loop
		}

		shardIterator = records.NextShardIterator
		if shardIterator == nil {
			sw.log.Info("shard closed", zap.String("shardId", aws.ToString(shard.ShardId)))

			return
		}
	}
}

// NewStreamsWorker registers the DynamoDB Streams processor with the Fx lifecycle.
func NewStreamsWorker(
	lc fx.Lifecycle,
	client *dynamodbstreams.Client,
	env *env.Env,
	log *zap.Logger,
	dispatcher *workerpool.WorkerPool,
) *StreamProcessor {
	ctx, cancel := context.WithCancel(context.Background())
	sw := &StreamProcessor{
		ctx:        ctx,
		log:        log,
		client:     client,
		tableName:  env.TableName,
		dispatcher: dispatcher,
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			log.Info("starting streams worker")
			go sw.Start()
			log.Info("started streams worker")
			return nil
		},
		OnStop: func(context.Context) error {
			log.Info("stopping streams worker")
			cancel()
			log.Info("stopped streams worker")
			return nil
		},
	})

	return sw
}
