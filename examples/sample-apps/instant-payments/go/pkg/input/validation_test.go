package input

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apphttp "instantpayments/pkg/http"
)

type validationFixture struct {
	Name string `json:"name"`
}

type validationRoute struct {
	validators []any
}

func (*validationRoute) Pattern() string                              { return "/validation" }
func (*validationRoute) ServeHTTP(http.ResponseWriter, *http.Request) {}
func (r *validationRoute) Validators() []any                          { return r.validators }

func TestWithRequestMethodValidator(t *testing.T) {
	validator := WithRequestMethodValidator(apphttp.POST)
	if err := validator.Validate(httptest.NewRequest(http.MethodPost, "/", nil)); err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(httptest.NewRequest(http.MethodGet, "/", nil)); err == nil {
		t.Fatal("expected invalid method error")
	}
}

func TestModuleAndInputValidator(t *testing.T) {
	if Module() == nil || NewValidate() == nil {
		t.Fatal("expected input module and validator")
	}
	validator := WithInputValidator[validationFixture]()
	if err := validator(nil); err != nil {
		t.Fatal(err)
	}
	if err := validator(&validationFixture{Name: "valid"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRequestReturnsErrorResponse(t *testing.T) {
	route := &validationRoute{validators: []any{
		WithRequestMethodValidator(apphttp.POST),
		ValidatorFunc[validationFixture](func(input *validationFixture) error {
			if input.Name == "" {
				return errors.New("name is required")
			}
			return nil
		}),
	}}
	request := httptest.NewRequest(http.MethodGet, "/validation", strings.NewReader(`{}`))
	result, writeResponse := ValidateRequest[validationFixture](route, request)
	if result != nil || writeResponse == nil {
		t.Fatalf("expected validation response, got %#v %v", result, writeResponse)
	}
	recorder := httptest.NewRecorder()
	writeResponse(recorder)
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != string(ValidationStatusInvalidInput) || !strings.Contains(body["message"], "invalid method") || !strings.Contains(body["message"], "name is required") {
		t.Fatalf("unexpected validation body %#v", body)
	}
}

func TestValidateRequestReturnsNoResponseForValidInput(t *testing.T) {
	route := &validationRoute{validators: []any{
		WithRequestMethodValidator(apphttp.POST),
		ValidatorFunc[validationFixture](func(input *validationFixture) error {
			if input.Name == "" {
				return errors.New("name is required")
			}
			return nil
		}),
	}}
	request := httptest.NewRequest(http.MethodPost, "/validation", strings.NewReader(`{"name":"valid"}`))
	result, writeResponse := ValidateRequest[validationFixture](route, request)
	if result != nil || writeResponse != nil {
		t.Fatalf("expected no validation response, got %#v %v", result, writeResponse)
	}
}

func TestValidateRequestIgnoresNonValidationRoute(t *testing.T) {
	if result, writeResponse := ValidateRequest[validationFixture](nil, httptest.NewRequest(http.MethodGet, "/", nil)); result != nil || writeResponse != nil {
		t.Fatalf("expected nil result for nil route, got %#v %v", result, writeResponse)
	}
}
