package aws

import (
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"

	"instantpayments/pkg/env"
)

func TestConfigClientsAndModule(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	configuration, err := Config(&env.Env{AWSRegion: "us-east-1"})
	if err != nil {
		t.Fatalf("Config failed: %v", err)
	}
	if configuration.Region != "us-east-1" {
		t.Fatalf("unexpected region %q", configuration.Region)
	}
	if DynamoDB(awssdk.Config{}) == nil || DynamoDBStreams(awssdk.Config{}) == nil || Module() == nil {
		t.Fatal("expected AWS client constructors and module")
	}
}
