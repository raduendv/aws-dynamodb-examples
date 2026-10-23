package job

import (
	"context"
	"fmt"
	"strings"

	"instantpayments/pkg/model"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/zap"
)

// IdentifyJob routes payment creation events and expired reservation timers.
type IdentifyJob struct {
	Record types.Record
	Result chan error
}

// Complete returns the processing result to the stream poller. Result must have
// capacity for one acknowledgment so forced poller shutdown cannot block a worker.
func (i *IdentifyJob) Complete(err error) {
	if i.Result != nil {
		i.Result <- err
	}
}

// Run ignores unrelated records and routes creation or timer-removal events to
// the payment service. Invalid keys, missing dependencies, and service failures
// are returned to the poller for its bounded retry policy.
func (i *IdentifyJob) Run(ctx context.Context) error {
	log, ok := ctx.Value(ContextKeyLogger{}).(*zap.Logger)
	if !ok || log == nil {
		return fmt.Errorf("logger context missing or invalid")
	}
	if i.Record.Dynamodb == nil {
		return nil
	}

	keys := i.Record.Dynamodb.Keys
	sk, _ := stringAttribute(keys["SK"])
	if i.Record.EventName == types.OperationTypeRemove {
		if !strings.HasPrefix(sk, model.ReservationTimerPrefix) {
			return nil
		}
		converted, err := attributevalue.FromDynamoDBStreamsMap(keys)
		if err != nil {
			return fmt.Errorf("convert reservation timer key: %w", err)
		}
		var timer model.ReservationTimerKeys
		if err := attributevalue.UnmarshalMap(converted, &timer); err != nil {
			return fmt.Errorf("decode reservation timer key: %w", err)
		}
		if timer.PK == "" || timer.SK == "" {
			return fmt.Errorf("reservation timer key is incomplete")
		}
		processor, ok := ctx.Value(ContextKeyPaymentProcessor{}).(PaymentProcessor)
		if !ok || processor == nil {
			return fmt.Errorf("payment processor context missing or invalid")
		}
		return processor.ReleaseReservation(ctx, string(timer.PK), string(timer.SK))
	}

	if i.Record.EventName != types.OperationTypeInsert {
		return nil
	}
	image := i.Record.Dynamodb.NewImage
	entityType, _ := stringAttribute(image["entityType"])
	eventType, _ := stringAttribute(image["eventType"])
	if entityType != "PAYMENT_EVENT" || eventType != "OUTBOUND_PAYMENT_CREATED" {
		return nil
	}
	converted, err := attributevalue.FromDynamoDBStreamsMap(keys)
	if err != nil {
		return fmt.Errorf("convert payment event key: %w", err)
	}
	var event model.PaymentEventKeys
	if err := attributevalue.UnmarshalMap(converted, &event); err != nil {
		return fmt.Errorf("decode payment event key: %w", err)
	}
	paymentID, _ := stringAttribute(image["paymentId"])
	if paymentID == "" || paymentID != string(event.PK) || event.SK <= 0 {
		return fmt.Errorf("payment creation event has missing or inconsistent keys/paymentId")
	}
	return (&PaymentJob{PaymentID: paymentID}).Run(ctx)
}

// stringAttribute extracts a nonempty string attribute without coercion.
func stringAttribute(value types.AttributeValue) (string, bool) {
	attribute, ok := value.(*types.AttributeValueMemberS)
	if !ok || attribute.Value == "" {
		return "", false
	}
	return attribute.Value, true
}
