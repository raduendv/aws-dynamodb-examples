package model

// PaymentEventType identifies the kind of payment lifecycle event.
type PaymentEventType string

const (
	// PaymentEventInitiated identifies the start of payment processing.
	PaymentEventInitiated PaymentEventType = "INITIATED"
	// PaymentEventSettled identifies a payment that has settled.
	PaymentEventSettled PaymentEventType = "SETTLED"
)

// PaymentEvent records a state change for a payment.
type PaymentEvent struct {
	// PK holds the logical payment ID; SK holds the numeric sequence.
	Item
	PaymentEventKeys
	EventType       PaymentEventType `dynamodbav:"eventType"`
	SequenceNumber  int64            `dynamodbav:"sequenceNumber"`
	CorrelationID   string           `dynamodbav:"correlationId"`
	ReasonCode      string           `dynamodbav:"reasonCode,omitempty"`
	OccurredAt      string           `dynamodbav:"occurredAt"`
	PaymentID       string           `dynamodbav:"paymentId"`
	MerchantID      string           `dynamodbav:"merchantId,omitempty"`
	DebtorAccountID string           `dynamodbav:"debtorAccountId,omitempty"`
	CreditorIBAN    string           `dynamodbav:"creditorIban,omitempty"`
	CreditorName    string           `dynamodbav:"creditorName,omitempty"`
	Amount          float64          `dynamodbav:"amount,omitempty"`
	AmountExact     string           `dynamodbav:"-"`
	Currency        string           `dynamodbav:"currency,omitempty"`
	IdempotencyKey  string           `dynamodbav:"idempotencyKey,omitempty"`
}
