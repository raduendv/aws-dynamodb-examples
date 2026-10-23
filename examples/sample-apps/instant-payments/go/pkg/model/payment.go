package model

import "time"

// Payment is the DynamoDB representation of an outbound payment.
type Payment struct {
	// PK holds the logical payment ID; DynamoDB stores PAYMENT#<paymentId>.
	Item
	PaymentKeys
	CorrelationID   string         `dynamodbav:"correlationId"`
	MerchantID      string         `dynamodbav:"merchantId"`
	DebtorAccountID string         `dynamodbav:"debtorAccountId"`
	CreditorIBAN    string         `dynamodbav:"creditorIban"`
	CreditorName    string         `dynamodbav:"creditorName"`
	Amount          float64        `dynamodbav:"amount"`
	AmountExact     string         `dynamodbav:"-"`
	Currency        string         `dynamodbav:"currency"`
	Status          string         `dynamodbav:"aggregateState"`
	ReasonCode      string         `dynamodbav:"reasonCode,nullempty"`
	Reference       string         `dynamodbav:"reference"`
	CreatedAt       time.Time      `dynamodbav:"createdAtUtc"`
	UpdatedAt       time.Time      `dynamodbav:"updatedAtUtc"`
	LastSequence    int64          `dynamodbav:"lastSequence"`
	Events          []PaymentEvent `dynamodbav:"-"`
}
