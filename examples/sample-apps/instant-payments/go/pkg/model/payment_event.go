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
	// PK: PAYMENT#<paymentId>
	// SK: EVENT#<timestamp>
	Item
	Type    PaymentEventType `dynamodbav:"type"`    // "INITIATED", "SETTLED", etc.
	Details string           `dynamodbav:"details"` // JSON string with event details
}
