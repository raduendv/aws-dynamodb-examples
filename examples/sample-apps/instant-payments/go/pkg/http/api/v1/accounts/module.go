package accounts

import (
	"instantpayments/pkg/http"

	"go.uber.org/fx"
)

// Module registers account overview and reservation lookup handlers.
func Module() fx.Option {
	return fx.Module("http.api.v1.accounts",
		fx.Provide(fx.Annotate(NewQuery, fx.As(new(http.Route)), fx.ResultTags(`group:"routes"`))),
		fx.Provide(fx.Annotate(NewBatchQuery, fx.As(new(http.Route)), fx.ResultTags(`group:"routes"`))),
	)
}
