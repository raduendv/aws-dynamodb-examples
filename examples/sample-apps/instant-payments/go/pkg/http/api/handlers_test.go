package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"instantpayments/pkg/env"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

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
	handler := NewHealthCheckHandler(ddb, &env.Env{TableName: "payments-test"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, handler.Pattern(), nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	if target != "DynamoDB_20120810.Scan" {
		t.Fatalf("expected DynamoDB Scan, got %q", target)
	}
	if body := decodeBody(t, recorder); body["healthy"] != true {
		t.Fatalf("expected healthy response, got %#v", body)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

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
	if body["openapi"] != "3.0.0" || body["info"] == nil || body["paths"] == nil {
		t.Fatalf("expected OpenAPI discovery fields, got %#v", body)
	}
}
