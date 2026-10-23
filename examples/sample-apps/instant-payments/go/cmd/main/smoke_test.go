//go:build smoke

// This suite exercises documentation, stream-driven settlement, and expired-timer
// release against DynamoDB Local using disposable tables and real application wiring.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"instantpayments/internal/testutil"
	"instantpayments/pkg/aws"
	"instantpayments/pkg/dto"
	"instantpayments/pkg/env"
	apphttp "instantpayments/pkg/http"
	"instantpayments/pkg/http/api"
	v1 "instantpayments/pkg/http/api/v1"
	"instantpayments/pkg/input"
	"instantpayments/pkg/model"
	"instantpayments/pkg/service"
	"instantpayments/pkg/workerpool"
)

// smokeApplication uses a disposable table and real DynamoDB Local stream APIs;
// HTTP requests use the application's registered mux, not mocked controllers.
func smokeApplication(t *testing.T) (context.Context, *dynamodb.Client, *aws.Repository, *httptest.Server, string) {
	t.Helper()
	settings := testutil.LocalSettings(t)
	endpoint := settings.DynamoDBEndpoint
	table := "GoInstantPaymentsSmoke-" + uuid.NewString()
	t.Setenv("DYNAMODB_ENDPOINT", endpoint)
	t.Setenv("APP_PROFILE", "test")
	t.Setenv("DOCS_ENABLED", "true")
	t.Setenv("AWS_REGION", "eu-west-1")
	t.Setenv("DYNAMODB_CLIENTTYPE", "low-level")
	t.Setenv("DYNAMODB_TABLE_INSTANTPAYMENTS_NAME", table)
	t.Setenv("DYNAMODB_PROVISIONING_CREATERESOURCES", "true")
	t.Setenv("DYNAMODB_STREAMS_ENABLED", "true")
	t.Setenv("DYNAMODB_STREAMS_ITERATOR_TYPE", "TRIM_HORIZON")
	t.Setenv("AWS_ACCESS_KEY_ID", "local")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "local")
	t.Setenv("AWS_SESSION_TOKEN", "")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	var client *dynamodb.Client
	var repository *aws.Repository
	var mux *stdhttp.ServeMux
	application := fx.New(
		fx.NopLogger,
		fx.Supply(zap.NewNop()),
		env.Module(), input.Module(), apphttp.Module(), api.Module(), v1.Module(),
		workerpool.Module(), aws.Module(), service.Module(),
		fx.Decorate(func(settings *env.Env) *env.Env {
			settings.TableName = table
			settings.CreateResources = true
			settings.StreamsEnabled = true
			settings.StreamIteratorType = "TRIM_HORIZON"
			settings.HTTPAsyncCoreSize = 4
			settings.HTTPAsyncMaxSize = 8
			settings.HTTPAsyncQueueSize = 50
			return settings
		}),
		fx.Invoke(initializeDynamoDB),
		fx.Invoke(validateDynamoDBSchema),
		fx.Invoke(seedAccounts),
		fx.Populate(&client, &repository, &mux),
	)
	if err := application.Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := application.Stop(stopCtx); err != nil {
			t.Errorf("stop smoke application: %v", err)
		}
		if _, err := client.DeleteTable(stopCtx, &dynamodb.DeleteTableInput{TableName: awssdk.String(table)}); err != nil {
			t.Errorf("delete smoke table %s: %v", table, err)
		}
	})
	if err := application.Start(ctx); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return ctx, client, repository, server, table
}

