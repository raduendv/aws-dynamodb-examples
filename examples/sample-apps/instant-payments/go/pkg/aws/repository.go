package aws

import (
	"context"
	"errors"
	"fmt"
	"math/big"
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

const paymentTimestampLayout = model.KeyTimestampLayout

const (
	// initialListLimit caps the convenience facade at one bounded merchant page.
	initialListLimit = 100
	// batchReadAttempts bounds repeated partial batch responses.
	batchReadAttempts = 5
	// batchReadBackoff yields between unprocessed-key retries.
	batchReadBackoff = 20 * time.Millisecond
	// releaseAttempts bounds account contention during one timer delivery.
	releaseAttempts = 3
	// releaseBackoff allows concurrent account transactions to finish before reload.
	releaseBackoff = 20 * time.Millisecond
)

var (
	ErrConcurrentUpdate   = errors.New("payment or account changed concurrently")
	ErrReservationExpired = errors.New("reservation expired")
)

// conditionalCheckFailedAt inspects the fixed transaction item index after unwrapping SDK errors.
func conditionalCheckFailedAt(err error, index int) bool {
	var canceled *types.TransactionCanceledException
	return errors.As(err, &canceled) && len(canceled.CancellationReasons) > index &&
		aws.ToString(canceled.CancellationReasons[index].Code) == "ConditionalCheckFailed"
}

// settlementError marks stale head/account guards for bounded reload-and-retry.
func settlementError(err error, staleIndexes ...int) error {
	for _, index := range staleIndexes {
		if conditionalCheckFailedAt(err, index) {
			return fmt.Errorf("%w: %w", ErrConcurrentUpdate, err)
		}
	}
	return err
}

// PutPayment writes a payment head projection.
func (r *Repository) PutPayment(ctx context.Context, payment *model.Payment) error {
	if r.client == nil {
		return fmt.Errorf("DynamoDB client is not configured")
	}
	if payment == nil {
		return fmt.Errorf("payment is nil")
	}
	item, err := attributevalue.MarshalMap(map[string]any{
		"PK": payment.PK, "SK": payment.SK, "entityType": "PAYMENT_STREAM_HEAD",
		"paymentId":       string(payment.PK),
		"correlationId":   payment.CorrelationID,
		"merchantId":      payment.MerchantID,
		"debtorAccountId": payment.DebtorAccountID,
		"creditorIban":    payment.CreditorIBAN, "creditorName": payment.CreditorName,
		"amount": payment.Amount, "currency": payment.Currency,
		"aggregateState": payment.Status, "lastSequence": payment.LastSequence,
		"createdAtUtc": payment.CreatedAt.UTC().Format(paymentTimestampLayout),
		"updatedAtUtc": model.FormatTimestamp(payment.UpdatedAt),
		"reasonCode":   nil,
	})
	if err != nil {
		return err
	}
	if payment.ReasonCode != "" {
		item["reasonCode"] = &types.AttributeValueMemberS{Value: payment.ReasonCode}
	}
	if payment.AmountExact != "" {
		exact, err := model.AmountDecimal(payment.Amount, payment.AmountExact)
		if err != nil {
			return fmt.Errorf("payment amount: %w", err)
		}
		item["amount"] = &types.AttributeValueMemberN{Value: exact}
	}
	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(r.tableName), Item: item})
	if err == nil && r.log != nil {
		r.log.Info("persisted payment", zap.String("paymentId", string(payment.PK)))
	}
	return err
}

// CreatePayment atomically stores the payment head, first event, and idempotency
// record. The idempotency condition is intentionally the last transaction item.
func (r *Repository) CreatePayment(ctx context.Context, payment *model.Payment, event *model.PaymentEvent, record *model.IdempotencyRecord) error {
	if r.client == nil {
		return fmt.Errorf("DynamoDB client is not configured")
	}
	if payment == nil || event == nil || record == nil {
		return fmt.Errorf("payment, event, and idempotency record are required")
	}
	head, err := marshalItem(payment)
	if err != nil {
		return fmt.Errorf("marshal payment head: %w", err)
	}
	head["entityType"] = &types.AttributeValueMemberS{Value: "PAYMENT_STREAM_HEAD"}
	head["paymentId"] = &types.AttributeValueMemberS{Value: string(payment.PK)}
	head["createdAtUtc"] = &types.AttributeValueMemberS{Value: payment.CreatedAt.UTC().Format(paymentTimestampLayout)}
	eventItem, err := marshalItem(event)
	if err != nil {
		return fmt.Errorf("marshal payment event: %w", err)
	}
	recordItem, err := marshalItem(record)
	if err != nil {
		return fmt.Errorf("marshal idempotency record: %w", err)
	}
	_, err = r.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{TableName: aws.String(r.tableName), Item: head}},
			{Put: &types.Put{TableName: aws.String(r.tableName), Item: eventItem}},
			{Put: &types.Put{
				TableName: aws.String(r.tableName), Item: recordItem,
				ConditionExpression: aws.String("attribute_not_exists(PK)"),
			}},
		},
	})
	return err
}

