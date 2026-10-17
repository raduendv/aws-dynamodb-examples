package service

import "go.uber.org/fx"

// Module provides the payment service to the Fx application.
func Module() fx.Option {
	return fx.Module("service",
		fx.Provide(NewPayment),
	)
}
