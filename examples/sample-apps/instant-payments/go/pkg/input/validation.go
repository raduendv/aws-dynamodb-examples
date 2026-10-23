package input

import (
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"slices"

	"instantpayments/pkg/http"
)

// ValidationStatus identifies the outcome of request validation.
type ValidationStatus string

const (
	ValidationStatusError        ValidationStatus = "error"
	ValidationStatusInvalidInput ValidationStatus = "invalid_input"
	ValidationStatusUnauthorized ValidationStatus = "unauthorized"
	ValidationStatusForbidden    ValidationStatus = "forbidden"
	ValidationStatusNotFound     ValidationStatus = "not_found"
	ValidationStatusBadMethod    ValidationStatus = "bad_method"
)

// RouteWithValidators describes a route with typed validation and processing.
type RouteWithValidators[T any] interface {
	http.Route

	Validators() []any
}

// Validator validates a typed request value.
type Validator[T any] interface {
	Validate(*T) error
}

// ValidatorFunc adapts a function into a Validator.
type ValidatorFunc[T any] func(*T) error

// RequestValidator validates an HTTP request before body processing.
type RequestValidator ValidatorFunc[stdhttp.Request]

// Validate applies the request validator function.
func (r RequestValidator) Validate(req *stdhttp.Request) error {
	return r(req)
}

// ValidationError describes a request validation failure.
type ValidationError struct {
	Status  ValidationStatus `json:"status"`
	Message string           `json:"message"`
}

// Error returns the validation failure message.
func (e *ValidationError) Error() string {
	return e.Message
}

// ResponseWriter writes a validation error response.
type ResponseWriter func(stdhttp.ResponseWriter)

// ValidateRequest validates and processes a request for a typed route.
func ValidateRequest[T any](r http.Route, req *stdhttp.Request) (*T, ResponseWriter) {
	if r == nil || req == nil {
		return nil, nil
	}

	rwv, ok := r.(RouteWithValidators[T])
	if !ok {
		return nil, nil
	}

	var errs []error

	i, err := DecodeJSON[T](req)
	if err != nil {
		errs = append(errs, err)
	}

	for _, validator := range rwv.Validators() {
		switch v := validator.(type) {
		case RequestValidator:
			if err := v.Validate(req); err != nil {
				errs = append(errs, err)
			}
		case Validator[T]:
			if err := v.Validate(&i); err != nil {
				errs = append(errs, err)
			}
		case ValidatorFunc[T]:
			if err := v(&i); err != nil {
				errs = append(errs, err)
			}
		default:
			continue
		}
	}

	if len(errs) == 0 {
		return nil, nil
	}

	return nil, func(w stdhttp.ResponseWriter) {
		out := map[string]interface{}{
			"status": ValidationStatusInvalidInput,
		}
		err := errors.Join(errs...)
		if err != nil {
			out["message"] = err.Error()
		}

		e := json.NewEncoder(w)
		e.SetIndent("", "  ")
		_ = e.Encode(out)
	}
}

// WithRequestMethodValidator returns a validator rejecting methods outside the supplied set.
func WithRequestMethodValidator(acceptedMethods ...http.Method) RequestValidator {
	return func(req *stdhttp.Request) error {
		if !slices.Contains(acceptedMethods, http.Method(req.Method)) {
			return &ValidationError{
				Status:  ValidationStatusBadMethod,
				Message: "invalid method",
			}
		}

		return nil
	}
}

// WithInputValidator supplies the legacy no-op typed-validator extension point.
// Business input validation is performed by the concrete payment handlers and service.
func WithInputValidator[T any]() ValidatorFunc[T] {
	return func(t *T) error {
		if t == nil {
			return nil
		}

		return nil
	}
}
