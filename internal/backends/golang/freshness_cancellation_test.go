package golang

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestCanceledDispatchDoesNotCountUnstartedWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	var wg sync.WaitGroup
	if acquireWitnessSlot(ctx, sem, &wg) {
		t.Fatal("acquired a witness slot after cancellation")
	}
	<-sem
	for range 100 {
		if acquireWitnessSlot(ctx, sem, &wg) {
			t.Fatal("acquired a free witness slot after cancellation")
		}
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled dispatch counted a worker that was never started")
	}
}
