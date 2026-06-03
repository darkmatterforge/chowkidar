package worker

import (
	"context"
	"log"
	"sync"
)

// Job is the unit of work executed by a Pool worker.
type Job interface {
	Run(ctx context.Context)
}

// Pool is a fixed-size goroutine pool with a bounded work queue.
type Pool struct {
	jobs chan Job
	wg   sync.WaitGroup
}

// NewPool starts workerCount goroutines each draining a channel of size queueSize.
func NewPool(workerCount int, queueSize int) *Pool {
	p := &Pool{jobs: make(chan Job, queueSize)}
	for i := 0; i < workerCount; i++ {
		p.wg.Add(1)
		go func(workerID int) {
			defer p.wg.Done()
			for job := range p.jobs {
				job.Run(context.Background())
			}
			log.Printf("worker %d stopped", workerID)
		}(i + 1)
	}
	return p
}

// Enqueue submits a job to the pool. Blocks when the queue is full.
func (p *Pool) Enqueue(job Job) {
	p.jobs <- job
}

// Stop drains the queue, waits for all in-flight jobs to finish, and shuts
// down all worker goroutines.
func (p *Pool) Stop() {
	close(p.jobs)
	p.wg.Wait()
}
