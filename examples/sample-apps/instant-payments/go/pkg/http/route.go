package http

import (
	"net/http"

	"go.uber.org/fx"
)

// Route is an HTTP handler that exposes a registered URL pattern.
type Route interface {
	http.Handler

	Pattern() string
}

// RoutesOut collects routes returned by an Fx module.
type RoutesOut struct {
	fx.Out
	Routes []Route `group:"routes"`
}

// RoutesIn injects the registered handler group into the HTTP server.
type RoutesIn struct {
	fx.In
	Routes []Route `group:"routes"`
}
