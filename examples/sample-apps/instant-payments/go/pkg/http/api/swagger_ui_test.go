// This suite verifies offline Swagger UI assets, attribution, route enablement,
// method handling, and OpenAPI metadata, constraints, and response definitions.
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"instantpayments/pkg/env"
	apphttp "instantpayments/pkg/http"

	"go.uber.org/fx"
)

// resolveDocumentReference resolves a local OpenAPI object reference and fails on missing targets.
func resolveDocumentReference(t *testing.T, document map[string]any, reference string) map[string]any {
	t.Helper()
	path, ok := strings.CutPrefix(reference, "#/")
	if !ok {
		t.Fatalf("non-local reference %q", reference)
	}
	var current any = document
	for _, part := range strings.Split(path, "/") {
		object, ok := current.(map[string]any)
		if !ok || object[part] == nil {
			t.Fatalf("unresolved document reference %q", reference)
		}
		current = object[part]
	}
	return current.(map[string]any)
}

// resolvedResponse returns an inline response or resolves its local reference.
func resolvedResponse(t *testing.T, document map[string]any, value any) map[string]any {
	t.Helper()
	response := value.(map[string]any)
	if reference, ok := response["$ref"].(string); ok {
		return resolveDocumentReference(t, document, reference)
	}
	return response
}

func TestSwaggerUIAttributionHTTP(t *testing.T) {
	server := httptest.NewServer(documentationMux(t, &env.Env{DocsEnabled: true}))
	defer server.Close()
	page, err := swaggerUI.ReadFile("ui-assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"LICENSE", "NOTICE"} {
		expected, err := swaggerUI.ReadFile("ui-assets/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(page), `href="/swagger-ui/`+name+`"`) {
			t.Errorf("%s must be discoverable from the UI", name)
		}
		response, err := server.Client().Get(server.URL + "/swagger-ui/" + name)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/plain; charset=utf-8" || string(body) != string(expected) {
			t.Errorf("%s: HTTP response must distribute the complete embedded attribution", name)
		}
	}
}

func TestSwaggerUIRedirectFollow(t *testing.T) {
	server := httptest.NewServer(documentationMux(t, &env.Env{DocsEnabled: true}))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/swagger-ui.html")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Request.URL.Path != "/swagger-ui/index.html" || !strings.Contains(string(body), `id="swagger-ui"`) {
		t.Fatalf("following redirect did not render Swagger UI: %d %s", response.StatusCode, response.Request.URL.Path)
	}
}

