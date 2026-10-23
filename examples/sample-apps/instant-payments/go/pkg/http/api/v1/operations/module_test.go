// This suite checks payment-processing route module construction.
package operations

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
		t.Fatalf("build operations module: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
}
