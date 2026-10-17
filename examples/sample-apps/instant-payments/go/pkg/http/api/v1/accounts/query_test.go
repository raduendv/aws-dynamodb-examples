package accounts

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"instantpayments/pkg/service"
)

func accountRequest(t *testing.T, handler http.Handler, method, target, body string) (int, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, bytes.NewBufferString(body)))
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return recorder.Code, payload
}

func TestAccountQuery(t *testing.T) {
	handler := NewQuery(service.NewPayment(zap.NewNop(), nil))
	mux := http.NewServeMux()
	mux.Handle(handler.Pattern(), handler)
	status, payload := accountRequest(t, mux, http.MethodGet, "/api/v1/accounts/acc_usd_1", "")
	if status != http.StatusOK || payload["accountId"] != "acc_usd_1" {
		t.Fatalf("expected account response, got %d %#v", status, payload)
	}
	status, payload = accountRequest(t, mux, http.MethodPost, "/api/v1/accounts/acc_usd_1", "")
	if status != http.StatusMethodNotAllowed || payload["error"] != "METHOD_NOT_ALLOWED" {
		t.Fatalf("expected method error, got %d %#v", status, payload)
	}
	status, payload = accountRequest(t, mux, http.MethodGet, "/api/v1/accounts/acc$bad", "")
	if status != http.StatusBadRequest || payload["error"] != "VALIDATION_ERROR" {
		t.Fatalf("expected identifier error, got %d %#v", status, payload)
	}
	status, payload = accountRequest(t, mux, http.MethodGet, "/api/v1/accounts/unknown", "")
	if status != http.StatusNotFound || payload["error"] != "ACCOUNT_NOT_FOUND" {
		t.Fatalf("expected not found error, got %d %#v", status, payload)
	}
}

func TestBatchQuery(t *testing.T) {
	handler := NewBatchQuery(service.NewPayment(zap.NewNop(), nil))
	mux := http.NewServeMux()
	mux.Handle(handler.Pattern(), handler)
	status, payload := accountRequest(t, mux, http.MethodPost, "/api/v1/accounts/acc_usd_1/batch-get-reservations", `{"reservationIds":["missing"]}`)
	if status != http.StatusOK || len(payload["missingReservationIds"].([]any)) != 1 {
		t.Fatalf("expected batch response, got %d %#v", status, payload)
	}
	status, payload = accountRequest(t, mux, http.MethodPost, "/api/v1/accounts/acc_usd_1/batch-get-reservations", `{`)
	if status != http.StatusBadRequest || payload["error"] != "VALIDATION_ERROR" {
		t.Fatalf("expected JSON validation error, got %d %#v", status, payload)
	}
	status, payload = accountRequest(t, mux, http.MethodGet, "/api/v1/accounts/acc_usd_1/batch-get-reservations", "")
	if status != http.StatusMethodNotAllowed || payload["error"] != "METHOD_NOT_ALLOWED" {
		t.Fatalf("expected method error, got %d %#v", status, payload)
	}
	status, payload = accountRequest(t, mux, http.MethodPost, "/api/v1/accounts/acc$bad/batch-get-reservations", `{"reservationIds":["missing"]}`)
	if status != http.StatusBadRequest || payload["error"] != "VALIDATION_ERROR" {
		t.Fatalf("expected identifier error, got %d %#v", status, payload)
	}
}