func TestOpenAPIRequestBodyConstraints(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(swaggerJSON, &document); err != nil {
		t.Fatal(err)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	create := schemas["CreateOutboundPaymentRequest"].(map[string]any)
	if len(create["required"].([]any)) != 7 {
		t.Fatal("all seven creation fields must be required")
	}
	properties := create["properties"].(map[string]any)
	for _, field := range []string{"idempotencyKey", "merchantId", "debtorAccountId", "creditorIban", "creditorName"} {
		schema := properties[field].(map[string]any)
		if schema["minLength"] != float64(1) || schema["pattern"] != `\S` {
			t.Errorf("%s must document nonblank input", field)
		}
	}
	amount := properties["amount"].(map[string]any)
	if amount["minimum"] != float64(0) || amount["exclusiveMinimum"] != true {
		t.Error("amount must be strictly positive")
	}
	currency := properties["currency"].(map[string]any)
	if currency["minLength"] != float64(3) || currency["maxLength"] != float64(3) {
		t.Error("currency must have three characters")
	}
	batch := schemas["BatchGetReservationsRequest"].(map[string]any)["properties"].(map[string]any)["reservationIds"].(map[string]any)
	if batch["minItems"] != float64(1) || batch["maxItems"] != float64(100) || batch["uniqueItems"] == true {
		t.Error("batch must accept 1 through 100 identifiers including duplicates")
	}
	if batch["items"].(map[string]any)["pattern"] != `\S` {
		t.Error("batch identifiers must be nonblank")
	}
	paths := document["paths"].(map[string]any)
	stateOperation := paths["/api/v1/merchants/{merchantId}/payments/state/{state}"].(map[string]any)["get"].(map[string]any)
	for _, raw := range stateOperation["parameters"].([]any) {
		parameter := raw.(map[string]any)
		if parameter["name"] != "state" {
			continue
		}
		schema := parameter["schema"].(map[string]any)
		pattern := regexp.MustCompile(schema["pattern"].(string))
		for _, state := range []string{"RECEIVED", "funds_reserved", "Completed", "rejected"} {
			if !pattern.MatchString(state) {
				t.Errorf("state schema must accept case-insensitive %s", state)
			}
		}
		if pattern.MatchString("UNKNOWN") {
			t.Error("state schema must reject unsupported lifecycle states")
		}
	}
}

// documentationMux constructs the real Fx route group without an AWS client or
// payment service, proving that documentation access does not depend on DynamoDB.
func documentationMux(t *testing.T, settings *env.Env) *http.ServeMux {
	t.Helper()
	var routes apphttp.RoutesIn
	app := fx.New(fx.NopLogger, fx.Supply(settings), Module(), fx.Populate(&routes))
	if err := app.Err(); err != nil {
		t.Fatalf("construct documentation module: %v", err)
	}
	mux := http.NewServeMux()
	for _, route := range routes.Routes {
		mux.Handle(route.Pattern(), route)
	}
	return mux
}

func TestDocumentationConfiguration(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings env.Env
		enabled  bool
	}{
		{"enabled", env.Env{AppProfile: "dev", DocsEnabled: true}, true},
		{"disabled", env.Env{AppProfile: "dev", DocsEnabled: false}, false},
		{"production", env.Env{AppProfile: "prod", DocsEnabled: false}, false},
		{"production cannot opt in", env.Env{AppProfile: "prod", DocsEnabled: true}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mux := documentationMux(t, &test.settings)
			for _, path := range []string{"/api-docs", "/swagger-ui.html", "/swagger-ui/index.html", "/swagger-ui/swagger-initializer.js", "/swagger-ui/swagger-ui.css", "/swagger-ui/swagger-ui-bundle.js", "/swagger-ui/LICENSE", "/swagger-ui/NOTICE"} {
				recorder := httptest.NewRecorder()
				mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
				expected := http.StatusNotFound
				if test.enabled {
					expected = http.StatusOK
					if path == "/swagger-ui.html" {
						expected = http.StatusFound
					}
				}
				if recorder.Code != expected {
					t.Errorf("%s: got %d, want %d", path, recorder.Code, expected)
				}
				if path == "/swagger-ui.html" && test.enabled && recorder.Header().Get("Location") != "/swagger-ui/index.html" {
					t.Error("Swagger UI redirect does not target the embedded entry point")
				}
			}
			for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
				recorder := httptest.NewRecorder()
				mux.ServeHTTP(recorder, httptest.NewRequest(method, "/favicon.ico", nil))
				if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 || recorder.Header().Get("Content-Type") != "" {
					t.Fatalf("favicon must be empty 204 without content type, got %d %q %q", recorder.Code, recorder.Body.String(), recorder.Header().Get("Content-Type"))
				}
			}
		})
	}
}

func TestSwaggerUIOfflineAssets(t *testing.T) {
	mux := documentationMux(t, &env.Env{DocsEnabled: true})
	for _, test := range []struct {
		path, contentType, marker string
	}{
		{"/swagger-ui/index.html", "text/html; charset=utf-8", `/swagger-ui/swagger-ui-bundle.js`},
		{"/swagger-ui/swagger-ui.css", "text/css; charset=utf-8", ".swagger-ui"},
		{"/swagger-ui/swagger-ui-bundle.js", "application/javascript", "SwaggerUIBundle"},
		{"/swagger-ui/swagger-initializer.js", "application/javascript", `operationsSorter: "method"`},
		{"/swagger-ui/LICENSE", "text/plain; charset=utf-8", "Apache License"},
		{"/swagger-ui/NOTICE", "text/plain; charset=utf-8", "Copyright 2020-2021 SmartBear Software Inc."},
	} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != test.contentType || !strings.Contains(recorder.Body.String(), test.marker) {
			t.Errorf("%s: incorrect embedded Swagger UI asset", test.path)
		}
		if recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing content type protection", test.path)
		}
	}
	for _, name := range []string{"index.html", "swagger-initializer.js"} {
		asset, err := swaggerUI.ReadFile("ui-assets/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(asset), "https://") || strings.Contains(string(asset), "http://") {
			t.Fatalf("%s must not load remote code or documents", name)
		}
	}
	initializer, _ := swaggerUI.ReadFile("ui-assets/swagger-initializer.js")
	for _, configuration := range []string{`url: "/api-docs"`, `validatorUrl: null`, `presets: [SwaggerUIBundle.presets.apis]`} {
		if !strings.Contains(string(initializer), configuration) {
			t.Errorf("missing Swagger UI setting %q", configuration)
		}
	}
	for _, path := range []string{"/swagger-ui/", "/swagger-ui/package.json", "/swagger-ui/package-lock.json", "/swagger-ui/license", "/swagger-ui/unknown.js", "/swagger-ui/nested/index.html", "/api-docs/extra"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("unexpected public documentation asset %s: %d", path, recorder.Code)
		}
	}
}

