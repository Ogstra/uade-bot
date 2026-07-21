package scheduler

import (
	"context"
	"sync"
	"testing"
)

func TestSchedulerCoordinatesJobsWithGlobalLimit(t *testing.T) {
	s := New(1)
	var mu sync.Mutex
	running, max := 0, 0
	for i := 0; i < 3; i++ {
		s.Add(Job{Account: "a", ID: string(rune('0' + i)), Run: func(context.Context) error {
			mu.Lock()
			running++
			if running > max {
				max = running
			}
			mu.Unlock()
			mu.Lock()
			running--
			mu.Unlock()
			return nil
		}})
	}
	s.RunOnce(context.Background())
	if max != 1 {
		t.Fatalf("max=%d", max)
	}
}
