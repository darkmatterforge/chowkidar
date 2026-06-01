package worker

import (
	"context"
	"log"
	"sync"
)

type Job interface {
	Run(ctx context.Context)
}

type Pool struct {
	jobs chan Job
	wg   sync.WaitGroup
}

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

func (p *Pool) Enqueue(job Job) {
	p.jobs <- job
}

func (p *Pool) Stop() {
	close(p.jobs)
	p.wg.Wait()
}
