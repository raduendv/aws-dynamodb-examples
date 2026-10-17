package v1

import (
	"io"
	"net/http"

	"go.uber.org/zap"
)

// EchoHandler copies a request body to the response.
type EchoHandler struct {
	log *zap.Logger
}

// Pattern returns the echo route.
func (*EchoHandler) Pattern() string {
	return "/api/v1/echo"
}

// ServeHTTP echoes the request body.
func (e *EchoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := io.Copy(w, r.Body); err != nil {
		if e.log != nil {
			e.log.Warn("Failed to handle request", zap.Error(err))
		}
	}
}

// NewEchoHandler constructs an echo handler.
func NewEchoHandler(log *zap.Logger) *EchoHandler {
	return &EchoHandler{log: log}
}
