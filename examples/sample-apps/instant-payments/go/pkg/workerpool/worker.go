package workerpool

import (
	"context"

	"instantpayments/pkg/workerpool/job"

	"go.uber.org/zap"
)

type Worker struct {
	id         int
	log        *zap.Logger
	ctx        context.Context
	workerPool *WorkerPool
}

func (w *Worker) Start() {
	w.log.Info("Starting worker", zap.Int("id", w.id))
	for {
		select {
		case <-w.ctx.Done():
			w.log.Debug("worker stopped")

			return
		case j, ok := <-w.workerPool.JobPool:
			if !ok {
				return
			}

			w.log.Info("Worker received job", zap.Int("id", w.id))

			c := context.WithValue(w.ctx, job.ContextKeyLogger{}, w.log)
			c = context.WithValue(c, job.ContextKeyWorkerPool{}, w.workerPool)
			c = context.WithValue(c, job.ContextKeyRepository{}, w.workerPool.repository)

			err := j.Run(c)
			if err != nil {
				w.log.Error("Worker failed to run job", zap.Int("id", w.id), zap.Error(err))
			} else {
				w.log.Info("Worker completed job", zap.Int("id", w.id))
			}
		}
	}
}

func newWorker(ctx context.Context, workerPool *WorkerPool, id int, log *zap.Logger) Worker {
	return Worker{workerPool: workerPool, id: id, ctx: ctx, log: log}
}
