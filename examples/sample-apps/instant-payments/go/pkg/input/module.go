package input

import (
	validator "github.com/go-playground/validator/v10"
	"go.uber.org/fx"
)

// Module provides the shared typed-input validation engine.
func Module() fx.Option {
	return fx.Module("input",
		fx.Provide(NewValidate),
	)
}

// NewValidate creates the standard validator used by typed route validators.
func NewValidate() *validator.Validate {
	return validator.New(
		validator.WithRequiredStructEnabled(),
	)
}
