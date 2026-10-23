package payments

import (
	"io"
	"net/http"

	"go.uber.org/zap"
)

// OutboundByID is a legacy body-echo handler not registered by Module.
// PaymentQuery serves the active outbound payment lookup route.
type OutboundByID struct {
	log *zap.Logger
}

// Pattern returns the legacy outbound payment route.
func (*OutboundByID) Pattern() string {
	return "/api/v1/payments/outbound/{paymentID}"
}

// ServeHTTP echoes the request body without looking up a payment.
func (p *OutboundByID) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := io.Copy(w, r.Body); err != nil {
		p.log.Warn("Failed to handle request", zap.Error(err))
	}
}

// NewOutboundByID constructs the legacy outbound payment handler.
func NewOutboundByID(log *zap.Logger) *OutboundByID {
	log.Log(zap.InfoLevel, "Registering handler for /v1/payments/outbound/{paymentID}")

	return &OutboundByID{log: log}
}
