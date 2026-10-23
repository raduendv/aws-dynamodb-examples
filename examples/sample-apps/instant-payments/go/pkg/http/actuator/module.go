package actuator

import (
	"instantpayments/pkg/http"

	"go.uber.org/fx"
)

// Module registers the datastore health route with the HTTP route group.
func Module() fx.Option {
	return fx.Module("http.actuator",
		fx.Provide(fx.Annotate(
			NewHealth,
			fx.As(new(http.Route)),
			fx.ResultTags(`group:"routes"`),
		)),
	)
}
