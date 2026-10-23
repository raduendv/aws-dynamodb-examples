// This suite checks payment-route validation and logging when response encoding or copying fails.
package payments

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"instantpayments/pkg/service"
)

// failedResponseWriter simulates a disconnected response while retaining writable headers.
type failedResponseWriter struct{ header http.Header }

// Header returns the fixture's mutable response headers.
func (writer *failedResponseWriter) Header() http.Header { return writer.header }

// WriteHeader ignores status because only write-failure handling is under test.
func (*failedResponseWriter) WriteHeader(int) {}

// Write always reports a disconnected client.
func (*failedResponseWriter) Write([]byte) (int, error) { return 0, errors.New("connection closed") }

func TestOutboundLogsResponseWriteFailure(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	handler := NewOutbound(zap.New(core), service.NewPayment(zap.NewNop(), nil))
	request := httptest.NewRequest(http.MethodPost, handler.Pattern(), strings.NewReader(
		`{"idempotencyKey":"failed-write","merchantId":"merchant","debtorAccountId":"acc_usd_1","creditorIban":"iban","creditorName":"Jane","amount":1,"currency":"USD"}`,
	))
	handler.ServeHTTP(&failedResponseWriter{header: make(http.Header)}, request)
	if logs.FilterMessage("failed to encode response").Len() != 1 {
		t.Fatal("response write error was not logged")
	}
}

func TestPaymentRoutesRejectInvalidInput(t *testing.T) {
	for _, test := range []struct {
		method, path, body, code string
		status                   int
	}{
		{http.MethodPost, "/api/v1/payments/outbound", `{}`, "VALIDATION_ERROR", 400},
		{http.MethodGet, "/api/v1/payments/outbound/pay_missing", "", "PAYMENT_NOT_FOUND", 404},
		{http.MethodGet, "/api/v1/payments/outbound/pay$invalid", "", "VALIDATION_ERROR", 400},
		{http.MethodGet, "/api/v1/merchants/merchant/payments/state/BAD$STATE", "", "VALIDATION_ERROR", 400},
		{http.MethodGet, "/api/v1/merchants/merchant/payments/state/UNKNOWN", "", "INVALID_PAYMENT_STATE", 400},
		{http.MethodGet, "/api/v1/merchants/merchant/payments?limit=not-a-number", "", "VALIDATION_ERROR", 400},
		{http.MethodGet, "/api/v1/merchants/merchant/payments?nextToken=not-a-token", "", "INVALID_PAGINATION_TOKEN", 400},
		{http.MethodPost, "/api/v1/accounts/acc_usd_1/batch-get-reservations", `{"reservationIds":[]}`, "VALIDATION_ERROR", 400},
	} {
		t.Run(test.path, func(t *testing.T) {
			status, payload, _ := performRequest(t, newTestMux(), test.method, test.path, test.body)
			if status != test.status || payload["error"] != test.code {
				t.Fatalf("expected %d/%s, got %d %#v", test.status, test.code, status, payload)
			}
		})
	}
}

func TestLegacyOutboundLogsCopyFailure(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	handler := NewOutboundByID(zap.New(core))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/payments/outbound/pay_1", strings.NewReader("body"))
	handler.ServeHTTP(&failedResponseWriter{header: make(http.Header)}, request)
	if logs.FilterMessage("Failed to handle request").Len() != 1 {
		t.Fatal("legacy handler swallowed copy error without logging")
	}
}
