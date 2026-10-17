package job

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/zap"

	"instantpayments/pkg/model"
)

type readRepository struct {
	account *model.Account
	payment *model.Payment
}

func (r *readRepository) GetAccount(context.Context, string, string) (*model.Account, error) {
	return r.account, nil
}

func (r *readRepository) GetPayment(context.Context, string) (*model.Payment, error) {
	return r.payment, nil
}

func TestAccountJobRunsWithLoggerContext(t *testing.T) {
	repository := &readRepository{account: &model.Account{Item: model.Item{PK: "ACCOUNT#alice", SK: "ACCOUNT#alice"}}}
	job := &AccountJob{
		Account: map[string]types.AttributeValue{
			"pk":       &types.AttributeValueMemberS{Value: "ACCOUNT#alice"},
			"sk":       &types.AttributeValueMemberS{Value: "ACCOUNT#alice"},
			"name":     &types.AttributeValueMemberS{Value: "Alice"},
			"currency": &types.AttributeValueMemberS{Value: "USD"},
		},
	}
	ctx := context.WithValue(context.Background(), ContextKeyLogger{}, zap.NewNop())
	ctx = context.WithValue(ctx, ContextKeyRepository{}, Repository(repository))
	if err := job.Run(ctx); err != nil {
		t.Fatal(err)
	}
}
