package request

import (
	"net/http"
	"slices"
	"strings"

	"instantpayments/pkg/http/api/v1/internal/response"
	"instantpayments/pkg/service"
)

// maximumIdentifierLength bounds user-supplied path identifiers to the shared API contract.
const maximumIdentifierLength = 64

// ValidIdentifier checks the shared nonempty ASCII identifier shape and 64-character bound.
func ValidIdentifier(value string) bool {
	if len(value) == 0 || len(value) > maximumIdentifierLength {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

// RequireMethod returns true for accepted verbs or writes a method-error envelope and Allow header.
func RequireMethod(writer http.ResponseWriter, r *http.Request, acceptedMethods ...string) bool {
	if slices.Contains(acceptedMethods, r.Method) {
		return true
	}

	writer.Header().Set("Allow", strings.Join(acceptedMethods, ", "))
	response.WriteError(writer, &service.DomainError{Status: http.StatusMethodNotAllowed, Code: "METHOD_NOT_ALLOWED", Text: "method not allowed"})

	return false
}
