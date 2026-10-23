package workerpool

import (
	"context"

	"instantpayments/pkg/workerpool/job"

	"go.uber.org/zap"
)

// Worker runs admitted jobs serially using the pool's shared processing context.
type Worker struct {
	id         int
	log        *zap.Logger
	ctx        context.Context
	workerPool *WorkerPool
}

// Start drains the queue until it closes; forced cancellation stops before the next job.
// Each dispatched completion is acknowledged before its outcome is logged.
func (w *Worker) Start() {
	w.log.Info("Starting worker", zap.Int("id", w.id))
	for {
		if w.ctx.Err() != nil {
			return
		}
		select {
		case <-w.ctx.Done():
			w.log.Debug("worker stopped")

			return
		case j, ok := <-w.workerPool.JobPool:
			if !ok || w.ctx.Err() != nil {
				return
			}

			w.log.Info("Worker received job", zap.Int("id", w.id))

			c := context.WithValue(w.ctx, job.ContextKeyLogger{}, w.log)
			c = context.WithValue(c, job.ContextKeyWorkerPool{}, w.workerPool)
			c = context.WithValue(c, job.ContextKeyRepository{}, w.workerPool.repository)
			c = context.WithValue(c, job.ContextKeyPaymentProcessor{}, w.workerPool.processor)

			err := j.Run(c)
			if result, ok := j.(interface{ Complete(error) }); ok {
				result.Complete(err)
			}
			if job.IsExpectedOutcome(err) {
				w.log.Debug("Worker completed job with expected outcome", zap.Int("id", w.id), zap.Error(err))
			} else if err != nil {
				w.log.Error("Worker failed to run job", zap.Int("id", w.id), zap.Error(err))
			} else {
				w.log.Info("Worker completed job", zap.Int("id", w.id))
			}
		}
	}
}

// newWorker binds a worker to its pool dependencies and cancellation boundary.
func newWorker(ctx context.Context, workerPool *WorkerPool, id int, log *zap.Logger) Worker {
	return Worker{workerPool: workerPool, id: id, ctx: ctx, log: log}
}
