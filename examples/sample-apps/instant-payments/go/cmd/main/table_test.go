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
		if len(index.KeySchema) != 2 || index.KeySchema[0].KeyType != types.KeyTypeHash || index.KeySchema[1].KeyType != types.KeyTypeRange {
			t.Fatalf("invalid GSI %s key schema %#v", *index.IndexName, index.KeySchema)
		}
	}
}
