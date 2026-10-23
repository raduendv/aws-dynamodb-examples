package operations

import (
	"net/http"

	"instantpayments/pkg/http/api/v1/internal/request"
	"instantpayments/pkg/http/api/v1/internal/response"
	"instantpayments/pkg/service"
)

// Process serves payment-processing requests.
type Process struct{ payment *service.Payment }

// Pattern returns the payment-processing route.
func (*Process) Pattern() string { return "/api/v1/payments/outbound/{paymentID}/process" }

// ServeHTTP processes a received payment.
func (h *Process) ServeHTTP(writer http.ResponseWriter, r *http.Request) {
	if !request.RequireMethod(writer, r, http.MethodPost) {
		return
	}

	id := r.PathValue("paymentID")
	if !request.ValidIdentifier(id) {
		response.WriteError(writer, &service.DomainError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Text: "invalid payment id"})

		return
	}

	result, err := h.payment.ProcessContext(r.Context(), id)
	if err != nil {
		response.WriteError(writer, err)

		return
	}

	_ = response.WriteJSON(writer, http.StatusOK, result)
}

// NewProcess constructs a payment-processing handler.
func NewProcess(payment *service.Payment) *Process { return &Process{payment: payment} }
