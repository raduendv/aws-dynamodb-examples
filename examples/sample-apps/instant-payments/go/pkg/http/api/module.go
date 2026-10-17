package api

import (
	"instantpayments/pkg/http"

	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module("http.api",
		fx.Provide(fx.Annotate(
			NewAPIDocsHandler,
			fx.As(new(http.Route)),
			fx.ResultTags(`group:"routes"`),
		)),
		fx.Provide(fx.Annotate(
			NewHealthCheckHandler,
			fx.As(new(http.Route)),
			fx.ResultTags(`group:"routes"`),
		)),
	)
}
