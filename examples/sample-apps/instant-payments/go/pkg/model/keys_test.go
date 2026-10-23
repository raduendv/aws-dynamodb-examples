// This suite verifies key codecs, malformed-key rejection, chronological ordering,
// typed item encoding, and legacy ledger-key identity preservation.
package model

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestNewLedgerKeysPreserveChronologicalOrderAcrossPrecisionBoundaries(t *testing.T) {
	var previous string
	for _, test := range []struct{ timestamp, want string }{
		{"2026-01-01T00:00:00Z", "2026-01-01T00:00:00.000000000Z"},
		{"2026-01-01T00:00:00.001Z", "2026-01-01T00:00:00.001000000Z"},
		{"2026-01-01T00:00:00.001001Z", "2026-01-01T00:00:00.001001000Z"},
		{"2026-01-01T01:00:00.001001001+01:00", "2026-01-01T00:00:00.001001001Z"},
		{"2026-01-01T00:00:01Z", "2026-01-01T00:00:01.000000000Z"},
	} {
		created, err := time.Parse(time.RFC3339Nano, test.timestamp)
		if err != nil {
			t.Fatal(err)
		}
		attribute, err := attributevalue.Marshal(LedgerKey{CreatedAt: created, EntryID: "led_1"})
		if err != nil {
			t.Fatal(err)
		}
		got := attribute.(*types.AttributeValueMemberS).Value
		if want := LedgerPrefix + test.want + "#led_1"; got != want {
			t.Fatalf("ledger key = %q, want %q", got, want)
		}
		if previous != "" && previous >= got {
			t.Fatalf("ledger keys are not chronological: %q >= %q", previous, got)
		}
		previous = got
	}
}

func TestKeyCodecsRoundTrip(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)
	for _, test := range []struct {
		name, wire string
		value      any
	}{
		{"payment", "PAYMENT#pay_123", PaymentKey("pay_123")},
		{"account", "ACCOUNT#acc_123", AccountKey("acc_123")},
		{"idempotency", "IDEMPOTENCY#IDEMPOTENCY#raw", IdempotencyKey("IDEMPOTENCY#raw")},
		{"reservation", "RESERVATION#res_pay_123", ReservationKey("res_pay_123")},
		{"timer", "RESERVATION_TEMP#res_pay_123", ReservationTimerKey("res_pay_123")},
		{"event", "EVENT#0000000000000000123", EventKey(123)},
		{"ledger", "LEDGER#2026-01-02T03:04:05.000000123Z#led_pay_123", LedgerKey{CreatedAt: created, EntryID: "led_pay_123"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := attributevalue.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := encoded.(*types.AttributeValueMemberS)
			if !ok || got.Value != test.wire {
				t.Fatalf("encoded = %#v, want %q", encoded, test.wire)
			}
			decoded := reflect.New(reflect.TypeOf(test.value))
			if err := attributevalue.Unmarshal(encoded, decoded.Interface()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Elem().Interface(), test.value) {
				t.Fatalf("decoded = %#v, want %#v", decoded.Elem().Interface(), test.value)
			}
		})
	}
}

