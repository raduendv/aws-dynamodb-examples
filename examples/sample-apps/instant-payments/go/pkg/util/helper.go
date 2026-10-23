package util

import (
	"encoding/json"
	"io"
	"net/http"
)

// encoder creates the indented JSON encoder used by the legacy error facade.
func encoder(w io.Writer) *json.Encoder {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e
}

// WriteErrorResponse writes the legacy status/message shape without choosing an HTTP status.
// Business routes use their shared domain-error response helper instead.
func WriteErrorResponse(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	out := map[string]interface{}{
		"status": "error",
	}
	if err != nil {
		out["message"] = err.Error()
	}

	if err := encoder(w).Encode(out); err != nil {
		return
	}
}
