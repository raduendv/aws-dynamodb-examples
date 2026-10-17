package model

import "time"

// AccountStatus identifies whether an account can be used for payments.
type AccountStatus string

const (
	// AccountStatusActive identifies an account that can process payments.
	AccountStatusActive AccountStatus = "ACTIVE"
	// AccountStatusInactive identifies an account that cannot process payments.
	AccountStatusInactive AccountStatus = "INACTIVE"
)

// Account is the DynamoDB representation of a payment account.
type Account struct {
	// PK: ACCOUNT#<username>
	// SK: ACCOUNT#<username>
	// @NOTE: having PK = "ACCOUNT" will translate to having every account on
	// a shard, which is not ideal for performance. By including the account
	// name in the PK, we ensure that all data related to that account is stored
	// together, improving query efficiency.
	Item
	Name      string           `dynamodbav:"name"`
	Balance   int64            `dynamodbav:"balance"`  // Use minor units (e.g. cents)
	Currency  string           `dynamodbav:"currency"` // ISO 4217, e.g. "EUR"
	Status    AccountStatus    `dynamodbav:"status"`   // ACTIVE/?
	Rows      []map[string]any `dynamodbav:"reservations,omitempty"`
	CreatedAt time.Time        `dynamodbav:"createdAtUtc"`
	UpdatedAt time.Time        `dynamodbav:"updatedAtUtc"`
}