func TestDocumentationMethods(t *testing.T) {
	mux := documentationMux(t, &env.Env{DocsEnabled: true})
	for _, path := range []string{"/api-docs", "/swagger-ui.html", "/swagger-ui/index.html", "/swagger-ui/swagger-ui.css"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("%s: expected 405 and Allow header", path)
		}
		body := decodeBody(t, recorder)
		if body["error"] != "METHOD_NOT_ALLOWED" || body["message"] != "method not allowed" {
			t.Errorf("%s: unexpected method error envelope: %#v", path, body)
		}
		if _, err := time.Parse(time.RFC3339Nano, body["timestamp"].(string)); err != nil {
			t.Error(err)
		}
		recorder = httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, path, nil))
		if recorder.Body.Len() != 0 || (recorder.Code != http.StatusOK && recorder.Code != http.StatusFound) {
			t.Errorf("%s: HEAD must have the GET status and no body", path)
		}
	}
}

func TestOpenAPIMetadataAndConstraints(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(swaggerJSON, &document); err != nil {
		t.Fatal(err)
	}
	info := document["info"].(map[string]any)
	if info["title"] != "Instant Payments API" || info["version"] != "1.0.0" || info["description"] == "" {
		t.Fatalf("incorrect fixed metadata: %#v", info)
	}
	contact := info["contact"].(map[string]any)
	if contact["name"] != "AWS DynamoDB Examples" || contact["url"] != "https://github.com/aws-samples/aws-dynamodb-examples" {
		t.Fatalf("incorrect contact metadata: %#v", contact)
	}
	servers := document["servers"].([]any)
	if len(servers) != 1 || servers[0].(map[string]any)["url"] != "http://localhost:8080" {
		t.Fatalf("incorrect server metadata: %#v", servers)
	}
	paths := document["paths"].(map[string]any)
	operationIDs := make(map[string]bool)
	for path, raw := range paths {
		for method, raw := range raw.(map[string]any) {
			operation := raw.(map[string]any)
			id, _ := operation["operationId"].(string)
			if id == "" || operationIDs[id] || operation["summary"] == nil || operation["description"] == nil {
				t.Errorf("%s %s: missing business metadata or duplicate operationId", method, path)
			}
			operationIDs[id] = true
			if !strings.HasPrefix(path, "/api/v1/") || path == "/api/v1/echo" {
				continue
			}
			responses := operation["responses"].(map[string]any)
			for _, status := range []string{"400", "405", "500", "503"} {
				if responses[status] == nil {
					t.Errorf("%s %s: missing %s response", method, path, status)
					continue
				}
				response := resolvedResponse(t, document, responses[status])
				content := response["content"].(map[string]any)["application/json"].(map[string]any)
				if content["schema"].(map[string]any)["$ref"] != "#/components/schemas/ErrorResponse" {
					t.Errorf("%s %s %s: missing shared error envelope", method, path, status)
				}
			}
			methodError := resolvedResponse(t, document, responses["405"])
			if methodError["headers"].(map[string]any)["Allow"] == nil {
				t.Errorf("%s %s: 405 missing Allow documentation", method, path)
			}
			unavailable := resolvedResponse(t, document, responses["503"])
			if unavailable["headers"].(map[string]any)["Retry-After"] == nil {
				t.Errorf("%s %s: 503 missing conditional retry header", method, path)
			}
			if parameters, ok := operation["parameters"].([]any); ok {
				for _, parameter := range parameters {
					p := parameter.(map[string]any)
					if p["in"] != "path" {
						continue
					}
					schema := p["schema"].(map[string]any)
					if p["required"] != true || schema["pattern"] == nil || schema["maxLength"] != float64(64) || schema["minLength"] != float64(1) {
						t.Errorf("%s %s: incomplete path constraint for %s", method, path, p["name"])
					}
				}
			}
		}
	}
}
