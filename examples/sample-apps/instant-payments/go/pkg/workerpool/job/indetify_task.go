package job

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/zap"
)

// IdentifyJob identifies a DynamoDB Stream record and dispatches a follow-up job.
type IdentifyJob struct {
	Record types.Record
}

// WorkerPool is the subset of worker-pool behavior required by jobs.
type WorkerPool interface {
	Dispatch(j Job)
}

func (i *IdentifyJob) Run(ctx context.Context) error {
	logVal := ctx.Value(ContextKeyLogger{})
	if logVal == nil {
		return fmt.Errorf("logger context missing")
	}
	log, ok := logVal.(*zap.Logger)
	if !ok {
		return fmt.Errorf("logger context has wrong type")
	}

	workerPoolVal := ctx.Value(ContextKeyWorkerPool{})
	if workerPoolVal == nil {
		return fmt.Errorf("worker pool context missing")
	}
	workerPool, ok := workerPoolVal.(WorkerPool)
	if !ok {
		return fmt.Errorf("worker pool context has wrong type")
	}
	log.Info("IdentifyJob started")
	if i.Record.EventName == types.OperationTypeRemove {
		log.Info("ignoring removed stream record")

		return nil
	}

	log.Info("Record details", zap.Any("record", i.Record))

	if pkVal := i.Record.Dynamodb.Keys["pk"]; pkVal != nil {
		if pkAttr, ok := pkVal.(*types.AttributeValueMemberS); ok {
			pk := pkAttr.Value
			log.Info("Extracted pk", zap.String("pk", pk))
			pkParts := strings.Split(pk, "#")

			if len(pkParts) < 1 {
				return errors.New("invalid pk format")
			}

			switch pkParts[0] {
			case "ACCOUNT":
				workerPool.Dispatch(&AccountJob{
					Account: streamImage(i.Record),
				})
				log.Info("Identified record as ACCOUNT")
			case "PAYMENT":
				workerPool.Dispatch(&PaymentJob{
					Record: streamImage(i.Record),
				})
				log.Info("Identified record as PAYMENT")
			default:
				log.Warn("Unknown record type", zap.String("pk_prefix", pkParts[0]))
				return errors.New("unknown record type")
			}

		} else {
			log.Warn("pk attribute is not of type AttributeValueMemberS")
		}
	} else {
		log.Warn("pk attribute not found in record keys")
	}

	log.Info("IdentifyJob done")

	return nil
}

func streamImage(record types.Record) map[string]types.AttributeValue {
	if record.Dynamodb == nil {
		return nil
	}
	if len(record.Dynamodb.NewImage) > 0 {
		return record.Dynamodb.NewImage
	}
	return record.Dynamodb.OldImage
}
