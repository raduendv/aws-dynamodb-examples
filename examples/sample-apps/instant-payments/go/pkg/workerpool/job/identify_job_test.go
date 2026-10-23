// This suite verifies creation-event and reservation-timer routing, unrelated-record
// filtering, and malformed encoded-key rejection.
package job

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"go.uber.org/zap"
)

// testPaymentProcessor records routed identifiers and returns controlled processing or release failures.
type testPaymentProcessor struct {
	processed  []string
	released   [][2]string
	processErr error
	releaseErr error
}

// ProcessContext records the payment identifier and returns the configured error.
func (p *testPaymentProcessor) ProcessContext(_ context.Context, id string) (map[string]any, error) {
	p.processed = append(p.processed, id)
	return nil, p.processErr
}

// ReleaseReservation records the account/reservation pair and returns the configured error.
func (p *testPaymentProcessor) ReleaseReservation(_ context.Context, accountID, reservationID string) error {
	p.released = append(p.released, [2]string{accountID, reservationID})
	return p.releaseErr
}

// processorContext supplies the worker logger and processor needed to run a routed job.
func processorContext(processor PaymentProcessor) context.Context {
	ctx := context.WithValue(context.Background(), ContextKeyLogger{}, zap.NewNop())
	return context.WithValue(ctx, ContextKeyPaymentProcessor{}, processor)
}

func TestIdentifyJobProcessesPaymentCreationEvent(t *testing.T) {
	processor := &testPaymentProcessor{}
	job := IdentifyJob{Record: types.Record{
		EventName: types.OperationTypeInsert,
		Dynamodb: &types.StreamRecord{
			Keys: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: "PAYMENT#pay_123"},
				"SK": &types.AttributeValueMemberS{Value: "EVENT#0000000000000000001"},
			},
			NewImage: map[string]types.AttributeValue{
				"entityType": &types.AttributeValueMemberS{Value: "PAYMENT_EVENT"},
				"eventType":  &types.AttributeValueMemberS{Value: "OUTBOUND_PAYMENT_CREATED"},
				"paymentId":  &types.AttributeValueMemberS{Value: "pay_123"},
			},
		},
	}}
	if err := job.Run(processorContext(processor)); err != nil {
		t.Fatal(err)
	}
	if len(processor.processed) != 1 || processor.processed[0] != "pay_123" {
		t.Fatalf("unexpected processed payment ids: %v", processor.processed)
	}
}

func TestIdentifyJobReleasesExpiredReservationTimer(t *testing.T) {
	processor := &testPaymentProcessor{}
	job := IdentifyJob{Record: types.Record{
		EventName: types.OperationTypeRemove,
		Dynamodb: &types.StreamRecord{
			Keys: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: "ACCOUNT#acc_1"},
				"SK": &types.AttributeValueMemberS{Value: "RESERVATION_TEMP#res_1"},
			},
		},
	}}
	if err := job.Run(processorContext(processor)); err != nil {
		t.Fatal(err)
	}
	if len(processor.released) != 1 || processor.released[0] != [2]string{"acc_1", "res_1"} {
		t.Fatalf("unexpected released reservations: %v", processor.released)
	}
}

func TestIdentifyJobIgnoresUnrelatedRecords(t *testing.T) {
	processor := &testPaymentProcessor{}
	records := []types.Record{
		{EventName: types.OperationTypeRemove, Dynamodb: &types.StreamRecord{
			Keys: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: "ACCOUNT#acc_1"},
				"SK": &types.AttributeValueMemberS{Value: "RESERVATION#res_1"},
			},
		}},
		{EventName: types.OperationTypeInsert, Dynamodb: &types.StreamRecord{
			Keys: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: "PAYMENT#pay_1"},
				"SK": &types.AttributeValueMemberS{Value: "EVENT#0000000000000000002"},
			},
			NewImage: map[string]types.AttributeValue{
				"entityType": &types.AttributeValueMemberS{Value: "PAYMENT_EVENT"},
				"eventType":  &types.AttributeValueMemberS{Value: "COMPLETED"},
			},
		}},
	}

	for _, record := range records {
		if err := (&IdentifyJob{Record: record}).Run(processorContext(processor)); err != nil {
			t.Fatal(err)
		}
	}
	if len(processor.processed) != 0 || len(processor.released) != 0 {
		t.Fatalf("unrelated records were processed: payments=%v reservations=%v", processor.processed, processor.released)
	}
}

func TestIdentifyJobRejectsMalformedEncodedKeys(t *testing.T) {
	for _, test := range []struct {
		pk, sk    string
		operation types.OperationType
	}{
		{"ACCOUNT#", "RESERVATION_TEMP#res_1", types.OperationTypeRemove},
		{"PAYMENT#pay_1", "RESERVATION_TEMP#res_1", types.OperationTypeRemove},
		{"ACCOUNT#acc_1", "RESERVATION_TEMP#", types.OperationTypeRemove},
		{"", "RESERVATION_TEMP#res_1", types.OperationTypeRemove},
		{"PAYMENT#", "EVENT#0000000000000000001", types.OperationTypeInsert},
		{"PAYMENT#pay_1", "EVENT#1", types.OperationTypeInsert},
		{"PAYMENT#pay_2", "EVENT#0000000000000000001", types.OperationTypeInsert},
	} {
		t.Run(test.pk+"/"+test.sk, func(t *testing.T) {
			processor := &testPaymentProcessor{}
			job := IdentifyJob{Record: types.Record{
				EventName: test.operation,
				Dynamodb: &types.StreamRecord{
					Keys: map[string]types.AttributeValue{
						"PK": &types.AttributeValueMemberS{Value: test.pk},
						"SK": &types.AttributeValueMemberS{Value: test.sk},
					},
					NewImage: map[string]types.AttributeValue{
						"entityType": &types.AttributeValueMemberS{Value: "PAYMENT_EVENT"},
						"eventType":  &types.AttributeValueMemberS{Value: "OUTBOUND_PAYMENT_CREATED"},
						"paymentId":  &types.AttributeValueMemberS{Value: "pay_1"},
					},
				},
			}}
			if err := job.Run(processorContext(processor)); err == nil {
				t.Fatal("malformed stream key was silently accepted")
			}
			if len(processor.processed) != 0 || len(processor.released) != 0 {
				t.Fatal("processor called for a malformed stream key")
			}
		})
	}
}
