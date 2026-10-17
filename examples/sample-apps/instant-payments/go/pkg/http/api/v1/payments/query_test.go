package payments

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/zap"

	apphttp "instantpayments/pkg/http"
	"instantpayments/pkg/http/api/v1/accounts"
	"instantpayments/pkg/http/api/v1/operations"
	"instantpayments/pkg/service"
)

func newTestMux() *http.ServeMux {
	payment := service.NewPayment(zap.NewNop(), nil)
	mux := http.NewServeMux()
	mux.Handle(NewOutbound(zap.NewNop(), payment).Pattern(), NewOutbound(zap.NewNop(), payment))
	mux.Handle(operations.NewProcess(payment).Pattern(), operations.NewProcess(payment))
	mux.Handle(NewPaymentQuery(payment).Pattern(), NewPaymentQuery(payment))
	mux.Handle(accounts.NewQuery(payment).Pattern(), accounts.NewQuery(payment))
	mux.Handle(accounts.NewBatchQuery(payment).Pattern(), accounts.NewBatchQuery(payment))
	mux.Handle(NewMerchantQuery(payment).Pattern(), NewMerchantQuery(payment))
	mux.Handle(NewMerchantStateQuery(payment).Pattern(), NewMerchantStateQuery(payment))

	return mux
}

func performRequest(t *testing.T, handler http.Handler, method, target, body string) (int, map[string]any, http.Header) {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	handler.ServeHTTP(recorder, req)
	var payload map[string]any
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
		}
	}
	return recorder.Code, payload, recorder.Header()
}

func TestOutboundHandlerCreatesReplaysAndValidates(t *testing.T) {
	mux := newTestMux()
	body := `{"idempotencyKey":"handler-key","merchantId":"merchant-handler","debtorAccountId":"acc_usd_1","creditorIban":"RO49AAAA1B31007593840000","creditorName":"Jane Doe","amount":100,"currency":"USD"}`

	status, created, headers := performRequest(t, mux, http.MethodPost, "/api/v1/payments/outbound", body)
	if status != http.StatusCreated || created["state"] != "RECEIVED" {
		t.Fatalf("expected created payment, got %d %#v", status, created)
	}
	if headers.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("unexpected content type %q", headers.Get("Content-Type"))
	}

	status, replayed, _ := performRequest(t, mux, http.MethodPost, "/api/v1/payments/outbound", body)
	if status != http.StatusOK || replayed["paymentId"] != created["paymentId"] {
		t.Fatalf("expected idempotent replay, got %d %#v", status, replayed)
	}
	status, invalid, _ := performRequest(t, mux, http.MethodPost, "/api/v1/payments/outbound", `{not json`)
	if status != http.StatusBadRequest || invalid["error"] != "VALIDATION_ERROR" {
		t.Fatalf("expected validation error, got %d %#v", status, invalid)
	}
	status, methodError, headers := performRequest(t, mux, http.MethodGet, "/api/v1/payments/outbound", "")
	if status != http.StatusMethodNotAllowed || methodError["error"] != "METHOD_NOT_ALLOWED" || headers.Get("Allow") != http.MethodPost {
		t.Fatalf("expected method error, got %d %#v allow=%q", status, methodError, headers.Get("Allow"))
	}
}

func TestLegacyOutboundByID(t *testing.T) {
	handler := NewOutboundByID(zap.NewNop())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, handler.Pattern(), bytes.NewBufferString("copied")))
	if recorder.Body.String() != "copied" {
		t.Fatalf("expected legacy handler to copy request body, got %q", recorder.Body.String())
	}
}

func TestModule(t *testing.T) {
	var routes []apphttp.Route
	app := fx.New(
		Module(),
		fx.Supply(zap.NewNop(), &service.Payment{}),
		fx.Invoke(fx.Annotate(func(group []apphttp.Route) {
			routes = group
		}, fx.ParamTags(`group:"routes"`))),
		fx.NopLogger,
	)
	if err := app.Err(); err != nil {
		t.Fatalf("build payments module: %v", err)
	}
	if len(routes) != 4 {
		t.Fatalf("expected 4 routes, got %d", len(routes))
	}
}

