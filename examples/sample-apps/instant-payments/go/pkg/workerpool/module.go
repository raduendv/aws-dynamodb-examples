package workerpool

import "go.uber.org/fx"

// Module provides the worker pool to the Fx application.
func Module() fx.Option {
	return fx.Module("workerpool",
		fx.Provide(NewDispatcher),
	)
}
