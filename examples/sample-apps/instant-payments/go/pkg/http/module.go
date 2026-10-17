package http

import (
	"go.uber.org/fx"
)

// Module provides the HTTP server and route registry to the Fx application.
func Module() fx.Option {
	return fx.Module("http",
		fx.Provide(NewServeMux),
		fx.Provide(NewServer),
	)
}
