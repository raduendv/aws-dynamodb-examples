package model

import "github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"

// Reservation is the durable hold audit row. ExpiresAt guards settlement but is not TTL.
type Reservation struct {
	Item
	ReservationKeys
	ReservationID string  `dynamodbav:"reservationId"`
	PaymentID     string  `dynamodbav:"paymentId"`
	Amount        float64 `dynamodbav:"amount"`
	AmountExact   string  `dynamodbav:"-"`
	Status        string  `dynamodbav:"status"`
	CreatedAtUTC  string  `dynamodbav:"createdAtUtc"`
	ExpiresAt     int64   `dynamodbav:"expiresAt"`
}

// ReservationTimer carries the TTL deadline whose deletion triggers safe hold release.
type ReservationTimer struct {
	Item
	ReservationTimerKeys
	ReservationID string  `dynamodbav:"reservationId"`
	PaymentID     string  `dynamodbav:"paymentId"`
	Amount        float64 `dynamodbav:"amount"`
	AmountExact   string  `dynamodbav:"-"`
	Status        string  `dynamodbav:"status"`
	CreatedAtUTC  string  `dynamodbav:"createdAtUtc"`
	ExpiresAt     int64   `dynamodbav:"expiresAt"`
	TTL           int64   `dynamodbav:"ttl"`
}

// LedgerEntry records one immutable debit and its exact posted balance after settlement.
type LedgerEntry struct {
	Item
	LedgerKeys
	EntryID      string                `dynamodbav:"ledgerEntryId"`
	PaymentID    string                `dynamodbav:"paymentId"`
	EntryType    string                `dynamodbav:"entryType"`
	Amount       float64               `dynamodbav:"amount"`
	AmountExact  string                `dynamodbav:"-"`
	BalanceAfter attributevalue.Number `dynamodbav:"balanceAfter"`
	CreatedAtUTC string                `dynamodbav:"createdAtUtc"`
}
