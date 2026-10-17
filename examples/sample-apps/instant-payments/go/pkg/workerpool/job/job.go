package job

import (
	"context"

	"instantpayments/pkg/model"
)

// ContextKeyLogger identifies the logger stored in a worker context.
type ContextKeyLogger struct{}

// ContextKeyWorkerPool identifies the worker pool stored in a worker context.
type ContextKeyWorkerPool struct{}

// ContextKeyRepository identifies the DynamoDB repository stored in a worker context.
type ContextKeyRepository struct{}

// Repository provides the read operations required by stream jobs.
type Repository interface {
	GetAccount(context.Context, string, string) (*model.Account, error)
	GetPayment(context.Context, string) (*model.Payment, error)
}

type Job interface {
	Run(context.Context) error
}
