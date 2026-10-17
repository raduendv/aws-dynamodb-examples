package env

import "go.uber.org/fx"

// Module provides environment configuration to the Fx application.
func Module() fx.Option {
	return fx.Module("env",
		fx.Provide(GetEnv),
	)
}
