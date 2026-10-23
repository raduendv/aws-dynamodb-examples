package main

import (
	"context"
	"errors"
	stdhttp "net/http"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"instantpayments/pkg/aws"
	"instantpayments/pkg/env"
	"instantpayments/pkg/http"
	"instantpayments/pkg/http/actuator"
	"instantpayments/pkg/http/api"
	v1 "instantpayments/pkg/http/api/v1"
	"instantpayments/pkg/input"
	"instantpayments/pkg/model"
	"instantpayments/pkg/service"
	"instantpayments/pkg/workerpool"
)

const (
	// bootstrapReadyTimeout bounds local table and stream activation during startup.
	bootstrapReadyTimeout = time.Minute
	// streamReadyPollInterval avoids a tight DescribeTable loop during activation.
	streamReadyPollInterval = time.Second
)

// main composes the application, validates persistence before serving, and runs Fx lifecycle hooks.
func main() {
	fx.New(
		// logging
		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),
		fx.Provide(zap.NewExample),

		// modules: base
		env.Module(),
		input.Module(),

		http.Module(),
		workerpool.Module(),

		// modules apis
		api.Module(),
		actuator.Module(),
		v1.Module(),
		aws.Module(),

		service.Module(),

		// run application setup
		fx.Invoke(initializeDynamoDB),
		fx.Invoke(validateDynamoDBSchema),
		fx.Invoke(seedAccounts),

		fx.Invoke(func(_ *stdhttp.Server) {}),
	).Run()
}

// initializeDynamoDB verifies the managed table or creates local resources when enabled.
// Bootstrap failures stop startup. Existing items are never replaced or migrated.
func initializeDynamoDB(ddb *dynamodb.Client, log *zap.Logger, env *env.Env) {
	log.Info("initializing DynamoDB tables")

	desc, err := ddb.DescribeTable(context.Background(), &dynamodb.DescribeTableInput{
		TableName: awssdk.String(env.TableName),
	})
	if _, ok := errors.AsType[*types.ResourceNotFoundException](err); ok {
		if !env.CreateResources {
			log.Fatal("DynamoDB table does not exist and automatic provisioning is disabled",
				zap.String("table_name", env.TableName),
				zap.String("setting", "DYNAMODB_PROVISIONING_CREATERESOURCES=true"))
		}
		log.Info("DynamoDB table not found; provisioning enabled", zap.String("table_name", env.TableName))
	} else if err != nil {
		log.Fatal("failed to describe table", zap.Error(err))
	}

	if desc != nil && desc.Table != nil {
		log.Info("table exists", zap.String("table_name", env.TableName))

		switch desc.Table.TableStatus {
		case types.TableStatusActive:
			log.Info("table is active", zap.String("table_name", env.TableName))
			if env.CreateResources {
				ensureDynamoDBTTL(ddb, log, env.TableName)
				ensureDynamoDBStream(ddb, log, env.TableName)
			}

			return
		case types.TableStatusCreating:
			log.Info("table is creating", zap.String("table_name", env.TableName))
			waiter := dynamodb.NewTableExistsWaiter(ddb)
			if err := waiter.Wait(context.Background(), &dynamodb.DescribeTableInput{
				TableName: awssdk.String(env.TableName),
			}, bootstrapReadyTimeout); err != nil {
				log.Fatal("failed to wait for table to become active", zap.Error(err))
			}
			log.Info("table is now active", zap.String("table_name", env.TableName))
			if env.CreateResources {
				ensureDynamoDBTTL(ddb, log, env.TableName)
				ensureDynamoDBStream(ddb, log, env.TableName)
			}

			return
		default:
			log.Fatal("table is in unexpected state", zap.String("table_name", env.TableName), zap.String("status", string(desc.Table.TableStatus)))
		}
	}

	input := newDynamoTableInput(env.TableName)

	res, err := ddb.CreateTable(context.Background(), input)
	if err != nil {
		if _, alreadyExists := errors.AsType[*types.ResourceInUseException](err); alreadyExists {
			log.Info("table was created concurrently", zap.String("table_name", env.TableName))
			waiter := dynamodb.NewTableExistsWaiter(ddb)
			if waitErr := waiter.Wait(context.Background(), &dynamodb.DescribeTableInput{
				TableName: awssdk.String(env.TableName),
			}, bootstrapReadyTimeout); waitErr != nil {
				log.Fatal("failed to wait for concurrently created table", zap.Error(waitErr))
			}
			ensureDynamoDBTTL(ddb, log, env.TableName)
			ensureDynamoDBStream(ddb, log, env.TableName)
			return
		}
		log.Fatal("failed to create table", zap.Error(err), zap.Any("input", input))
	}
	log.Info("table creation initiated", zap.String("table_name", env.TableName), zap.String("table_status", string(res.TableDescription.TableStatus)))

	waiter := dynamodb.NewTableExistsWaiter(ddb)
	if err := waiter.Wait(context.Background(), &dynamodb.DescribeTableInput{
		TableName: awssdk.String(env.TableName),
	}, bootstrapReadyTimeout); err != nil {
		log.Fatal("failed to wait for table to become active", zap.Error(err))
	}
	log.Info("table is now active", zap.String("table_name", env.TableName))
	ensureDynamoDBTTL(ddb, log, env.TableName)
	ensureDynamoDBStream(ddb, log, env.TableName)

	log.Info("done initializing DynamoDB tables")
}

