package job

import (
	"context"
	"errors"

	"instantpayments/pkg/model"
)

// ContextKeyLogger identifies the logger stored in a worker context.
type ContextKeyLogger struct{}

// ContextKeyWorkerPool identifies the worker pool stored in a worker context.
type ContextKeyWorkerPool struct{}

// ContextKeyRepository identifies the DynamoDB repository stored in a worker context.
type ContextKeyRepository struct{}

// ContextKeyPaymentProcessor identifies the shared payment service in a job context.
type ContextKeyPaymentProcessor struct{}

// Repository provides the read operations required by stream jobs.
type Repository interface {
	GetAccount(context.Context, model.AccountKey, model.AccountKey) (*model.Account, error)
	GetPayment(context.Context, string) (*model.Payment, error)
}

// PaymentProcessor exposes the service operations required by stream jobs.
type PaymentProcessor interface {
	ProcessContext(context.Context, string) (map[string]any, error)
	ReleaseReservation(context.Context, string, string) error
}

// Job executes business processing with the worker's dependencies and cancellation context.
type Job interface {
	Run(context.Context) error
}

// IsExpectedOutcome recognizes classified business outcomes through wrapped errors.
func IsExpectedOutcome(err error) bool {
	var outcome interface{ ExpectedOutcome() bool }
	return errors.As(err, &outcome) && outcome.ExpectedOutcome()
}
