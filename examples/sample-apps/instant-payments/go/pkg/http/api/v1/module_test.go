// This suite checks versioned API module construction.
package v1

import (
	"testing"

	apphttp "instantpayments/pkg/http"
	"instantpayments/pkg/service"

	"go.uber.org/fx"
	"go.uber.org/zap"
)

func TestModule(t *testing.T) {
	var routes []apphttp.Route
	app := fx.New(
		Module(),
		fx.Supply(zap.NewNop(), &service.Payment{}),
		fx.Invoke(fx.Annotate(func(group []apphttp.Route) {
			routes = group
		}, fx.ParamTags(`group:"routes"`))),
		fx.NopLogger,
	)
	if err := app.Err(); err != nil {
		t.Fatalf("build v1 module: %v", err)
	}
	if len(routes) != 8 {
		t.Fatalf("expected 8 routes, got %d", len(routes))
	}
}
