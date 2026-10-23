package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	"instantpayments/pkg/model"
	"instantpayments/pkg/service"
)

// WriteJSON commits a JSON response and returns any encoding or transport error.
func WriteJSON(writer http.ResponseWriter, status int, value any) error {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)

	return json.NewEncoder(writer).Encode(value)
}

// WriteError writes a standardized API error response.
func WriteError(writer http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error"
	var domain *service.DomainError
	if errors.As(err, &domain) {
		status, code, message = domain.Status, domain.Code, domain.Text
	} else {
		switch dynamoErrorCode(err) {
		case "ProvisionedThroughputExceededException", "ProvisionedThroughputExceeded", "ThrottlingException", "ThrottlingError":
			status, code, message = http.StatusServiceUnavailable, "THROUGHPUT_EXCEEDED", "DynamoDB throughput exceeded"
		case "RequestLimitExceeded":
			status, code, message = http.StatusServiceUnavailable, "REQUEST_LIMIT_EXCEEDED", "DynamoDB request limit exceeded"
		case "InternalServerError":
			status, code, message = http.StatusServiceUnavailable, "DYNAMODB_INTERNAL_ERROR", "DynamoDB internal error"
		case "ResourceNotFoundException":
			status, code, message = http.StatusServiceUnavailable, "TABLE_NOT_FOUND", "configured DynamoDB table was not found"
		}
	}
	if code == "THROUGHPUT_EXCEEDED" || code == "REQUEST_LIMIT_EXCEEDED" || code == "DYNAMODB_INTERNAL_ERROR" {
		writer.Header().Set("Retry-After", "1")
	}
	_ = WriteJSON(writer, status, map[string]any{"error": code, "message": message, "timestamp": model.FormatTimestamp(time.Now())})
}

// dynamoErrorCode inspects wrapped cancellation reasons and SDK causes before classification.
func dynamoErrorCode(err error) string {
	var canceled *dynamodbtypes.TransactionCanceledException
	if errors.As(err, &canceled) {
		for _, reason := range canceled.CancellationReasons {
			if reason.Code == nil {
				continue
			}
			switch *reason.Code {
			case "ProvisionedThroughputExceeded", "ThrottlingError", "RequestLimitExceeded", "InternalServerError":
				return *reason.Code
			}
		}
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		return apiError.ErrorCode()
	}
	return ""
}
