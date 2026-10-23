// This suite checks JSON responses and domain or dependency error codes, media types, and retry headers.
package response

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	"instantpayments/pkg/service"
)

func TestWriteJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	if err := WriteJSON(recorder, http.StatusCreated, map[string]string{"status": "created"}); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusCreated || recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("unexpected response status=%d content-type=%q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	if !containsJSONField(recorder.Body.String(), `"status":"created"`) {
		t.Fatalf("unexpected body %s", recorder.Body.String())
	}
}

func TestWriteError(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "domain", err: &service.DomainError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Text: "invalid"}, status: http.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "generic", err: errors.New("boom"), status: http.StatusInternalServerError, code: "INTERNAL_ERROR"},
		{name: "throughput", err: &smithy.GenericAPIError{Code: "ProvisionedThroughputExceededException"}, status: http.StatusServiceUnavailable, code: "THROUGHPUT_EXCEEDED"},
		{name: "request limit", err: &smithy.GenericAPIError{Code: "RequestLimitExceeded"}, status: http.StatusServiceUnavailable, code: "REQUEST_LIMIT_EXCEEDED"},
		{name: "internal", err: &smithy.GenericAPIError{Code: "InternalServerError"}, status: http.StatusServiceUnavailable, code: "DYNAMODB_INTERNAL_ERROR"},
		{name: "transaction throttling", err: &dynamodbtypes.TransactionCanceledException{CancellationReasons: []dynamodbtypes.CancellationReason{{Code: awssdk.String("ThrottlingError")}}}, status: http.StatusServiceUnavailable, code: "THROUGHPUT_EXCEEDED"},
		{name: "table missing", err: &smithy.GenericAPIError{Code: "ResourceNotFoundException"}, status: http.StatusServiceUnavailable, code: "TABLE_NOT_FOUND"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			WriteError(recorder, testCase.err)
			if recorder.Code != testCase.status || recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("unexpected response status=%d content-type=%q", recorder.Code, recorder.Header().Get("Content-Type"))
			}
			if !containsJSONField(recorder.Body.String(), `"error":"`+testCase.code+`"`) {
				t.Fatalf("expected error code in %s", recorder.Body.String())
			}
			retryAfter := recorder.Header().Get("Retry-After")
			if testCase.code == "THROUGHPUT_EXCEEDED" || testCase.code == "REQUEST_LIMIT_EXCEEDED" || testCase.code == "DYNAMODB_INTERNAL_ERROR" {
				if retryAfter != "1" {
					t.Fatalf("expected Retry-After: 1, got %q", retryAfter)
				}
			} else if retryAfter != "" {
				t.Fatalf("unexpected Retry-After header %q", retryAfter)
			}
		})
	}
}

// containsJSONField checks that an encoded field fragment is present in a response.
func containsJSONField(body, field string) bool {
	return len(body) >= len(field) && (body == field || contains(body, field))
}

// contains finds an exact byte substring for response assertions.
func contains(body, field string) bool {
	for index := 0; index+len(field) <= len(body); index++ {
		if body[index:index+len(field)] == field {
			return true
		}
	}
	return false
}
