package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return nil
}

type memoryStore struct {
	mu            sync.Mutex
	active        []PersistedJob
	accounts      map[string]AccountState
	notifications map[string]NotificationState
	writes        int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{accounts: map[string]AccountState{}, notifications: map[string]NotificationState{}}
}
func (s *memoryStore) ActiveJobs(context.Context) ([]PersistedJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PersistedJob(nil), s.active...), nil
}
func (s *memoryStore) AccountState(_ context.Context, account string) (AccountState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accounts[account], nil
}
func (s *memoryStore) SaveAccountState(_ context.Context, account string, state AccountState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state.LastPauseNotifiedReason = s.accounts[account].LastPauseNotifiedReason
	s.accounts[account] = state
	s.writes++
	return nil
}
func (s *memoryStore) NotificationState(_ context.Context, id string) (NotificationState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notifications[id], nil
}
func (s *memoryStore) SaveNotificationState(_ context.Context, id string, state NotificationState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifications[id] = state
	s.writes++
	return nil
}
func (s *memoryStore) SaveOutcome(context.Context, string, Outcome, time.Time) error {
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	return nil
}
func (s *memoryStore) MarkPauseNotification(_ context.Context, account, reason string) error {
	s.mu.Lock()
	state := s.accounts[account]
	state.LastPauseNotifiedReason = reason
	s.accounts[account] = state
	s.writes++
	s.mu.Unlock()
	return nil
}

type recordingNotifier struct {
	mu     sync.Mutex
	events []Event
	fail   bool
}

func (n *recordingNotifier) Notify(_ context.Context, event Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, event)
	if n.fail {
		return errors.New("send failed")
	}
	return nil
}
func (n *recordingNotifier) count() int { n.mu.Lock(); defer n.mu.Unlock(); return len(n.events) }

func outcomeRun(code string) func(context.Context) (Outcome, error) {
	return func(context.Context) (Outcome, error) { return Outcome{Code: code}, nil }
}

func TestRunOnceUsesGlobalCapAndRoundRobinPerAccount(t *testing.T) {
	s := New(2)
	var running, maximum atomic.Int32
	run := func(id string) func(context.Context) (Outcome, error) {
		return func(context.Context) (Outcome, error) {
			current := running.Add(1)
			for {
				old := maximum.Load()
				if current <= old || maximum.CompareAndSwap(old, current) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
			return Outcome{Code: "no_vacancies"}, nil
		}
	}
	var mu sync.Mutex
	order := []string{}
	for _, spec := range []struct{ account, id string }{{"a", "a1"}, {"a", "a2"}, {"b", "b1"}, {"c", "c1"}} {
		spec := spec
		base := run(spec.id)
		s.Add(Job{Account: spec.account, ID: spec.id, Run: func(ctx context.Context) (Outcome, error) {
			mu.Lock()
			order = append(order, spec.id)
			mu.Unlock()
			return base(ctx)
		}})
	}
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 2 {
		t.Fatalf("global maximum=%d want 2", maximum.Load())
	}
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	counts := map[string]int{}
	for _, id := range order {
		counts[id]++
	}
	if counts["a1"] != 1 || counts["a2"] != 1 || counts["b1"] != 2 || counts["c1"] != 2 {
		t.Fatalf("round robin/order counts=%v", counts)
	}
}

func TestImmediateQueueSerializesAccountAndDeduplicates(t *testing.T) {
	s := New(3)
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	order := []string{}
	s.Add(Job{Account: "a", ID: "1", Run: func(context.Context) (Outcome, error) {
		close(started)
		<-release
		mu.Lock()
		order = append(order, "1")
		mu.Unlock()
		return Outcome{Code: "no_vacancies"}, nil
	}})
	for _, id := range []string{"2", "3"} {
		id := id
		s.Add(Job{Account: "a", ID: id, Run: func(context.Context) (Outcome, error) {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			return Outcome{Code: "no_vacancies"}, nil
		}})
	}
	done := make(chan error, 1)
	go func() { done <- s.RunOnce(context.Background()) }()
	<-started
	if !s.PollNow(context.Background(), "2") || !s.PollNow(context.Background(), "3") {
		t.Fatal("distinct immediate jobs must queue")
	}
	if s.PollNow(context.Background(), "2") {
		t.Fatal("duplicate immediate job must be suppressed")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := len(order); got != 3 || order[0] != "1" || order[1] != "2" || order[2] != "3" {
		t.Fatalf("order=%v", order)
	}
}

func TestBackoffAndPauseUseInjectedClock(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	state := AccountState{}
	wants := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for i, want := range wants {
		state = NextBackoff(state, "rate_limited", now)
		if state.BackoffAttempt != i+1 || !state.PauseUntil.Equal(now.Add(want)) {
			t.Fatalf("attempt %d state=%+v", i+1, state)
		}
	}
	state = NextBackoff(state, "success", now)
	if state.Reason != "" || state.BackoffAttempt != 0 {
		t.Fatalf("success did not reset: %+v", state)
	}
	if got := NextBackoff(state, "invalid_credentials", now); got.Reason != "needs_credentials" || !got.PauseUntil.IsZero() {
		t.Fatalf("credential pause=%+v", got)
	}

	clock := &fakeClock{now: now}
	store := newMemoryStore()
	store.accounts["a"] = AccountState{Reason: "rate_limited", PauseUntil: now.Add(time.Minute), BackoffAttempt: 1}
	s := New(1, WithClock(clock), WithStore(store))
	var calls atomic.Int32
	s.Add(Job{Account: "a", ID: "1", Run: func(context.Context) (Outcome, error) { calls.Add(1); return Outcome{Code: "no_vacancies"}, nil }})
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("paused account ran")
	}
	clock.now = now.Add(time.Minute)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("expired pause did not resume")
	}
}

