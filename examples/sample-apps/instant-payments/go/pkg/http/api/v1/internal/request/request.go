package request

import (
	"net/http"
	"slices"
	"strings"

	"instantpayments/pkg/http/api/v1/internal/response"
	"instantpayments/pkg/service"
)

func ValidIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

func RequireMethod(writer http.ResponseWriter, r *http.Request, acceptedMethods ...string) bool {
	if slices.Contains(acceptedMethods, r.Method) {
		return true
	}

	writer.Header().Set("Allow", strings.Join(acceptedMethods, ", "))
	response.WriteError(writer, &service.DomainError{Status: http.StatusMethodNotAllowed, Code: "METHOD_NOT_ALLOWED", Text: "method not allowed"})

	return false
}
