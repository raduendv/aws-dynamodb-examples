package dto

import "time"

// OutboundPaymentInput is the JSON request used to create an outbound payment.
type OutboundPaymentInput struct {
	IdempotencyKey  string  `json:"idempotencyKey" validate:"required" hash:"-"`
	MerchantID      string  `json:"merchantId" validate:"required"`
	DebtorAccountID string  `json:"debtorAccountId" validate:"required"`
	CreditorIban    string  `json:"creditorIban" validate:"required"`
	CreditorName    string  `json:"creditorName" validate:"required"`
	Amount          float64 `json:"amount" validate:"required,gte=0"`
	Currency        string  `json:"currency" validate:"required,iso4217"`
}

// OutboundPaymentOutput is the standard outbound-payment response envelope.
type OutboundPaymentOutput struct {
	PaymentID     string    `json:"paymentId"`
	State         string    `json:"state"`
	CorrelationID *string   `json:"correlationId"`
	CreatedAtUTC  time.Time `json:"createdAtUtc"`
}
