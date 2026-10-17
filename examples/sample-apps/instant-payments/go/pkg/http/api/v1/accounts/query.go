package accounts

import (
	"net/http"

	"instantpayments/pkg/http/api/v1/internal/request"
	"instantpayments/pkg/http/api/v1/internal/response"
	requestinput "instantpayments/pkg/input"
	"instantpayments/pkg/service"
)

// Query serves account lookup requests.
type Query struct{ payment *service.Payment }

// Pattern returns the account lookup route.
func (*Query) Pattern() string { return "/api/v1/accounts/{accountID}" }

// ServeHTTP returns an account and its reservations.
func (handler *Query) ServeHTTP(writer http.ResponseWriter, r *http.Request) {
	if !request.RequireMethod(writer, r, http.MethodGet) {
		return
	}

	id := r.PathValue("accountID")
	if !request.ValidIdentifier(id) {
		response.WriteError(writer, &service.DomainError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Text: "invalid account id"})

		return
	}

	result, err := handler.payment.Account(id)
	if err != nil {
		response.WriteError(writer, err)

		return
	}

	_ = response.WriteJSON(writer, http.StatusOK, result)
}

// BatchQuery serves reservation batch lookup requests.
type BatchQuery struct{ payment *service.Payment }

// Pattern returns the reservation batch route.
func (*BatchQuery) Pattern() string { return "/api/v1/accounts/{accountID}/batch-get-reservations" }

// ServeHTTP returns the requested reservations for an account.
func (h *BatchQuery) ServeHTTP(writer http.ResponseWriter, r *http.Request) {
	if !request.RequireMethod(writer, r, http.MethodPost) {
		return
	}

	input, err := requestinput.DecodeJSON[struct {
		IDs []string `json:"reservationIds"`
	}](r)
	if err != nil {
		response.WriteError(writer, &service.DomainError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Text: "not valid JSON"})

		return
	}

	id := r.PathValue("accountID")
	if !request.ValidIdentifier(id) {
		response.WriteError(writer, &service.DomainError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Text: "invalid account id"})

		return
	}

	result, err := h.payment.Batch(id, input.IDs)
	if err != nil {
		response.WriteError(writer, err)

		return
	}

	_ = response.WriteJSON(writer, http.StatusOK, result)
}

// NewQuery constructs an account lookup handler.
func NewQuery(payment *service.Payment) *Query { return &Query{payment: payment} }

// NewBatchQuery constructs a reservation batch lookup handler.
func NewBatchQuery(payment *service.Payment) *BatchQuery { return &BatchQuery{payment: payment} }