// ListPayments is retained for callers that need an initial bounded page.
func (r *Repository) ListPayments(ctx context.Context, merchant, state string) ([]*model.Payment, error) {
	payments, _, err := r.ListPaymentPage(ctx, merchant, state, initialListLimit, false, nil)
	return payments, err
}

// ListPaymentPage queries one merchant GSI page and returns its continuation key.
func (r *Repository) ListPaymentPage(ctx context.Context, merchant, state string, limit int32, forward bool, startKey map[string]types.AttributeValue) ([]*model.Payment, map[string]types.AttributeValue, error) {
	if r.client == nil {
		return nil, nil, fmt.Errorf("DynamoDB client is not configured")
	}
	indexName := "GSI_MERCHANT_PAYMENTS"
	condition := "#merchant = :merchant"
	names := map[string]string{"#merchant": "merchantId"}
	values := map[string]types.AttributeValue{":merchant": &types.AttributeValueMemberS{Value: merchant}}
	if state != "" {
		indexName = "GSI_MERCHANT_STATE_PAYMENTS"
		condition += " AND #state = :state"
		names["#state"] = "aggregateState"
		values[":state"] = &types.AttributeValueMemberS{Value: state}
	}
	result, err := r.client.Query(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(r.tableName),
		IndexName:                 aws.String(indexName),
		KeyConditionExpression:    aws.String(condition),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
		Limit:                     aws.Int32(limit), ScanIndexForward: aws.Bool(forward), ExclusiveStartKey: startKey,
	})
	if err != nil {
		return nil, nil, err
	}
	payments := make([]*model.Payment, 0, len(result.Items))
	for _, item := range result.Items {
		payment := &model.Payment{}
		if err := attributevalue.UnmarshalMap(item, payment); err != nil {
			return nil, nil, err
		}
		payment.AmountExact = storedDecimal(item, "amount")
		if payment.MerchantID == "" {
			if value, ok := item["merchantId"].(*types.AttributeValueMemberS); ok {
				payment.MerchantID = value.Value
			}
		}
		if payment.Status == "" {
			if value, ok := item["aggregateState"].(*types.AttributeValueMemberS); ok {
				payment.Status = value.Value
			}
		}
		if payment.LastSequence == 0 {
			if value, ok := item["lastSequence"].(*types.AttributeValueMemberN); ok {
				if _, err := fmt.Sscan(value.Value, &payment.LastSequence); err != nil {
					return nil, nil, fmt.Errorf("decode payment sequence: %w", err)
				}
			}
		}
		payment.MerchantID = merchant
		if state != "" {
			payment.Status = state
		}
		payments = append(payments, payment)
	}
	return payments, result.LastEvaluatedKey, nil
}

// PutIdempotencyRecord persists an idempotency record.
func (r *Repository) PutIdempotencyRecord(ctx context.Context, record *model.IdempotencyRecord) error {
	if r.client == nil {
		return fmt.Errorf("DynamoDB client is not configured")
	}
	item, err := marshalItem(record)
	if err != nil {
		return err
	}
	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(r.tableName), Item: item})
	return err
}

// GetAccount eventually reads an account by its base-table keys.
func (r *Repository) GetAccount(ctx context.Context, pk, sk model.AccountKey) (*model.Account, error) {
	if r.client == nil {
		return nil, fmt.Errorf("DynamoDB client is not configured")
	}
	key, err := attributevalue.MarshalMap(model.AccountKeys{PK: pk, SK: sk})
	if err != nil {
		return nil, fmt.Errorf("marshal account key: %w", err)
	}
	result, err := r.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.tableName),
		Key:       key,
	})
	if err != nil || len(result.Item) == 0 {
		return nil, err
	}

	account := &model.Account{}
	if err := attributevalue.UnmarshalMap(result.Item, account); err != nil {
		return nil, err
	}
	if balance, ok := result.Item["currentBalance"].(*types.AttributeValueMemberN); ok {
		account.CurrentBalanceExact = balance.Value
	}
	if balance, ok := result.Item["availableBalance"].(*types.AttributeValueMemberN); ok {
		account.AvailableBalanceExact = balance.Value
	}
	return account, nil
}

