package service

import (
	"errors"
	"fmt"
	"testing"

	"go.uber.org/zap"

	"instantpayments/pkg/dto"
)

func newTestPayment() *Payment {
	return NewPayment(zap.NewNop(), nil)
}

func validInput(key string) *dto.OutboundPaymentInput {
	return &dto.OutboundPaymentInput{
		IdempotencyKey:  key,
		MerchantID:      "merchant-test",
		DebtorAccountID: "acc_usd_1",
		CreditorIban:    "RO49AAAA1B31007593840000",
		CreditorName:    "Jane Doe",
		Amount:          100,
		Currency:        "USD",
	}
}

func requireDomainError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var domain *DomainError
	if !errors.As(err, &domain) {
		t.Fatalf("expected DomainError, got %v", err)
	}
	if domain.Status != status || domain.Code != code {
		t.Fatalf("expected %d %s, got %d %s", status, code, domain.Status, domain.Code)
	}
}

func TestCreateValidatesInput(t *testing.T) {
	testCases := []struct {
		name  string
		input *dto.OutboundPaymentInput
	}{
		{name: "nil", input: nil},
		{name: "blank required field", input: &dto.OutboundPaymentInput{IdempotencyKey: "key", MerchantID: "merchant", DebtorAccountID: "account", CreditorIban: "iban", CreditorName: ""}},
		{name: "zero amount", input: func() *dto.OutboundPaymentInput { input := validInput("zero"); input.Amount = 0; return input }()},
		{name: "lowercase currency", input: func() *dto.OutboundPaymentInput {
			input := validInput("currency")
			input.Currency = "usd"
			return input
		}()},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := newTestPayment().Create(testCase.input)
			requireDomainError(t, err, 400, "VALIDATION_ERROR")
		})
	}
}

func TestCreateIdempotencyAndConflict(t *testing.T) {
	payment := newTestPayment()
	input := validInput("same-key")

	created, status, err := payment.Create(input)
	if err != nil || status != 201 {
		t.Fatalf("expected first create status 201 without error, got %d, %v", status, err)
	}
	replayed, status, err := payment.Create(input)
	if err != nil || status != 200 {
		t.Fatalf("expected replay status 200 without error, got %d, %v", status, err)
	}
	if created.PaymentID != replayed.PaymentID {
		t.Fatalf("expected replay to preserve payment id, got %v and %v", created.PaymentID, replayed.PaymentID)
	}

	conflict := validInput("same-key")
	conflict.Amount = 101
	_, _, err = payment.Create(conflict)
	requireDomainError(t, err, 409, "IDEMPOTENCY_CONFLICT")
}

func TestProcessCompletedPaymentUpdatesAccountAndReservation(t *testing.T) {
	payment := newTestPayment()
	created, _, err := payment.Create(validInput("complete"))
	if err != nil {
		t.Fatal(err)
	}
	paymentID := created.PaymentID

	processed, err := payment.Process(paymentID)
	if err != nil {
		t.Fatal(err)
	}
	if processed["state"] != "COMPLETED" || processed["reasonCode"] != nil {
		t.Fatalf("expected completed result without reason, got %#v", processed)
	}

	processed, err = payment.Process(paymentID)
	if err != nil || processed["state"] != "COMPLETED" {
		t.Fatalf("expected idempotent completed process, got %#v, %v", processed, err)
	}

	account, err := payment.Account("acc_usd_1")
	if err != nil {
		t.Fatal(err)
	}
	if account["currentBalance"] != float64(9900) {
		t.Fatalf("expected debited balance, got %#v", account["currentBalance"])
	}
	reservations := account["reservations"].([]map[string]any)
	if len(reservations) != 1 || reservations[0]["status"] != "CONSUMED" {
		t.Fatalf("expected one consumed reservation, got %#v", reservations)
	}

	found, err := payment.Batch("acc_usd_1", []string{"res_" + paymentID, "res_" + paymentID, "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found["reservations"].([]any)) != 1 || len(found["missingReservationIds"].([]string)) != 1 {
		t.Fatalf("expected deduped batch result, got %#v", found)
	}
}

func TestProcessRejectsInsufficientFunds(t *testing.T) {
	payment := newTestPayment()
	input := validInput("reject")
	input.DebtorAccountID = "acc_usd_5"
	created, _, err := payment.Create(input)
	if err != nil {
		t.Fatal(err)
	}

	result, err := payment.Process(created.PaymentID)
	if err != nil {
		t.Fatal(err)
	}
	if result["state"] != "REJECTED" || result["reasonCode"] != "INSUFFICIENT_FUNDS" {
		t.Fatalf("expected rejected payment, got %#v", result)
	}
	account, err := payment.Account("acc_usd_5")
	if err != nil {
		t.Fatal(err)
	}
	if account["currentBalance"] != float64(0) || len(account["reservations"].([]map[string]any)) != 0 {
		t.Fatalf("expected untouched rejected account, got %#v", account)
	}
}

func TestGetAndBatchErrors(t *testing.T) {
	payment := newTestPayment()
	_, err := payment.Get("missing")
	requireDomainError(t, err, 404, "PAYMENT_NOT_FOUND")
	_, err = payment.Account("missing")
	requireDomainError(t, err, 404, "ACCOUNT_NOT_FOUND")
	_, err = payment.Batch("acc_usd_1", nil)
	requireDomainError(t, err, 400, "VALIDATION_ERROR")
	_, err = payment.Batch("acc_usd_1", []string{"  "})
	requireDomainError(t, err, 400, "VALIDATION_ERROR")
}

func TestBatchRejectsMalformedReservationRecord(t *testing.T) {
	payment := newTestPayment()
	payment.accounts["acc_usd_1"].Rows = []map[string]any{{"reservationId": 1}}
	_, err := payment.Batch("acc_usd_1", []string{"res_1"})
	if err == nil || err.Error() != "invalid reservation record" {
		t.Fatalf("expected malformed reservation error, got %v", err)
	}
}

func TestListPaginatesAndValidatesTokens(t *testing.T) {
	payment := newTestPayment()
	for index := 0; index < 3; index++ {
		input := validInput(fmt.Sprintf("list-%d", index))
		input.MerchantID = "merchant-list"
		created, _, err := payment.Create(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := payment.Process(created.PaymentID); err != nil {
			t.Fatal(err)
		}
	}

	first, err := payment.List("merchant-list", "", "merchant", "", 2, true)
	if err != nil || len(first["items"].([]any)) != 2 {
		t.Fatalf("expected first page of two, got %#v, %v", first, err)
	}
	token, ok := first["nextToken"].(string)
	if !ok || token == "" {
		t.Fatalf("expected next token, got %#v", first)
	}
	second, err := payment.List("merchant-list", "", "merchant", token, 2, true)
	if err != nil || len(second["items"].([]any)) != 1 {
		t.Fatalf("expected second page of one, got %#v, %v", second, err)
	}
	_, err = payment.List("merchant-list", "COMPLETED", "state", token, 2, true)
	requireDomainError(t, err, 400, "INVALID_PAGINATION_TOKEN")
	_, err = payment.List("other-merchant", "", "merchant", token, 2, true)
	requireDomainError(t, err, 400, "INVALID_PAGINATION_TOKEN")
}
