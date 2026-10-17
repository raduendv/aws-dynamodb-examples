package model

import "time"

// Payment is the DynamoDB representation of an outbound payment.
type Payment struct {
	// PK: PAYMENT#<paymentId>
	// SK: PAYMENT#<paymentId>
	Item
	MerchantID      string `dynamodbav:"merchantId"`
	DebtorAccountID string `dynamodbav:"debtorAccountId"`
	//CreditorAccountID string    `dynamodbav:"creditor_account_id"`
	CreditorIBAN string    `dynamodbav:"creditorIban"`
	CreditorName string    `dynamodbav:"creditorName"`
	Amount       int64     `dynamodbav:"amount"` // Minor units 1.01 becomes 101 to avoid floating point issues
	Currency     string    `dynamodbav:"currency"`
	Status       string    `dynamodbav:"state"` // "RECEIVED", "COMPLETED", etc.
	Reference    string    `dynamodbav:"reference"`
	CreatedAt    time.Time `dynamodbav:"createdAtUtc"`
	UpdatedAt    time.Time `dynamodbav:"updatedAtUtc"`
	LastSequence int       `dynamodbav:"lastSequence"`
}
