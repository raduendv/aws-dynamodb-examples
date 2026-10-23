package workerpool

import (
	"context"
	"errors"
	"fmt"
	"instantpayments/pkg/env"
	"instantpayments/pkg/workerpool/job"
	"sync"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"
)

// ShutdownGracePeriod bounds draining so a stuck job cannot indefinitely block application shutdown.
const ShutdownGracePeriod = 10 * time.Second

// ErrPoolStopped reports that shutdown has closed job admission.
var ErrPoolStopped = errors.New("worker pool is stopping")

// WorkerPool dispatches jobs to a bounded set of workers.
type WorkerPool struct {
	// JobPool is exposed for compatibility; producers must use DispatchContext to coordinate shutdown.
	JobPool    chan job.Job
	repository job.Repository
	processor  job.PaymentProcessor
	wg         sync.WaitGroup
	admission  sync.RWMutex
	stopOnce   sync.Once
	stopping   chan struct{}
	done       chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
}

// Dispatch queues a job, or returns ErrPoolStopped once admission closes.
func (d *WorkerPool) Dispatch(j job.Job) error {
	return d.DispatchContext(context.Background(), j)
}

// DispatchContext waits for queue capacity until cancellation or shutdown.
// The admission lock prevents a send from racing with queue closure.
func (d *WorkerPool) DispatchContext(ctx context.Context, j job.Job) error {
	d.admission.RLock()
	defer d.admission.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-d.stopping:
		return ErrPoolStopped
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.stopping:
		return ErrPoolStopped
	case d.JobPool <- j:
		return nil
	}
}

// Shutdown closes admission and drains accepted jobs within the caller's deadline
// and ShutdownGracePeriod. A timeout cancels running jobs and returns an explicit
// error without waiting for jobs that ignore cancellation. Repeated calls are safe.
func (d *WorkerPool) Shutdown(ctx context.Context) error {
	d.stopOnce.Do(func() {
		close(d.stopping)
		d.admission.Lock()
		close(d.JobPool)
		d.admission.Unlock()
	})
	if err := ctx.Err(); err != nil {
		d.cancel()
		return fmt.Errorf("wait for worker pool shutdown: %w", err)
	}
	select {
	case <-d.done:
		return nil
	default:
	}
	if err := d.ctx.Err(); err != nil {
		return fmt.Errorf("worker pool was force-stopped: %w", err)
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, ShutdownGracePeriod)
	defer cancel()
	select {
	case <-shutdownCtx.Done():
		d.cancel()
		return fmt.Errorf("wait for worker pool shutdown: %w", shutdownCtx.Err())
	case <-d.done:
		if err := shutdownCtx.Err(); err != nil {
			d.cancel()
			return fmt.Errorf("wait for worker pool shutdown: %w", err)
		}
		return nil
	}
}

// NewDispatcher starts workers with an independent processing context and registers
// their bounded draining hook. Stream shutdown can call Shutdown first because Fx
// stops dependent hooks in reverse registration order.
func NewDispatcher(lc fx.Lifecycle, env *env.Env, log *zap.Logger, repository job.Repository, processor job.PaymentProcessor) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())

	wp := &WorkerPool{
		JobPool:    make(chan job.Job, env.JobPoolSize),
		repository: repository,
		processor:  processor,
		stopping:   make(chan struct{}),
		done:       make(chan struct{}),
		ctx:        ctx,
		cancel:     cancel,
	}
	for c := 0; c < env.WorkerPoolSize; c++ {
		wp.wg.Add(1)
		w := newWorker(ctx, wp, c, log)
		go func() {
			defer wp.wg.Done()
			w.Start()
		}()
	}
	go func() {
		defer cancel()
		wp.wg.Wait()
		close(wp.done)
	}()

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			log.Info("starting worker pool")
			log.Info("started worker pool")
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			log.Info("stopping worker pool")
			if err := wp.Shutdown(stopCtx); err != nil {
				return err
			}
			log.Info("stopped worker pool")
			return nil
		},
	})

	return wp
}
