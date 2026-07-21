package scheduler

import (
	"context"
	"sync"
	"time"
)

type Job struct {
	Account string
	ID      string
	Run     func(context.Context) error
}
type Scheduler struct {
	mu   sync.Mutex
	jobs map[string][]Job
	sem  chan struct{}
	next map[string]time.Time
}

func New(limit int) *Scheduler {
	if limit < 1 {
		limit = 1
	}
	return &Scheduler{jobs: map[string][]Job{}, sem: make(chan struct{}, limit), next: map[string]time.Time{}}
}
func (s *Scheduler) Add(job Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.Account] = append(s.jobs[job.Account], job)
}
func (s *Scheduler) Accounts() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.jobs) }
func (s *Scheduler) RunOnce(ctx context.Context) {
	s.mu.Lock()
	snapshot := make([]Job, 0)
	for _, jobs := range s.jobs {
		snapshot = append(snapshot, jobs...)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, job := range snapshot {
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case s.sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-s.sem }()
			_ = job.Run(ctx)
		}()
	}
	wg.Wait()
}