func TestKeyCodecsRejectInvalidAttributes(t *testing.T) {
	for _, test := range []struct {
		name, prefix string
		target       attributevalue.Unmarshaler
	}{
		{"payment", PaymentPrefix, new(PaymentKey)},
		{"account", AccountPrefix, new(AccountKey)},
		{"idempotency", IdempotencyPrefix, new(IdempotencyKey)},
		{"reservation", ReservationPrefix, new(ReservationKey)},
		{"timer", ReservationTimerPrefix, new(ReservationTimerKey)},
		{"event", EventPrefix, new(EventKey)},
		{"ledger", LedgerPrefix, new(LedgerKey)},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, value := range []types.AttributeValue{
				nil, (*types.AttributeValueMemberS)(nil),
				&types.AttributeValueMemberN{Value: "1"},
				&types.AttributeValueMemberNULL{Value: true},
				&types.AttributeValueMemberS{Value: ""},
				&types.AttributeValueMemberS{Value: test.prefix},
				&types.AttributeValueMemberS{Value: "WRONG#id"},
			} {
				if err := test.target.UnmarshalDynamoDBAttributeValue(value); err == nil {
					t.Fatalf("accepted %#v", value)
				}
			}
		})
	}
	for _, value := range []any{PaymentKey(""), AccountKey(""), IdempotencyKey(""), ReservationKey(""), ReservationTimerKey(""), EventKey(0), EventKey(-1), LedgerKey{}, LedgerKey{CreatedAt: time.Now(), EntryID: "bad#id"}} {
		if _, err := attributevalue.Marshal(value); err == nil {
			t.Fatalf("accepted invalid key %#v", value)
		}
	}
}

func TestStructuredKeyValidation(t *testing.T) {
	for _, suffix := range []string{"1", "0000000000000000000", "-000000000000000001", "+000000000000000001", "9223372036854775808", "000000000000000000x"} {
		var key EventKey
		if err := attributevalue.Unmarshal(&types.AttributeValueMemberS{Value: EventPrefix + suffix}, &key); err == nil {
			t.Fatalf("accepted event suffix %q", suffix)
		}
	}
	for _, suffix := range []string{"bad#led_1", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z#", "2026-01-01T00:00:00Z#led_1#extra"} {
		var key LedgerKey
		if err := attributevalue.Unmarshal(&types.AttributeValueMemberS{Value: LedgerPrefix + suffix}, &key); err == nil {
			t.Fatalf("accepted ledger suffix %q", suffix)
		}
	}
	// Existing ledger rows may contain variable-width RFC3339Nano timestamps.
	var legacy LedgerKey
	if err := attributevalue.Unmarshal(&types.AttributeValueMemberS{Value: "LEDGER#2026-01-01T01:00:00.1+01:00#led_1"}, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.CreatedAt.Format(time.RFC3339Nano) != "2026-01-01T00:00:00.1Z" {
		t.Fatalf("legacy timestamp = %s", legacy.CreatedAt)
	}
	for _, pair := range [][2]EventKey{{1, 2}, {9, 10}, {99, 100}, {999999, 1000000}} {
		if strings.Compare(pair[0].String(), pair[1].String()) >= 0 {
			t.Fatalf("event keys do not sort numerically: %v", pair)
		}
	}
}

func TestTypedItemKeysUseCodecs(t *testing.T) {
	for _, test := range []struct {
		item   any
		pk, sk string
	}{
		{Payment{PaymentKeys: PaymentKeys{PK: "pay_1", SK: PaymentHeadSK}}, "PAYMENT#pay_1", "#HEAD"},
		{Account{AccountKeys: AccountKeys{PK: "acc_1", SK: "acc_1"}}, "ACCOUNT#acc_1", "ACCOUNT#acc_1"},
		{PaymentEvent{PaymentEventKeys: PaymentEventKeys{PK: "pay_1", SK: 1}}, "PAYMENT#pay_1", "EVENT#0000000000000000001"},
		{IdempotencyRecord{IdempotencyKeys: IdempotencyKeys{PK: "key", SK: IdempotencySK}}, "IDEMPOTENCY#key", "IDEMPOTENCY"},
		{Reservation{ReservationKeys: ReservationKeys{PK: "acc_1", SK: "res_1"}}, "ACCOUNT#acc_1", "RESERVATION#res_1"},
		{ReservationTimer{ReservationTimerKeys: ReservationTimerKeys{PK: "acc_1", SK: "res_1"}}, "ACCOUNT#acc_1", "RESERVATION_TEMP#res_1"},
		{LedgerEntry{LedgerKeys: LedgerKeys{PK: "acc_1", SK: LedgerKey{CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EntryID: "led_1"}}}, "ACCOUNT#acc_1", "LEDGER#2026-01-01T00:00:00.000000000Z#led_1"},
	} {
		item, err := attributevalue.MarshalMap(test.item)
		if err != nil {
			t.Fatal(err)
		}

		if item["PK"].(*types.AttributeValueMemberS).Value != test.pk || item["SK"].(*types.AttributeValueMemberS).Value != test.sk {
			t.Fatalf("%T keys = %#v", test.item, item)
		}
		decoded := reflect.New(reflect.TypeOf(test.item))
		if err := attributevalue.UnmarshalMap(item, decoded.Interface()); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"PK", "SK"} {
			got := decoded.Elem().FieldByName(field).Interface()
			want := reflect.ValueOf(test.item).FieldByName(field).Interface()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%T %s round trip = %#v, want %#v", test.item, field, got, want)
			}
		}
	}
}

