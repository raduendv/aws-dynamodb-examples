// This suite checks SDK client construction and Fx module composition.
package aws

import (
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"

	"instantpayments/pkg/env"
)

func TestConfigClientsAndModule(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	configuration, err := Config(&env.Env{
		AWSRegion: "us-east-1", DynamoDBEndpoint: "http://localhost:8000", DynamoDBClientType: "high-level",
	})
	if err != nil {
		t.Fatalf("Config failed: %v", err)
	}
	if configuration.Region != "us-east-1" {
		t.Fatalf("unexpected region %q", configuration.Region)
	}
	if awssdk.ToString(configuration.BaseEndpoint) != "http://localhost:8000" {
		t.Fatalf("unexpected DynamoDB endpoint %q", awssdk.ToString(configuration.BaseEndpoint))
	}
	credentials, err := configuration.Credentials.Retrieve(context.Background())
	if err != nil || credentials.AccessKeyID != "AKIAlocal" || credentials.SecretAccessKey != "local" {
		t.Fatalf("expected local static credentials, got %#v, %v", credentials, err)
	}
	if DynamoDB(awssdk.Config{}) == nil || DynamoDBStreams(awssdk.Config{}) == nil || Module() == nil {
		t.Fatal("expected AWS client constructors and module")
	}
}
