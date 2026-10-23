package dto

import (
	"encoding/json"
	"time"

	"instantpayments/pkg/model"
)

// OutboundPaymentInput is the JSON request used to create an outbound payment.
type OutboundPaymentInput struct {
	IdempotencyKey  string  `json:"idempotencyKey" validate:"required" hash:"-"`
	MerchantID      string  `json:"merchantId" validate:"required"`
	DebtorAccountID string  `json:"debtorAccountId" validate:"required"`
	CreditorIban    string  `json:"creditorIban" validate:"required"`
	CreditorName    string  `json:"creditorName" validate:"required"`
	Amount          float64 `json:"amount" validate:"required,gte=0"`
	// AmountExact retains the JSON decimal before float64 conversion for hashing and writes.
	AmountExact string `json:"-"`
	Currency    string `json:"currency" validate:"required,iso4217"`
}

// UnmarshalJSON preserves the original numeric amount while decoding the request fields.
func (input *OutboundPaymentInput) UnmarshalJSON(data []byte) error {
	type fields OutboundPaymentInput
	var decoded struct {
		fields
		Amount json.RawMessage `json:"amount"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*input = OutboundPaymentInput(decoded.fields)
	if len(decoded.Amount) == 0 || string(decoded.Amount) == "null" {
		return nil
	}
	exact, err := model.DecimalString(string(decoded.Amount))
	if err != nil {
		return err
	}
	amount, err := json.Number(exact).Float64()
	if err != nil {
		return err
	}
	input.Amount, input.AmountExact = amount, exact
	return nil
}

// MarshalJSON keeps a decoded request's original decimal when it is re-encoded.
func (input OutboundPaymentInput) MarshalJSON() ([]byte, error) {
	type fields OutboundPaymentInput
	amount, err := model.AmountDecimal(input.Amount, input.AmountExact)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		fields
		Amount json.Number `json:"amount"`
	}{fields: fields(input), Amount: json.Number(amount)})
}

// OutboundPaymentOutput is the standard outbound-payment response envelope.
type OutboundPaymentOutput struct {
	PaymentID     string    `json:"paymentId"`
	State         string    `json:"state"`
	CorrelationID *string   `json:"correlationId"`
	CreatedAtUTC  time.Time `json:"createdAtUtc"`
}

// MarshalJSON keeps response timestamps identical to persisted replay snapshots.
func (output OutboundPaymentOutput) MarshalJSON() ([]byte, error) {
	type fields OutboundPaymentOutput
	return json.Marshal(struct {
		fields
		CreatedAtUTC string `json:"createdAtUtc"`
	}{fields: fields(output), CreatedAtUTC: model.FormatTimestamp(output.CreatedAtUTC)})
}
