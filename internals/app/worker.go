package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ashishDevv/outbox-worker/internals/outbox"
	"github.com/ashishDevv/outbox-worker/pkg/apperror"
	"github.com/rs/zerolog"
)

type Worker struct {
	ctx       context.Context
	outboxSvc *outbox.Service
	wg        *sync.WaitGroup
	workCh    <-chan struct{}
	log       *zerolog.Logger
}

func NewWorker(ctx context.Context, outboxSvc *outbox.Service, wg *sync.WaitGroup, workCh <-chan struct{}, logger *zerolog.Logger) *Worker {

	return &Worker{
		ctx:       ctx,
		outboxSvc: outboxSvc,
		wg:        wg,
		workCh:    workCh,
		log: logger,
	}
}

func (w *Worker) Start() {
	w.wg.Add(1)

	go func() {
		defer w.wg.Done()

		for {
			select {
			case <-w.ctx.Done():
				return
			case _, ok := <-w.workCh:
				if !ok {
					return
				}
				w.runOnce()

			}
		}
	}()
}

func (w *Worker) runOnce() {
	ctx, cancle := context.WithTimeout(w.ctx, 10*time.Second)
	defer cancle()

	if err := w.outboxSvc.ProcessOutboxEvents(ctx); err != nil {
		// log it here , reason of error
		var appErr *apperror.Error
		if errors.As(err, &appErr) {
			w.log.Error().
				Str("op", appErr.Op).
				Any("Kind", appErr.Kind).
				Str("errMsg", appErr.Message).
				Err(appErr.Err).
				Msg("outbox processing failed")
		}
	}
}

// func (r *Runner) doWork(ctx context.Context) {
// 	if r.running.Swap(true) {
// 		return // already running
// 	}
// 	defer r.running.Store(false)

// 	if err := r.outboxSvc.ProcessOutboxEvents(ctx); err != nil {
// 		log.Printf("outbox error: %v", err)
// 	}
// }
