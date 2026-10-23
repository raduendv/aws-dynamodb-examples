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
	// PK and SK hold the logical account ID; DynamoDB adds ACCOUNT#.
	Item
	AccountKeys
	AccountID      string  `dynamodbav:"accountId"`
	CurrentBalance float64 `dynamodbav:"currentBalance"`
	// CurrentBalanceExact preserves the stored decimal for ledger arithmetic.
	CurrentBalanceExact   string           `dynamodbav:"-"`
	AvailableBalance      float64          `dynamodbav:"availableBalance"`
	AvailableBalanceExact string           `dynamodbav:"-"`
	Version               int64            `dynamodbav:"version"`
	Currency              string           `dynamodbav:"currency"`
	Status                AccountStatus    `dynamodbav:"status"`
	Rows                  []map[string]any `dynamodbav:"-"`
	CreatedAt             time.Time        `dynamodbav:"createdAtUtc"`
	UpdatedAt             time.Time        `dynamodbav:"updatedAtUtc"`
}