// ensureDynamoDBTTL enables ttl for local bootstrap or stops startup on an incompatible policy.
func ensureDynamoDBTTL(ddb *dynamodb.Client, log *zap.Logger, tableName string) {
	description, err := ddb.DescribeTimeToLive(context.Background(), &dynamodb.DescribeTimeToLiveInput{
		TableName: awssdk.String(tableName),
	})
	if err != nil {
		log.Fatal("failed to describe DynamoDB TTL state", zap.Error(err))
	}

	if description == nil || description.TimeToLiveDescription == nil {
		log.Fatal("DynamoDB TTL description is missing", zap.String("table_name", tableName))
		return
	}
	status := description.TimeToLiveDescription.TimeToLiveStatus
	if status == types.TimeToLiveStatusEnabled || status == types.TimeToLiveStatusEnabling {
		if awssdk.ToString(description.TimeToLiveDescription.AttributeName) != "ttl" {
			log.Fatal("DynamoDB TTL targets a different attribute; disable its current TTL policy and enable ttl before restarting",
				zap.String("table_name", tableName), zap.String("ttl_attribute", awssdk.ToString(description.TimeToLiveDescription.AttributeName)))
			return
		}
		log.Info("DynamoDB TTL is enabled or enabling", zap.String("table_name", tableName), zap.String("status", string(status)))

		return
	}

	log.Info("enabling DynamoDB TTL", zap.String("table_name", tableName))
	if _, err := ddb.UpdateTimeToLive(context.Background(), &dynamodb.UpdateTimeToLiveInput{
		TableName: awssdk.String(tableName),
		TimeToLiveSpecification: &types.TimeToLiveSpecification{
			AttributeName: awssdk.String("ttl"),
			Enabled:       awssdk.Bool(true),
		},
	}); err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) && (apiError.ErrorCode() == "ValidationException" || apiError.ErrorCode() == "ResourceInUseException") {
			current, describeErr := ddb.DescribeTimeToLive(context.Background(), &dynamodb.DescribeTimeToLiveInput{
				TableName: awssdk.String(tableName),
			})
			if describeErr != nil {
				log.Fatal("failed to reconcile concurrent DynamoDB TTL enablement", zap.Error(describeErr), zap.NamedError("update_error", err))
			}
			if current != nil && current.TimeToLiveDescription != nil {
				state := current.TimeToLiveDescription
				if awssdk.ToString(state.AttributeName) == "ttl" &&
					(state.TimeToLiveStatus == types.TimeToLiveStatusEnabled || state.TimeToLiveStatus == types.TimeToLiveStatusEnabling) {
					log.Info("DynamoDB TTL was enabled concurrently", zap.String("table_name", tableName))
					return
				}
			}
		}
		log.Fatal("failed to enable DynamoDB TTL", zap.Error(err))
	}
	log.Info("DynamoDB TTL enablement requested", zap.String("table_name", tableName))
}

// ensureDynamoDBStream enables NEW_IMAGE and waits for the stream to become available.
// It stops startup if configuration or readiness fails.
func ensureDynamoDBStream(ddb *dynamodb.Client, log *zap.Logger, tableName string) {
	desc, err := ddb.DescribeTable(context.Background(), &dynamodb.DescribeTableInput{
		TableName: awssdk.String(tableName),
	})
	if err != nil {
		log.Fatal("failed to describe table stream state", zap.Error(err))
	}
	if desc.Table != nil && desc.Table.LatestStreamArn != nil && desc.Table.StreamSpecification != nil &&
		desc.Table.StreamSpecification.StreamViewType == types.StreamViewTypeNewImage {
		log.Info("DynamoDB NEW_IMAGE stream is enabled", zap.String("table_name", tableName), zap.String("stream_arn", awssdk.ToString(desc.Table.LatestStreamArn)))
		return
	}

	_, err = ddb.UpdateTable(context.Background(), &dynamodb.UpdateTableInput{
		TableName: awssdk.String(tableName),
		StreamSpecification: &types.StreamSpecification{
			StreamEnabled:  awssdk.Bool(true),
			StreamViewType: types.StreamViewTypeNewImage,
		},
	})
	if err != nil {
		log.Fatal("failed to enable DynamoDB stream", zap.Error(err))
	}
	log.Info("DynamoDB stream enablement requested", zap.String("table_name", tableName))

	deadline := time.Now().Add(bootstrapReadyTimeout)
	for time.Now().Before(deadline) {
		desc, err = ddb.DescribeTable(context.Background(), &dynamodb.DescribeTableInput{
			TableName: awssdk.String(tableName),
		})
		if err != nil {
			log.Fatal("failed to wait for DynamoDB stream", zap.Error(err))
		}
		if desc.Table != nil && desc.Table.LatestStreamArn != nil &&
			desc.Table.StreamSpecification != nil &&
			desc.Table.StreamSpecification.StreamViewType == types.StreamViewTypeNewImage {
			log.Info("DynamoDB stream is ready", zap.String("table_name", tableName), zap.String("stream_arn", awssdk.ToString(desc.Table.LatestStreamArn)))

			return
		}
		time.Sleep(streamReadyPollInterval)
	}

	log.Fatal("timed out waiting for DynamoDB stream", zap.String("table_name", tableName))
}

