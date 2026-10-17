package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCanAcceptMethod(t *testing.T) {
	acceptedWriter := httptest.NewRecorder()
	acceptedRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	if !CanAcceptMethod(acceptedWriter, acceptedRequest, GET, POST) {
		t.Fatal("expected GET to be accepted")
	}
	if acceptedWriter.Code != http.StatusOK {
		t.Fatalf("expected no error status, got %d", acceptedWriter.Code)
	}

	rejectedWriter := httptest.NewRecorder()
	rejectedRequest := httptest.NewRequest(http.MethodDelete, "/", nil)
	if CanAcceptMethod(rejectedWriter, rejectedRequest, GET, POST) {
		t.Fatal("expected DELETE to be rejected")
	}
	if rejectedWriter.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rejectedWriter.Code)
	}
	if allow := rejectedWriter.Header().Values("Allow"); len(allow) != 2 || allow[0] != "GET" || allow[1] != "POST" {
		t.Fatalf("unexpected Allow header %v", allow)
	}
}
