// This suite verifies malformed job context, missing records, and preserved repository or processor errors.
package job

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/zap"

	"instantpayments/pkg/model"
)

// failingAccountRepository returns the same controlled failure from both read operations.
type failingAccountRepository struct{ err error }

// GetAccount returns the configured repository failure without an account.
func (r failingAccountRepository) GetAccount(context.Context, model.AccountKey, model.AccountKey) (*model.Account, error) {
	return nil, r.err
}

// GetPayment returns the configured repository failure without a payment.
func (r failingAccountRepository) GetPayment(context.Context, string) (*model.Payment, error) {
	return nil, r.err
}

func TestAccountJobErrors(t *testing.T) {
	image := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: "ACCOUNT#acc_1"},
		"SK": &types.AttributeValueMemberS{Value: "ACCOUNT#acc_1"},
	}
	loggerContext := context.WithValue(context.Background(), ContextKeyLogger{}, zap.NewNop())
	withRepository := func(repository Repository) context.Context {
		return context.WithValue(loggerContext, ContextKeyRepository{}, repository)
	}
	storageErr := errors.New("storage unavailable")
	for _, test := range []struct {
		name, message string
		ctx           context.Context
		image         map[string]types.AttributeValue
	}{
		{"no logger", "logger context missing", context.Background(), image},
		{"bad logger", "logger context has wrong type", context.WithValue(context.Background(), ContextKeyLogger{}, "wrong"), image},
		{"no repository", "repository context missing", loggerContext, image},
		{"empty image", "account stream image is empty", withRepository(&readRepository{}), nil},
		{"bad key", "decode account stream image", withRepository(&readRepository{}), map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: "PAYMENT#acc_1"}}},
		{"storage error", "load account from repository", withRepository(failingAccountRepository{err: storageErr}), image},
		{"missing account", "not found in repository", withRepository(&readRepository{}), image},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (&AccountJob{Account: test.image}).Run(test.ctx)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected %q error, got %v", test.message, err)
			}
			if test.name == "storage error" && !errors.Is(err, storageErr) {
				t.Fatalf("storage cause was lost: %v", err)
			}
		})
	}
}

func TestPaymentJobRejectsInvalidContext(t *testing.T) {
	loggerContext := context.WithValue(context.Background(), ContextKeyLogger{}, zap.NewNop())
	for _, test := range []struct {
		name, message string
		ctx           context.Context
		id            string
	}{
		{"no logger", "logger context missing", context.Background(), "pay_1"},
		{"bad logger", "logger context has wrong type", context.WithValue(context.Background(), ContextKeyLogger{}, "wrong"), "pay_1"},
		{"no processor", "payment processor context missing or invalid", loggerContext, "pay_1"},
		{"empty payment ID", "payment id is empty", processorContext(&testPaymentProcessor{}), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := (&PaymentJob{PaymentID: test.id}).Run(test.ctx); err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected %q error, got %v", test.message, err)
			}
		})
	}
}

func TestIdentifyJobContextAndProcessorErrors(t *testing.T) {
	if err := (&IdentifyJob{}).Run(context.Background()); err == nil {
		t.Fatal("missing logger was accepted")
	}
	if err := (&IdentifyJob{}).Run(processorContext(&testPaymentProcessor{})); err != nil {
		t.Fatalf("empty record should be ignored: %v", err)
	}
	record := types.Record{EventName: types.OperationTypeRemove, Dynamodb: &types.StreamRecord{Keys: map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: "ACCOUNT#acc_1"},
		"SK": &types.AttributeValueMemberS{Value: "RESERVATION_TEMP#res_1"},
	}}}
	ctx := context.WithValue(context.Background(), ContextKeyLogger{}, zap.NewNop())
	if err := (&IdentifyJob{Record: record}).Run(ctx); err == nil || !strings.Contains(err.Error(), "processor context") {
		t.Fatalf("expected missing processor error, got %v", err)
	}
	cause := errors.New("release failed")
	if err := (&IdentifyJob{Record: record}).Run(processorContext(&testPaymentProcessor{releaseErr: cause})); !errors.Is(err, cause) {
		t.Fatalf("release failure was lost: %v", err)
	}
	record.EventName = types.OperationTypeModify
	if err := (&IdentifyJob{Record: record}).Run(ctx); err != nil {
		t.Fatalf("modify record should be ignored: %v", err)
	}
	record.EventName = types.OperationTypeInsert
	record.Dynamodb.NewImage = map[string]types.AttributeValue{"entityType": &types.AttributeValueMemberS{Value: "ACCOUNT"}}
	if err := (&IdentifyJob{Record: record}).Run(ctx); err != nil {
		t.Fatalf("unrelated insert should be ignored: %v", err)
	}
}
