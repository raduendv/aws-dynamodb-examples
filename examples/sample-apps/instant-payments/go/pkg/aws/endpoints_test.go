// This suite verifies local and remote endpoint selection for table and Streams clients.
package aws

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams"

	"instantpayments/pkg/env"
)

func TestConfiguredClientsUseServiceEndpoints(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_CONFIG_FILE", t.TempDir()+"/config")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", t.TempDir()+"/credentials")
	t.Setenv("AWS_ENDPOINT_URL", "")
	t.Setenv("AWS_ENDPOINT_URL_DYNAMODB", "")
	t.Setenv("AWS_ENDPOINT_URL_DYNAMODB_STREAMS", "")
	t.Setenv("AWS_USE_FIPS_ENDPOINT", "false")
	t.Setenv("AWS_USE_DUALSTACK_ENDPOINT", "false")

	for _, test := range []struct {
		name, endpoint, region, tableHost, streamHost string
	}{
		{"AWS US", "https://dynamodb.us-east-1.amazonaws.com", "us-east-1", "dynamodb.us-east-1.amazonaws.com", "streams.dynamodb.us-east-1.amazonaws.com"},
		{"AWS EU", "https://dynamodb.eu-west-1.amazonaws.com", "eu-west-1", "dynamodb.eu-west-1.amazonaws.com", "streams.dynamodb.eu-west-1.amazonaws.com"},
		{"custom table", "https://table.example.test", "us-east-1", "table.example.test", "streams.dynamodb.us-east-1.amazonaws.com"},
		{"localhost", "http://localhost:8000", "eu-west-1", "localhost:8000", "localhost:8000"},
		{"loopback", "http://127.0.0.1:8000", "eu-west-1", "127.0.0.1:8000", "127.0.0.1:8000"},
		{"docker", "http://dynamodb:8000", "eu-west-1", "dynamodb:8000", "dynamodb:8000"},
		{"compose", "http://dynamodb-local:8000", "eu-west-1", "dynamodb-local:8000", "dynamodb-local:8000"},
		{"extenddb", "http://extenddb:18080", "eu-west-1", "extenddb:18080", "extenddb:18080"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := Config(&env.Env{
				AWSRegion: test.region, DynamoDBEndpoint: test.endpoint, DynamoDBClientType: "high-level",
			})
			if err != nil {
				t.Fatal(err)
			}
			var hosts []string
			cfg.HTTPClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				hosts = append(hosts, request.URL.Host)
				return &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(`{}`)), Request: request,
				}, nil
			})}
			cfg.RetryMaxAttempts = 1
			streams := DynamoDBStreams(cfg)
			if awssdk.ToString(cfg.BaseEndpoint) != test.endpoint {
				t.Fatal("Streams constructor changed the shared table configuration")
			}
			if _, err := streams.ListStreams(context.Background(), &dynamodbstreams.ListStreamsInput{}); err != nil {
				t.Fatalf("Streams request: %v", err)
			}
			if _, err := DynamoDB(cfg).ListTables(context.Background(), &dynamodb.ListTablesInput{}); err != nil {
				t.Fatalf("table request: %v", err)
			}
			if len(hosts) != 2 || hosts[0] != test.streamHost || hosts[1] != test.tableHost {
				t.Fatalf("request hosts = %v, want Streams=%s table=%s", hosts, test.streamHost, test.tableHost)
			}
		})
	}
}
