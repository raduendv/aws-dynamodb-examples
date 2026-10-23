package main

import (
	"instantpayments/pkg/model"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// newDynamoTableInput defines the on-demand single table, NEW_IMAGE stream and native multi-key GSIs.
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
			stringAttribute("PK"),
			stringAttribute("SK"),
			stringAttribute("createdAtUtc"),
			stringAttribute("merchantId"),
			stringAttribute("paymentId"),
			stringAttribute("aggregateState"),
		},
		KeySchema:   []types.KeySchemaElement{key("PK", types.KeyTypeHash), key("SK", types.KeyTypeRange)},
		BillingMode: types.BillingModePayPerRequest,
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{
			{IndexName: awssdk.String("GSI_MERCHANT_PAYMENTS"), KeySchema: []types.KeySchemaElement{key("merchantId", types.KeyTypeHash), key("createdAtUtc", types.KeyTypeRange), key("paymentId", types.KeyTypeRange)}, Projection: &types.Projection{ProjectionType: types.ProjectionTypeAll}},
			{IndexName: awssdk.String("GSI_MERCHANT_STATE_PAYMENTS"), KeySchema: []types.KeySchemaElement{key("merchantId", types.KeyTypeHash), key("aggregateState", types.KeyTypeHash), key("createdAtUtc", types.KeyTypeRange)}, Projection: &types.Projection{ProjectionType: types.ProjectionTypeInclude, NonKeyAttributes: model.MerchantStateProjection()}},
		},
		StreamSpecification: &types.StreamSpecification{StreamEnabled: awssdk.Bool(true), StreamViewType: types.StreamViewTypeNewImage},
	}
}
