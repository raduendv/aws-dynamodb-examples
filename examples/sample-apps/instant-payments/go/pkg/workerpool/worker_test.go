package workerpool

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"instantpayments/pkg/env"
	"instantpayments/pkg/workerpool/job"
)

type testJob struct {
	run func(context.Context) error
}

type workerHookLifecycle struct {
	hooks []fx.Hook
}

func (lifecycle *workerHookLifecycle) Append(hook fx.Hook) {
	lifecycle.hooks = append(lifecycle.hooks, hook)
}

func (j testJob) Run(ctx context.Context) error {
	return j.run(ctx)
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
	dispatcher := NewDispatcher(lifecycle, &env.Env{WorkerPoolSize: 1, JobPoolSize: 1}, zap.NewNop(), nil)
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
