//go:build integration

// This suite verifies expiry guards, exact settlement, head rewrites, and GSI
// pagination against DynamoDB Local with isolated disposable tables.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"instantpayments/internal/testutil"
	"instantpayments/pkg/aws"
	"instantpayments/pkg/model"
)

func TestDynamoDBExactIntentSettlementAndRelease(t *testing.T) {
	ctx, client, repository, table := dynamoDBIntegrationRepository(t)
	const exactAmount = "1.0000000000000001"
	accountKey, err := attributevalue.MarshalMap(model.AccountKeys{PK: "acc_usd_1", SK: "acc_usd_1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(table), Key: accountKey,
		UpdateExpression: awssdk.String("SET currentBalance = :balance, availableBalance = :balance"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":balance": &types.AttributeValueMemberN{Value: "1000.0000000000000001"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	payment := NewPayment(zap.NewNop(), repository)
	created, status, err := payment.Create(preciseInput(t, "exact-intent", exactAmount))
	if err != nil || status != 201 {
		t.Fatalf("create precise intent: %#v %d %v", created, status, err)
	}
	replay, status, err := payment.Create(preciseInput(t, "exact-intent", "1.000000000000000100e0"))
	if err != nil || status != 200 || replay.PaymentID != created.PaymentID {
		t.Fatalf("durable decimal replay: %#v %d %v", replay, status, err)
	}
	_, _, err = payment.Create(preciseInput(t, "exact-intent", "1.0000000000000002"))
	requireDomainError(t, err, 409, "IDEMPOTENCY_CONFLICT")
	row, err := payment.loadPayment(created.PaymentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ReservePayment(ctx, modelPayment(row), &model.Account{Version: 1}, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Inspect both hold rows before completion removes the timer.
	holds, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName: awssdk.String(table), ConsistentRead: awssdk.Bool(true),
		KeyConditionExpression: awssdk.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: "ACCOUNT#acc_usd_1"},
			":prefix": &types.AttributeValueMemberS{Value: "RESERVATION"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(holds.Items) != 2 {
		t.Fatalf("expected audit and timer, got %d", len(holds.Items))
	}
	for _, item := range holds.Items {
		amount, ok := item["amount"].(*types.AttributeValueMemberN)
		if !ok || amount.Value != exactAmount {
			t.Fatalf("hold lost exact amount: %#v", item)
		}
	}
	for name, read := range map[string]func() (map[string]any, error){
		"overview": func() (map[string]any, error) { return payment.Account("acc_usd_1") },
		"batch": func() (map[string]any, error) {
			return payment.Batch("acc_usd_1", []string{"res_" + created.PaymentID})
		},
	} {
		result, err := read()
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(result)
		if err != nil || !strings.Contains(string(encoded), `"amount":`+exactAmount) {
			t.Fatalf("%s lost precision: %s %v", name, encoded, err)
		}
	}
	if result, err := payment.Process(created.PaymentID); err != nil || result["state"] != "COMPLETED" {
		t.Fatalf("precise settlement: %#v %v", result, err)
	}
	persisted, err := repository.GetPayment(ctx, created.PaymentID)
	if err != nil || persisted.AmountExact != exactAmount || persisted.Events[0].AmountExact != exactAmount {
		t.Fatalf("head/event lost exact intent: %#v %v", persisted, err)
	}
	ledger, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName: awssdk.String(table), ConsistentRead: awssdk.Bool(true),
		KeyConditionExpression: awssdk.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: "ACCOUNT#acc_usd_1"},
			":prefix": &types.AttributeValueMemberS{Value: model.LedgerPrefix},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Items) != 1 {
		t.Fatalf("expected exactly one debit, got %d", len(ledger.Items))
	}
	for name, want := range map[string]string{"amount": exactAmount, "balanceAfter": "999"} {
		value, ok := ledger.Items[0][name].(*types.AttributeValueMemberN)
		if !ok || value.Value != want {
			t.Fatalf("ledger %s=%#v, want %s", name, ledger.Items[0][name], want)
		}
	}
	released, _, err := payment.Create(preciseInput(t, "exact-release", exactAmount))
	if err != nil {
		t.Fatal(err)
	}
	row, err = payment.loadPayment(released.PaymentID)
	if err != nil {
		t.Fatal(err)
	}
	account, err := repository.GetAccount(ctx, "acc_usd_1", "acc_usd_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ReservePayment(ctx, modelPayment(row), account, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := repository.ReleaseReservation(ctx, "acc_usd_1", "res_"+released.PaymentID); err != nil {
			t.Fatal(err)
		}
	}
	account, err = repository.GetAccount(ctx, "acc_usd_1", "acc_usd_1")
	if err != nil {
		t.Fatal(err)
	}
	current, err := model.DecimalString(account.CurrentBalanceExact)
	if err != nil {
		t.Fatal(err)
	}
	available, err := model.DecimalString(account.AvailableBalanceExact)
	if err != nil || current != "999" || available != "999" {
		t.Fatalf("release must restore exact hold once: %#v %v", account, err)
	}
}

// dynamoDBIntegrationRepository activates the shared Local test profile and gives
// each test a disposable table reset to the schema and demo account baseline.
// Cleanup deletes only that test's table using a fresh context.
func dynamoDBIntegrationRepository(t *testing.T) (context.Context, *dynamodb.Client, *aws.Repository, string) {
	t.Helper()
	settings := testutil.LocalSettings(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	config, err := aws.Config(settings)
	if err != nil {
		t.Fatal(err)
	}
	client := aws.DynamoDB(config)
	table := "GoInstantPaymentsTest-" + uuid.NewString()
	settings.TableName = table
	repository := aws.NewRepository(zap.NewNop(), client, settings)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := deleteIntegrationTable(cleanupCtx, client, table); err != nil {
			t.Errorf("delete integration-test table %s: %v", table, err)
		}
	})
	resetDynamoDBIntegrationTable(t, ctx, client, repository, table)
	return ctx, client, repository, table
}

// deleteIntegrationTable deletes only UUID-named fixture tables and waits until absent.
// An absent table is already reset. Other SDK errors are returned to the caller.
func deleteIntegrationTable(ctx context.Context, client *dynamodb.Client, table string) error {
	const prefix = "GoInstantPaymentsTest-"
	if !strings.HasPrefix(table, prefix) {
		return fmt.Errorf("refusing to delete non-integration table %q", table)
	}
	if _, err := uuid.Parse(strings.TrimPrefix(table, prefix)); err != nil {
		return fmt.Errorf("refusing to delete non-UUID integration table %q: %w", table, err)
	}
	if _, err := client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: awssdk.String(table)}); err != nil {
		var missing *types.ResourceNotFoundException
		if errors.As(err, &missing) {
			return nil
		}
		return fmt.Errorf("delete table: %w", err)
	}
	return dynamodb.NewTableNotExistsWaiter(client).Wait(ctx, &dynamodb.DescribeTableInput{
		TableName: awssdk.String(table),
	}, 30*time.Second)
}

