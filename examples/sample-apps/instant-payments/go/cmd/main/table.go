package main

import (
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func newDynamoTableInput(tableName string) *dynamodb.CreateTableInput {
	stringAttribute := func(name string) types.AttributeDefinition {
		return types.AttributeDefinition{AttributeName: awssdk.String(name), AttributeType: types.ScalarAttributeTypeS}
	}
	key := func(name string, keyType types.KeyType) types.KeySchemaElement {
		return types.KeySchemaElement{AttributeName: awssdk.String(name), KeyType: keyType}
	}
	return &dynamodb.CreateTableInput{
		TableName: &tableName,
		AttributeDefinitions: []types.AttributeDefinition{
			stringAttribute("pk"), stringAttribute("sk"), stringAttribute("createdAtUtc"), stringAttribute("merchantId"), stringAttribute("merchantState"),
		},
		KeySchema:   []types.KeySchemaElement{key("pk", types.KeyTypeHash), key("sk", types.KeyTypeRange)},
		BillingMode: types.BillingModePayPerRequest,
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{
			{IndexName: awssdk.String("GSI_MERCHANT_PAYMENTS"), KeySchema: []types.KeySchemaElement{key("merchantId", types.KeyTypeHash), key("createdAtUtc", types.KeyTypeRange)}, Projection: &types.Projection{ProjectionType: types.ProjectionTypeAll}},
			{IndexName: awssdk.String("GSI_MERCHANT_STATE_PAYMENTS"), KeySchema: []types.KeySchemaElement{key("merchantState", types.KeyTypeHash), key("createdAtUtc", types.KeyTypeRange)}, Projection: &types.Projection{ProjectionType: types.ProjectionTypeInclude, NonKeyAttributes: []string{"paymentId", "merchantId", "state", "currency", "amount", "correlationId", "updatedAtUtc", "reasonCode", "lastSequence"}}},
		},
		StreamSpecification: &types.StreamSpecification{StreamEnabled: awssdk.Bool(true), StreamViewType: types.StreamViewTypeNewAndOldImages},
	}
}
