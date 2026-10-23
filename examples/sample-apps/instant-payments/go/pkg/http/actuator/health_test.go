// This suite checks health responses for successful and failed DynamoDB access.
package actuator

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"instantpayments/pkg/env"
)

func TestHealthHandler(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		databaseStatus int
		databaseBody   string
		expectedStatus string
	}{
		{"healthy", http.StatusOK, `{"Items":[]}`, "healthy"},
		{"unhealthy", http.StatusBadRequest, `{"__type":"ResourceNotFoundException","message":"table missing"}`, "unhealthy"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := dynamodb.NewFromConfig(aws.Config{
				Region:      "us-east-1",
				Credentials: credentials.NewStaticCredentialsProvider("key", "secret", ""),
				HTTPClient: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: testCase.databaseStatus,
						Header:     http.Header{"Content-Type": []string{"application/x-amz-json-1.0"}},
						Body:       io.NopCloser(strings.NewReader(testCase.databaseBody)),
						Request:    request,
					}, nil
				})},
			}, func(options *dynamodb.Options) {
				options.BaseEndpoint = aws.String("http://dynamodb.test")
			})
			handler := NewHealth(client, &env.Env{TableName: "payments"})
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest("GET", handler.Pattern(), nil))
			if recorder.Code != 200 {
				t.Fatalf("expected 200, got %d", recorder.Code)
			}
			var body map[string]string
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 1 || body["status"] != testCase.expectedStatus {
				t.Fatalf("expected status %q, got %#v", testCase.expectedStatus, body)
			}
		})
	}
}

// roundTripperFunc supplies a controlled health-check SDK response without network access.
type roundTripperFunc func(*http.Request) (*http.Response, error)

// RoundTrip delegates the SDK request to the fixture.
func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
