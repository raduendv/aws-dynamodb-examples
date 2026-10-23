package api

import (
	"bytes"
	"embed"
	"net/http"
	"strings"
	"time"
)

// Runtime assets and upstream attribution travel with the binary. Package
// manifests remain private; only explicitly allowlisted files are served.
//
//go:embed ui-assets/index.html ui-assets/swagger-initializer.js ui-assets/swagger-ui-bundle.js ui-assets/swagger-ui.css ui-assets/LICENSE ui-assets/NOTICE
var swaggerUI embed.FS

// documentationRoute adapts a standard handler to the application's route registry.
type documentationRoute struct {
	pattern string
	handler http.Handler
}

// Pattern returns the registered documentation URL pattern.
func (route documentationRoute) Pattern() string { return route.pattern }

// ServeHTTP delegates to the documentation handler without domain persistence.
func (route documentationRoute) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route.handler.ServeHTTP(w, r)
}

// serveSwaggerUI serves the official, pinned Swagger UI distribution offline.
// The explicit allowlist prevents directory listings and path traversal.
func serveSwaggerUI(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/swagger-ui/")
	switch name {
	case "index.html", "swagger-initializer.js", "swagger-ui-bundle.js", "swagger-ui.css", "LICENSE", "NOTICE":
	default:
		http.NotFound(w, r)
		return
	}
	if !documentationMethod(w, r) {
		return
	}
	data, err := swaggerUI.ReadFile("ui-assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case name == "LICENSE" || name == "NOTICE":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	case strings.HasSuffix(name, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript")
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
