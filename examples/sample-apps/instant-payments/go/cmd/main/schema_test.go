// This suite checks acceptance and rejection of native DynamoDB index schemas.
package main

import (
	"encoding/json"
	"strings"
	"testing"

	"instantpayments/pkg/env"
)

func TestValidateNativeDynamoDBSchema(t *testing.T) {
	indexes := newDynamoTableInput("payments").GlobalSecondaryIndexes
	for _, test := range []struct {
		name, body, want string
		status           int
	}{
		{"native", "", "", 200},
		{"missing table", `{}`, "description is missing", 200},
		{"missing indexes", `{"Table":{}}`, "fresh table", 200},
		{"legacy indexes", `{"Table":{"GlobalSecondaryIndexes":[{"IndexName":"GSI_MERCHANT_PAYMENTS","KeySchema":[{"AttributeName":"merchantId","KeyType":"HASH"},{"AttributeName":"createdAtUtc","KeyType":"RANGE"}]}]}}`, "incompatible index", 200},
		{"wrong state partition", `{"Table":{"GlobalSecondaryIndexes":[{"IndexName":"GSI_MERCHANT_PAYMENTS","KeySchema":[{"AttributeName":"merchantId","KeyType":"HASH"},{"AttributeName":"createdAtUtc","KeyType":"RANGE"},{"AttributeName":"paymentId","KeyType":"RANGE"}]},{"IndexName":"GSI_MERCHANT_STATE_PAYMENTS","KeySchema":[{"AttributeName":"merchantState","KeyType":"HASH"},{"AttributeName":"createdAtUtc","KeyType":"RANGE"}]}]}}`, "GSI_MERCHANT_STATE_PAYMENTS", 200},
		{"describe error", `{"__type":"AccessDeniedException"}`, "validate DynamoDB index schema", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			if body == "" {
				encoded, err := json.Marshal(map[string]any{"Table": map[string]any{"GlobalSecondaryIndexes": indexes}})
				if err != nil {
					t.Fatal(err)
				}
				body = string(encoded)
			}
			client := startupClient(t, startupReply{operation: "DescribeTable", status: test.status, body: body})
			err := validateDynamoDBSchema(client, &env.Env{TableName: "payments"})
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("schema error = %v, want %q", err, test.want)
			}
		})
	}
}
