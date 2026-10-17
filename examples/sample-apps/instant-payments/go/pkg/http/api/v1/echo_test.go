package v1

import (
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestEchoHandlerCopiesRequestBody(t *testing.T) {
	handler := NewEchoHandler(zap.NewNop())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("POST", handler.Pattern(), strings.NewReader("echo")))
	if recorder.Body.String() != "echo" {
		t.Fatalf("expected echoed body, got %q", recorder.Body.String())
	}
}
