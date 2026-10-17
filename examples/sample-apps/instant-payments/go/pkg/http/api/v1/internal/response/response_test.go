package response

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
		})
	}
}

func containsJSONField(body, field string) bool {
	return len(body) >= len(field) && (body == field || contains(body, field))
}

func contains(body, field string) bool {
	for index := 0; index+len(field) <= len(body); index++ {
		if body[index:index+len(field)] == field {
			return true
		}
	}
	return false
}
