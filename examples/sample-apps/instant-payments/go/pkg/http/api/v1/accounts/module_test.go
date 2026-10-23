// This suite checks account-route Fx module construction.
package accounts

import (
	"testing"

	apphttp "instantpayments/pkg/http"
	"instantpayments/pkg/service"

	"go.uber.org/fx"
)

func TestModule(t *testing.T) {
	var routes []apphttp.Route
	app := fx.New(
		Module(),
		fx.Supply(&service.Payment{}),
		fx.Invoke(fx.Annotate(func(group []apphttp.Route) {
			routes = group
		}, fx.ParamTags(`group:"routes"`))),
		fx.NopLogger,
	)
	if err := app.Err(); err != nil {
		t.Fatalf("build accounts module: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
}
