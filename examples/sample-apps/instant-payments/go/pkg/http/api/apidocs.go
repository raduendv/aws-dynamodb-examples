package api

import (
	"encoding/json"
	"fmt"
	stdhttp "net/http"

	"instantpayments/gen/restapi"
)

// APIDocsHandler serves the embedded OpenAPI document.
type APIDocsHandler struct {
	document map[string]interface{}
}

// Pattern returns the API documentation route.
func (*APIDocsHandler) Pattern() string {
	return "/api-docs"
}

// ServeHTTP writes the OpenAPI document as JSON.
func (h *APIDocsHandler) ServeHTTP(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	if err := e.Encode(h.document); err != nil {

		return
	}
}

// NewAPIDocsHandler parses the embedded OpenAPI document.
func NewAPIDocsHandler() (*APIDocsHandler, error) {
	var document map[string]interface{}
	if err := json.Unmarshal(restapi.SwaggerJSON, &document); err != nil {
		return nil, fmt.Errorf("failed to unmarshal swagger JSON: %w", err)
	}
	document["openapi"] = "3.0.0"
	return &APIDocsHandler{document: document}, nil
}
