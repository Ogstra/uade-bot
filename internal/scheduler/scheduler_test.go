package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
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
	progress      map[string][]NotificationFragment
	commits       int
	writes        int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{accounts: map[string]AccountState{}, notifications: map[string]NotificationState{}, progress: map[string][]NotificationFragment{}}
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
func (s *memoryStore) DeliveredNotificationFragments(_ context.Context, id, delivery string) ([]NotificationFragment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]NotificationFragment(nil), s.progress[id+"|"+delivery]...), nil
}
func (s *memoryStore) MarkNotificationFragmentDelivered(_ context.Context, id, delivery string, fragment NotificationFragment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := id + "|" + delivery
	s.progress[key] = append(s.progress[key], fragment)
	return nil
}
func (s *memoryStore) CommitNotificationDelivery(_ context.Context, id, delivery string, state NotificationState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifications[id] = state
	delete(s.progress, id+"|"+delivery)
	s.commits++
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
	mu       sync.Mutex
	events   []Event
	fail     bool
	onNotify func(Event) error
}

func (n *recordingNotifier) Notify(_ context.Context, event Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, event)
	if n.onNotify != nil {
		if err := n.onNotify(event); err != nil {
			return err
		}
	}
	if n.fail {
		return errors.New("send failed")
	}
	return nil
}
func (n *recordingNotifier) count() int { n.mu.Lock(); defer n.mu.Unlock(); return len(n.events) }

func (n *recordingNotifier) last() Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.events[len(n.events)-1]
}

const schedulerPanicSentinel = "scheduler-secret-sentinel password=UadePass!123 param=eyJhbHVtSWQiOiIxIn0="

type panicOnceStore struct {
	*memoryStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *panicOnceStore) SaveOutcome(ctx context.Context, id string, outcome Outcome, at time.Time) error {
	panicked := false
	s.once.Do(func() {
		panicked = true
		close(s.entered)
		<-s.release
	})
	if panicked {
		panic(schedulerPanicSentinel)
	}
	return s.memoryStore.SaveOutcome(ctx, id, outcome, at)
}

type panicOnceNotifier struct {
	mu       sync.Mutex
	attempts int
}

func (n *panicOnceNotifier) Notify(context.Context, Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.attempts++
	if n.attempts == 1 {
		panic(schedulerPanicSentinel)
	}
	return nil
}

func (n *panicOnceNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.attempts
}

func captureStandardLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	originalPrefix := log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
		log.SetPrefix(originalPrefix)
	})
	return &output
}

func assertSchedulerRecoverySanitized(t *testing.T, value, marker string) {
	t.Helper()
	if !strings.Contains(value, marker) {
		t.Fatalf("recovery output %q does not contain %q", value, marker)
	}
	for _, secret := range []string{"scheduler-secret-sentinel", "UadePass!123", "eyJhbHVtSWQiOiIxIn0="} {
		if strings.Contains(value, secret) {
			t.Fatalf("recovery output leaked %q: %q", secret, value)
		}
	}
}

func assertAccountReleased(t *testing.T, s *Scheduler, account string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sem) != 0 || s.inFlight[account] || len(s.immediate[account]) != 0 {
		t.Fatalf("scheduler cleanup sem=%d inFlight=%v immediate=%v", len(s.sem), s.inFlight[account], s.immediate[account])
	}
	for _, job := range s.jobs[account] {
		if s.queued[job.ID] {
			t.Fatalf("job %s remains queued after account cleanup", job.ID)
		}
	}
}

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

func TestRunAccountRecoversFromJobPanicAndAccountStaysPollable(t *testing.T) {
	logs := captureStandardLog(t)
	s := New(1)
	var calls atomic.Int32
	s.Add(Job{Account: "a", ID: "1", Run: func(context.Context) (Outcome, error) {
		calls.Add(1)
		panic(schedulerPanicSentinel)
	}})

	err := s.RunOnce(context.Background())
	if err == nil {
		t.Fatal("first RunOnce returned nil after job panic")
	}
	assertSchedulerRecoverySanitized(t, err.Error(), "job 1 run panic recovered")

	err = s.RunOnce(context.Background())
	if err == nil {
		t.Fatal("second RunOnce returned nil after job panic")
	}
	assertSchedulerRecoverySanitized(t, err.Error(), "job 1 run panic recovered")
	assertSchedulerRecoverySanitized(t, logs.String(), "scheduler job run panic recovered")

	if got := calls.Load(); got != 2 {
		t.Fatalf("calls=%d, want 2 (account must remain pollable after a recovered panic)", got)
	}
	assertAccountReleased(t, s, "a")
}

