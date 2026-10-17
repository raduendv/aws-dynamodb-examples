package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
	"instantpayments/pkg/model"
)

// Repository provides DynamoDB operations for the sample's persisted models.
type Repository struct {
	log       *zap.Logger
	client    *dynamodb.Client
	tableName string
}

// PutPayment writes the payment projection used by the payment GSIs.
func (r *Repository) PutPayment(ctx context.Context, payment *model.Payment) error {
	if r.client == nil {
		return fmt.Errorf("DynamoDB client is not configured")
	}
	if payment == nil {
		return fmt.Errorf("payment is nil")
	}

	item, err := attributevalue.MarshalMap(map[string]any{
		"pk":              payment.PK,
		"sk":              payment.SK,
		"paymentId":       strings.TrimPrefix(payment.PK, "PAYMENT#"),
		"merchantId":      payment.MerchantID,
		"merchantState":   payment.MerchantID + "#" + payment.Status,
		"debtorAccountId": payment.DebtorAccountID,
		"creditorIban":    payment.CreditorIBAN,
		"creditorName":    payment.CreditorName,
		"amount":          payment.Amount,
		"currency":        payment.Currency,
		"state":           payment.Status,
		"createdAtUtc":    payment.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updatedAtUtc":    payment.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"lastSequence":    payment.LastSequence,
	})
	if err != nil {
		return err
	}

	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(r.tableName),
		Item:      item,
	})
	if err == nil {
		r.log.Info("persisted payment", zap.String("paymentId", strings.TrimPrefix(payment.PK, "PAYMENT#")))
	}

	return err
}

// ListPayments returns payment projections for a merchant from the appropriate GSI.
func (r *Repository) ListPayments(ctx context.Context, merchant, state string) ([]*model.Payment, error) {
	if r.client == nil {
		return nil, fmt.Errorf("DynamoDB client is not configured")
	}
	indexName := "GSI_MERCHANT_PAYMENTS"
	partitionKey := merchant
	if state != "" {
		indexName = "GSI_MERCHANT_STATE_PAYMENTS"
		partitionKey = merchant + "#" + state
	}
	result, err := r.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(r.tableName),
		IndexName:              aws.String(indexName),
		KeyConditionExpression: aws.String("#partition = :partition"),
		ExpressionAttributeNames: map[string]string{
			"#partition": map[string]string{"GSI_MERCHANT_PAYMENTS": "merchantId", "GSI_MERCHANT_STATE_PAYMENTS": "merchantState"}[indexName],
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":partition": &types.AttributeValueMemberS{Value: partitionKey},
		},
	})
	if err != nil {
		return nil, err
	}
	payments := make([]*model.Payment, 0, len(result.Items))
	for _, item := range result.Items {
		payment := &model.Payment{}
		if err := attributevalue.UnmarshalMap(item, payment); err != nil {
			return nil, err
		}
		if payment.MerchantID == "" {
			if merchantValue, ok := item["merchantId"].(*types.AttributeValueMemberS); ok {
				payment.MerchantID = merchantValue.Value
			}
		}
		if payment.Status == "" && state != "" {
			payment.Status = state
		}
		payments = append(payments, payment)
	}
	return payments, nil
}

// PutIdempotencyRecord persists an idempotency record in DynamoDB.
func (r *Repository) PutIdempotencyRecord(ctx context.Context, record *model.IdempotencyRecord) error {
	if r.client == nil {
		return fmt.Errorf("DynamoDB client is not configured")
	}

	item, err := attributevalue.MarshalMap(record)
	if err != nil {
		return err
	}

	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(r.tableName),
		Item:      item,
	})
	if err == nil {
		r.log.Info("persisted idempotency record", zap.String("key", strings.TrimPrefix(record.PK, "IDEMPOTENCY#")))
	}

	return err
}

// GetAccount retrieves an account by its DynamoDB partition and sort keys.
func (r *Repository) GetAccount(ctx context.Context, pk, sk string) (*model.Account, error) {
	res, err := r.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{
				Value: pk,
			},
			"sk": &types.AttributeValueMemberS{
				Value: sk,
			},
		},
	})

	if err != nil {
		return nil, err
	}

	if len(res.Item) == 0 {
		return nil, nil
	}

	account := &model.Account{}

	err = attributevalue.UnmarshalMap(res.Item, account)

	return account, err
}

// PutAccount writes an account and updates its timestamps.
func (r *Repository) PutAccount(ctx context.Context, account *model.Account) error {
	if r.client == nil {
		return fmt.Errorf("DynamoDB client is not configured")
	}
	if account == nil {
		return fmt.Errorf("account is nil")
	}
	if account.CreatedAt.IsZero() {
		account.CreatedAt = time.Now()
	}

	account.UpdatedAt = time.Now()

	m, err := attributevalue.MarshalMap(account)
	if err != nil {
		return err
	}
	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(r.tableName),
		Item:      m,
	})
	return err
}

// GetPayment retrieves a payment from DynamoDB.
func (r *Repository) GetPayment(ctx context.Context, id string) (*model.Payment, error) {
	if r.client == nil {
		return nil, fmt.Errorf("DynamoDB client is not configured")
	}
	res, err := r.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: "PAYMENT#" + id},
			"sk": &types.AttributeValueMemberS{Value: "PAYMENT#" + id},
		},
	})
	if err != nil {
		return nil, err
	}
	if len(res.Item) == 0 {
		return nil, nil
	}
	payment := &model.Payment{}
	if err := attributevalue.UnmarshalMap(res.Item, payment); err != nil {
		return nil, err
	}
	return payment, nil
}

// GetIdempotencyRecord retrieves an idempotency record by key.
func (r *Repository) GetIdempotencyRecord(ctx context.Context, key string) (*model.IdempotencyRecord, error) {
	if r.client == nil {
		return nil, fmt.Errorf("DynamoDB client is not configured")
	}
	res, err := r.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: "IDEMPOTENCY#" + key},
			"sk": &types.AttributeValueMemberS{Value: "IDEMPOTENCY#" + key},
		},
	})
	if err != nil {
		return nil, err
	}
	if len(res.Item) == 0 {
		return nil, nil
	}
	record := &model.IdempotencyRecord{}
	if err := attributevalue.UnmarshalMap(res.Item, record); err != nil {
		return nil, err
	}
	return record, nil
}

// CreateIdempotencyRecord creates an idempotency record representation.
func (r *Repository) CreateIdempotencyRecord(key, requestHash string) (*model.IdempotencyRecord, error) {
	// TODO: Implement idempotency record storage in DynamoDB when persistence is required.
	// Currently, this creates an in-memory representation only.
	ir := &model.IdempotencyRecord{
		Item: model.Item{
			PK: fmt.Sprintf("IDEMPOTENCY#%s", key),
			SK: fmt.Sprintf("IDEMPOTENCY#%s", key),
		},
		RequestHash: requestHash,
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour),
	}

	return ir, nil
}

// NewRepository constructs a Repository using the configured DynamoDB table.
func NewRepository(log *zap.Logger, client *dynamodb.Client, env *env.Env) *Repository {
	return &Repository{
		log:       log,
		client:    client,
		tableName: env.TableName,
	}
}
