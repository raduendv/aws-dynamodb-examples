// This suite checks dispatch context, worker lifecycle, and bounded shutdown of blocked jobs.
package workerpool

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
	"instantpayments/pkg/workerpool/job"
)

// testJob delegates execution to a caller-supplied function for worker coordination.
type testJob struct {
	run func(context.Context) error
}

// workerHookLifecycle captures dispatcher hooks without running an Fx application.
type workerHookLifecycle struct {
	hooks []fx.Hook
}

// Append records a hook for explicit lifecycle assertions.
func (lifecycle *workerHookLifecycle) Append(hook fx.Hook) {
	lifecycle.hooks = append(lifecycle.hooks, hook)
}

// Run forwards the worker-provided context to the test function.
func (j testJob) Run(ctx context.Context) error {
	return j.run(ctx)
}

func TestDispatcherShutdownHonorsStopDeadline(t *testing.T) {
	lifecycle := &workerHookLifecycle{}
	pool := NewDispatcher(lifecycle, &env.Env{WorkerPoolSize: 1, JobPoolSize: 1}, zap.NewNop(), nil, nil)
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	pool.Dispatch(testJob{run: func(context.Context) error {
		close(started)
		<-release
		return nil
	}})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := lifecycle.hooks[0].OnStop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded shutdown, got %v", err)
	}
}

func TestWorkerProcessesDispatchedJobWithContextValues(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := &WorkerPool{JobPool: make(chan job.Job, 1)}
	worker := newWorker(ctx, pool, 1, zap.NewNop())
	completed := make(chan struct{})
	var once sync.Once
	go worker.Start()

	pool.Dispatch(testJob{run: func(jobContext context.Context) error {
		if jobContext.Value(job.ContextKeyLogger{}) == nil || jobContext.Value(job.ContextKeyWorkerPool{}) != pool {
			t.Errorf("worker did not attach expected context values")
		}
		once.Do(func() { close(completed) })
		return nil
	}})

	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("worker did not execute dispatched job")
	}
}

func TestNewDispatcherLifecycle(t *testing.T) {
	lifecycle := &workerHookLifecycle{}
	dispatcher := NewDispatcher(lifecycle, &env.Env{WorkerPoolSize: 1, JobPoolSize: 1}, zap.NewNop(), nil, nil)
	if dispatcher == nil || cap(dispatcher.JobPool) != 1 || len(lifecycle.hooks) != 1 {
		t.Fatalf("unexpected dispatcher setup %#v hooks=%d", dispatcher, len(lifecycle.hooks))
	}
	hook := lifecycle.hooks[0]
	if err := hook.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := hook.OnStop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDispatcherShutdownDrainsActiveAndQueuedJobsBeforeCanceling(t *testing.T) {
	lifecycle := &workerHookLifecycle{}
	pool := NewDispatcher(lifecycle, &env.Env{WorkerPoolSize: 1, JobPoolSize: 2}, zap.NewNop(), nil, nil)
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	completed := make(chan int, 3)
	if err := pool.Dispatch(testJob{run: func(ctx context.Context) error {
		started <- ctx
		select {
		case <-release:
			completed <- 0
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}); err != nil {
		t.Fatal(err)
	}
	jobCtx := <-started
	for n := 1; n <= 2; n++ {
		if err := pool.Dispatch(testJob{run: func(ctx context.Context) error {
			if ctx.Err() != nil {
				t.Errorf("queued job canceled during grace: %v", ctx.Err())
			}
			completed <- n
			return nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- lifecycle.hooks[0].OnStop(ctx) }()
	<-pool.stopping
	if err := jobCtx.Err(); err != nil {
		t.Fatalf("shutdown immediately canceled active work: %v", err)
	}
	if err := pool.Dispatch(testJob{}); !errors.Is(err, ErrPoolStopped) {
		t.Fatalf("shutdown still admits work: %v", err)
	}
	close(release)
	if err := <-stopped; err != nil {
		t.Fatalf("graceful drain failed: %v", err)
	}
	for want := 0; want < 3; want++ {
		select {
		case got := <-completed:
			if got != want {
				t.Fatalf("completion order = %d, want %d", got, want)
			}
		default:
			t.Fatal("shutdown returned before draining all jobs")
		}
	}
	if err := pool.Shutdown(context.Background()); err != nil {
		t.Fatalf("repeated shutdown failed: %v", err)
	}
}

func TestDispatcherShutdownForcesCancellationAndDoesNotRunQueuedJobs(t *testing.T) {
	for _, callerCanceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "caller cancellation"}[callerCanceled], func(t *testing.T) {
			lifecycle := &workerHookLifecycle{}
			pool := NewDispatcher(lifecycle, &env.Env{WorkerPoolSize: 1, JobPoolSize: 1}, zap.NewNop(), nil, nil)
			started, canceled, queuedRan := make(chan struct{}), make(chan struct{}), make(chan struct{})
			pool.Dispatch(testJob{run: func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				close(canceled)
				return ctx.Err()
			}})
			<-started
			pool.Dispatch(testJob{run: func(context.Context) error {
				close(queuedRan)
				return nil
			}})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			want := context.DeadlineExceeded
			if callerCanceled {
				cancel()
				want = context.Canceled
			}
			if err := lifecycle.hooks[0].OnStop(ctx); !errors.Is(err, want) {
				t.Fatalf("forced stop must report %v, got %v", want, err)
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("timeout did not cancel the active job")
			}
			select {
			case <-pool.done:
			case <-time.After(time.Second):
				t.Fatal("cooperative worker did not stop")
			}
			select {
			case <-queuedRan:
				t.Fatal("forced stop started queued work")
			default:
			}
		})
	}
}

func TestDispatcherShutdownReleasesBlockedAdmission(t *testing.T) {
	lifecycle := &workerHookLifecycle{}
	pool := NewDispatcher(lifecycle, &env.Env{WorkerPoolSize: 1, JobPoolSize: 1}, zap.NewNop(), nil, nil)
	started, release := make(chan struct{}), make(chan struct{})
	pool.Dispatch(testJob{run: func(context.Context) error {
		close(started)
		<-release
		return nil
	}})
	<-started
	pool.Dispatch(testJob{run: func(context.Context) error { return nil }})
	admitted := make(chan error, 1)
	go func() { admitted <- pool.Dispatch(testJob{}) }()
	stopped := make(chan error, 1)
	go func() { stopped <- pool.Shutdown(context.Background()) }()
	select {
	case err := <-admitted:
		if !errors.Is(err, ErrPoolStopped) {
			t.Fatalf("blocked admission was not rejected: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked behind queue admission")
	}
	close(release)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}
