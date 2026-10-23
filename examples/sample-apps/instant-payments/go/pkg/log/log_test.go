// This suite checks construction of the echo logging handler.
package log

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewEchoHandler(t *testing.T) {
	logger := zap.NewNop()
	if handler := NewEchoHandler(logger); handler == nil || handler.log != logger {
		t.Fatalf("expected handler to preserve logger, got %#v", handler)
	}
}
