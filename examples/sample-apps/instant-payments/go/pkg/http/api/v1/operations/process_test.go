// This suite checks manual processing responses, validation, and cancellation without state advancement.
package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"instantpayments/pkg/dto"
	"instantpayments/pkg/service"
)

func TestProcessHandlerCancellationDoesNotAdvancePayment(t *testing.T) {
	payment := service.NewPayment(zap.NewNop(), nil)
	created, _, err := payment.Create(&dto.OutboundPaymentInput{
		IdempotencyKey: "canceled-http", MerchantID: "merchant", DebtorAccountID: "acc_usd_1",
		CreditorIban: "RO49AAAA1B31007593840000", CreditorName: "Jane", Amount: 1, Currency: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/payments/outbound/"+created.PaymentID+"/process", nil).WithContext(ctx)
	mux := http.NewServeMux()
	handler := NewProcess(payment)
	mux.Handle(handler.Pattern(), handler)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code == http.StatusOK {
		t.Fatal("canceled processing returned success")
	}
	state, err := payment.Get(created.PaymentID)
	if err != nil || state["state"] != "RECEIVED" || state["version"] != 1 {
		t.Fatalf("canceled HTTP request advanced payment: %#v, %v", state, err)
	}
	account, err := payment.Account("acc_usd_1")
	if err != nil || account["currentBalance"] != float64(10000) || account["availableBalance"] != float64(10000) {
		t.Fatalf("canceled HTTP request moved money: %#v, %v", account, err)
	}
}

func TestProcessHandler(t *testing.T) {
	payment := service.NewPayment(zap.NewNop(), nil)
	created, _, err := payment.Create(&dto.OutboundPaymentInput{IdempotencyKey: "process-handler", MerchantID: "merchant", DebtorAccountID: "acc_usd_1", CreditorIban: "RO49AAAA1B31007593840000", CreditorName: "Jane", Amount: 1, Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewProcess(payment)
	mux := http.NewServeMux()
	mux.Handle(handler.Pattern(), handler)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/payments/outbound/"+created.PaymentID+"/process", nil))
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || payload["state"] != "COMPLETED" {
		t.Fatalf("expected completed response, got %d %#v", recorder.Code, payload)
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/payments/outbound/pay_123/process", bytes.NewBuffer(nil)))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected method error, got %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/payments/outbound/pay$bad/process", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected identifier error, got %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/payments/outbound/pay_missing/process", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected not found error, got %d", recorder.Code)
	}
}
