package worker

import (
	"context"
	"testing"
	"time"
)

type blockingJob struct {
	started chan struct{}
	release chan struct{}
}

func (j blockingJob) Run(_ context.Context) {
	close(j.started)
	<-j.release
}

func TestPoolStopWaitsForRunningJob(t *testing.T) {
	pool := NewPool(1, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	pool.Enqueue(blockingJob{started: started, release: release})

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("job did not start")
	}

	stopped := make(chan struct{})
	go func() {
		pool.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("pool stopped before running job was released")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("pool did not stop after job release")
	}
}