// seedAccounts conditionally inserts the demo baseline only during enabled provisioning.
// Existing account state is preserved and unexpected write failures stop startup.
func seedAccounts(log *zap.Logger, repository *aws.Repository, settings *env.Env) {
	if !settings.CreateResources {
		log.Info("account seeding skipped because automatic provisioning is disabled")
		return
	}
	accounts := []model.Account{
		{
			Item: model.Item{EntityType: "ACCOUNT"}, AccountKeys: model.AccountKeys{PK: "acc_usd_1", SK: "acc_usd_1"},
			AccountID: "acc_usd_1", Status: model.AccountStatusActive, CurrentBalance: 10000, AvailableBalance: 10000, Currency: "USD", Version: 1,
		},
		{
			Item: model.Item{EntityType: "ACCOUNT"}, AccountKeys: model.AccountKeys{PK: "acc_usd_2", SK: "acc_usd_2"},
			AccountID: "acc_usd_2", Status: model.AccountStatusActive, CurrentBalance: 5000, AvailableBalance: 5000, Currency: "USD", Version: 1,
		},
		{
			Item: model.Item{EntityType: "ACCOUNT"}, AccountKeys: model.AccountKeys{PK: "acc_usd_3", SK: "acc_usd_3"},
			AccountID: "acc_usd_3", Status: model.AccountStatusActive, CurrentBalance: 500, AvailableBalance: 500, Currency: "USD", Version: 1,
		},
		{
			Item: model.Item{EntityType: "ACCOUNT"}, AccountKeys: model.AccountKeys{PK: "acc_usd_4", SK: "acc_usd_4"},
			AccountID: "acc_usd_4", Status: model.AccountStatusActive, CurrentBalance: 100, AvailableBalance: 100, Currency: "USD", Version: 1,
		},
		{
			Item: model.Item{EntityType: "ACCOUNT"}, AccountKeys: model.AccountKeys{PK: "acc_usd_5", SK: "acc_usd_5"},
			AccountID: "acc_usd_5", Status: model.AccountStatusActive, Currency: "USD", Version: 1,
		},
		{
			Item: model.Item{EntityType: "ACCOUNT"}, AccountKeys: model.AccountKeys{PK: "acc_eur_1", SK: "acc_eur_1"},
			AccountID: "acc_eur_1", Status: model.AccountStatusActive, CurrentBalance: 10000, AvailableBalance: 10000, Currency: "EUR", Version: 1,
		},
		{
			Item: model.Item{EntityType: "ACCOUNT"}, AccountKeys: model.AccountKeys{PK: "acc_eur_2", SK: "acc_eur_2"},
			AccountID: "acc_eur_2", Status: model.AccountStatusActive, CurrentBalance: 5000, AvailableBalance: 5000, Currency: "EUR", Version: 1,
		},
		{
			Item: model.Item{EntityType: "ACCOUNT"}, AccountKeys: model.AccountKeys{PK: "acc_eur_3", SK: "acc_eur_3"},
			AccountID: "acc_eur_3", Status: model.AccountStatusActive, CurrentBalance: 500, AvailableBalance: 500, Currency: "EUR", Version: 1,
		},
		{
			Item: model.Item{EntityType: "ACCOUNT"}, AccountKeys: model.AccountKeys{PK: "acc_eur_4", SK: "acc_eur_4"},
			AccountID: "acc_eur_4", Status: model.AccountStatusActive, CurrentBalance: 100, AvailableBalance: 100, Currency: "EUR", Version: 1,
		},
		{
			Item: model.Item{EntityType: "ACCOUNT"}, AccountKeys: model.AccountKeys{PK: "acc_eur_5", SK: "acc_eur_5"},
			AccountID: "acc_eur_5", Status: model.AccountStatusActive, Currency: "EUR", Version: 1,
		},
	}

	for _, account := range accounts {
		log.Info("seeding account", zap.String("account_id", account.AccountID))
		err := repository.PutSeedAccount(context.Background(), &account)
		if err != nil {
			var conditional *types.ConditionalCheckFailedException
			if errors.As(err, &conditional) {
				log.Info("account already exists; preserving stored state", zap.String("account_id", account.AccountID))
				continue
			}
			log.Fatal("failed to seed account", zap.String("account_id", account.AccountID), zap.Error(err))
		}
	}
}
