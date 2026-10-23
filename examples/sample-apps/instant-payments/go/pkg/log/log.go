package log

import "go.uber.org/zap"

// EchoHandler holds the logger dependency for the diagnostic echo facade.
type EchoHandler struct {
	log *zap.Logger
}

// NewEchoHandler constructs a logging echo handler.
func NewEchoHandler(log *zap.Logger) *EchoHandler {
	return &EchoHandler{log: log}
}
