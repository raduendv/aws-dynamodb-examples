package main

import (
	"context"
	"errors"
	stdhttp "net/http"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
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
		fx.Invoke(seedAccounts),

		fx.Invoke(func(_ *stdhttp.Server, _ *aws.StreamProcessor) {}),
	).Run()
}

func initializeDynamoDB(ddb *dynamodb.Client, log *zap.Logger, env *env.Env) {
	log.Info("initializing DynamoDB tables")

	desc, err := ddb.DescribeTable(context.Background(), &dynamodb.DescribeTableInput{
		TableName: awssdk.String(env.TableName),
	})
	if _, ok := errors.AsType[*types.ResourceNotFoundException](err); ok {
		log.Info("DynamoDB table not found", zap.String("table_name", env.TableName))
	} else if err != nil {
		log.Fatal("failed to describe table", zap.Error(err))
	}

	if desc != nil && desc.Table != nil {
		log.Info("table exists", zap.String("table_name", env.TableName))

		switch desc.Table.TableStatus {
		case types.TableStatusActive:
			log.Info("table is active", zap.String("table_name", env.TableName))
			ensureDynamoDBTTL(ddb, log, env.TableName)
			ensureDynamoDBStream(ddb, log, env.TableName)

			return
		case types.TableStatusCreating:
			log.Info("table is creating", zap.String("table_name", env.TableName))
			waiter := dynamodb.NewTableExistsWaiter(ddb)
			if err := waiter.Wait(context.Background(), &dynamodb.DescribeTableInput{
				TableName: awssdk.String(env.TableName),
			}, time.Minute); err != nil {
				log.Fatal("failed to wait for table to become active", zap.Error(err))
			}
			log.Info("table is now active", zap.String("table_name", env.TableName))
			ensureDynamoDBTTL(ddb, log, env.TableName)
			ensureDynamoDBStream(ddb, log, env.TableName)

			return
		default:
			log.Fatal("table is in unexpected state", zap.String("table_name", env.TableName), zap.String("status", string(desc.Table.TableStatus)))
		}
	}

	input := newDynamoTableInput(env.TableName)

	res, err := ddb.CreateTable(context.Background(), input)
	if err != nil {
		log.Fatal("failed to create table", zap.Error(err), zap.Any("input", input))
	}
	log.Info("table creation initiated", zap.String("table_name", env.TableName), zap.String("table_status", string(res.TableDescription.TableStatus)))

	waiter := dynamodb.NewTableExistsWaiter(ddb)
	if err := waiter.Wait(context.Background(), &dynamodb.DescribeTableInput{
		TableName: awssdk.String(env.TableName),
	}, time.Minute); err != nil {
		log.Fatal("failed to wait for table to become active", zap.Error(err))
	}
	log.Info("table is now active", zap.String("table_name", env.TableName))
	ensureDynamoDBTTL(ddb, log, env.TableName)
	ensureDynamoDBStream(ddb, log, env.TableName)

	log.Info("done initializing DynamoDB tables")
}

func ensureDynamoDBTTL(ddb *dynamodb.Client, log *zap.Logger, tableName string) {
	description, err := ddb.DescribeTimeToLive(context.Background(), &dynamodb.DescribeTimeToLiveInput{
		TableName: awssdk.String(tableName),
	})
	if err != nil {
		log.Fatal("failed to describe DynamoDB TTL state", zap.Error(err))
	}

	status := description.TimeToLiveDescription.TimeToLiveStatus
	if status == types.TimeToLiveStatusEnabled || status == types.TimeToLiveStatusEnabling {
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
		log.Fatal("failed to enable DynamoDB TTL", zap.Error(err))
	}
	log.Info("DynamoDB TTL enablement requested", zap.String("table_name", tableName))
}

func ensureDynamoDBStream(ddb *dynamodb.Client, log *zap.Logger, tableName string) {
	desc, err := ddb.DescribeTable(context.Background(), &dynamodb.DescribeTableInput{
		TableName: awssdk.String(tableName),
	})
	if err != nil {
		log.Fatal("failed to describe table stream state", zap.Error(err))
	}
	if desc.Table != nil && desc.Table.LatestStreamArn != nil {
		log.Info("DynamoDB stream is enabled", zap.String("table_name", tableName), zap.String("stream_arn", awssdk.ToString(desc.Table.LatestStreamArn)))
		return
	}

	_, err = ddb.UpdateTable(context.Background(), &dynamodb.UpdateTableInput{
		TableName: awssdk.String(tableName),
		StreamSpecification: &types.StreamSpecification{
			StreamEnabled:  awssdk.Bool(true),
			StreamViewType: types.StreamViewTypeNewAndOldImages,
		},
	})
	if err != nil {
		log.Fatal("failed to enable DynamoDB stream", zap.Error(err))
	}
	log.Info("DynamoDB stream enablement requested", zap.String("table_name", tableName))

	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		desc, err = ddb.DescribeTable(context.Background(), &dynamodb.DescribeTableInput{
			TableName: awssdk.String(tableName),
		})
		if err != nil {
			log.Fatal("failed to wait for DynamoDB stream", zap.Error(err))
		}
		if desc.Table != nil && desc.Table.LatestStreamArn != nil {
			log.Info("DynamoDB stream is ready", zap.String("table_name", tableName), zap.String("stream_arn", awssdk.ToString(desc.Table.LatestStreamArn)))

			return
		}
		time.Sleep(time.Second)
	}

	log.Fatal("timed out waiting for DynamoDB stream", zap.String("table_name", tableName))
}

func seedAccounts(log *zap.Logger, repository *aws.Repository) {
	accounts := []model.Account{
		{
			Item: model.Item{
				PK: "ACCOUNT#acc_usd_1",
				SK: "ACCOUNT#acc_usd_1",
			},
			Status:   model.AccountStatusActive,
			Name:     "USD Account 1",
			Balance:  1000000,
			Currency: "USD",
		},
		{
			Item: model.Item{
				PK: "ACCOUNT#acc_usd_2",
				SK: "ACCOUNT#acc_usd_2",
			},
			Status:   model.AccountStatusActive,
			Name:     "USD Account 2",
			Balance:  1000000,
			Currency: "USD",
		},
		{
			Item: model.Item{
				PK: "ACCOUNT#acc_usd_3",
				SK: "ACCOUNT#acc_usd_3",
			},
			Status:   model.AccountStatusActive,
			Name:     "USD Account 3",
			Balance:  1000000,
			Currency: "USD",
		},
		{
			Item: model.Item{
				PK: "ACCOUNT#acc_usd_5",
				SK: "ACCOUNT#acc_usd_5",
			},
			Status:   model.AccountStatusActive,
			Name:     "USD Account 5",
			Balance:  0,
			Currency: "USD",
		},
		{
			Item: model.Item{
				PK: "ACCOUNT#acc_eur_5",
				SK: "ACCOUNT#acc_eur_5",
			},
			Status:   model.AccountStatusInactive,
			Name:     "EUR Account 5",
			Balance:  0,
			Currency: "EUR",
		},
	}

	for _, account := range accounts {
		a, err := repository.GetAccount(context.Background(), account.Item.PK, account.Item.PK)
		if err != nil {
			log.Fatal("failed to get account", zap.Error(err))
		}

		if a != nil {
			log.Info("account already exists", zap.Any("account", account))
			continue
		}

		log.Info("seeding account", zap.String("account_id", account.Item.PK))

		err = repository.PutAccount(context.Background(), &account)
		if err != nil {
			log.Fatal("failed to put account", zap.Error(err))
		}
	}
}
