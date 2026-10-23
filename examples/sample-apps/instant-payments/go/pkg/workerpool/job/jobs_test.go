// This suite checks account-job execution with worker context and repository fixtures.
package job

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/zap"

	"instantpayments/pkg/model"
)

// readRepository returns selected account and payment rows without persistence.
type readRepository struct {
	account *model.Account
	payment *model.Payment
}

// GetAccount returns the configured row, or nil to represent absence.
func (r *readRepository) GetAccount(context.Context, model.AccountKey, model.AccountKey) (*model.Account, error) {
	return r.account, nil
}

// GetPayment returns the configured row, or nil to represent absence.
func (r *readRepository) GetPayment(context.Context, string) (*model.Payment, error) {
	return r.payment, nil
}

func TestAccountJobRunsWithLoggerContext(t *testing.T) {
	repository := &readRepository{account: &model.Account{AccountKeys: model.AccountKeys{PK: "alice", SK: "alice"}}}
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
