package actuator

import (
	"instantpayments/pkg/http"

	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module("http.actuator",
		fx.Provide(fx.Annotate(
			NewHealth,
			fx.As(new(http.Route)),
			fx.ResultTags(`group:"routes"`),
		)),
	)
}
