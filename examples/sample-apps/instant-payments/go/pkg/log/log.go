package log

import "go.uber.org/zap"

type EchoHandler struct {
	log *zap.Logger
}

// NewEchoHandler constructs a logging echo handler.
func NewEchoHandler(log *zap.Logger) *EchoHandler {
	return &EchoHandler{log: log}
}
