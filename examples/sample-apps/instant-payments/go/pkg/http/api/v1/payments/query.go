package payments

import (
	"net/http"
	"strconv"
	"strings"

	"instantpayments/pkg/http/api/v1/internal/request"
	"instantpayments/pkg/http/api/v1/internal/response"
	"instantpayments/pkg/service"
)

// PaymentQuery serves payment lookup requests.
type PaymentQuery struct{ payment *service.Payment }

// Pattern returns the payment lookup route.
func (*PaymentQuery) Pattern() string { return "/api/v1/payments/outbound/{paymentID}" }

// ServeHTTP returns a payment by ID.
func (h *PaymentQuery) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !request.RequireMethod(w, r, http.MethodGet) {
		return
	}

	id := r.PathValue("paymentID")
	if !request.ValidIdentifier(id) {
		response.WriteError(w, &service.DomainError{Status: 400, Code: "VALIDATION_ERROR", Text: "invalid payment id"})

		return
	}

	result, err := h.payment.Get(id)
	if err != nil {
		response.WriteError(w, err)

		return
	}

	_ = response.WriteJSON(w, http.StatusOK, result)
}

// MerchantQuery serves merchant payment list requests.
type MerchantQuery struct {
	payment *service.Payment
	state   bool
}

func (h *MerchantQuery) Pattern() string {
	if h.state {
		return "/api/v1/merchants/{merchantID}/payments/state/{state}"
	}

	return "/api/v1/merchants/{merchantID}/payments"
}

// ServeHTTP returns a paginated merchant payment list.
func (h *MerchantQuery) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !request.RequireMethod(w, r, http.MethodGet) {
		return
	}

	state := ""
	route := "merchant"
	if h.state {
		state, route = r.PathValue("state"), "state"
		if !request.ValidIdentifier(state) {
			response.WriteError(w, &service.DomainError{Status: 400, Code: "VALIDATION_ERROR", Text: "invalid payment state"})

			return
		}
		state = strings.ToUpper(state)
		if state != "RECEIVED" && state != "COMPLETED" && state != "REJECTED" {
			response.WriteError(w, &service.DomainError{Status: 400, Code: "INVALID_PAYMENT_STATE", Text: "invalid payment state"})

			return
		}
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			response.WriteError(w, &service.DomainError{Status: 400, Code: "VALIDATION_ERROR", Text: "limit must be a positive integer"})

			return
		}
		if parsed > 100 {
			parsed = 100
		}
		if parsed > 0 {
			limit = parsed
		}
	}

	forward := strings.EqualFold(r.URL.Query().Get("scanIndexForward"), "true")
	merchant := r.PathValue("merchantID")
	if !request.ValidIdentifier(merchant) {
		response.WriteError(w, &service.DomainError{Status: 400, Code: "VALIDATION_ERROR", Text: "invalid merchant id"})

		return
	}

	result, err := h.payment.List(merchant, state, route, r.URL.Query().Get("nextToken"), limit, forward)
	if err != nil {
		response.WriteError(w, err)

		return
	}

	_ = response.WriteJSON(w, http.StatusOK, result)
}

// NewPaymentQuery constructs a payment lookup handler.
func NewPaymentQuery(payment *service.Payment) *PaymentQuery { return &PaymentQuery{payment: payment} }

// NewMerchantQuery constructs a merchant payment list handler.
func NewMerchantQuery(payment *service.Payment) *MerchantQuery {
	return &MerchantQuery{payment: payment}
}

// NewMerchantStateQuery constructs a merchant payment list handler filtered by state.
func NewMerchantStateQuery(payment *service.Payment) *MerchantQuery {
	return &MerchantQuery{payment: payment, state: true}
}
