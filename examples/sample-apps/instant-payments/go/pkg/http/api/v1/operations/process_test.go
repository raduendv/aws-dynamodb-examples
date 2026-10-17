package operations

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"instantpayments/pkg/dto"
	"instantpayments/pkg/service"
)

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
