// This suite verifies the table and native multi-attribute index creation schema.
package main

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestNewDynamoTableInputUsesValidIndexKeySchemas(t *testing.T) {
	input := newDynamoTableInput("payments")
	if input.TableName == nil || *input.TableName != "payments" {
		t.Fatalf("unexpected table name %#v", input.TableName)
	}
	for _, index := range input.GlobalSecondaryIndexes {
		if len(index.KeySchema) != 3 || index.KeySchema[0].KeyType != types.KeyTypeHash ||
			index.KeySchema[2].KeyType != types.KeyTypeRange {
			t.Fatalf("invalid GSI %s key schema %#v", *index.IndexName, index.KeySchema)
		}
		if *index.IndexName == "GSI_MERCHANT_PAYMENTS" {
			if *index.KeySchema[1].AttributeName != "createdAtUtc" || index.KeySchema[1].KeyType != types.KeyTypeRange ||
				*index.KeySchema[2].AttributeName != "paymentId" {
				t.Fatalf("merchant index must sort by timestamp then paymentId: %#v", index.KeySchema)
			}
		} else if *index.KeySchema[1].AttributeName != "aggregateState" || index.KeySchema[1].KeyType != types.KeyTypeHash ||
			*index.KeySchema[2].AttributeName != "createdAtUtc" {
			t.Fatalf("state index must partition by merchant and state: %#v", index.KeySchema)
		}
		if input.KeySchema[0].AttributeName == nil || *input.KeySchema[0].AttributeName != "PK" ||
			input.KeySchema[1].AttributeName == nil || *input.KeySchema[1].AttributeName != "SK" {
			t.Fatalf("expected uppercase base-table keys, got %#v", input.KeySchema)
		}
		if input.StreamSpecification == nil || input.StreamSpecification.StreamViewType != types.StreamViewTypeNewImage {
			t.Fatalf("expected NEW_IMAGE stream configuration, got %#v", input.StreamSpecification)
		}
		expectedDefinitions := map[string]bool{"PK": true, "SK": true, "createdAtUtc": true, "merchantId": true, "paymentId": true, "aggregateState": true}
		for _, definition := range input.AttributeDefinitions {
			delete(expectedDefinitions, *definition.AttributeName)
		}
		if len(expectedDefinitions) != 0 || len(input.AttributeDefinitions) != 6 {
			t.Fatalf("attribute definitions must exactly match table and index keys, missing=%v definitions=%#v", expectedDefinitions, input.AttributeDefinitions)
		}
		for _, index := range input.GlobalSecondaryIndexes {
			if *index.IndexName == "GSI_MERCHANT_STATE_PAYMENTS" {
				if index.Projection.ProjectionType != types.ProjectionTypeInclude {
					t.Fatalf("expected INCLUDE state-index projection, got %#v", index.Projection)
				}
				expected := map[string]bool{
					"paymentId": true, "lastSequence": true, "correlationId": true,
					"amount": true, "currency": true, "updatedAtUtc": true, "reasonCode": true,
				}
				for _, attribute := range index.Projection.NonKeyAttributes {
					delete(expected, attribute)
				}
				if len(expected) != 0 {
					t.Fatalf("state-index projection is missing attributes: %#v", expected)
				}
			}
		}
	}
}
