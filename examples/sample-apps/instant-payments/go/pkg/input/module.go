package input

import (
	validator "github.com/go-playground/validator/v10"
	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module("input",
		fx.Provide(NewValidate),
	)
}

func NewValidate() *validator.Validate {
	return validator.New(
		validator.WithRequiredStructEnabled(),
	)
}