// GetAccountForUpdate eventually reads an account before a conditional balance transaction.
// Version guards and bounded retries handle stale account versions.
func (r *Repository) GetAccountForUpdate(ctx context.Context, pk, sk model.AccountKey) (*model.Account, error) {
	return r.GetAccount(ctx, pk, sk)
}

// IsTransactionConflict reports whether DynamoDB canceled a transaction because
// another transaction concurrently modified one of its items.
func IsTransactionConflict(err error) bool {
	var canceled *types.TransactionCanceledException
	if !errors.As(err, &canceled) {
		return false
	}
	for _, reason := range canceled.CancellationReasons {
		if reason.Code != nil && *reason.Code == "TransactionConflict" {
			return true
		}
	}
	return false
}

// GetAccountReservations reads the account separately, then pages only durable
// RESERVATION# rows. That prefix cannot match the ACCOUNT# sort key; querying the
// entire partition instead would unnecessarily read timers and unbounded ledgers.
func (r *Repository) GetAccountReservations(ctx context.Context, accountID string) (*model.Account, []map[string]any, error) {
	account, err := r.GetAccount(ctx, model.AccountKey(accountID), model.AccountKey(accountID))
	if err != nil || account == nil {
		return account, nil, err
	}
	values, err := attributevalue.MarshalMap(map[string]any{
		":pk": model.AccountKey(accountID), ":prefix": model.ReservationPrefix,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal reservation query: %w", err)
	}
	input := &dynamodb.QueryInput{
		TableName:                 aws.String(r.tableName),
		KeyConditionExpression:    aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: values,
	}
	reservations := make([]map[string]any, 0)
	for {
		result, err := r.client.Query(ctx, input)
		if err != nil {
			return nil, nil, err
		}
		for _, item := range result.Items {
			var row model.Reservation
			if err := attributevalue.UnmarshalMap(item, &row); err != nil {
				return nil, nil, fmt.Errorf("decode reservation: %w", err)
			}
			amount, err := model.DecimalJSON(row.Amount, storedDecimal(item, "amount"))
			if err != nil {
				return nil, nil, fmt.Errorf("decode reservation amount: %w", err)
			}
			reservations = append(reservations, map[string]any{
				"reservationId": row.ReservationID, "paymentId": row.PaymentID,
				"amount": amount, "status": row.Status, "createdAtUtc": row.CreatedAtUTC,
			})
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		input.ExclusiveStartKey = result.LastEvaluatedKey
	}
	return account, reservations, nil
}

// GetReservations performs a bounded BatchGetItem lookup with retries for
// unprocessed keys, without requiring an account item to exist.
func (r *Repository) GetReservations(ctx context.Context, accountID string, reservationIDs []string) (map[string]map[string]any, error) {
	keys := make([]map[string]types.AttributeValue, 0, len(reservationIDs))
	for _, id := range reservationIDs {
		key, err := attributevalue.MarshalMap(model.ReservationKeys{PK: model.AccountKey(accountID), SK: model.ReservationKey(id)})
		if err != nil {
			return nil, fmt.Errorf("marshal reservation key: %w", err)
		}
		keys = append(keys, key)
	}
	request := map[string]types.KeysAndAttributes{r.tableName: {Keys: keys}}
	found := make(map[string]map[string]any, len(keys))
	for attempt := 0; attempt < batchReadAttempts; attempt++ {
		result, err := r.client.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{RequestItems: request})
		if err != nil {
			return nil, err
		}
		for _, item := range result.Responses[r.tableName] {
			var row model.Reservation
			if err := attributevalue.UnmarshalMap(item, &row); err != nil {
				return nil, fmt.Errorf("decode reservation: %w", err)
			}
			amount, err := model.DecimalJSON(row.Amount, storedDecimal(item, "amount"))
			if err != nil {
				return nil, fmt.Errorf("decode reservation amount: %w", err)
			}
			found[row.ReservationID] = map[string]any{
				"reservationId": row.ReservationID, "paymentId": row.PaymentID,
				"amount": amount, "status": row.Status, "createdAtUtc": row.CreatedAtUTC,
			}
		}
		request = result.UnprocessedKeys
		if len(request) == 0 {
			return found, nil
		}
		if attempt < batchReadAttempts-1 {
			timer := time.NewTimer(time.Duration(attempt+1) * batchReadBackoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, fmt.Errorf("BatchGetItem unprocessed keys remained after 5 attempts")
}

// PutAccount writes an account and updates its timestamps.
func (r *Repository) PutAccount(ctx context.Context, account *model.Account) error {
	if r.client == nil {
		return fmt.Errorf("DynamoDB client is not configured")
	}
	now := time.Now().UTC()
	if account.CreatedAt.IsZero() {
		account.CreatedAt = now
	}
	account.UpdatedAt = now
	account.EntityType = "ACCOUNT"
	if account.AccountID == "" {
		account.AccountID = string(account.PK)
	}
	if account.Version == 0 {
		account.Version = 1
	}
	item, err := marshalItem(account)
	if err != nil {
		return err
	}
	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(r.tableName), Item: item})
	return err
}

// PutSeedAccount creates a demo account without replacing existing balances.
func (r *Repository) PutSeedAccount(ctx context.Context, account *model.Account) error {
	account.Version = 1
	account.EntityType = "ACCOUNT"
	account.AccountID = string(account.PK)
	now := time.Now().UTC()
	account.CreatedAt, account.UpdatedAt = now, now
	item, err := marshalItem(account)
	if err != nil {
		return err
	}
	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(r.tableName), Item: item,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	return err
}

// GetPayment retrieves a payment stream head by its partition key.
func (r *Repository) GetPayment(ctx context.Context, id string) (*model.Payment, error) {
	if r.client == nil {
		return nil, fmt.Errorf("DynamoDB client is not configured")
	}
	var payment *model.Payment
	var startKey map[string]types.AttributeValue
	var events []model.PaymentEvent
	values, err := attributevalue.MarshalMap(map[string]any{":pk": model.PaymentKey(id)})
	if err != nil {
		return nil, fmt.Errorf("marshal payment query: %w", err)
	}
	for {
		result, err := r.client.Query(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String(r.tableName),
			KeyConditionExpression:    aws.String("PK = :pk"),
			ExpressionAttributeValues: values,
			ConsistentRead:            aws.Bool(true),
			ExclusiveStartKey:         startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("query payment partition: %w", err)
		}
		for _, item := range result.Items {
			sk, _ := item["SK"].(*types.AttributeValueMemberS)
			if sk == nil {
				return nil, fmt.Errorf("payment partition item is missing SK")
			}
			if sk.Value == model.PaymentHeadSK {
				payment = &model.Payment{}
				if err := attributevalue.UnmarshalMap(item, payment); err != nil {
					return nil, fmt.Errorf("decode payment head: %w", err)
				}
				payment.AmountExact = storedDecimal(item, "amount")
			} else if strings.HasPrefix(sk.Value, model.EventPrefix) {
				var event model.PaymentEvent
				if err := attributevalue.UnmarshalMap(item, &event); err != nil {
					return nil, fmt.Errorf("decode payment event: %w", err)
				}
				event.AmountExact = storedDecimal(item, "amount")
				// Read old sample tables, but write only the cross-SDK attribute.
				if _, exists := item["occurredAt"]; !exists {
					if old, ok := item["occurredAtUtc"].(*types.AttributeValueMemberS); ok {
						event.OccurredAt = old.Value
					}
				}
				events = append(events, event)
			}
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		startKey = result.LastEvaluatedKey
	}
	if payment == nil {
		return nil, nil
	}
	payment.Events = events
	return payment, nil
}

// GetIdempotencyRecord strongly reads the idempotency binding for a key.
func (r *Repository) GetIdempotencyRecord(ctx context.Context, key string) (*model.IdempotencyRecord, error) {
	if r.client == nil {
		return nil, fmt.Errorf("DynamoDB client is not configured")
	}
	itemKey, err := attributevalue.MarshalMap(model.IdempotencyKeys{PK: model.IdempotencyKey(key), SK: model.IdempotencySK})
	if err != nil {
		return nil, fmt.Errorf("marshal idempotency key: %w", err)
	}
	result, err := r.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(r.tableName),
		Key:            itemKey,
		ConsistentRead: aws.Bool(true),
	})
	if err != nil || len(result.Item) == 0 {
		return nil, err
	}
	record := &model.IdempotencyRecord{}
	if err := attributevalue.UnmarshalMap(result.Item, record); err != nil {
		return nil, err
	}
	return record, nil
}

// CreateIdempotencyRecord creates an idempotency record representation.
func (r *Repository) CreateIdempotencyRecord(key, requestHash string, retention time.Duration) (*model.IdempotencyRecord, error) {
	now := time.Now().UTC()
	return &model.IdempotencyRecord{
		Item: model.Item{EntityType: "IDEMPOTENCY"}, IdempotencyKeys: model.IdempotencyKeys{PK: model.IdempotencyKey(key), SK: model.IdempotencySK},
		RequestHash: requestHash, CreatedAt: now, ExpiresAt: now.Add(retention),
	}, nil
}

// ReservePayment atomically creates reservation rows, updates available
// balance, appends an event, and advances the payment head.
func (r *Repository) ReservePayment(ctx context.Context, payment *model.Payment, account *model.Account, deadline time.Time) error {
	sequence := payment.LastSequence + 1
	now := time.Now().UTC()
	accountPK := model.AccountKey(payment.DebtorAccountID)
	paymentID := string(payment.PK)
	reservationID := "res_" + paymentID
	amount, err := model.AmountDecimal(payment.Amount, payment.AmountExact)
	if err != nil {
		return fmt.Errorf("reserve amount: %w", err)
	}
	reservation, err := marshalItem(model.Reservation{
		Item:            model.Item{EntityType: "RESERVATION"},
		ReservationKeys: model.ReservationKeys{PK: accountPK, SK: model.ReservationKey(reservationID)},
		ReservationID:   reservationID, PaymentID: paymentID, Amount: payment.Amount, AmountExact: amount,
		Status: "ACTIVE", CreatedAtUTC: model.FormatTimestamp(now), ExpiresAt: deadline.Unix(),
	})
	if err != nil {
		return err
	}
	timer, err := marshalItem(model.ReservationTimer{
		Item:                 model.Item{EntityType: "RESERVATION_TEMP"},
		ReservationTimerKeys: model.ReservationTimerKeys{PK: accountPK, SK: model.ReservationTimerKey(reservationID)},
		ReservationID:        reservationID, PaymentID: paymentID, Amount: payment.Amount, AmountExact: amount,
		Status: "ACTIVE", CreatedAtUTC: model.FormatTimestamp(now),
		TTL: deadline.Unix(), ExpiresAt: deadline.Unix(),
	})
	if err != nil {
		return err
	}
	event, err := attributevalue.MarshalMap(model.PaymentEvent{
		Item: model.Item{EntityType: "PAYMENT_EVENT"}, PaymentEventKeys: model.PaymentEventKeys{PK: payment.PK, SK: model.EventKey(sequence)},
		EventType: "FUNDS_RESERVED", SequenceNumber: sequence, CorrelationID: payment.CorrelationID,
		OccurredAt: model.FormatTimestamp(now), PaymentID: paymentID,
	})
	if err != nil {
		return err
	}
	accountKey, err := attributevalue.MarshalMap(model.AccountKeys{PK: accountPK, SK: accountPK})
	if err != nil {
		return fmt.Errorf("marshal account key: %w", err)
	}
	headKey, err := attributevalue.MarshalMap(model.PaymentKeys{PK: payment.PK, SK: model.PaymentHeadSK})
	if err != nil {
		return fmt.Errorf("marshal payment key: %w", err)
	}
	_, err = r.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(r.tableName), Item: reservation}},
		{Put: &types.Put{TableName: aws.String(r.tableName), Item: timer}},
		{Update: &types.Update{
			TableName:           aws.String(r.tableName),
			Key:                 accountKey,
			UpdateExpression:    aws.String("SET availableBalance = availableBalance - :amount, version = version + :one"),
			ConditionExpression: aws.String("availableBalance >= :amount AND version = :version"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":amount":  &types.AttributeValueMemberN{Value: fmt.Sprint(amount)},
				":one":     &types.AttributeValueMemberN{Value: "1"},
				":version": &types.AttributeValueMemberN{Value: fmt.Sprint(account.Version)},
			},
		}},
		{Update: &types.Update{
			TableName:           aws.String(r.tableName),
			Key:                 headKey,
			UpdateExpression:    aws.String("SET aggregateState = :state, lastSequence = :next, updatedAtUtc = :updated"),
			ConditionExpression: aws.String("lastSequence = :sequence AND aggregateState = :expected"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":state":    &types.AttributeValueMemberS{Value: "FUNDS_RESERVED"},
				":next":     &types.AttributeValueMemberN{Value: fmt.Sprint(sequence)},
				":sequence": &types.AttributeValueMemberN{Value: fmt.Sprint(payment.LastSequence)},
				":expected": &types.AttributeValueMemberS{Value: "RECEIVED"},
				":updated":  &types.AttributeValueMemberS{Value: model.FormatTimestamp(now)},
			},
		}},
		{Put: &types.Put{TableName: aws.String(r.tableName), Item: event}},
	}})
	return settlementError(err, 2, 3)
}

