package api

import (
	_ "embed"
	"encoding/json"
	"fmt"
	stdhttp "net/http"
	"time"
)

//go:embed swagger.json
var swaggerJSON []byte

// APIDocsHandler serves the embedded OpenAPI document.
type APIDocsHandler struct {
	document map[string]interface{}
}

// Pattern returns the API documentation route.
func (*APIDocsHandler) Pattern() string {
	return "/api-docs"
}

// ServeHTTP writes the OpenAPI document as JSON.
func (h *APIDocsHandler) ServeHTTP(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if !documentationMethod(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == stdhttp.MethodHead {
		return
	}
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	if err := e.Encode(h.document); err != nil {
		return
	}
}

// documentationMethod keeps documentation reads side-effect free and reports
// unsupported methods using the same public error shape as the business API.
func documentationMethod(w stdhttp.ResponseWriter, r *stdhttp.Request) bool {
	if r.Method == stdhttp.MethodGet || r.Method == stdhttp.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(stdhttp.StatusMethodNotAllowed)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "METHOD_NOT_ALLOWED", "message": "method not allowed",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
	return false
}

// NewAPIDocsHandler parses the embedded OpenAPI document.
func NewAPIDocsHandler() (*APIDocsHandler, error) {
	var document map[string]interface{}
	if err := json.Unmarshal(swaggerJSON, &document); err != nil {
		return nil, fmt.Errorf("failed to unmarshal swagger JSON: %w", err)
	}
	return &APIDocsHandler{document: document}, nil
}
