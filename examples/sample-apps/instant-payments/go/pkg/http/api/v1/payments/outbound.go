package payments

import (
	stdhttp "net/http"

	"go.uber.org/zap"

	"instantpayments/pkg/dto"
	"instantpayments/pkg/http/api/v1/internal/request"
	"instantpayments/pkg/http/api/v1/internal/response"
	requestinput "instantpayments/pkg/input"
	"instantpayments/pkg/service"
)

// Outbound serves outbound payment creation requests.
type Outbound struct {
	log     *zap.Logger
	payment *service.Payment
}

// Pattern returns the outbound payment creation route.
func (*Outbound) Pattern() string {
	return "/api/v1/payments/outbound"
}

// ServeHTTP accepts POST JSON OutboundPaymentInput. Creation atomically stores
// the head, creation event and idempotency binding (201); an identical key/body
// replays the original response (200), while a changed request returns 409.
// Method/JSON/domain validation returns 405/400. DynamoDB errors use the shared
// 500/503 envelope. Settlement happens later through Streams, not this handler.
func (h *Outbound) ServeHTTP(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if !request.RequireMethod(w, r, stdhttp.MethodPost) {
		return
	}

	input, err := requestinput.DecodeJSON[dto.OutboundPaymentInput](r)
	if err != nil {
		response.WriteError(w, &service.DomainError{Status: stdhttp.StatusBadRequest, Code: "VALIDATION_ERROR", Text: "not valid JSON"})

		return
	}

	o, status, err := h.payment.Create(&input)
	if err != nil {
		response.WriteError(w, err)

		return
	}

	if err := response.WriteJSON(w, status, o); err != nil {
		h.log.Error("failed to encode response", zap.Error(err))
	}
}

// NewOutbound constructs an outbound payment handler.
func NewOutbound(
	log *zap.Logger,
	payment *service.Payment,
) *Outbound {
	log.Log(zap.InfoLevel, "Registering handler for /v1/payments/outbound")
	return &Outbound{
		log:     log,
		payment: payment,
	}
}
