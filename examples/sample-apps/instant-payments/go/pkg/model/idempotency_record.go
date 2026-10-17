package model

import "time"

// IdempotencyRecord stores the request fingerprint and response associated
// with an idempotency key.
type IdempotencyRecord struct {
	// PK: IDEMPOTENCY#<key>
	// SK: IDEMPOTENCY#<key>
	Item
	RequestHash string    `dynamodbav:"requestHash"`
	Response    string    `dynamodbav:"response"`
	CreatedAt   time.Time `dynamodbav:"createdAtUtc"`
	ExpiresAt   time.Time `dynamodbav:"ttl,unixtime"` // DynamoDB TTL epoch seconds
}