func TestSmokeDocumentationAndStreamSettlement(t *testing.T) {
	ctx, _, repository, server, _ := smokeApplication(t)
	for _, path := range []string{"/api-docs", "/swagger-ui/index.html"} {
		result, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, result.Body)
		result.Body.Close()
		if readErr != nil || result.StatusCode != stdhttp.StatusOK {
			t.Fatalf("documentation %s: status %d, %v", path, result.StatusCode, readErr)
		}
	}
	payload, err := json.Marshal(dto.OutboundPaymentInput{
		IdempotencyKey: uuid.NewString(), MerchantID: "smoke", DebtorAccountID: "acc_usd_1",
		CreditorIban: "DE89370400440532013000", CreditorName: "Smoke Payee", Currency: "USD", Amount: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := server.Client().Post(server.URL+"/api/v1/payments/outbound", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	var created dto.OutboundPaymentOutput
	if err := json.NewDecoder(result.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != stdhttp.StatusCreated || created.PaymentID == "" {
		t.Fatalf("create status %d: %#v", result.StatusCode, created)
	}
	awaitSmoke(t, ctx, func() (bool, error) {
		payment, err := repository.GetPayment(ctx, created.PaymentID)
		return payment != nil && payment.Status == "COMPLETED" && len(payment.Events) == 3, err
	})
	account, err := repository.GetAccountForUpdate(ctx, "acc_usd_1", "acc_usd_1")
	if err != nil || account == nil || account.CurrentBalance != 9990 || account.AvailableBalance != 9990 {
		t.Fatalf("stream settlement balances: %#v, %v", account, err)
	}
}

// DynamoDB Local exposes TTL configuration but does not run managed TTL expiry.
// Delete the expired timer explicitly, then verify its real REMOVE stream event
// releases the durable hold; this does not claim to test AWS's expiry scheduler.
func TestSmokeExpiredTimerStreamRelease(t *testing.T) {
	ctx, client, repository, _, table := smokeApplication(t)
	ttl, err := client.DescribeTimeToLive(ctx, &dynamodb.DescribeTimeToLiveInput{TableName: awssdk.String(table)})
	if err != nil {
		t.Fatal(err)
	}
	if ttl.TimeToLiveDescription == nil || awssdk.ToString(ttl.TimeToLiveDescription.AttributeName) != "ttl" ||
		(ttl.TimeToLiveDescription.TimeToLiveStatus != types.TimeToLiveStatusEnabled && ttl.TimeToLiveDescription.TimeToLiveStatus != types.TimeToLiveStatusEnabling) {
		t.Fatalf("TTL not configured: %#v", ttl)
	}
	id := "pay_" + uuid.NewString()
	now := time.Now().UTC()
	payment := &model.Payment{
		PaymentKeys: model.PaymentKeys{PK: model.PaymentKey(id), SK: model.PaymentHeadSK},
		MerchantID:  "smoke", DebtorAccountID: "acc_usd_1", Currency: "USD", Amount: 100,
		Status: "RECEIVED", LastSequence: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.PutPayment(ctx, payment); err != nil {
		t.Fatal(err)
	}
	account, err := repository.GetAccountForUpdate(ctx, "acc_usd_1", "acc_usd_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ReservePayment(ctx, payment, account, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	reservationID := "res_" + id
	_, err = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: awssdk.String(table),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "ACCOUNT#acc_usd_1"},
			"SK": &types.AttributeValueMemberS{Value: "RESERVATION_TEMP#" + reservationID},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	awaitSmoke(t, ctx, func() (bool, error) {
		reservations, err := repository.GetReservations(ctx, "acc_usd_1", []string{reservationID})
		return reservations[reservationID] != nil && reservations[reservationID]["status"] == "RELEASED", err
	})
	account, err = repository.GetAccountForUpdate(ctx, "acc_usd_1", "acc_usd_1")
	if err != nil || account.CurrentBalance != 10000 || account.AvailableBalance != 10000 {
		t.Fatalf("timer release must restore only the hold: %#v, %v", account, err)
	}
}

// awaitSmoke polls an asynchronous assertion until it succeeds or the context expires.
// Assertion errors fail immediately rather than being retried as eventual consistency.
func awaitSmoke(t *testing.T, ctx context.Context, ready func() (bool, error)) {
	t.Helper()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := ready()
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(fmt.Errorf("waiting for asynchronous stream work: %w", ctx.Err()))
		case <-ticker.C:
		}
	}
}
