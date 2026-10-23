// This suite checks invalid connection settings, remote credentials, and SDK configuration failures.
package aws

import (
	"context"
	"strings"
	"testing"

	"instantpayments/pkg/env"
)

func TestConfigRejectsInvalidSettings(t *testing.T) {
	for _, test := range []struct {
		name, endpoint, region, clientType, want string
	}{
		{name: "endpoint required", region: "us-east-1", clientType: "high-level", want: "DYNAMODB_ENDPOINT is required"},
		{name: "region required", endpoint: "http://localhost:8000", clientType: "high-level", want: "DYNAMODB_REGION is required"},
		{name: "unknown client", endpoint: "http://localhost:8000", region: "us-east-1", clientType: "other", want: "DYNAMODB_CLIENTTYPE"},
		{name: "relative endpoint", endpoint: "/dynamodb", region: "us-east-1", clientType: "low-level", want: "absolute http or https URL"},
		{name: "wrong scheme", endpoint: "ftp://localhost:8000", region: "us-east-1", clientType: "low-level", want: "absolute http or https URL"},
		{name: "malformed endpoint", endpoint: "://%", region: "us-east-1", clientType: "low-level", want: "absolute http or https URL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Config(&env.Env{DynamoDBEndpoint: test.endpoint, AWSRegion: test.region, DynamoDBClientType: test.clientType})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %q", err, test.want)
			}
		})
	}
	if _, err := Config(nil); err == nil {
		t.Fatal("nil settings should be rejected")
	}
}

func TestConfigRemoteEndpointUsesConfiguredCredentials(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "configured-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "configured-secret")
	t.Setenv("AWS_CONFIG_FILE", "nonexistent-aws-config-for-test")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "nonexistent-aws-credentials-for-test")
	configuration, err := Config(&env.Env{
		DynamoDBEndpoint: "https://dynamodb.us-east-1.amazonaws.com", AWSRegion: "us-east-1", DynamoDBClientType: "low-level",
	})
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := configuration.Credentials.Retrieve(context.Background())
	if err != nil || credentials.AccessKeyID != "configured-key" || credentials.SecretAccessKey != "configured-secret" {
		t.Fatalf("remote endpoint must not get local credentials: %#v, %v", credentials, err)
	}
}

func TestConfigPropagatesSDKConfigurationErrors(t *testing.T) {
	t.Setenv("AWS_RETRY_MODE", "not-a-valid-retry-mode")
	_, err := Config(&env.Env{
		DynamoDBEndpoint: "http://localhost:8000", AWSRegion: "us-east-1", DynamoDBClientType: "high-level",
	})
	if err == nil || !strings.Contains(err.Error(), "AWS_RETRY_MODE") {
		t.Fatalf("expected SDK configuration error, got %v", err)
	}
}
