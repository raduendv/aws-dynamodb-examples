package job

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/zap"
)

type capturingPool struct {
	jobs []Job
}

func (p *capturingPool) Dispatch(job Job) {
	p.jobs = append(p.jobs, job)
}

func jobContext(pool WorkerPool) context.Context {
	ctx := context.WithValue(context.Background(), ContextKeyLogger{}, zap.NewNop())
	return context.WithValue(ctx, ContextKeyWorkerPool{}, pool)
}

func TestIdentifyJobDispatchesAccountJob(t *testing.T) {
	pool := &capturingPool{}
	job := IdentifyJob{Record: types.Record{Dynamodb: &types.StreamRecord{
		Keys:     map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "ACCOUNT#alice"}},
		NewImage: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "ACCOUNT#alice"}},
	}}}
	if err := job.Run(jobContext(pool)); err != nil {
		t.Fatal(err)
	}
	if len(pool.jobs) != 1 {
		t.Fatalf("expected one dispatched job, got %d", len(pool.jobs))
	}
	if _, ok := pool.jobs[0].(*AccountJob); !ok {
		t.Fatalf("expected AccountJob, got %T", pool.jobs[0])
	}
}

func TestIdentifyJobRejectsUnknownRecordType(t *testing.T) {
	pool := &capturingPool{}
	job := IdentifyJob{Record: types.Record{Dynamodb: &types.StreamRecord{Keys: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "UNKNOWN#value"}}}}}
	if err := job.Run(jobContext(pool)); err == nil {
		t.Fatal("expected unknown record type error")
	}
}

func TestIdentifyJobAcceptsMissingAndNonStringPartitionKeys(t *testing.T) {
	pool := &capturingPool{}
	for _, keys := range []map[string]types.AttributeValue{
		nil,
		{"pk": &types.AttributeValueMemberN{Value: "1"}},
	} {
		job := IdentifyJob{Record: types.Record{Dynamodb: &types.StreamRecord{Keys: keys}}}
		if err := job.Run(jobContext(pool)); err != nil {
			t.Fatalf("expected ignored key, got %v", err)
		}
	}
}
