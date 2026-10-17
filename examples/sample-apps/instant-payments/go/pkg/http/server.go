package http

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
)

// NewServer creates the HTTP server and manages its lifecycle through Fx.
func NewServer(lc fx.Lifecycle, mux *http.ServeMux, log *zap.Logger, env *env.Env) *http.Server {
	srv := &http.Server{Addr: env.HTTPServerAddress, Handler: mux}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			ln, err := net.Listen("tcp", srv.Addr)
			if err != nil {

				return err
			}

			log.Info("Starting HTTP server", zap.String("addr", srv.Addr))
			go func() {
				if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					log.Error("HTTP server stopped unexpectedly", zap.Error(serveErr))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	})

	return srv
}

// NewServeMux registers all application routes on a standard ServeMux.
func NewServeMux(in RoutesIn, log *zap.Logger) *http.ServeMux {
	if len(in.Routes) == 0 {
		panic("no routes")
	}

	mux := http.NewServeMux()
	for _, route := range in.Routes {
		log.Info(fmt.Sprintf("Registering route: %s", route.Pattern()))

		mux.Handle(route.Pattern(), route)
	}

	return mux
}
