// This suite verifies route registration, server lifecycle, bounded admission,
// request cancellation, result deadlines, and graceful-shutdown limits.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
)

// hookLifecycle captures server hooks for deterministic startup and shutdown.
type hookLifecycle struct {
	hooks []fx.Hook
}

// Append records a hook without invoking it.
func (lifecycle *hookLifecycle) Append(hook fx.Hook) {
	lifecycle.hooks = append(lifecycle.hooks, hook)
}

// fixedRoute supplies a static path and response for mux registration tests.
type fixedRoute struct {
	pattern string
	body    string
}

// Pattern returns the fixture's registered path.
func (route fixedRoute) Pattern() string { return route.pattern }

// ServeHTTP writes the fixture response.
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

func TestNewServerReturnsListenFailure(t *testing.T) {
	lifecycle := &hookLifecycle{}
	NewServer(lifecycle, http.NewServeMux(), zap.NewNop(), &env.Env{HTTPServerAddress: "127.0.0.1:invalid"})
	if err := lifecycle.hooks[0].OnStart(context.Background()); err == nil {
		t.Fatal("invalid listen address must fail startup")
	}

}

func TestAsyncAdmissionCoreQueueAndMaximum(t *testing.T) {
	admission := &requestAdmission{core: 10, maximum: 50, capacity: 100}
	for range 10 {
		if !admission.acquire(context.Background()) {
			t.Fatal("core request was rejected")
		}
	}
	var waiters sync.WaitGroup
	releaseQueued := make(chan struct{})
	defer close(releaseQueued)
	for range 100 {
		waiters.Add(1)
		go func() {
			defer waiters.Done()
			if !admission.acquire(context.Background()) {
				t.Error("queued request was rejected")
				return
			}
			<-releaseQueued
			admission.release()
		}()
	}
	deadline := time.Now().Add(time.Second)
	for {
		admission.mu.Lock()
		pending, running := len(admission.pending), admission.running
		admission.mu.Unlock()
		if pending == 100 {
			if running != 10 {
				t.Fatalf("expanded before queue filled: running=%d", running)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue did not fill: pending=%d", pending)
		}
		time.Sleep(time.Millisecond)
	}
	for range 40 {
		if !admission.acquire(context.Background()) {
			t.Fatal("burst request was rejected below maximum")
		}
	}
	if admission.acquire(context.Background()) {
		t.Fatal("accepted request above 50 active + 100 queued")
	}
	for range 50 {
		admission.release()
	}
	// Let queued requests cascade through the available slots.
	for range 100 {
		releaseQueued <- struct{}{}
	}
	waiters.Wait()
	admission.wg.Wait()
	if admission.running != 0 || len(admission.pending) != 0 {
		t.Fatal("admission permits or queue entries leaked")
	}
}

func TestAsyncAdmissionCanceledQueueDoesNotLeak(t *testing.T) {
	admission := &requestAdmission{core: 1, maximum: 1, capacity: 1}
	if !admission.acquire(context.Background()) {
		t.Fatal("first request rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if admission.acquire(ctx) || time.Since(start) > time.Second {
		t.Fatal("queued request ignored cancellation")
	}
	admission.release()
	admission.wg.Wait()
	if admission.running != 0 || len(admission.pending) != 0 {
		t.Fatal("canceled queue entry leaked")
	}
}

func TestAsyncAdmissionTimeoutReleaseRaceDoesNotLeak(t *testing.T) {
	admission := &requestAdmission{core: 1, maximum: 1, capacity: 1}
	for range 250 {
		if !admission.acquire(context.Background()) {
			t.Fatal("active request was rejected")
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan bool, 1)
		go func() { result <- admission.acquire(ctx) }()
		deadline := time.Now().Add(time.Second)
		for {
			admission.mu.Lock()
			pending := len(admission.pending)
			admission.mu.Unlock()
			if pending == 1 {
				break
			}
			if time.Now().After(deadline) {
				cancel()
				t.Fatal("request did not enter the pending queue")
			}
			time.Sleep(time.Microsecond)
		}

		start := make(chan struct{})
		released := make(chan struct{})
		go func() {
			<-start
			admission.release()
			close(released)
		}()
		go func() {
			<-start
			cancel()
		}()
		close(start)
		acquired := <-result
		<-released
		if acquired {
			admission.release()
		}
		admission.wg.Wait()
	}

	admission.mu.Lock()
	defer admission.mu.Unlock()
	if admission.running != 0 || len(admission.pending) != 0 {
		t.Fatalf("timeout/release race leaked admission state: running=%d pending=%d", admission.running, len(admission.pending))
	}
}

func TestHTTPResultTimeoutAndCancellation(t *testing.T) {
	for _, timeout := range []time.Duration{0, 40 * time.Millisecond} {
		t.Run(timeout.String(), func(t *testing.T) {
			lifecycle := &hookLifecycle{}
			mux := http.NewServeMux()
			stopped := make(chan struct{})
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				deadline, ok := r.Context().Deadline()
				remaining := time.Until(deadline)
				if timeout == 0 {
					if !ok || remaining < 9*time.Second || remaining > 10*time.Second {
						t.Errorf("default result deadline = %v, present=%v", remaining, ok)
					}
					close(stopped)
					return
				}
				<-r.Context().Done()
				close(stopped)
			})
			server := NewServer(lifecycle, mux, zap.NewNop(), &env.Env{HTTPAsyncTimeout: timeout})
			response := httptest.NewRecorder()
			start := time.Now()
			server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
			if timeout != 0 && (response.Code != http.StatusServiceUnavailable || time.Since(start) < 30*time.Millisecond || time.Since(start) > time.Second) {
				t.Fatalf("result timeout response=%d elapsed=%v", response.Code, time.Since(start))
			}
			if timeout != 0 {
				var payload map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
					t.Fatalf("decode timeout error: %v; body=%s", err, response.Body.String())
				}
				if response.Header().Get("Content-Type") != "application/json" ||
					payload["error"] != "REQUEST_TIMEOUT" || payload["message"] != "async request timed out" ||
					payload["timestamp"] == "" || len(response.Header()) != 1 {
					t.Fatalf("unexpected timeout envelope or headers: %d %#v headers=%v", response.Code, payload, response.Header())
				}
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("result timeout did not cancel handler")
			}
			if err := lifecycle.hooks[0].OnStop(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServerPreservesNormalResponse(t *testing.T) {
	lifecycle := &hookLifecycle{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/custom")
		w.Header().Set("X-Route-Header", "kept")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("accepted"))
	})
	server := NewServer(lifecycle, mux, zap.NewNop(), &env.Env{HTTPAsyncTimeout: time.Second})
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusAccepted || response.Body.String() != "accepted" ||
		response.Header().Get("Content-Type") != "application/custom" ||
		response.Header().Get("X-Route-Header") != "kept" {
		t.Fatalf("normal response changed: %d %q headers=%v", response.Code, response.Body.String(), response.Header())
	}
	if err := lifecycle.hooks[0].OnStop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestServerAdmissionFailureUsesJSONEnvelope(t *testing.T) {
	lifecycle := &hookLifecycle{}
	mux := http.NewServeMux()
	entered := make(chan struct{})
	release := make(chan struct{})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte("accepted"))
	})
	server := NewServer(lifecycle, mux, zap.NewNop(), &env.Env{
		HTTPAsyncCoreSize: 1, HTTPAsyncMaxSize: 1, HTTPAsyncQueueSize: 0,
		HTTPAsyncTimeout: time.Second,
	})
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		server.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("first request did not enter handler")
	}

	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode admission error: %v; body=%s", err, response.Body.String())
	}
	if response.Code != http.StatusServiceUnavailable ||
		response.Header().Get("Content-Type") != "application/json" ||
		payload["error"] != "ASYNC_REQUEST_CAPACITY_EXHAUSTED" ||
		payload["message"] != "async request capacity exhausted" ||
		payload["timestamp"] == "" || len(response.Header()) != 1 {
		t.Fatalf("unexpected admission error envelope or headers: %d %#v headers=%v", response.Code, payload, response.Header())
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
	if err := lifecycle.hooks[0].OnStop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPShutdownBoundsTimedOutWork(t *testing.T) {
	lifecycle := &hookLifecycle{}
	mux := http.NewServeMux()
	release := make(chan struct{})
	finished := make(chan struct{})
	defer close(release)
	mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) {
		defer close(finished)
		<-release
	})
	server := NewServer(lifecycle, mux, zap.NewNop(), &env.Env{
		HTTPAsyncTimeout: 20 * time.Millisecond, HTTPShutdownTimeout: 40 * time.Millisecond,
	})
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("expected timed-out handler")
	}
	start := time.Now()
	if err := lifecycle.hooks[0].OnStop(context.Background()); err != context.DeadlineExceeded {
		t.Fatalf("shutdown ignored its deadline: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond || elapsed > time.Second {
		t.Fatalf("shutdown elapsed=%v, want 40ms", elapsed)
	}
}
