// This suite verifies configuration validation and idempotency-retention boundaries.
package env

import (
	"strings"
	"testing"
)

func TestConfigurationValidation(t *testing.T) {
	for _, test := range []struct {
		name, variable, value, message string
	}{
		{"missing endpoint", "DYNAMODB_ENDPOINT", "", "DYNAMODB_ENDPOINT is required"},
		{"client type", "DYNAMODB_CLIENTTYPE", "invalid", "DYNAMODB_CLIENTTYPE"},
		{"short retention", "DYNAMODB_TABLE_INSTANTPAYMENTS_IDEMPOTENCY_TTL", "3599", "between 3600 and 31536000"},
		{"long retention", "DYNAMODB_TABLE_INSTANTPAYMENTS_IDEMPOTENCY_TTL", "31536001", "between 3600 and 31536000"},
		{"zero reservation timeout", "DYNAMODB_TABLE_INSTANTPAYMENTS_RESERVATION_TIMEOUT", "0", "must be positive"},
		{"negative reservation timeout", "DYNAMODB_TABLE_INSTANTPAYMENTS_RESERVATION_TIMEOUT", "-1", "must be positive"},
		{"stream iterator", "DYNAMODB_STREAMS_ITERATOR_TYPE", "INVALID", "LATEST or TRIM_HORIZON"},
		{"invalid number", "WORKER_POOL_SIZE", "not-a-number", "error reading environment"},
		{"invalid boolean", "DYNAMODB_STREAMS_ENABLED", "not-a-bool", "error reading environment"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DYNAMODB_ENDPOINT", "http://localhost:8000")
			t.Setenv("DYNAMODB_CLIENTTYPE", "high-level")
			t.Setenv("DYNAMODB_TABLE_INSTANTPAYMENTS_IDEMPOTENCY_TTL", "2592000")
			t.Setenv("DYNAMODB_TABLE_INSTANTPAYMENTS_RESERVATION_TIMEOUT", "900")
			t.Setenv("DYNAMODB_STREAMS_ITERATOR_TYPE", "LATEST")
			t.Setenv("WORKER_POOL_SIZE", "10")
			t.Setenv("DYNAMODB_STREAMS_ENABLED", "true")
			t.Setenv(test.variable, test.value)
			if loaded, err := GetEnv(); err == nil || !strings.Contains(err.Error(), test.message) || loaded != nil {
				t.Fatalf("expected %q error without settings, got %#v, %v", test.message, loaded, err)
			}
		})
	}
}

func TestConfigurationRetentionBoundaries(t *testing.T) {
	for _, retention := range []string{"3600", "31536000"} {
		t.Run(retention, func(t *testing.T) {
			t.Setenv("DYNAMODB_ENDPOINT", "http://localhost:8000")
			t.Setenv("DYNAMODB_TABLE_INSTANTPAYMENTS_IDEMPOTENCY_TTL", retention)
			t.Setenv("DYNAMODB_CLIENTTYPE", "low-level")
			t.Setenv("DYNAMODB_STREAMS_ITERATOR_TYPE", "TRIM_HORIZON")
			t.Setenv("DYNAMODB_STREAMS_ENABLED", "false")
			settings, err := GetEnv()
			if err != nil {
				t.Fatal(err)
			}
			if settings.StreamsEnabled || settings.StreamIteratorType != "TRIM_HORIZON" || settings.DynamoDBClientType != "low-level" {
				t.Fatalf("configuration overrides were ignored: %#v", settings)
			}
		})
	}
}
