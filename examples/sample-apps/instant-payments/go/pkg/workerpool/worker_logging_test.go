// This suite checks worker log levels with real wrapped service errors,
// independently of payment settlement and stream retry behavior.
package workerpool_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"instantpayments/pkg/env"
	"instantpayments/pkg/service"
	"instantpayments/pkg/workerpool"
	"instantpayments/pkg/workerpool/job"
)

// loggingLifecycle captures the dispatcher shutdown hook for bounded test cleanup.
type loggingLifecycle struct{ hook fx.Hook }

// Append records the single worker-pool lifecycle hook.
func (l *loggingLifecycle) Append(hook fx.Hook) { l.hook = hook }

// loggingProcessor returns a controlled service outcome without touching persistence.
type loggingProcessor struct{ err error }

// ProcessContext supplies the outcome that PaymentJob wraps before worker logging.
func (p loggingProcessor) ProcessContext(context.Context, string) (map[string]any, error) {
	return nil, p.err
}

// ReleaseReservation satisfies the processor contract without releasing a hold.
func (p loggingProcessor) ReleaseReservation(context.Context, string, string) error { return nil }

// loggingBarrier signals that the preceding job has finished logging.
type loggingBarrier chan struct{}

// Run acknowledges that the single worker has reached the barrier.
func (b loggingBarrier) Run(context.Context) error { close(b); return nil }

func TestWorkerClassifiesWrappedDomainOutcomes(t *testing.T) {
	for _, test := range []struct {
		name  string
		err   error
		level zapcore.Level
	}{
		{"payment missing", &service.DomainError{Code: "PAYMENT_NOT_FOUND", Text: "payment not found"}, zap.DebugLevel},
		{"account missing", &service.DomainError{Code: "ACCOUNT_NOT_FOUND", Text: "account not found"}, zap.DebugLevel},
		{"wrapped account missing", fmt.Errorf("lookup: %w", &service.DomainError{Code: "ACCOUNT_NOT_FOUND"}), zap.DebugLevel},
		{"unexpected domain failure", &service.DomainError{Code: "INTERNAL_ERROR"}, zap.ErrorLevel},
		{"unclassified matching text", errors.New("account not found"), zap.ErrorLevel},
		{"success", nil, zap.InfoLevel},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			lifecycle := &loggingLifecycle{}
			pool := workerpool.NewDispatcher(lifecycle, &env.Env{WorkerPoolSize: 1, JobPoolSize: 2},
				zap.New(core), nil, loggingProcessor{err: test.err})
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := lifecycle.hook.OnStop(ctx); err != nil {
					t.Error(err)
				}
			})
			pool.Dispatch(&job.PaymentJob{PaymentID: "pay_test"})
			barrier := make(loggingBarrier)
			pool.Dispatch(barrier)
			select {
			case <-barrier:
			case <-time.After(time.Second):
				t.Fatal("worker did not reach logging barrier")
			}
			message := "Worker failed to run job"
			if test.level == zap.DebugLevel {
				message = "Worker completed job with expected outcome"
			} else if test.err == nil {
				message = "processed payment creation stream event"
			}
			entries := logs.FilterMessage(message).All()
			if len(entries) != 1 || entries[0].Level != test.level {
				t.Fatalf("outcome logs = %#v, want one %s entry", entries, test.level)
			}
			if test.level != zap.ErrorLevel && logs.FilterLevelExact(zap.ErrorLevel).Len() != 0 {
				t.Fatal("routine outcome produced an error log")
			}
		})
	}
}
