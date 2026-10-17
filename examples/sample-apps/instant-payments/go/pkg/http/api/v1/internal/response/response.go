package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"instantpayments/pkg/service"
)

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
	}
	_ = WriteJSON(writer, status, map[string]any{"error": code, "message": message, "timestamp": time.Now().UTC().Format(time.RFC3339Nano)})
}