// resetDynamoDBIntegrationTable deletes and recreates a test-owned table, waits for
// its schema to become available, then seeds and verifies all ten demo accounts.
func resetDynamoDBIntegrationTable(t *testing.T, ctx context.Context, client *dynamodb.Client, repository *aws.Repository, table string) {
	t.Helper()
	if err := deleteIntegrationTable(ctx, client, table); err != nil {
		t.Fatalf("reset integration table %s: %v", table, err)
	}
	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: awssdk.String(table), BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: awssdk.String("PK"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: awssdk.String("SK"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: awssdk.String("merchantId"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: awssdk.String("paymentId"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: awssdk.String("aggregateState"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: awssdk.String("createdAtUtc"), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: awssdk.String("PK"), KeyType: types.KeyTypeHash},
			{AttributeName: awssdk.String("SK"), KeyType: types.KeyTypeRange},
		},
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{
			{
				IndexName: awssdk.String("GSI_MERCHANT_PAYMENTS"),
				KeySchema: []types.KeySchemaElement{
					{AttributeName: awssdk.String("merchantId"), KeyType: types.KeyTypeHash},
					{AttributeName: awssdk.String("createdAtUtc"), KeyType: types.KeyTypeRange},
					{AttributeName: awssdk.String("paymentId"), KeyType: types.KeyTypeRange},
				},
				Projection: &types.Projection{ProjectionType: types.ProjectionTypeAll},
			},
			{
				IndexName: awssdk.String("GSI_MERCHANT_STATE_PAYMENTS"),
				KeySchema: []types.KeySchemaElement{
					{AttributeName: awssdk.String("merchantId"), KeyType: types.KeyTypeHash},
					{AttributeName: awssdk.String("aggregateState"), KeyType: types.KeyTypeHash},
					{AttributeName: awssdk.String("createdAtUtc"), KeyType: types.KeyTypeRange},
				},
				Projection: &types.Projection{
					ProjectionType:   types.ProjectionTypeInclude,
					NonKeyAttributes: model.MerchantStateProjection(),
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := dynamodb.NewTableExistsWaiter(client).Wait(ctx, &dynamodb.DescribeTableInput{
		TableName: awssdk.String(table),
	}, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	for _, currency := range []string{"USD", "EUR"} {
		for index, balance := range []float64{10000, 5000, 500, 100, 0} {
			id := fmt.Sprintf("acc_%s_%d", strings.ToLower(currency), index+1)
			if err := repository.PutSeedAccount(ctx, &model.Account{
				AccountKeys: model.AccountKeys{PK: model.AccountKey(id), SK: model.AccountKey(id)},
				Currency:    currency, Status: model.AccountStatusActive,
				CurrentBalance: balance, AvailableBalance: balance,
			}); err != nil {
				t.Fatal(err)
			}
			key, err := attributevalue.MarshalMap(model.AccountKeys{PK: model.AccountKey(id), SK: model.AccountKey(id)})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.GetItem(ctx, &dynamodb.GetItemInput{
				TableName: awssdk.String(table), Key: key, ConsistentRead: awssdk.Bool(true),
			})
			if err != nil {
				t.Fatal(err)
			}
			var account model.Account
			if err := attributevalue.UnmarshalMap(result.Item, &account); err != nil {
				t.Fatal(err)
			}
			if account.AccountID != id || account.Currency != currency || account.Status != model.AccountStatusActive ||
				account.Version != 1 || account.CurrentBalance != balance || account.AvailableBalance != balance {
				t.Fatalf("incorrect reset seed %s: %#v", id, account)
			}
		}
	}
}

func TestDynamoDBIntegrationResetRemovesPriorStateAndRestoresSeeds(t *testing.T) {
	ctx, client, repository, table := dynamoDBIntegrationRepository(t)
	if err := repository.PutAccount(ctx, &model.Account{
		AccountKeys: model.AccountKeys{PK: model.AccountKey("acc_usd_2"), SK: model.AccountKey("acc_usd_2")}, Currency: "USD",
		CurrentBalance: 1, AvailableBalance: 0, Status: model.AccountStatusInactive, Version: 99,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewPayment(zap.NewNop(), repository).Create(validInput("reset-fixture")); err != nil {
		t.Fatal(err)
	}
	resetDynamoDBIntegrationTable(t, ctx, client, repository, table)
	result, err := client.Scan(ctx, &dynamodb.ScanInput{
		TableName: awssdk.String(table), ConsistentRead: awssdk.Bool(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 10 || len(result.LastEvaluatedKey) != 0 {
		t.Fatalf("reset must remove old state and leave only ten seeds, got %d items", len(result.Items))
	}
	for _, name := range []string{"GoInstantPayments", "GoInstantPaymentsTest-not-a-uuid"} {
		if err := deleteIntegrationTable(ctx, nil, name); err == nil {
			t.Fatalf("unsafe table deletion accepted: %s", name)
		}
	}
}

func TestDynamoDBExpiredReservations(t *testing.T) {
	ctx, client, repository, table := dynamoDBIntegrationRepository(t)
	for _, status := range []string{"ACTIVE", "RELEASED"} {
		t.Run(status, func(t *testing.T) {
			accountPK := "ACCOUNT#acc_usd_1"
			if err := repository.PutAccount(ctx, &model.Account{
				AccountKeys: model.AccountKeys{PK: model.AccountKey("acc_usd_1"), SK: model.AccountKey("acc_usd_1")}, Currency: "USD",
				CurrentBalance: 1000, AvailableBalance: 1000, Version: 1, Status: model.AccountStatusActive,
			}); err != nil {
				t.Fatal(err)
			}
			payment := NewPayment(zap.NewNop(), repository)
			created, _, err := payment.Create(validInput(uuid.NewString()))
			if err != nil {
				t.Fatal(err)
			}
			row, err := payment.loadPayment(created.PaymentID)
			if err != nil {
				t.Fatal(err)
			}
			if err := repository.ReservePayment(ctx, modelPayment(row), &model.Account{Version: 1}, time.Now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			reservationID := "res_" + created.PaymentID
			expectedAvailable := float64(900)
			if status == "RELEASED" {
				if _, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
					TableName: awssdk.String(table), Key: map[string]types.AttributeValue{
						"PK": &types.AttributeValueMemberS{Value: accountPK},
						"SK": &types.AttributeValueMemberS{Value: "RESERVATION_TEMP#" + reservationID},
					},
				}); err != nil {
					t.Fatal(err)
				}
				for delivery := 0; delivery < 2; delivery++ {
					if err := repository.ReleaseReservation(ctx, "acc_usd_1", reservationID); err != nil {
						t.Fatal(err)
					}
				}
				expectedAvailable = 1000
			}
			for attempt := 0; attempt < 2; attempt++ {
				result, err := payment.Process(created.PaymentID)
				if err != nil || result["state"] != "REJECTED" || result["reasonCode"] != "RESERVATION_EXPIRED" {
					t.Fatalf("expected expired-hold rejection, got %#v, %v", result, err)
				}
			}
			tracked, err := payment.Get(created.PaymentID)
			if err != nil || tracked["version"] != 3 {
				t.Fatalf("expected replayable three-event rejection, got %#v, %v", tracked, err)
			}
			account, err := repository.GetAccountForUpdate(ctx, model.AccountKey("acc_usd_1"), model.AccountKey("acc_usd_1"))
			if err != nil || account.CurrentBalance != 1000 || account.AvailableBalance != expectedAvailable {
				t.Fatalf("expiry must not debit funds, got %#v, %v", account, err)
			}
			items, err := client.Query(ctx, &dynamodb.QueryInput{
				TableName: awssdk.String(table), KeyConditionExpression: awssdk.String("PK = :pk"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: accountPK}},
				ConsistentRead:            awssdk.Bool(true),
			})
			if err != nil {
				t.Fatal(err)
			}
			foundReservation, foundTimer := false, false
			for _, item := range items.Items {
				sk := item["SK"].(*types.AttributeValueMemberS).Value
				if strings.HasPrefix(sk, "LEDGER#") {
					t.Fatalf("expired payment must not create a ledger debit: %s", sk)
				}
				if sk == "RESERVATION#"+reservationID {
					foundReservation = true
					if item["status"].(*types.AttributeValueMemberS).Value != status {
						t.Fatalf("rejection changed the reservation: %#v", item)
					}
				}
				if sk == "RESERVATION_TEMP#"+reservationID {
					foundTimer = true
				}
			}
			if !foundReservation || foundTimer != (status == "ACTIVE") {
				t.Fatalf("unexpected reservation/timer mutation: reservation=%v timer=%v", foundReservation, foundTimer)
			}
		})
	}
}

func TestDynamoDBFractionalSettlement(t *testing.T) {
	ctx, client, repository, table := dynamoDBIntegrationRepository(t)
	for _, balance := range []string{"0.3", "100.00000000000000001"} {
		t.Run(balance, func(t *testing.T) {
			accountPK := "ACCOUNT#acc_usd_1"
			if err := repository.PutAccount(ctx, &model.Account{
				AccountKeys: model.AccountKeys{PK: model.AccountKey("acc_usd_1"), SK: model.AccountKey("acc_usd_1")}, Currency: "USD",
				CurrentBalance: 0.3, AvailableBalance: 0.3, Version: 1, Status: model.AccountStatusActive,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
				TableName: awssdk.String(table),
				Key: map[string]types.AttributeValue{
					"PK": &types.AttributeValueMemberS{Value: accountPK},
					"SK": &types.AttributeValueMemberS{Value: accountPK},
				},
				UpdateExpression: awssdk.String("SET currentBalance = :balance, availableBalance = :balance"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":balance": &types.AttributeValueMemberN{Value: balance},
				},
			}); err != nil {
				t.Fatal(err)
			}
			payment := NewPayment(zap.NewNop(), repository)
			input := validInput(uuid.NewString())
			input.Amount = 0.1
			created, _, err := payment.Create(input)
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				result, err := payment.Process(created.PaymentID)
				if err != nil || result["state"] != "COMPLETED" {
					t.Fatalf("fractional payment failed: %#v, %v", result, err)
				}
			}
			account, err := repository.GetAccountForUpdate(ctx, model.AccountKey("acc_usd_1"), model.AccountKey("acc_usd_1"))
			if err != nil {
				t.Fatal(err)
			}
			want := "0.2"
			if balance != "0.3" {
				want = "99.90000000000000001"
			}
			if account.CurrentBalanceExact != want {
				t.Fatalf("current balance = %q, want %q", account.CurrentBalanceExact, want)
			}
			items, err := client.Query(ctx, &dynamodb.QueryInput{
				TableName: awssdk.String(table), KeyConditionExpression: awssdk.String("PK = :pk AND begins_with(SK, :ledger)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pk": &types.AttributeValueMemberS{Value: accountPK}, ":ledger": &types.AttributeValueMemberS{Value: "LEDGER#"},
				},
				ConsistentRead: awssdk.Bool(true),
			})
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, item := range items.Items {
				if item["paymentId"].(*types.AttributeValueMemberS).Value == created.PaymentID {
					found++
					if got := item["balanceAfter"].(*types.AttributeValueMemberN).Value; got != account.CurrentBalanceExact {
						t.Fatalf("ledger balance %q differs from account balance %q", got, account.CurrentBalanceExact)
					}
				}
			}
			if found != 1 {
				t.Fatalf("expected exactly one debit for this payment, got %d", found)
			}
		})
	}
}

func TestDynamoDBExactInsufficientFunds(t *testing.T) {
	ctx, _, repository, _ := dynamoDBIntegrationRepository(t)
	if err := repository.PutAccount(ctx, &model.Account{
		AccountKeys: model.AccountKeys{PK: model.AccountKey("acc_usd_1"), SK: model.AccountKey("acc_usd_1")}, Currency: "USD",
		CurrentBalance: 100, AvailableBalance: 100, Version: 1, Status: model.AccountStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	payment := NewPayment(zap.NewNop(), repository)
	for _, amount := range []float64{1e-17, 100} {
		input := validInput(uuid.NewString())
		input.Amount = amount
		created, _, err := payment.Create(input)
		if err != nil {
			t.Fatal(err)
		}
		wantState, wantReason := "COMPLETED", any(nil)
		if amount == 100 {
			wantState, wantReason = "REJECTED", "INSUFFICIENT_FUNDS"
		}
		for attempt := 0; attempt < 2; attempt++ {
			result, err := payment.ProcessContext(ctx, created.PaymentID)
			if err != nil || result["state"] != wantState || result["reasonCode"] != wantReason {
				t.Fatalf("amount %v: expected %s/%v, got %#v, %v", amount, wantState, wantReason, result, err)
			}
		}
		tracked, err := payment.Get(created.PaymentID)
		if err != nil || tracked["state"] != wantState {
			t.Fatalf("terminal state did not replay: %#v, %v", tracked, err)
		}
	}
	account, err := repository.GetAccountForUpdate(ctx, model.AccountKey("acc_usd_1"), model.AccountKey("acc_usd_1"))
	if err != nil {
		t.Fatal(err)
	}
	const want = "99.99999999999999999"
	if account.CurrentBalanceExact != want || account.AvailableBalanceExact != want {
		t.Fatalf("rejected payment changed funds: current=%s available=%s", account.CurrentBalanceExact, account.AvailableBalanceExact)
	}
}

func TestDynamoDBRejectedHeadRewritePreservesReason(t *testing.T) {
	ctx, client, repository, table := dynamoDBIntegrationRepository(t)
	if err := repository.PutAccount(ctx, &model.Account{
		AccountKeys: model.AccountKeys{PK: model.AccountKey("acc_usd_1"), SK: model.AccountKey("acc_usd_1")}, Currency: "USD",
		Version: 1, Status: model.AccountStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	payment := NewPayment(zap.NewNop(), repository)
	input := validInput(uuid.NewString())
	created, _, err := payment.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := payment.ProcessContext(ctx, created.PaymentID)
	if err != nil || result["state"] != "REJECTED" {
		t.Fatalf("expected rejection: %#v, %v", result, err)
	}
	head, err := repository.GetPayment(ctx, created.PaymentID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the old timestamp encoding, then read/rewrite through the migration path.
	headKey, err := attributevalue.MarshalMap(head.PaymentKeys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        awssdk.String(table),
		Key:              headKey,
		UpdateExpression: awssdk.String("SET createdAtUtc = :created"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":created": &types.AttributeValueMemberS{Value: "2026-01-01T00:00:00.1Z"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	head, err = repository.GetPayment(ctx, created.PaymentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.PutPayment(ctx, head); err != nil {
		t.Fatal(err)
	}
	rewritten, err := repository.GetPayment(ctx, created.PaymentID)
	if err != nil || rewritten.ReasonCode != "INSUFFICIENT_FUNDS" {
		t.Fatalf("rewrite lost rejection reason: %#v, %v", rewritten, err)
	}
	row, err := paymentFromModel(rewritten)
	if err != nil || row.RejectReason != rewritten.ReasonCode {
		t.Fatalf("head reason differs from replay: %#v, %v", row, err)
	}
	for _, state := range []string{"", "REJECTED"} {
		route := "merchant"
		if state != "" {
			route = "state"
		}
		for {
			page, err := payment.List(input.MerchantID, state, route, "", 10, true)
			if err != nil {
				t.Fatal(err)
			}
			items := page["items"].([]any)
			if len(items) == 1 {
				item := items[0].(map[string]any)
				if item["reasonCode"] == "INSUFFICIENT_FUNDS" && item["createdAtUtc"] == "2026-01-01T00:00:00.100Z" {
					break
				}
			}
			select {
			case <-ctx.Done():
				t.Fatalf("GSI %s did not preserve rewritten rejection: %#v", route, page)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
}

func TestDynamoDBGSIChronologicalPagination(t *testing.T) {
	ctx, _, repository, _ := dynamoDBIntegrationRepository(t)
	timestamps := []string{
		"2026-01-01T00:00:00Z",
		"2026-01-01T00:00:00.1Z",
		"2026-01-01T00:00:00.100000001Z",
		"2026-01-01T00:00:00.11Z",
		"2026-01-01T00:00:00.9Z",
	}
	for index, timestamp := range timestamps {
		created, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("pay_%d", index)
		head := &model.Payment{
			PaymentKeys: model.PaymentKeys{PK: model.PaymentKey(id), SK: "#HEAD"},
			MerchantID:  "merchant-ordering", Status: "RECEIVED", CreatedAt: created, UpdatedAt: created,
		}
		if index%2 == 0 {
			err = repository.PutPayment(ctx, head)
		} else {
			err = repository.CreatePayment(ctx, head, &model.PaymentEvent{
				PaymentEventKeys: model.PaymentEventKeys{PK: head.PK, SK: 1},
				PaymentID:        id,
			}, &model.IdempotencyRecord{
				IdempotencyKeys: model.IdempotencyKeys{PK: model.IdempotencyKey(id), SK: "IDEMPOTENCY"},
			})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []string{"", "RECEIVED"} {
		for _, forward := range []bool{true, false} {
			t.Run(fmt.Sprintf("state=%s/forward=%t", state, forward), func(t *testing.T) {
				// Wait for the eventually consistent GSI before testing page boundaries.
				for {
					rows, _, err := repository.ListPaymentPage(ctx, "merchant-ordering", state, 100, forward, nil)
					if err != nil {
						t.Fatal(err)
					}
					if len(rows) == len(timestamps) {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(20 * time.Millisecond):
					}
				}
				payment := NewPayment(zap.NewNop(), repository)
				route, token := "merchant", ""
				if state != "" {
					route = "state"
				}
				for page := 0; page < len(timestamps); page++ {
					result, err := payment.List("merchant-ordering", state, route, token, 1, forward)
					if err != nil {
						t.Fatal(err)
					}
					items := result["items"].([]any)
					index := page
					if !forward {
						index = len(timestamps) - 1 - page
					}
					want := fmt.Sprintf("pay_%d", index)
					if len(items) != 1 || items[0].(map[string]any)["paymentId"] != want {
						t.Fatalf("page %d: got %#v, want %s", page, result, want)
					}
					token, _ = result["nextToken"].(string)
					if page < len(timestamps)-1 && token == "" {
						t.Fatalf("missing continuation on page %d", page)
					}
				}
				if token != "" {
					last, err := payment.List("merchant-ordering", state, route, token, 1, forward)
					if err != nil || len(last["items"].([]any)) != 0 || last["nextToken"] != nil {
						t.Fatalf("unexpected final page: %#v, %v", last, err)
					}
				}
			})
		}
	}
}
