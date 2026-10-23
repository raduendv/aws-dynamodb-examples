package job

import (
	"context"
	"fmt"

	"instantpayments/pkg/model"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/zap"
)

// AccountJob processes an account-related stream record.
type AccountJob struct {
	Account map[string]types.AttributeValue
}

// Run decodes the account image, reads the stored account using the worker's
// repository, and logs it. Missing dependencies, decoding, and read failures
// are returned without changing account state.
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

	image, err := attributevalue.FromDynamoDBStreamsMap(a.Account)
	if err != nil {
		return fmt.Errorf("convert account stream image: %w", err)
	}
	var account model.Account
	if err := attributevalue.UnmarshalMap(image, &account); err != nil {
		return fmt.Errorf("decode account stream image: %w", err)
	}
	stored, err := repository.GetAccount(ctx, account.PK, account.SK)
	if err != nil {
		return fmt.Errorf("load account from repository: %w", err)
	}
	if stored == nil {
		return fmt.Errorf("account %q not found in repository", account.PK)
	}
	log.Info("AccountJob processed account", zap.String("accountId", string(account.PK)), zap.String("currency", account.Currency), zap.String("status", string(account.Status)))

	return nil
}
