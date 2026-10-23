package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
	"instantpayments/pkg/model"
)

const (
	// defaultAsyncCoreSize keeps baseline concurrency modest for local and sample deployments.
	defaultAsyncCoreSize = env.DefaultHTTPCoreSize
	// defaultAsyncMaximumSize bounds bursts after the pending queue fills.
	defaultAsyncMaximumSize = env.DefaultHTTPMaxSize
	// defaultAsyncQueueSize absorbs brief bursts without creating unbounded waiting requests.
	defaultAsyncQueueSize = env.DefaultHTTPQueueSize
	// defaultAsyncTimeout limits how long callers wait for an application result.
	defaultAsyncTimeout = env.DefaultHTTPTimeout
	// defaultShutdownTimeout gives in-flight requests a bounded graceful-drain period.
	defaultShutdownTimeout = env.DefaultHTTPShutdownTimeout
	// readHeaderTimeout limits slow-client time spent sending request headers.
	readHeaderTimeout = 10 * time.Second
	// timeoutResponseSentinel distinguishes TimeoutHandler's timeout response from normal output.
	timeoutResponseSentinel = "instantpayments-timeout-response"
)

// NewServer creates the HTTP server and manages its lifecycle through Fx.
func NewServer(lc fx.Lifecycle, mux *http.ServeMux, log *zap.Logger, env *env.Env) *http.Server {
	core, maximum, queue := env.HTTPAsyncCoreSize, env.HTTPAsyncMaxSize, env.HTTPAsyncQueueSize
	if core == 0 && maximum == 0 {
		core, maximum, queue = defaultAsyncCoreSize, defaultAsyncMaximumSize, defaultAsyncQueueSize
	}
	resultTimeout, shutdownTimeout := env.HTTPAsyncTimeout, env.HTTPShutdownTimeout
	if resultTimeout == 0 {
		resultTimeout = defaultAsyncTimeout
	}
	if shutdownTimeout == 0 {
		shutdownTimeout = defaultShutdownTimeout
	}
	admission := &requestAdmission{core: core, maximum: maximum, capacity: queue}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !admission.acquire(r.Context()) {
			writeMiddlewareError(w, http.StatusServiceUnavailable, "ASYNC_REQUEST_CAPACITY_EXHAUSTED", "async request capacity exhausted")
			return
		}
		defer admission.release()
		mux.ServeHTTP(w, r)
	})
	timeoutHandler := http.TimeoutHandler(handler, resultTimeout, timeoutResponseSentinel)
	srv := &http.Server{
		Addr:              env.HTTPServerAddress,
		Handler:           jsonTimeoutHandler(timeoutHandler),
		ReadHeaderTimeout: readHeaderTimeout,
	}

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
			ctx, cancel := context.WithTimeout(ctx, shutdownTimeout)
			defer cancel()
			admission.mu.Lock()
			admission.closed = true
			admission.mu.Unlock()
			err := srv.Shutdown(ctx)
			done := make(chan struct{})
			go func() {
				admission.wg.Wait()
				close(done)
			}()
			select {
			case <-done:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})

	return srv
}

// middlewareErrorResponse is the JSON shape used for server middleware failures.
type middlewareErrorResponse struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// writeMiddlewareError writes the common API error envelope without middleware headers.
func writeMiddlewareError(w http.ResponseWriter, status int, code, message string) {
	for key := range w.Header() {
		delete(w.Header(), key)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(middlewareErrorResponse{
		Error:     code,
		Message:   message,
		Timestamp: model.FormatTimestamp(time.Now()),
	})
}

// timeoutCapture buffers a TimeoutHandler response before applying the public response contract.
type timeoutCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

// Header returns the buffered response headers.
func (w *timeoutCapture) Header() http.Header { return w.header }

// WriteHeader records the first response status.
func (w *timeoutCapture) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

// Write appends bytes to the buffered response body.
func (w *timeoutCapture) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}

// jsonTimeoutHandler converts TimeoutHandler's sentinel response to the standard JSON envelope.
func jsonTimeoutHandler(timeoutHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured := &timeoutCapture{header: make(http.Header)}
		timeoutHandler.ServeHTTP(captured, r)
		if captured.status == http.StatusServiceUnavailable &&
			captured.body.String() == timeoutResponseSentinel {
			writeMiddlewareError(w, http.StatusServiceUnavailable, "REQUEST_TIMEOUT", "async request timed out")
			return
		}
		copyTimeoutHeaders(w.Header(), captured.header)
		status := captured.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(captured.body.Bytes())
	})
}

// copyTimeoutHeaders copies a completed response's headers without sharing mutable slices.
func copyTimeoutHeaders(destination, source http.Header) {
	for key, values := range source {
		destination[key] = append([]string(nil), values...)
	}
}

// net/http already runs requests in goroutines. Bound their admission instead of
// emulating JVM threads: queue after core concurrency, then grow up to maximum.
// requestAdmission tracks active requests and a bounded FIFO of waiting requests.
type requestAdmission struct {
	mu                      sync.Mutex
	wg                      sync.WaitGroup
	core, maximum, capacity int
	running                 int
	pending                 []chan struct{}
	closed                  bool
}

// acquire reserves an active slot or waits for one until the context is canceled.
func (a *requestAdmission) acquire(ctx context.Context) bool {
	a.mu.Lock()
	if a.closed || ctx.Err() != nil {
		a.mu.Unlock()
		return false
	}
	if a.running < a.core || (len(a.pending) >= a.capacity && a.running < a.maximum) {
		a.running++
		a.wg.Add(1)
		a.mu.Unlock()
		return true
	}
	if len(a.pending) >= a.capacity {
		a.mu.Unlock()
		return false
	}
	ready := make(chan struct{})
	a.pending = append(a.pending, ready)
	a.wg.Add(1)
	a.mu.Unlock()
	select {
	case <-ready:
		return true
	case <-ctx.Done():
		a.mu.Lock()
		for i, pending := range a.pending {
			if pending == ready {
				a.pending = append(a.pending[:i], a.pending[i+1:]...)
				a.mu.Unlock()
				a.wg.Done()
				return false
			}
		}
		a.mu.Unlock()
		// A release granted the slot concurrently with cancellation.
		a.release()
		return false
	}
}

// release returns an active slot and wakes the oldest pending request.
func (a *requestAdmission) release() {
	a.mu.Lock()
	if len(a.pending) > 0 {
		ready := a.pending[0]
		a.pending = a.pending[1:]
		close(ready)
	} else {
		a.running--
	}
	a.mu.Unlock()
	a.wg.Done()
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
