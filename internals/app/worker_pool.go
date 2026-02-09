package app

import (
	"context"
	"sync"
	"time"

	"github.com/ashishDevv/outbox-worker/config"
)

type WorkerPool struct {
	ctx         context.Context
	cancle      context.CancelFunc
	ticker      *time.Ticker
	WorkerCount int
	workers     []*Worker
	workCh      chan struct{}
	wg          *sync.WaitGroup
}

func NewWorkerPool(parentCtx context.Context, c *Container, cfg *config.Config) *WorkerPool {

	ctx, cancel := context.WithCancel(parentCtx)
	wg := &sync.WaitGroup{}
	workCh := make(chan struct{})

	var workers []*Worker

	for _, svc := range c.OutboxSvrs {
		workers = append(workers, NewWorker(ctx, svc, wg, workCh, c.Logger))
	}

	return &WorkerPool{
		ctx:         ctx,
		WorkerCount: len(workers),
		cancle: cancel,
		ticker: time.NewTicker(time.Duration(cfg.Runner.IntervalTimeSeconds)),
		workCh: workCh,
		workers: workers,
		wg: wg,
	}
}

// func (wp *WorkerPool) initWorkers() {
// 	for range wf.workerCount {
// 		// runnerID := fmt.Sprintf("runner:&v", i)
// 		runner := NewRunner(wf.ctx, wf.outboxSvc, 5*time.Second)
// 		wf.workers = append(wf.workers, runner)
// 	}
// }

func (wp *WorkerPool) StartWorkers() {
	for _, worker := range wp.workers {
		worker.Start()
	}

	// start scheduler
	go func() {
		for {
			select {
				case <-wp.ctx.Done():
				return
				case <-wp.ticker.C:
					select {
						case wp.workCh <- struct{}{}:
						default:
							// backpressure
							// no idle worker, drop tick
					}
			}
		}
	}()
}

func (wp *WorkerPool) ShutdownWorkers() {

	// 1. Stop scheduling
	wp.cancle()

	// 2. Stop ticker
	wp.ticker.Stop()

	// 3. close workCh channel so that idle workers exits
	close(wp.workCh)

	// 4. wait for completion of running workers
	wp.wg.Wait()
}
