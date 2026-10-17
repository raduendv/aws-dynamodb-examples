package job

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodbstreams/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/zap"
)

// PaymentJob processes a payment-related stream record.
type PaymentJob struct {
	Record map[string]types.AttributeValue
}

// Run executes the payment job.
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
	repository, ok := ctx.Value(ContextKeyRepository{}).(Repository)
	if !ok || repository == nil {
		return fmt.Errorf("repository context missing")
	}

	if len(p.Record) == 0 {
		return fmt.Errorf("payment stream image is empty")
	}

	var payment struct {
		PK     string `dynamodbav:"pk"`
		State  string `dynamodbav:"state"`
		Amount int64  `dynamodbav:"amount"`
	}
	if err := attributevalue.UnmarshalMap(p.Record, &payment); err != nil {
		return fmt.Errorf("decode payment stream image: %w", err)
	}
	stored, err := repository.GetPayment(ctx, strings.TrimPrefix(payment.PK, "PAYMENT#"))
	if err != nil {
		return fmt.Errorf("load payment from repository: %w", err)
	}
	if stored == nil {
		return fmt.Errorf("payment %q not found in repository", payment.PK)
	}
	log.Info("PaymentJob processed payment", zap.String("paymentId", payment.PK), zap.String("state", payment.State), zap.Int64("amountMinor", payment.Amount))

	return nil
}
