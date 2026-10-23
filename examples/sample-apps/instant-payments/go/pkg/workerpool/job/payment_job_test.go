// This suite checks shared payment-processor invocation and failure propagation from payment jobs.
package job

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

func TestPaymentJobRunsSharedProcessor(t *testing.T) {
	processor := &testPaymentProcessor{}
	ctx := context.WithValue(context.Background(), ContextKeyLogger{}, zap.NewNop())
	ctx = context.WithValue(ctx, ContextKeyPaymentProcessor{}, processor)
	if err := (&PaymentJob{PaymentID: "pay_1"}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(processor.processed) != 1 || processor.processed[0] != "pay_1" {
		t.Fatalf("unexpected processed payment ids: %v", processor.processed)
	}
}

func TestPaymentJobReturnsProcessorFailure(t *testing.T) {
	processor := &testPaymentProcessor{processErr: errors.New("failed")}
	ctx := context.WithValue(context.Background(), ContextKeyLogger{}, zap.NewNop())
	ctx = context.WithValue(ctx, ContextKeyPaymentProcessor{}, processor)
	if err := (&PaymentJob{PaymentID: "pay_1"}).Run(ctx); err == nil {
		t.Fatal("expected processor failure")
	}
}
