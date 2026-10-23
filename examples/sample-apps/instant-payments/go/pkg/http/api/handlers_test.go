// This suite checks health-store access and the embedded OpenAPI discovery response.
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"instantpayments/pkg/env"
	"instantpayments/pkg/http/actuator"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// decodeBody decodes a recorded JSON object and fails the test on invalid output.
func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body
}

func TestHealthCheckHandler(t *testing.T) {
	var target string
	configuration := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("key", "secret", ""),
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			target = request.Header.Get("X-Amz-Target")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"Items":[],"Count":0,"ScannedCount":0}`)),
				Request:    request,
			}, nil
		})},
	}
	ddb := dynamodb.NewFromConfig(configuration, func(options *dynamodb.Options) {
		options.BaseEndpoint = aws.String("http://dynamodb.test")
	})
	handler := actuator.NewHealth(ddb, &env.Env{TableName: "payments-test"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, handler.Pattern(), nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	if target != "DynamoDB_20120810.Scan" {
		t.Fatalf("expected DynamoDB Scan, got %q", target)
	}
	if body := decodeBody(t, recorder); len(body) != 1 || body["status"] != "healthy" {
		t.Fatalf("expected healthy response, got %#v", body)
	}
}

// roundTripperFunc supplies controlled SDK HTTP responses without network access.
type roundTripperFunc func(*http.Request) (*http.Response, error)

// RoundTrip delegates the SDK request to the fixture.
func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestAPIDocsHandler(t *testing.T) {
	handler, err := NewAPIDocsHandler()
	if err != nil {
		t.Fatalf("create API docs handler: %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", handler.Pattern(), nil))
	if recorder.Code != 200 || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("expected JSON 200, got %d %q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	body := decodeBody(t, recorder)
	if body["openapi"] != "3.0.3" || body["swagger"] != nil || body["info"] == nil || body["paths"] == nil {
		t.Fatalf("expected OpenAPI 3 discovery fields without a Swagger 2 marker, got %#v", body)
	}
	var expected map[string]any
	if err := json.Unmarshal(swaggerJSON, &expected); err != nil {
		t.Fatalf("decode embedded specification: %v", err)
	}
	if !reflect.DeepEqual(body, expected) {
		t.Fatal("API docs response differs from embedded specification")
	}
	paths := body["paths"].(map[string]any)
	for _, path := range []string{
		"/actuator/health", "/api-docs", "/api/v1/payments/outbound",
		"/api/v1/payments/outbound/{paymentId}", "/api/v1/payments/outbound/{paymentId}/process",
		"/api/v1/accounts/{accountId}", "/api/v1/accounts/{accountId}/batch-get-reservations",
		"/api/v1/merchants/{merchantId}/payments", "/api/v1/merchants/{merchantId}/payments/state/{state}",
	} {
		if paths[path] == nil {
			t.Errorf("missing API contract for %s", path)
		}
	}
	definitions := body["components"].(map[string]any)["schemas"].(map[string]any)
	account := definitions["GetAccountResponse"].(map[string]any)["properties"].(map[string]any)
	if account["currentBalance"] == nil || account["availableBalance"] == nil || account["Name"] != nil || account["Balance"] != nil {
		t.Fatal("account schema does not describe current balance fields")
	}
	var checkReferences func(any)
	checkReferences = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if reference, ok := node["$ref"].(string); ok {
				resolveDocumentReference(t, body, reference)
			}
			for _, child := range node {
				checkReferences(child)
			}
		case []any:
			for _, child := range node {
				checkReferences(child)
			}
		}
	}
	checkReferences(body)
	create := paths["/api/v1/payments/outbound"].(map[string]any)["post"].(map[string]any)
	if create["requestBody"] == nil {
		t.Fatal("OpenAPI 3 creation operation must declare a requestBody")
	}
	for path, value := range paths {
		for method, value := range value.(map[string]any) {
			operation := value.(map[string]any)
			responses := operation["responses"].(map[string]any)
			if responses["503"] == nil {
				t.Errorf("%s %s must document shared server middleware failures", method, path)
			}
			for status, value := range responses {
				response := resolvedResponse(t, body, value)
				if response["schema"] != nil || response["content"] == nil {
					t.Errorf("%s %s response %s must use OpenAPI 3 content", method, path, status)
				}

			}

		}
	}

}

func TestInfrastructureOperationsDocumentOnlyServerUnavailableErrors(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(swaggerJSON, &document); err != nil {
		t.Fatal(err)
	}
	paths := document["paths"].(map[string]any)
	for _, path := range []string{
		"/actuator/health", "/api-docs", "/api/v1/echo", "/swagger-ui.html", "/swagger-ui/index.html",
	} {
		t.Run(path, func(t *testing.T) {
			for method, raw := range paths[path].(map[string]any) {
				responses := raw.(map[string]any)["responses"].(map[string]any)
				value, ok := responses["503"]
				if !ok {
					t.Fatalf("%s must document server middleware failures", method)
				}
				response := resolvedResponse(t, document, value)
				description := response["description"].(string)
				for _, code := range []string{"ASYNC_REQUEST_CAPACITY_EXHAUSTED", "REQUEST_TIMEOUT"} {
					if !strings.Contains(description, code) {
						t.Errorf("%s must describe %s", method, code)
					}
				}
				if response["headers"] != nil || strings.Contains(description, "THROUGHPUT_EXCEEDED") {
					t.Fatal("server-only failures must not promise dependency codes or retry headers")
				}
				schema := response["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
				if schema["$ref"] != "#/components/schemas/ErrorResponse" {
					t.Fatalf("expected shared error envelope, got %#v", schema)
				}
			}
		})
	}
}
