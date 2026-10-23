package api

import (
	"instantpayments/pkg/env"
	"instantpayments/pkg/http"
	stdhttp "net/http"

	"go.uber.org/fx"
)

// Module registers documentation only when centrally parsed runtime settings
// enable it. Favicon requests remain independent of documentation and DynamoDB.
func Module() fx.Option {
	return fx.Module("http.api",
		fx.Provide(newDocumentationRoutes),
	)
}

// documentationRoutes contributes enabled public documentation handlers to Fx.
type documentationRoutes struct {
	fx.Out
	Routes []http.Route `group:"routes,flatten"`
}

// newDocumentationRoutes always installs the favicon shortcut and omits docs in production.
// Invalid embedded OpenAPI prevents route construction.
func newDocumentationRoutes(settings *env.Env) (documentationRoutes, error) {
	routes := documentationRoutes{Routes: []http.Route{documentationRoute{
		pattern: "/favicon.ico",
		handler: stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
			w.WriteHeader(stdhttp.StatusNoContent)
		}),
	}}}
	if !settings.DocsEnabled || settings.AppProfile == "prod" {
		return routes, nil
	}
	document, err := NewAPIDocsHandler()
	if err != nil {
		return documentationRoutes{}, err
	}
	routes.Routes = append(routes.Routes, document,
		documentationRoute{pattern: "/swagger-ui.html", handler: stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			if documentationMethod(w, r) {
				stdhttp.Redirect(w, r, "/swagger-ui/index.html", stdhttp.StatusFound)
			}
		})},
		documentationRoute{pattern: "/swagger-ui/", handler: stdhttp.HandlerFunc(serveSwaggerUI)},
	)
	return routes, nil
}