func TestRunAccountRecoversFromStorePanicAndDrainsImmediateQueue(t *testing.T) {
	logs := captureStandardLog(t)
	store := &panicOnceStore{memoryStore: newMemoryStore(), entered: make(chan struct{}), release: make(chan struct{})}
	s := New(1, WithStore(store))
	var firstCalls, immediateCalls atomic.Int32
	s.Add(Job{Account: "a", ID: "1", Run: func(context.Context) (Outcome, error) {
		firstCalls.Add(1)
		return Outcome{Code: "no_vacancies"}, nil
	}})
	s.Add(Job{Account: "a", ID: "2", Run: func(context.Context) (Outcome, error) {
		immediateCalls.Add(1)
		return Outcome{Code: "no_vacancies"}, nil
	}})

	done := make(chan error, 1)
	go func() { done <- s.RunOnce(context.Background()) }()
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SaveOutcome")
	}
	if !s.PollNow(context.Background(), "2") {
		t.Fatal("immediate poll must be accepted while the account is in flight")
	}
	close(store.release)

	var err error
	select {
	case err = <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recovered store panic")
	}
	if err == nil {
		t.Fatal("RunOnce returned nil after store panic")
	}
	assertSchedulerRecoverySanitized(t, err.Error(), "job 1 pipeline panic recovered")
	assertSchedulerRecoverySanitized(t, logs.String(), "scheduler job pipeline panic recovered")
	if firstCalls.Load() != 1 || immediateCalls.Load() != 1 {
		t.Fatalf("calls first=%d immediate=%d, want 1 each", firstCalls.Load(), immediateCalls.Load())
	}
	assertAccountReleased(t, s, "a")

	if err = s.RunOnce(context.Background()); err != nil {
		t.Fatalf("subsequent RunOnce failed: %v", err)
	}
	if immediateCalls.Load() != 2 {
		t.Fatalf("subsequent work calls=%d, want 2", immediateCalls.Load())
	}
	assertAccountReleased(t, s, "a")
}

func TestRunAccountRecoversFromNotifierPanicAndRemainsPollable(t *testing.T) {
	logs := captureStandardLog(t)
	store := newMemoryStore()
	notifier := &panicOnceNotifier{}
	s := New(1, WithStore(store), WithNotifier(notifier))
	vacancy := Vacancy{Turno: "Noche", Sede: "Lima", Horario: "18:30", Dias: []string{"Lunes"}, Cupos: 1}
	s.Add(Job{Account: "a", ID: "1", Run: func(context.Context) (Outcome, error) {
		return Outcome{Code: "found", Vacancies: []Vacancy{vacancy}}, nil
	}})

	err := s.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce returned nil after notifier panic")
	}
	assertSchedulerRecoverySanitized(t, err.Error(), "job 1 pipeline panic recovered")
	assertSchedulerRecoverySanitized(t, logs.String(), "scheduler job pipeline panic recovered")
	if state := store.notifications["1"]; state.VacancyKey != "" {
		t.Fatalf("notification dedup persisted after panic: %+v", state)
	}
	assertAccountReleased(t, s, "a")

	if err = s.RunOnce(context.Background()); err != nil {
		t.Fatalf("retry RunOnce failed: %v", err)
	}
	if notifier.count() != 2 {
		t.Fatalf("notify attempts=%d, want 2", notifier.count())
	}
	if state := store.notifications["1"]; state.VacancyKey == "" {
		t.Fatal("notification dedup was not persisted after successful retry")
	}
	assertAccountReleased(t, s, "a")
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