// decimalBalanceAfter subtracts finite decimals exactly for the ledger snapshot.
func decimalBalanceAfter(balance, amount string) (attributevalue.Number, error) {
	current, ok := new(big.Rat).SetString(balance)
	if !ok {
		return "", fmt.Errorf("invalid current balance %q", balance)
	}
	debit, ok := new(big.Rat).SetString(amount)
	if !ok {
		return "", fmt.Errorf("invalid debit amount %q", amount)
	}
	difference := new(big.Rat).Sub(current, debit)
	denominator := new(big.Int).Set(difference.Denom())
	scale := 0
	// A finite decimal's denominator contains only factors of two and five.
	for _, factor := range []int64{2, 5} {
		count := 0
		divisor := big.NewInt(factor)
		for new(big.Int).Mod(denominator, divisor).Sign() == 0 {
			denominator.Quo(denominator, divisor)
			count++
		}
		if count > scale {
			scale = count
		}
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", fmt.Errorf("balance after debit is not a finite decimal")
	}
	return attributevalue.Number(difference.FloatString(scale)), nil
}

// CompletePayment atomically consumes the hold and applies the debit.
func (r *Repository) CompletePayment(ctx context.Context, payment *model.Payment, account *model.Account, now time.Time) error {
	sequence := payment.LastSequence + 1
	accountPK := model.AccountKey(payment.DebtorAccountID)
	paymentID := string(payment.PK)
	reservationID := "res_" + paymentID
	amount, err := model.AmountDecimal(payment.Amount, payment.AmountExact)
	if err != nil {
		return fmt.Errorf("complete amount: %w", err)
	}
	balanceAfter, err := decimalBalanceAfter(account.CurrentBalanceExact, amount)
	if err != nil {
		return fmt.Errorf("calculate ledger balance: %w", err)
	}
	event, err := attributevalue.MarshalMap(model.PaymentEvent{
		Item: model.Item{EntityType: "PAYMENT_EVENT"}, PaymentEventKeys: model.PaymentEventKeys{PK: payment.PK, SK: model.EventKey(sequence)},
		EventType: "COMPLETED", SequenceNumber: sequence, CorrelationID: payment.CorrelationID,
		OccurredAt: model.FormatTimestamp(now), PaymentID: paymentID,
	})
	if err != nil {
		return err
	}
	ledgerID := "led_" + paymentID
	ledger, err := marshalItem(model.LedgerEntry{
		Item:       model.Item{EntityType: "LEDGER_ENTRY"},
		LedgerKeys: model.LedgerKeys{PK: accountPK, SK: model.LedgerKey{CreatedAt: payment.CreatedAt, EntryID: ledgerID}},
		EntryID:    ledgerID, PaymentID: paymentID, EntryType: "DEBIT", Amount: payment.Amount, AmountExact: amount, BalanceAfter: balanceAfter,
		CreatedAtUTC: model.FormatTimestamp(now),
	})
	if err != nil {
		return err
	}
	accountKey, err := attributevalue.MarshalMap(model.AccountKeys{PK: accountPK, SK: accountPK})
	if err != nil {
		return fmt.Errorf("marshal account key: %w", err)
	}
	headKey, err := attributevalue.MarshalMap(model.PaymentKeys{PK: payment.PK, SK: model.PaymentHeadSK})
	if err != nil {
		return fmt.Errorf("marshal payment key: %w", err)
	}
	reservationKey, err := attributevalue.MarshalMap(model.ReservationKeys{PK: accountPK, SK: model.ReservationKey(reservationID)})
	if err != nil {
		return fmt.Errorf("marshal reservation key: %w", err)
	}
	timerKey, err := attributevalue.MarshalMap(model.ReservationTimerKeys{PK: accountPK, SK: model.ReservationTimerKey(reservationID)})
	if err != nil {
		return fmt.Errorf("marshal timer key: %w", err)
	}
	_, err = r.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Update: &types.Update{
			TableName:           aws.String(r.tableName),
			Key:                 accountKey,
			UpdateExpression:    aws.String("SET currentBalance = currentBalance - :amount, version = version + :one"),
			ConditionExpression: aws.String("version = :version"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":amount":  &types.AttributeValueMemberN{Value: fmt.Sprint(amount)},
				":one":     &types.AttributeValueMemberN{Value: "1"},
				":version": &types.AttributeValueMemberN{Value: fmt.Sprint(account.Version)},
			},
		}},
		{Update: &types.Update{
			TableName:                aws.String(r.tableName),
			Key:                      reservationKey,
			UpdateExpression:         aws.String("SET #status = :consumed"),
			ConditionExpression:      aws.String("#status = :active AND expiresAt > :now"),
			ExpressionAttributeNames: map[string]string{"#status": "status"},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":consumed": &types.AttributeValueMemberS{Value: "CONSUMED"},
				":active":   &types.AttributeValueMemberS{Value: "ACTIVE"},
				":now":      &types.AttributeValueMemberN{Value: fmt.Sprint(now.Unix())},
			},
		}},
		{Delete: &types.Delete{
			TableName: aws.String(r.tableName),
			Key:       timerKey,
		}},
		{Put: &types.Put{TableName: aws.String(r.tableName), Item: ledger, ConditionExpression: aws.String("attribute_not_exists(PK)")}},
		{Update: &types.Update{
			TableName:           aws.String(r.tableName),
			Key:                 headKey,
			UpdateExpression:    aws.String("SET aggregateState = :state, lastSequence = :next, updatedAtUtc = :updated"),
			ConditionExpression: aws.String("lastSequence = :sequence AND aggregateState = :expected"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":state":    &types.AttributeValueMemberS{Value: "COMPLETED"},
				":next":     &types.AttributeValueMemberN{Value: fmt.Sprint(sequence)},
				":sequence": &types.AttributeValueMemberN{Value: fmt.Sprint(payment.LastSequence)},
				":expected": &types.AttributeValueMemberS{Value: "FUNDS_RESERVED"},
				":updated":  &types.AttributeValueMemberS{Value: model.FormatTimestamp(now)},
			},
		}},
		{Put: &types.Put{TableName: aws.String(r.tableName), Item: event}},
	}})
	if IsTransactionConflict(err) {
		return err
	}
	if classified := settlementError(err, 0, 4); errors.Is(classified, ErrConcurrentUpdate) {
		return classified
	}
	if conditionalCheckFailedAt(err, 1) {
		return fmt.Errorf("%w: %w", ErrReservationExpired, err)
	}
	return err
}

