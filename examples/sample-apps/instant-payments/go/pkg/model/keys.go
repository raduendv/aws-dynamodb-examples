package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	PaymentPrefix          = "PAYMENT#"
	AccountPrefix          = "ACCOUNT#"
	IdempotencyPrefix      = "IDEMPOTENCY#"
	ReservationPrefix      = "RESERVATION#"
	ReservationTimerPrefix = "RESERVATION_TEMP#"
	EventPrefix            = "EVENT#"
	LedgerPrefix           = "LEDGER#"
	PaymentHeadSK          = "#HEAD"
	IdempotencySK          = "IDEMPOTENCY"
	// Fixed precision keeps physical timestamp keys chronological across whole seconds.
	KeyTimestampLayout = "2006-01-02T15:04:05.000000000Z"
)

// PaymentKey holds a logical payment ID and adds PAYMENT# only at the SDK boundary.
type PaymentKey string

// AccountKey holds a logical account ID shared by account and hold partitions.
type AccountKey string

// IdempotencyKey holds the caller's logical intent key.
type IdempotencyKey string

// ReservationKey identifies a durable audit hold within an account partition.
type ReservationKey string

// ReservationTimerKey identifies an expiring hold timer within an account partition.
type ReservationTimerKey string

// EventKey is a positive lifecycle sequence encoded with nineteen decimal digits.
type EventKey int64

// LedgerKey preserves an existing physical timestamp spelling while its instant
// is unchanged. New or changed instants use fixed-width chronological UTC keys.
type LedgerKey struct {
	CreatedAt time.Time
	EntryID   string

	originalTimestamp string
	originalCreatedAt time.Time
}

// marshalPrefixed encodes a nonempty logical identifier into its physical namespace.
func marshalPrefixed(prefix, value string) (types.AttributeValue, error) {
	if value == "" {
		return nil, fmt.Errorf("%s key requires a nonempty value", prefix)
	}
	return &types.AttributeValueMemberS{Value: prefix + value}, nil
}

// unmarshalPrefixed rejects wrong attribute types, namespaces and empty identifiers.
func unmarshalPrefixed(attribute types.AttributeValue, prefix string) (string, error) {
	value, ok := attribute.(*types.AttributeValueMemberS)
	if !ok || value == nil || !strings.HasPrefix(value.Value, prefix) || len(value.Value) == len(prefix) {
		return "", fmt.Errorf("expected a nonempty %s string key", prefix)
	}
	return strings.TrimPrefix(value.Value, prefix), nil
}

// MarshalDynamoDBAttributeValue writes a nonempty PAYMENT# key.
func (key PaymentKey) MarshalDynamoDBAttributeValue() (types.AttributeValue, error) {
	return marshalPrefixed(PaymentPrefix, string(key))
}

// UnmarshalDynamoDBAttributeValue validates the namespace and restores the logical payment ID.
func (key *PaymentKey) UnmarshalDynamoDBAttributeValue(value types.AttributeValue) error {
	decoded, err := unmarshalPrefixed(value, PaymentPrefix)
	if err == nil {
		*key = PaymentKey(decoded)
	}
	return err
}

// MarshalDynamoDBAttributeValue writes a nonempty ACCOUNT# key.
func (key AccountKey) MarshalDynamoDBAttributeValue() (types.AttributeValue, error) {
	return marshalPrefixed(AccountPrefix, string(key))
}

// UnmarshalDynamoDBAttributeValue validates the namespace and restores the logical account ID.
func (key *AccountKey) UnmarshalDynamoDBAttributeValue(value types.AttributeValue) error {
	decoded, err := unmarshalPrefixed(value, AccountPrefix)
	if err == nil {
		*key = AccountKey(decoded)
	}
	return err
}

// MarshalDynamoDBAttributeValue writes a nonempty IDEMPOTENCY# key.
func (key IdempotencyKey) MarshalDynamoDBAttributeValue() (types.AttributeValue, error) {
	return marshalPrefixed(IdempotencyPrefix, string(key))
}

// UnmarshalDynamoDBAttributeValue validates and restores the caller's logical intent key.
func (key *IdempotencyKey) UnmarshalDynamoDBAttributeValue(value types.AttributeValue) error {
	decoded, err := unmarshalPrefixed(value, IdempotencyPrefix)
	if err == nil {
		*key = IdempotencyKey(decoded)
	}
	return err
}

// MarshalDynamoDBAttributeValue writes a nonempty durable RESERVATION# key.
func (key ReservationKey) MarshalDynamoDBAttributeValue() (types.AttributeValue, error) {
	return marshalPrefixed(ReservationPrefix, string(key))
}

// UnmarshalDynamoDBAttributeValue rejects timer keys and restores the logical reservation ID.
func (key *ReservationKey) UnmarshalDynamoDBAttributeValue(value types.AttributeValue) error {
	decoded, err := unmarshalPrefixed(value, ReservationPrefix)
	if err == nil {
		*key = ReservationKey(decoded)
	}
	return err
}

