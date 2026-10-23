package payments

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"instantpayments/pkg/http/api/v1/internal/request"
	"instantpayments/pkg/http/api/v1/internal/response"
	"instantpayments/pkg/service"
)

const (
	// defaultMerchantPaymentLimit balances useful page sizes with bounded query work.
	defaultMerchantPaymentLimit = 50
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

// Pattern returns the merchant payment list route.
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
		if state != "RECEIVED" && state != "FUNDS_RESERVED" && state != "COMPLETED" && state != "REJECTED" {
			response.WriteError(w, &service.DomainError{Status: 400, Code: "INVALID_PAYMENT_STATE", Text: "invalid payment state"})

			return
		}
	}

	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		response.WriteError(w, &service.DomainError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Text: "invalid query syntax"})
		return
	}
	forward := false
	if values, supplied := query["scanIndexForward"]; supplied {
		if len(values) != 1 {
			response.WriteError(w, &service.DomainError{Status: 400, Code: "VALIDATION_ERROR", Text: "scanIndexForward must be true or false"})

			return
		}
		switch {
		case strings.EqualFold(values[0], "true"):
			forward = true
		case strings.EqualFold(values[0], "false"):
			forward = false
		default:
			response.WriteError(w, &service.DomainError{Status: 400, Code: "VALIDATION_ERROR", Text: "scanIndexForward must be true or false"})

			return
		}
	}

	limit := defaultMerchantPaymentLimit
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			response.WriteError(w, &service.DomainError{Status: 400, Code: "VALIDATION_ERROR", Text: "limit must be an integer"})

			return
		}
		limit = parsed
	}

	merchant := r.PathValue("merchantID")
	if !request.ValidIdentifier(merchant) {
		response.WriteError(w, &service.DomainError{Status: 400, Code: "VALIDATION_ERROR", Text: "invalid merchant id"})

		return
	}

	result, err := h.payment.List(merchant, state, route, query.Get("nextToken"), limit, forward)
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