func TestReconcileAddsAndRemovesDurableJobs(t *testing.T) {
	store := newMemoryStore()
	store.active = []PersistedJob{{Account: "a", ID: "1"}, {Account: "a", ID: "2"}}
	factory := func(record PersistedJob) (Job, error) {
		return Job{Account: record.Account, ID: record.ID, Run: outcomeRun("no_vacancies")}, nil
	}
	s := New(1, WithStore(store))
	if added, err := s.Reconcile(context.Background(), factory); err != nil || added != 2 {
		t.Fatalf("first reconcile added=%d err=%v", added, err)
	}
	store.active = []PersistedJob{{Account: "a", ID: "2"}, {Account: "b", ID: "3"}}
	if added, err := s.Reconcile(context.Background(), factory); err != nil || added != 1 {
		t.Fatalf("second reconcile added=%d err=%v", added, err)
	}
	removed := s.PollNow(context.Background(), "1")
	newJob := s.PollNow(context.Background(), "3")
	if s.Jobs() != 2 || removed || !newJob {
		t.Fatalf("jobs=%d removed=%v new=%v", s.Jobs(), removed, newJob)
	}
	s.Wait()
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

func TestDeliveryKeyIsStableAndChangesWithDeliveryInputs(t *testing.T) {
	baseJob := Job{ID: "17", Account: "account", Channel: "channel", MateriaCodigo: "31.202", Label: "Redes"}
	baseOutcome := Outcome{Code: "found", Vacancies: []Vacancy{
		{Materia: "31.202", Turno: "Noche", Sede: "Lima", Horario: "18:30", Dias: []string{"Lunes", "Miércoles"}, Cupos: 2},
		{Materia: "31.202", Turno: "Mañana", Sede: "Monserrat", Horario: "08:00", Dias: []string{"Martes"}, Cupos: 1},
	}}
	base := notificationDeliveryKey(baseJob, baseOutcome)
	if base == "" || base != notificationDeliveryKey(baseJob, baseOutcome) {
		t.Fatalf("delivery key is empty or unstable: %q", base)
	}
	cases := []struct {
		name   string
		mutate func(*Job, *Outcome)
	}{
		{"job", func(job *Job, _ *Outcome) { job.ID = "18" }},
		{"account", func(job *Job, _ *Outcome) { job.Account = "other" }},
		{"route target", func(job *Job, _ *Outcome) { job.Channel = "other-channel" }},
		{"materia", func(job *Job, _ *Outcome) { job.MateriaCodigo = "31.203" }},
		{"label", func(job *Job, _ *Outcome) { job.Label = "Otra" }},
		{"ordered vacancy", func(_ *Job, outcome *Outcome) {
			outcome.Vacancies[0], outcome.Vacancies[1] = outcome.Vacancies[1], outcome.Vacancies[0]
		}},
		{"vacancy content", func(_ *Job, outcome *Outcome) { outcome.Vacancies[0].Cupos++ }},
		{"ordered days", func(_ *Job, outcome *Outcome) {
			outcome.Vacancies[0].Dias[0], outcome.Vacancies[0].Dias[1] = outcome.Vacancies[0].Dias[1], outcome.Vacancies[0].Dias[0]
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := baseJob
			outcome := baseOutcome
			outcome.Vacancies = append([]Vacancy(nil), baseOutcome.Vacancies...)
			for i := range outcome.Vacancies {
				outcome.Vacancies[i].Dias = append([]string(nil), baseOutcome.Vacancies[i].Dias...)
			}
			tc.mutate(&job, &outcome)
			if got := notificationDeliveryKey(job, outcome); got == base {
				t.Fatalf("key did not change for %s", tc.name)
			}
		})
	}
}

func TestNotificationDedupPendingProgressSurvivesNotifierError(t *testing.T) {
	store := newMemoryStore()
	notifier := &recordingNotifier{fail: true}
	notifier.onNotify = func(event Event) error {
		return store.MarkNotificationFragmentDelivered(context.Background(), event.Job.ID, event.DeliveryKey, NotificationFragment{
			Route: "channel:" + event.Job.Channel, FragmentIndex: 0, FragmentCount: 2, FragmentFingerprint: "first",
		})
	}
	s := New(1, WithStore(store), WithNotifier(notifier))
	s.Add(Job{Account: "a", ID: "1", Channel: "123", MateriaCodigo: "31.202", Label: "Redes", Run: func(context.Context) (Outcome, error) {
		return Outcome{Code: "found", Vacancies: []Vacancy{{Materia: "31.202", Cupos: 1}}}, nil
	}})
	if err := s.RunOnce(context.Background()); err == nil {
		t.Fatal("notifier failure must surface")
	}
	event := notifier.last()
	fragments, err := store.DeliveredNotificationFragments(context.Background(), "1", event.DeliveryKey)
	if err != nil || len(fragments) != 1 {
		t.Fatalf("durable progress=%+v err=%v", fragments, err)
	}
	if store.notifications["1"] != (NotificationState{}) || store.commits != 0 {
		t.Fatalf("failed delivery advanced state=%+v commits=%d", store.notifications["1"], store.commits)
	}
}

func TestNotificationSuccessfulDeliveryCommitsOnce(t *testing.T) {
	store := newMemoryStore()
	notifier := &recordingNotifier{}
	s := New(1, WithStore(store), WithNotifier(notifier))
	s.Add(Job{Account: "a", ID: "1", Channel: "123", MateriaCodigo: "31.202", Label: "Redes", Run: func(context.Context) (Outcome, error) {
		return Outcome{Code: "found", Vacancies: []Vacancy{{Materia: "31.202", Turno: "Noche", Cupos: 2}}}, nil
	}})
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := notifier.last()
	if first.DeliveryKey == "" || store.commits != 1 || store.notifications["1"].VacancyKey == "" {
		t.Fatalf("event=%+v commits=%d state=%+v", first, store.commits, store.notifications["1"])
	}
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notifier.count() != 1 || store.commits != 1 {
		t.Fatalf("notify=%d commits=%d, want one each", notifier.count(), store.commits)
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