// MarshalDynamoDBAttributeValue writes a nonempty RESERVATION_TEMP# key.
func (key ReservationTimerKey) MarshalDynamoDBAttributeValue() (types.AttributeValue, error) {
	return marshalPrefixed(ReservationTimerPrefix, string(key))
}

// UnmarshalDynamoDBAttributeValue validates the timer namespace for stream-driven release.
func (key *ReservationTimerKey) UnmarshalDynamoDBAttributeValue(value types.AttributeValue) error {
	decoded, err := unmarshalPrefixed(value, ReservationTimerPrefix)
	if err == nil {
		*key = ReservationTimerKey(decoded)
	}
	return err
}

// String formats the physical event key with lexical sequence ordering.
func (key EventKey) String() string { return fmt.Sprintf("%s%019d", EventPrefix, key) }

// MarshalDynamoDBAttributeValue rejects nonpositive sequences before storing an event key.
func (key EventKey) MarshalDynamoDBAttributeValue() (types.AttributeValue, error) {
	if key <= 0 {
		return nil, fmt.Errorf("event sequence must be positive")
	}
	return &types.AttributeValueMemberS{Value: key.String()}, nil
}

// UnmarshalDynamoDBAttributeValue requires the positive canonical nineteen-digit encoding.
func (key *EventKey) UnmarshalDynamoDBAttributeValue(value types.AttributeValue) error {
	suffix, err := unmarshalPrefixed(value, EventPrefix)
	if err != nil {
		return err
	}
	sequence, err := strconv.ParseInt(suffix, 10, 64)
	if err != nil || sequence <= 0 || EventKey(sequence).String() != EventPrefix+suffix {
		return fmt.Errorf("expected a positive 19-digit event sequence")
	}
	*key = EventKey(sequence)
	return nil
}

// MarshalDynamoDBAttributeValue encodes a ledger identity without silently migrating existing keys.
func (key LedgerKey) MarshalDynamoDBAttributeValue() (types.AttributeValue, error) {
	if key.CreatedAt.IsZero() || key.EntryID == "" || strings.Contains(key.EntryID, "#") {
		return nil, fmt.Errorf("ledger key requires a timestamp and entry ID")
	}
	timestamp := key.CreatedAt.UTC().Format(KeyTimestampLayout)
	if key.originalTimestamp != "" && key.CreatedAt.Equal(key.originalCreatedAt) {
		timestamp = key.originalTimestamp
	}
	return marshalPrefixed(LedgerPrefix, timestamp+"#"+key.EntryID)
}

// UnmarshalDynamoDBAttributeValue validates the timestamp and entry ID and retains legacy spelling.
func (key *LedgerKey) UnmarshalDynamoDBAttributeValue(value types.AttributeValue) error {
	suffix, err := unmarshalPrefixed(value, LedgerPrefix)
	if err != nil {
		return err
	}
	timestamp, entryID, ok := strings.Cut(suffix, "#")
	created, err := time.Parse(time.RFC3339Nano, timestamp)
	if !ok || err != nil || created.IsZero() || entryID == "" || strings.Contains(entryID, "#") {
		return fmt.Errorf("expected a ledger timestamp and entry ID")
	}
	*key = LedgerKey{CreatedAt: created.UTC(), EntryID: entryID}
	// Legacy timestamp spellings are part of the physical primary key.
	if timestamp != key.CreatedAt.Format(KeyTimestampLayout) {
		key.originalTimestamp = timestamp
		key.originalCreatedAt = key.CreatedAt
	}
	return nil
}

// PaymentKeys addresses the head or another fixed sort key in a payment partition.
type PaymentKeys struct {
	PK PaymentKey `dynamodbav:"PK"`
	SK string     `dynamodbav:"SK"`
}

// AccountKeys addresses the account row using the same logical ID in both keys.
type AccountKeys struct {
	PK AccountKey `dynamodbav:"PK"`
	SK AccountKey `dynamodbav:"SK"`
}

// PaymentEventKeys addresses an immutable ordered lifecycle event.
type PaymentEventKeys struct {
	PK PaymentKey `dynamodbav:"PK"`
	SK EventKey   `dynamodbav:"SK"`
}

// IdempotencyKeys addresses the durable binding under its fixed sort key.
type IdempotencyKeys struct {
	PK IdempotencyKey `dynamodbav:"PK"`
	SK string         `dynamodbav:"SK"`
}

// ReservationKeys addresses a durable hold in its debtor's partition.
type ReservationKeys struct {
	PK AccountKey     `dynamodbav:"PK"`
	SK ReservationKey `dynamodbav:"SK"`
}

// ReservationTimerKeys addresses the TTL timer deleted on expiry or completion.
type ReservationTimerKeys struct {
	PK AccountKey          `dynamodbav:"PK"`
	SK ReservationTimerKey `dynamodbav:"SK"`
}

// LedgerKeys addresses an immutable debit using the payment's creation timestamp.
type LedgerKeys struct {
	PK AccountKey `dynamodbav:"PK"`
	SK LedgerKey  `dynamodbav:"SK"`
}
