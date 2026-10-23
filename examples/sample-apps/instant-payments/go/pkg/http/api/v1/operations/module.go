package operations

import (
	"instantpayments/pkg/http"

	"go.uber.org/fx"
)

// Module registers the manual retry endpoint using the shared payment processor.
func Module() fx.Option {
	return fx.Module("http.api.v1.operations",
		fx.Provide(fx.Annotate(NewProcess, fx.As(new(http.Route)), fx.ResultTags(`group:"routes"`))),
	)
}
