package job

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// PaymentJob processes a payment-related stream record.
type PaymentJob struct {
	PaymentID string
}

// Run processes PaymentID through the worker's payment service using ctx.
// Missing dependencies or IDs and wrapped service failures are returned for retry.
func (p *PaymentJob) Run(ctx context.Context) error {
	logVal := ctx.Value(ContextKeyLogger{})
	if logVal == nil {
		return fmt.Errorf("logger context missing")
	}
	log, ok := logVal.(*zap.Logger)
	if !ok {
		return fmt.Errorf("logger context has wrong type")
	}

	log.Info("PaymentJob started")
	processor, ok := ctx.Value(ContextKeyPaymentProcessor{}).(PaymentProcessor)
	if !ok || processor == nil {
		return fmt.Errorf("payment processor context missing or invalid")
	}
	if p.PaymentID == "" {
		return fmt.Errorf("payment id is empty")
	}
	result, err := processor.ProcessContext(ctx, p.PaymentID)
	if err != nil {
		return fmt.Errorf("process payment %s: %w", p.PaymentID, err)
	}
	log.Info("processed payment creation stream event", zap.String("paymentId", p.PaymentID), zap.Any("result", result))
	return nil
}
