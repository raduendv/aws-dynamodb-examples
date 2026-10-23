package aws

import (
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"instantpayments/pkg/model"
)

// marshalItem shares value timestamp formatting without changing typed physical
// keys or the SDK's unixtime handling for numeric TTL attributes.
func marshalItem(value any) (map[string]types.AttributeValue, error) {
	item, err := attributevalue.MarshalMapWithOptions(value, func(options *attributevalue.EncoderOptions) {
		options.EncodeTime = func(value time.Time) (types.AttributeValue, error) {
			return &types.AttributeValueMemberS{Value: model.FormatTimestamp(value)}, nil
		}
	})
	if err != nil {
		return nil, err
	}
	var exact string
	switch value := value.(type) {
	case *model.Payment:
		exact = value.AmountExact
	case *model.PaymentEvent:
		exact = value.AmountExact
	case model.PaymentEvent:
		exact = value.AmountExact
	case model.Reservation:
		exact = value.AmountExact
	case model.ReservationTimer:
		exact = value.AmountExact
	case model.LedgerEntry:
		exact = value.AmountExact
	}
	if exact != "" {
		normalized, err := model.DecimalString(exact)
		if err != nil {
			return nil, fmt.Errorf("marshal exact amount: %w", err)
		}
		item["amount"] = &types.AttributeValueMemberN{Value: normalized}
	}
	return item, nil
}

// storedDecimal returns the original numeric attribute for exact arithmetic and JSON output.
func storedDecimal(item map[string]types.AttributeValue, name string) string {
	if number, ok := item[name].(*types.AttributeValueMemberN); ok {
		return number.Value
	}
	return ""
}