// RejectPayment records a failed business precondition without money writes.
func (r *Repository) RejectPayment(ctx context.Context, payment *model.Payment, reason string) error {
	now := time.Now().UTC()
	sequence := payment.LastSequence + 1
	event, err := attributevalue.MarshalMap(model.PaymentEvent{
		Item: model.Item{EntityType: "PAYMENT_EVENT"}, PaymentEventKeys: model.PaymentEventKeys{PK: payment.PK, SK: model.EventKey(sequence)},
		EventType: "REJECTED", SequenceNumber: sequence, CorrelationID: payment.CorrelationID,
		ReasonCode: reason, OccurredAt: model.FormatTimestamp(now), PaymentID: string(payment.PK),
	})
	if err != nil {
		return err
	}
	headKey, err := attributevalue.MarshalMap(model.PaymentKeys{PK: payment.PK, SK: model.PaymentHeadSK})
	if err != nil {
		return fmt.Errorf("marshal payment key: %w", err)
	}
	_, err = r.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Update: &types.Update{
			TableName:           aws.String(r.tableName),
			Key:                 headKey,
			UpdateExpression:    aws.String("SET aggregateState = :state, reasonCode = :reason, lastSequence = :next, updatedAtUtc = :updated"),
			ConditionExpression: aws.String("lastSequence = :sequence AND aggregateState = :expected"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":state":    &types.AttributeValueMemberS{Value: "REJECTED"},
				":reason":   &types.AttributeValueMemberS{Value: reason},
				":next":     &types.AttributeValueMemberN{Value: fmt.Sprint(sequence)},
				":sequence": &types.AttributeValueMemberN{Value: fmt.Sprint(payment.LastSequence)},
				":expected": &types.AttributeValueMemberS{Value: payment.Status},
				":updated":  &types.AttributeValueMemberS{Value: model.FormatTimestamp(now)},
			},
		}},
		{Put: &types.Put{TableName: aws.String(r.tableName), Item: event}},
	}})
	return settlementError(err, 0)
}

