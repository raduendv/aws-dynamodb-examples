package job

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/zap"

	"instantpayments/pkg/model"
)

func TestPaymentJobRunsWithLoggerContext(t *testing.T) {
	repository := &readRepository{payment: &model.Payment{Item: model.Item{PK: "PAYMENT#pay_1", SK: "PAYMENT#pay_1"}}}
	job := &PaymentJob{
		Record: map[string]types.AttributeValue{
			"pk":           &types.AttributeValueMemberS{Value: "PAYMENT#pay_1"},
			"sk":           &types.AttributeValueMemberS{Value: "PAYMENT#pay_1"},
			"paymentId":    &types.AttributeValueMemberS{Value: "pay_1"},
			"merchantId":   &types.AttributeValueMemberS{Value: "merchant_1"},
			"state":        &types.AttributeValueMemberS{Value: "RECEIVED"},
			"currency":     &types.AttributeValueMemberS{Value: "USD"},
			"creditorIban": &types.AttributeValueMemberS{Value: "DE123"},
		},
	}
	ctx := context.WithValue(context.Background(), ContextKeyLogger{}, zap.NewNop())
	ctx = context.WithValue(ctx, ContextKeyRepository{}, Repository(repository))
	if err := job.Run(ctx); err != nil {
		t.Fatal(err)
	}
}
