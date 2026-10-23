// This suite checks identifier syntax and allowed-method validation.
package request

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidIdentifier(t *testing.T) {
	for value, want := range map[string]bool{
		"pay_123-abc":                         true,
		"":                                    false,
		"pay$bad":                             false,
		string(bytes.Repeat([]byte("a"), 64)): true,
		string(bytes.Repeat([]byte("a"), 65)): false,
	} {
		if got := ValidIdentifier(value); got != want {
			t.Errorf("ValidIdentifier(%q) = %t, want %t", value, got, want)
		}
	}
}

func TestRequireMethod(t *testing.T) {
	accepted := httptest.NewRecorder()
	if !RequireMethod(accepted, httptest.NewRequest(http.MethodGet, "/", nil), http.MethodGet, http.MethodHead) {
		t.Fatal("expected GET to be accepted")
	}
	if accepted.Code != http.StatusOK || accepted.Body.Len() != 0 {
		t.Fatalf("expected untouched response, got status=%d body=%q", accepted.Code, accepted.Body.String())
	}

	rejected := httptest.NewRecorder()
	if RequireMethod(rejected, httptest.NewRequest(http.MethodPost, "/", nil), http.MethodGet, http.MethodHead) {
		t.Fatal("expected POST to be rejected")
	}
	if rejected.Code != http.StatusMethodNotAllowed || rejected.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("unexpected response status=%d allow=%q", rejected.Code, rejected.Header().Get("Allow"))
	}
	var body map[string]any
	if err := json.Unmarshal(rejected.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] != "METHOD_NOT_ALLOWED" {
		t.Fatalf("expected method error, got %#v", body)
	}
}
