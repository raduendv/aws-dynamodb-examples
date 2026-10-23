package payments

import (
	"instantpayments/pkg/http"

	"go.uber.org/fx"
)

// Module registers creation, tracking and merchant payment-list handlers.
func Module() fx.Option {
	return fx.Module("http.api.v1.payments",
		fx.Provide(fx.Annotate(
			NewPaymentQuery,
			fx.As(new(http.Route)),
			fx.ResultTags(`group:"routes"`),
		)),
		fx.Provide(fx.Annotate(
			NewOutbound,
			fx.As(new(http.Route)),
			fx.ResultTags(`group:"routes"`),
		)),
		fx.Provide(fx.Annotate(NewMerchantQuery, fx.As(new(http.Route)), fx.ResultTags(`group:"routes"`))),
		fx.Provide(fx.Annotate(NewMerchantStateQuery, fx.As(new(http.Route)), fx.ResultTags(`group:"routes"`))),
	)
}
