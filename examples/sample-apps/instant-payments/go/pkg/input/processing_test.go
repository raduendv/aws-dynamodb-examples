package input

import (
	"net/http/httptest"
	"strings"
	"testing"
)

type processingFixture struct {
	Name string `json:"name"`
}

func TestDecodeJSON(t *testing.T) {
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"payment"}`))
	result, err := DecodeJSON[processingFixture](request)
	if err != nil || result.Name != "payment" {
		t.Fatalf("expected decoded fixture, got %#v, %v", result, err)
	}
	invalidRequest := httptest.NewRequest("POST", "/", strings.NewReader(`{`))
	if _, err := DecodeJSON[processingFixture](invalidRequest); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}