func TestPaymentQueryHandlersCompleteLifecycle(t *testing.T) {
	mux := newTestMux()
	body := `{"idempotencyKey":"lifecycle-key","merchantId":"merchant-lifecycle","debtorAccountId":"acc_usd_1","creditorIban":"RO49AAAA1B31007593840000","creditorName":"Jane Doe","amount":100,"currency":"USD"}`
	_, created, _ := performRequest(t, mux, http.MethodPost, "/api/v1/payments/outbound", body)
	paymentID := created["paymentId"].(string)

	status, processed, _ := performRequest(t, mux, http.MethodPost, "/api/v1/payments/outbound/"+paymentID+"/process", "")
	if status != http.StatusOK || processed["state"] != "COMPLETED" {
		t.Fatalf("expected completed process, got %d %#v", status, processed)
	}
	status, payment, _ := performRequest(t, mux, http.MethodGet, "/api/v1/payments/outbound/"+paymentID, "")
	if status != http.StatusOK || payment["version"] != float64(3) {
		t.Fatalf("expected payment history, got %d %#v", status, payment)
	}
	status, account, _ := performRequest(t, mux, http.MethodGet, "/api/v1/accounts/acc_usd_1", "")
	if status != http.StatusOK || account["currentBalance"] != float64(9900) {
		t.Fatalf("expected debited account, got %d %#v", status, account)
	}
	batch := `{"reservationIds":["res_` + paymentID + `","missing"]}`
	status, reservations, _ := performRequest(t, mux, http.MethodPost, "/api/v1/accounts/acc_usd_1/batch-get-reservations", batch)
	if status != http.StatusOK || len(reservations["reservations"].([]any)) != 1 {
		t.Fatalf("expected reservation batch result, got %d %#v", status, reservations)
	}

	status, missing, _ := performRequest(t, mux, http.MethodGet, "/api/v1/payments/outbound/pay$bad", "")
	if status != http.StatusBadRequest || missing["error"] != "VALIDATION_ERROR" {
		t.Fatalf("expected malformed payment id error, got %d %#v", status, missing)
	}
}

func TestQueryHandlersRejectWrongMethodsAndInvalidBodies(t *testing.T) {
	mux := newTestMux()
	for _, testCase := range []struct {
		method string
		target string
		body   string
	}{
		{method: http.MethodGet, target: "/api/v1/payments/outbound/pay_123/process"},
		{method: http.MethodPost, target: "/api/v1/payments/outbound/pay_123"},
		{method: http.MethodPost, target: "/api/v1/accounts/acc_usd_1"},
		{method: http.MethodGet, target: "/api/v1/accounts/acc_usd_1/batch-get-reservations"},
		{method: http.MethodPost, target: "/api/v1/accounts/acc_usd_1/batch-get-reservations", body: `{`},
	} {
		status, payload, _ := performRequest(t, mux, testCase.method, testCase.target, testCase.body)
		if status != http.StatusMethodNotAllowed && status != http.StatusBadRequest {
			t.Fatalf("expected rejected request for %s %s, got %d %#v", testCase.method, testCase.target, status, payload)
		}
	}
}

func TestMerchantHandlersPaginateAndValidateInput(t *testing.T) {
	mux := newTestMux()
	for index := 0; index < 3; index++ {
		body := `{"idempotencyKey":"merchant-key-` + string(rune('0'+index)) + `","merchantId":"merchant-list","debtorAccountId":"acc_usd_1","creditorIban":"RO49AAAA1B31007593840000","creditorName":"Jane Doe","amount":1,"currency":"USD"}`
		_, created, _ := performRequest(t, mux, http.MethodPost, "/api/v1/payments/outbound", body)
		performRequest(t, mux, http.MethodPost, "/api/v1/payments/outbound/"+created["paymentId"].(string)+"/process", "")
	}

	status, first, _ := performRequest(t, mux, http.MethodGet, "/api/v1/merchants/merchant-list/payments?limit=2", "")
	if status != http.StatusOK || len(first["items"].([]any)) != 2 {
		t.Fatalf("expected first merchant page, got %d %#v", status, first)
	}
	token := first["nextToken"].(string)
	status, second, _ := performRequest(t, mux, http.MethodGet, "/api/v1/merchants/merchant-list/payments?limit=2&nextToken="+token, "")
	if status != http.StatusOK || len(second["items"].([]any)) != 1 {
		t.Fatalf("expected second merchant page, got %d %#v", status, second)
	}
	status, statePage, _ := performRequest(t, mux, http.MethodGet, "/api/v1/merchants/merchant-list/payments/state/completed?limit=2", "")
	if status != http.StatusOK || len(statePage["items"].([]any)) != 2 {
		t.Fatalf("expected case-insensitive state page, got %d %#v", status, statePage)
	}

	for _, target := range []string{
		"/api/v1/merchants/merchant-list/payments?limit=abc",
		"/api/v1/merchants/merchant-list/payments/state/COMPLETED$bad",
		"/api/v1/merchants/merchant-list/payments/state/not-a-state",
	} {
		status, payload, _ := performRequest(t, mux, http.MethodGet, target, "")
		if status != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d %#v", target, status, payload)
		}
	}
}