func TestLedgerKeyPreservesPhysicalIdentity(t *testing.T) {
	for _, timestamp := range []string{
		"2026-01-01T00:00:00Z",
		"2026-01-01T00:00:00.1Z",
		"2026-01-01T00:00:00.100Z",
		"2026-01-01T00:00:00.123456789Z",
		"2026-01-01T01:00:00.1+01:00",
		"2025-12-31T19:00:00.100000000-05:00",
	} {
		t.Run(timestamp, func(t *testing.T) {
			wire := LedgerPrefix + timestamp + "#led_1"
			attribute := &types.AttributeValueMemberS{Value: wire}
			var key LedgerKey
			if err := attributevalue.Unmarshal(attribute, &key); err != nil {
				t.Fatal(err)
			}
			encoded, err := attributevalue.Marshal(key)
			if err != nil {
				t.Fatal(err)
			}
			if got := encoded.(*types.AttributeValueMemberS).Value; got != wire {
				t.Fatalf("physical key changed: got %q, want %q", got, wire)
			}
			var entry LedgerEntry
			if err := attributevalue.UnmarshalMap(map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: "ACCOUNT#acc_1"}, "SK": attribute,
			}, &entry); err != nil {
				t.Fatal(err)
			}
			item, err := attributevalue.MarshalMap(entry)
			if err != nil {
				t.Fatal(err)
			}
			if got := item["SK"].(*types.AttributeValueMemberS).Value; got != wire {
				t.Fatalf("ledger row key changed: got %q, want %q", got, wire)
			}

			key.EntryID = "led_2"
			encoded, err = attributevalue.Marshal(key)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := encoded.(*types.AttributeValueMemberS).Value, LedgerPrefix+timestamp+"#led_2"; got != want {
				t.Fatalf("entry ID change lost timestamp encoding: got %q, want %q", got, want)
			}
			key.CreatedAt = key.CreatedAt.Add(time.Second)
			encoded, err = attributevalue.Marshal(key)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := encoded.(*types.AttributeValueMemberS).Value, LedgerPrefix+key.CreatedAt.UTC().Format(KeyTimestampLayout)+"#led_2"; got != want {
				t.Fatalf("changed timestamp reused stale encoding: got %q, want %q", got, want)
			}
			// Reusing a destination must clear any previous legacy encoding.
			canonical := "LEDGER#2026-02-01T00:00:00.000000000Z#led_3"
			if err := attributevalue.Unmarshal(&types.AttributeValueMemberS{Value: canonical}, &key); err != nil {
				t.Fatal(err)
			}
			if key.originalTimestamp != "" || !key.originalCreatedAt.IsZero() {
				t.Fatal("canonical decode retained legacy timestamp state")
			}
			encoded, err = attributevalue.Marshal(key)
			if err != nil {
				t.Fatal(err)
			}
			if got := encoded.(*types.AttributeValueMemberS).Value; got != canonical {
				t.Fatalf("reused destination key = %q, want %q", got, canonical)
			}
		})
	}
}
