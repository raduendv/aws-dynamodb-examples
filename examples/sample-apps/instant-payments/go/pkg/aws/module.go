package aws

import (
	"go.uber.org/fx"

	"instantpayments/pkg/workerpool/job"
)

// Module provides AWS configuration and clients to the Fx application.
func Module() fx.Option {
	return fx.Module("aws",
		fx.Provide(Config),
		fx.Provide(DynamoDB),
		fx.Provide(DynamoDBStreams),
		fx.Provide(NewRepository),
		fx.Provide(func(repository *Repository) job.Repository { return repository }),
		fx.Invoke(ConfigureStreams),
	)
}
