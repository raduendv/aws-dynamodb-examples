// This suite checks generic JSON decoding and malformed-input errors.
package input

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// processingFixture provides a typed JSON target for decoder assertions.
type processingFixture struct {
	Name string `json:"name"`
}

func TestDecodeJSON(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "one value", body: `{"name":"payment"}`},
		{name: "trailing whitespace", body: "{\"name\":\"payment\"} \n\t"},
		{name: "malformed first value", body: `{`, wantErr: true},
		{name: "malformed trailing data", body: `{"name":"payment"} {`, wantErr: true},
		{name: "multiple values", body: `{"name":"payment"} {"name":"second"}`, wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/", strings.NewReader(testCase.body))
			result, err := DecodeJSON[processingFixture](request)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("expected invalid JSON error for %q", testCase.body)
				}
				return
			}
			if err != nil || result.Name != "payment" {
				t.Fatalf("expected decoded fixture, got %#v, %v", result, err)
			}
		})
	}
}