func TestReconstructIsIdempotent(t *testing.T) {
	store := newMemoryStore()
	store.active = []PersistedJob{{Account: "a", ID: "1"}, {Account: "a", ID: "2"}, {Account: "b", ID: "3"}}
	factory := func(record PersistedJob) (Job, error) {
		return Job{Account: record.Account, ID: record.ID, Run: outcomeRun("no_vacancies")}, nil
	}
	s := New(1, WithStore(store))
	added, err := s.Reconstruct(context.Background(), factory)
	if err != nil || added != 3 {
		t.Fatalf("first added=%d err=%v", added, err)
	}
	added, err = s.Reconstruct(context.Background(), factory)
	if err != nil || added != 0 || s.Jobs() != 3 {
		t.Fatalf("repeat added=%d jobs=%d err=%v", added, s.Jobs(), err)
	}
	// A new process reconstructs the same durable rows once, without DB duplication.
	restarted := New(1, WithStore(store))
	added, err = restarted.Reconstruct(context.Background(), factory)
	if err != nil || added != 3 || len(store.active) != 3 {
		t.Fatalf("restart added=%d rows=%d err=%v", added, len(store.active), err)
	}
}

func TestNotificationDedupPersistsAcrossRestartAndRetriesFailures(t *testing.T) {
	store := newMemoryStore()
	notifier := &recordingNotifier{}
	vacancy := Vacancy{Turno: "Noche", Sede: "Lima", Horario: "18:30", Dias: []string{"Lunes"}, Cupos: 1}
	makeScheduler := func(n Notifier) *Scheduler {
		s := New(1, WithStore(store), WithNotifier(n))
		s.Add(Job{Account: "a", ID: "1", Run: func(context.Context) (Outcome, error) {
			return Outcome{Code: "found", Vacancies: []Vacancy{vacancy}}, nil
		}})
		return s
	}
	if err := makeScheduler(notifier).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := makeScheduler(notifier).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notifier.count() != 1 {
		t.Fatalf("duplicate sent across restart: %d", notifier.count())
	}
	vacancy.Cupos = 2
	if err := makeScheduler(notifier).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notifier.count() != 2 {
		t.Fatal("cupos increase was not sent")
	}
	failing := &recordingNotifier{fail: true}
	store.notifications["1"] = NotificationState{}
	if err := makeScheduler(failing).RunOnce(context.Background()); err == nil {
		t.Fatal("send failure should surface")
	}
	if store.notifications["1"].VacancyKey != "" {
		t.Fatal("failed send must not persist dedup state")
	}
}

func TestShadowModeNeverWritesOrNotifies(t *testing.T) {
	store := newMemoryStore()
	notifier := &recordingNotifier{}
	s := New(1, WithStore(store), WithNotifier(notifier), WithShadow(true))
	s.Add(Job{Account: "a", ID: "1", Run: func(context.Context) (Outcome, error) {
		return Outcome{Code: "found", Vacancies: []Vacancy{{Cupos: 1}}}, nil
	}})
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.writes != 0 || notifier.count() != 0 {
		t.Fatalf("shadow writes=%d notifications=%d", store.writes, notifier.count())
	}
}

func TestThrottledNotifierRemainsUsableAfterFailure(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	next := &recordingNotifier{fail: true}
	throttled := &ThrottledNotifier{Next: next, Clock: clock, Delay: 250 * time.Millisecond}
	if err := throttled.Notify(context.Background(), Event{}); err == nil {
		t.Fatal("expected first failure")
	}
	next.fail = false
	if err := throttled.Notify(context.Background(), Event{}); err != nil {
		t.Fatal(err)
	}
	if len(clock.sleeps) != 2 || clock.sleeps[0] != 250*time.Millisecond || next.count() != 2 {
		t.Fatalf("sleeps=%v sends=%d", clock.sleeps, next.count())
	}
}
