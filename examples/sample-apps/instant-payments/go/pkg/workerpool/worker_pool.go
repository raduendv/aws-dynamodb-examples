package workerpool

import (
	"context"
	"instantpayments/pkg/env"
	"instantpayments/pkg/workerpool/job"
	"sync"

	"go.uber.org/fx"
	"go.uber.org/zap"
)

// WorkerPool dispatches jobs to a bounded set of workers.
type WorkerPool struct {
	JobPool    chan job.Job
	repository job.Repository
	wg         sync.WaitGroup
}

// Dispatch queues a job for worker-pool processing.
func (d *WorkerPool) Dispatch(j job.Job) {
	d.JobPool <- j
}

// NewDispatcher creates a worker pool and manages its shutdown lifecycle.
func NewDispatcher(lc fx.Lifecycle, env *env.Env, log *zap.Logger, repository job.Repository) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())

	wp := &WorkerPool{
		JobPool:    make(chan job.Job, env.JobPoolSize),
		repository: repository,
	}

	for c := 0; c < env.WorkerPoolSize; c++ {
		wp.wg.Add(1)
		w := newWorker(ctx, wp, c, log)
		go func() {
			defer wp.wg.Done()
			w.Start()
		}()
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			log.Info("starting worker pool")
			log.Info("started worker pool")
			return nil
		},
		OnStop: func(context.Context) error {
			log.Info("stopping worker pool")
			cancel()
			wp.wg.Wait() // Wait for all workers to finish
			close(wp.JobPool)
			log.Info("stopped worker pool")
			return nil
		},
	})

	return wp
}
