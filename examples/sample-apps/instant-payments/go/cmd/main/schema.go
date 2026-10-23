package main

import (
	"context"
	"fmt"
	"slices"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"instantpayments/pkg/env"
)

// validateDynamoDBSchema fails before seeding or serving an incompatible table.
// GSI keys cannot be altered in place; bootstrap never deletes or migrates data.
func validateDynamoDBSchema(client *dynamodb.Client, settings *env.Env) error {
	result, err := client.DescribeTable(context.Background(), &dynamodb.DescribeTableInput{
		TableName: awssdk.String(settings.TableName),
	})
	if err != nil {
		return fmt.Errorf("validate DynamoDB index schema: %w", err)
	}
	if result.Table == nil {
		return fmt.Errorf("validate DynamoDB index schema: table description is missing")
	}
	for _, expected := range newDynamoTableInput(settings.TableName).GlobalSecondaryIndexes {
		found := slices.IndexFunc(result.Table.GlobalSecondaryIndexes, func(index types.GlobalSecondaryIndexDescription) bool {
			return awssdk.ToString(index.IndexName) == awssdk.ToString(expected.IndexName)
		})
		if found >= 0 {
			actual := result.Table.GlobalSecondaryIndexes[found]
			if slices.EqualFunc(actual.KeySchema, expected.KeySchema, func(a, b types.KeySchemaElement) bool {
				return awssdk.ToString(a.AttributeName) == awssdk.ToString(b.AttributeName) && a.KeyType == b.KeyType
			}) {
				continue
			}
		}
		return fmt.Errorf("table %q has incompatible index %s: native multi-attribute GSI keys are required; use a fresh table or migrate indexes offline as described in docs/Runbook.md (bootstrap will not delete data)",
			settings.TableName, awssdk.ToString(expected.IndexName))
	}
	return nil
}
