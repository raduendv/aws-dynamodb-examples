// This suite verifies exact intent hashing, missing-debtor rejection, persisted
// decimal responses, and independent progress during blocked persistence calls.
package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"instantpayments/pkg/dto"
)

// preciseInput decodes a real numeric JSON amount without losing its spelling.
func preciseInput(t *testing.T, key, amount string) *dto.OutboundPaymentInput {
	t.Helper()
	payload := `{"idempotencyKey":"` + key + `","merchantId":"merchant","debtorAccountId":"acc_usd_1","creditorIban":"fixture","creditorName":"fixture","currency":"USD","amount":` + amount + `}`
	var input dto.OutboundPaymentInput
	if err := json.Unmarshal([]byte(payload), &input); err != nil {
		t.Fatal(err)
	}
	return &input
}

func TestCreateDistinguishesExactDecimalsAndNormalizesReplay(t *testing.T) {
	payment := newTestPayment()
	first, status, err := payment.Create(preciseInput(t, "decimal", "1.0000000000000001"))
	if err != nil || status != 201 {
		t.Fatalf("create: %#v %d %v", first, status, err)
	}
	replay, status, err := payment.Create(preciseInput(t, "decimal", "1.000000000000000100e0"))
	if err != nil || status != 200 || replay.PaymentID != first.PaymentID {
		t.Fatalf("normalized replay: %#v %d %v", replay, status, err)
	}
	_, _, err = payment.Create(preciseInput(t, "decimal", "1"))
	requireDomainError(t, err, 409, "IDEMPOTENCY_CONFLICT")
	tracked, err := payment.Get(first.PaymentID)
	if err != nil {
		t.Fatal(err)
	}
	amount, err := json.Marshal(tracked["amount"])
	if err != nil || string(amount) != "1.0000000000000001" {
		t.Fatalf("accepted intent must remain exact: %s %v", amount, err)
	}
}

func TestPersistedMissingDebtorRejectsWithoutMoneyWrites(t *testing.T) {
	writes := 0
	payment := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
		switch target {
		case "Query":
			return 200, paymentPartition(t, "RECEIVED", "")
		case "GetItem":
			return 200, map[string]any{}
		case "TransactWriteItems":
			writes++
			items := body["TransactItems"].([]any)
			if len(items) != 2 {
				t.Fatal("missing debtor must not reserve or debit funds")
			}
			update := items[0].(map[string]any)["Update"].(map[string]any)
			values := update["ExpressionAttributeValues"].(map[string]any)
			if values[":reason"].(map[string]any)["S"] != "ACCOUNT_NOT_FOUND" {
				t.Fatalf("wrong rejection: %#v", values)
			}
			return 200, map[string]any{}
		default:
			t.Fatalf("unexpected operation %s", target)
			return 500, nil
		}
	})
	result, err := payment.Process("pay_1")
	if err != nil || writes != 1 || result["state"] != "REJECTED" || result["reasonCode"] != "ACCOUNT_NOT_FOUND" {
		t.Fatalf("missing debtor outcome: %#v %v writes=%d", result, err, writes)
	}
}

func TestAccountResponsePreservesExactStoredBalances(t *testing.T) {
	payment := persistedPaymentService(t, func(target string, _ map[string]any) (int, any) {
		if target == "Query" {
			return 200, map[string]any{}
		}
		return 200, map[string]any{"Item": map[string]any{
			"PK": map[string]string{"S": "ACCOUNT#acc_usd_1"}, "SK": map[string]string{"S": "ACCOUNT#acc_usd_1"},
			"currentBalance":   map[string]string{"N": "1.0000000000000001"},
			"availableBalance": map[string]string{"N": "0.9999999999999999"},
		}}
	})
	result, err := payment.Account("acc_usd_1")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(encoded), `"currentBalance":1.0000000000000001`) ||
		!strings.Contains(string(encoded), `"availableBalance":0.9999999999999999`) {
		t.Fatalf("balances lost precision: %s %v", encoded, err)
	}
}

func TestIndependentPersistentReadsDoNotWaitForPaymentIO(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	payment := persistedPaymentService(t, func(target string, body map[string]any) (int, any) {
		if target == "Query" && body["KeyConditionExpression"] == "PK = :pk" {
			close(started)
			<-release
			return 200, paymentPartition(t, "COMPLETED", "")
		}
		if target == "GetItem" {
			return 200, accountItem(t, 1000, 1)
		}
		return 200, map[string]any{}
	})
	done := make(chan error, 1)
	go func() {
		_, err := payment.Get("pay_1")
		done <- err
	}()
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("payment read did not start")
	}
	accountDone := make(chan error, 1)
	go func() {
		_, err := payment.Account("acc_usd_1")
		accountDone <- err
	}()
	select {
	case err := <-accountDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated account read blocked behind payment I/O")
	}
	if len(payment.payments) != 0 {
		t.Fatal("persistent requests must not publish shared mutable cache rows")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := payment.ProcessContext(ctx, "pay_1"); err == nil {
		t.Fatal("persistent processor must still honor cancellation")
	}
}
