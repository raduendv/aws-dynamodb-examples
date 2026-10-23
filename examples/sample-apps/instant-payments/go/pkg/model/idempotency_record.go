package model

import "time"

// IdempotencyRecord stores the request fingerprint and response associated
// with an idempotency key.
type IdempotencyRecord struct {
	// PK holds the raw idempotency key; DynamoDB adds IDEMPOTENCY#.
	Item
	IdempotencyKeys
	RequestHash string         `dynamodbav:"requestHash"`
	Response    map[string]any `dynamodbav:"responseSnapshot"`
	CreatedAt   time.Time      `dynamodbav:"createdAtUtc"`
	ExpiresAt   time.Time      `dynamodbav:"ttl,unixtime"` // DynamoDB TTL epoch seconds
}
