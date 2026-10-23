// This suite checks error-response serialization.
package util

import (
	"errors"
	"net/http/httptest"
	"testing"
)

func TestWriteErrorResponse(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		body string
	}{
		{name: "without error", body: "{\n  \"status\": \"error\"\n}\n"},
		{name: "with error", err: errors.New("boom"), body: "{\n  \"message\": \"boom\",\n  \"status\": \"error\"\n}\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			WriteErrorResponse(recorder, testCase.err)
			if recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" || recorder.Body.String() != testCase.body {
				t.Fatalf("unexpected response %q %q", recorder.Header().Get("Content-Type"), recorder.Body.String())
			}
		})
	}
}
