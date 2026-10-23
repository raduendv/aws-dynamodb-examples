// This suite checks grouped timestamp encoding and numeric TTL persistence.
package aws

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"instantpayments/pkg/model"
)

func TestMarshalItemUsesGroupedTimestampsAndNumericTTL(t *testing.T) {
	created := time.Date(2026, 1, 1, 1, 0, 0, 100_000_000, time.FixedZone("offset", 3600))
	record := model.IdempotencyRecord{
		IdempotencyKeys: model.IdempotencyKeys{PK: "key", SK: model.IdempotencySK},
		CreatedAt:       created, ExpiresAt: created.Add(time.Hour),
	}
	item, err := marshalItem(record)
	if err != nil {
		t.Fatal(err)
	}
	if got := item["createdAtUtc"].(*types.AttributeValueMemberS).Value; got != "2026-01-01T00:00:00.100Z" {
		t.Fatalf("createdAtUtc = %q", got)
	}
	if _, ok := item["ttl"].(*types.AttributeValueMemberN); !ok {
		t.Fatalf("TTL must remain numeric: %#v", item["ttl"])
	}
}