// ReleaseReservation restores available funds exactly once after a timer removal.
func (r *Repository) ReleaseReservation(ctx context.Context, accountID, reservationID string) error {
	accountPK := model.AccountKey(accountID)
	reservationKey, err := attributevalue.MarshalMap(model.ReservationKeys{PK: accountPK, SK: model.ReservationKey(reservationID)})
	if err != nil {
		return fmt.Errorf("marshal reservation key: %w", err)
	}
	accountKey, err := attributevalue.MarshalMap(model.AccountKeys{PK: accountPK, SK: accountPK})
	if err != nil {
		return fmt.Errorf("marshal account key: %w", err)
	}
	result, err := r.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(r.tableName),
		Key:            reservationKey,
		ConsistentRead: aws.Bool(true),
	})
	if err != nil || len(result.Item) == 0 {
		return err
	}
	var reservation model.Reservation
	if err := attributevalue.UnmarshalMap(result.Item, &reservation); err != nil {
		return err
	}
	amount, err := model.AmountDecimal(reservation.Amount, storedDecimal(result.Item, "amount"))
	if err != nil {
		return fmt.Errorf("release amount: %w", err)
	}
	if reservation.Status != "ACTIVE" {
		return nil
	}
	var lastErr error
	for attempt := 0; attempt < releaseAttempts; attempt++ {
		account, err := r.GetAccountForUpdate(ctx, accountPK, accountPK)
		if err != nil {
			return err
		}
		if account == nil {
			return fmt.Errorf("release account %s does not exist", accountID)
		}
		_, lastErr = r.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
			{Update: &types.Update{
				TableName:                aws.String(r.tableName),
				Key:                      reservationKey,
				UpdateExpression:         aws.String("SET #status = :released"),
				ConditionExpression:      aws.String("#status = :active"),
				ExpressionAttributeNames: map[string]string{"#status": "status"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":released": &types.AttributeValueMemberS{Value: "RELEASED"},
					":active":   &types.AttributeValueMemberS{Value: "ACTIVE"},
				},
			}},
			{Update: &types.Update{
				TableName:           aws.String(r.tableName),
				Key:                 accountKey,
				UpdateExpression:    aws.String("SET availableBalance = availableBalance + :amount, version = version + :one"),
				ConditionExpression: aws.String("version = :version"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":amount":  &types.AttributeValueMemberN{Value: amount},
					":one":     &types.AttributeValueMemberN{Value: "1"},
					":version": &types.AttributeValueMemberN{Value: fmt.Sprint(account.Version)},
				},
			}},
		}})
		if lastErr == nil {
			return nil
		}
		var canceled *types.TransactionCanceledException
		if errors.As(lastErr, &canceled) && len(canceled.CancellationReasons) > 0 &&
			canceled.CancellationReasons[0].Code != nil && *canceled.CancellationReasons[0].Code == "ConditionalCheckFailed" {
			return nil
		}
		retryable := IsTransactionConflict(lastErr)
		if errors.As(lastErr, &canceled) && len(canceled.CancellationReasons) > 1 &&
			canceled.CancellationReasons[1].Code != nil && *canceled.CancellationReasons[1].Code == "ConditionalCheckFailed" {
			retryable = true
		}
		if !retryable {
			return fmt.Errorf("release reservation: %w", lastErr)
		}
		if attempt < releaseAttempts-1 {
			timer := time.NewTimer(time.Duration(attempt+1) * releaseBackoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("release reservation retries exhausted: %w", lastErr)
}

// NewRepository constructs a Repository using the configured DynamoDB table.
func NewRepository(log *zap.Logger, client *dynamodb.Client, settings *env.Env) *Repository {
	return &Repository{log: log, client: client, tableName: settings.TableName}
}
