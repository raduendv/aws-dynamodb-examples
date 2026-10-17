package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
)

type hookLifecycle struct {
	hooks []fx.Hook
}

func (lifecycle *hookLifecycle) Append(hook fx.Hook) {
	lifecycle.hooks = append(lifecycle.hooks, hook)
}

type fixedRoute struct {
	pattern string
	body    string
}

func (route fixedRoute) Pattern() string { return route.pattern }
func (route fixedRoute) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	_, _ = writer.Write([]byte(route.body))
}

func TestNewServeMuxRegistersRoutes(t *testing.T) {
	mux := NewServeMux(RoutesIn{Routes: []Route{fixedRoute{pattern: "/one", body: "first"}, fixedRoute{pattern: "/two", body: "second"}}}, zap.NewNop())
	for _, testCase := range []struct {
		path string
		body string
	}{{path: "/one", body: "first"}, {path: "/two", body: "second"}} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, testCase.path, nil))
		if recorder.Code != http.StatusOK || recorder.Body.String() != testCase.body {
			t.Fatalf("unexpected response for %s: %d %q", testCase.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestNewServeMuxPanicsWithoutRoutes(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected no-routes panic")
		}
	}()
	NewServeMux(RoutesIn{}, zap.NewNop())
}

func TestNewServerLifecycleStartsAndStops(t *testing.T) {
	lifecycle := &hookLifecycle{}
	server := NewServer(lifecycle, http.NewServeMux(), zap.NewNop(), &env.Env{HTTPServerAddress: "127.0.0.1:0"})
	if server.Addr != "127.0.0.1:0" || len(lifecycle.hooks) != 1 {
		t.Fatalf("unexpected server lifecycle setup %#v hooks=%d", server, len(lifecycle.hooks))
	}
	hook := lifecycle.hooks[0]
	if err := hook.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := hook.OnStop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
