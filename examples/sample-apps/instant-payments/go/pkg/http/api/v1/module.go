package v1

import (
	"instantpayments/pkg/http"
	"instantpayments/pkg/http/api/v1/accounts"
	"instantpayments/pkg/http/api/v1/operations"
	"instantpayments/pkg/http/api/v1/payments"

	"go.uber.org/fx"
)

// Module composes version-one business routes and the local diagnostic echo route.
func Module() fx.Option {
	return fx.Module("http.api.v1",
		fx.Provide(fx.Annotate(
			NewEchoHandler,
			fx.As(new(http.Route)),
			fx.ResultTags(`group:"routes"`),
		)),
		payments.Module(),
		accounts.Module(),
		operations.Module(),
	)
}
