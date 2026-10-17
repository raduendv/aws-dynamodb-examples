package job

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodbstreams/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/zap"
)

// AccountJob processes an account-related stream record.
type AccountJob struct {
	Account map[string]types.AttributeValue
}

// Run executes the account job.
func (a *AccountJob) Run(ctx context.Context) error {
	logVal := ctx.Value(ContextKeyLogger{})
	if logVal == nil {
		return fmt.Errorf("logger context missing")
	}
	log, ok := logVal.(*zap.Logger)
	if !ok {
		return fmt.Errorf("logger context has wrong type")
	}

	log.Info("AccountJob started")
	repository, ok := ctx.Value(ContextKeyRepository{}).(Repository)
	if !ok || repository == nil {
		return fmt.Errorf("repository context missing")
	}

	if len(a.Account) == 0 {
		return fmt.Errorf("account stream image is empty")
	}

	var account struct {
		PK       string `dynamodbav:"pk"`
		SK       string `dynamodbav:"sk"`
		Currency string `dynamodbav:"currency"`
		Status   string `dynamodbav:"status"`
	}
	if err := attributevalue.UnmarshalMap(a.Account, &account); err != nil {
		return fmt.Errorf("decode account stream image: %w", err)
	}
	stored, err := repository.GetAccount(ctx, account.PK, account.SK)
	if err != nil {
		return fmt.Errorf("load account from repository: %w", err)
	}
	if stored == nil {
		return fmt.Errorf("account %q not found in repository", account.PK)
	}
	log.Info("AccountJob processed account", zap.String("accountId", account.PK), zap.String("currency", account.Currency), zap.String("status", account.Status))

	return nil
}
