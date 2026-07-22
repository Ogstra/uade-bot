package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Clock keeps scheduling and throttling deterministic in tests.
type Clock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type Vacancy struct {
	Turno, Sede, Horario string
	Dias                 []string
	Cupos                int
}

type Outcome struct {
	Code      string
	Vacancies []Vacancy
}

type Job struct {
	Account string
	ID      string
	Channel string
	Run     func(context.Context) (Outcome, error)
}

type PersistedJob struct {
	Account string
	ID      string
	Channel string
}

type AccountState struct {
	Reason                  string
	PauseUntil              time.Time
	BackoffAttempt          int
	LastPauseNotifiedReason string
}

type NotificationState struct {
	VacancyKey string
	MaxCupos   int
}

// Store is the scheduler's complete write boundary. Shadow mode may read it,
// but never invokes any Save/Mark method.
type Store interface {
	ActiveJobs(context.Context) ([]PersistedJob, error)
	AccountState(context.Context, string) (AccountState, error)
	SaveAccountState(context.Context, string, AccountState) error
	NotificationState(context.Context, string) (NotificationState, error)
	SaveNotificationState(context.Context, string, NotificationState) error
	SaveOutcome(context.Context, string, Outcome, time.Time) error
	MarkPauseNotification(context.Context, string, string) error
}

type Event struct {
	Kind    string
	Job     Job
	Outcome Outcome
	Reason  string
}

type Notifier interface {
	Notify(context.Context, Event) error
}

type Option func(*Scheduler)

func WithClock(clock Clock) Option {
	return func(s *Scheduler) {
		if clock != nil {
			s.clock = clock
		}
	}
}
func WithStore(store Store) Option          { return func(s *Scheduler) { s.store = store } }
func WithNotifier(notifier Notifier) Option { return func(s *Scheduler) { s.notifier = notifier } }
func WithShadow(shadow bool) Option         { return func(s *Scheduler) { s.shadow = shadow } }

// Scheduler selects one job per account on each tick, serializes work within
// an account, and applies one global concurrency cap across overlapping ticks.
type Scheduler struct {
	mu        sync.Mutex
	jobs      map[string][]Job
	byID      map[string]Job
	pointer   map[string]int
	inFlight  map[string]bool
	immediate map[string][]string
	queued    map[string]bool
	sem       chan struct{}
	clock     Clock
	store     Store
	notifier  Notifier
	shadow    bool
	memory    map[string]AccountState
	notifyMem map[string]NotificationState
	wg        sync.WaitGroup
}

func New(limit int, options ...Option) *Scheduler {
	if limit < 1 {
		limit = 1
	}
	s := &Scheduler{
		jobs: map[string][]Job{}, byID: map[string]Job{}, pointer: map[string]int{},
		inFlight: map[string]bool{}, immediate: map[string][]string{}, queued: map[string]bool{},
		sem: make(chan struct{}, limit), clock: realClock{}, memory: map[string]AccountState{},
		notifyMem: map[string]NotificationState{},
	}
	for _, option := range options {
		option(s)
	}
	return s
}

// Add is idempotent by job ID, which makes reconstruction safe to repeat.
func (s *Scheduler) Add(job Job) bool {
	if job.ID == "" || job.Account == "" || job.Run == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byID[job.ID]; exists {
		return false
	}
	s.byID[job.ID] = job
	s.jobs[job.Account] = append(s.jobs[job.Account], job)
	return true
}

func (s *Scheduler) Accounts() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.jobs) }
func (s *Scheduler) Jobs() int     { s.mu.Lock(); defer s.mu.Unlock(); return len(s.byID) }

// Reconstruct reads active jobs from durable storage and registers each once.
func (s *Scheduler) Reconstruct(ctx context.Context, factory func(PersistedJob) (Job, error)) (int, error) {
	if s.store == nil {
		return 0, errors.New("scheduler: store is required for reconstruction")
	}
	records, err := s.store.ActiveJobs(ctx)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, record := range records {
		job, buildErr := factory(record)
		if buildErr != nil {
			return added, fmt.Errorf("reconstruct job %s: %w", record.ID, buildErr)
		}
		if s.Add(job) {
			added++
		}
	}
	return added, nil
}

// Reconcile makes the in-memory schedule match durable active jobs. It is safe
// to call before every tick: unchanged jobs are retained, new jobs are added,
// and stopped/paused jobs are removed so they cannot keep polling after a
// command changed their durable status.
func (s *Scheduler) Reconcile(ctx context.Context, factory func(PersistedJob) (Job, error)) (int, error) {
	if s.store == nil {
		return 0, errors.New("scheduler: store is required for reconciliation")
	}
	records, err := s.store.ActiveJobs(ctx)
	if err != nil {
		return 0, err
	}
	desired := make(map[string]Job, len(records))
	for _, record := range records {
		job, buildErr := factory(record)
		if buildErr != nil {
			return 0, fmt.Errorf("reconcile job %s: %w", record.ID, buildErr)
		}
		if job.ID == "" || job.Account == "" || job.Run == nil {
			return 0, fmt.Errorf("reconcile job %s: invalid job", record.ID)
		}
		desired[job.ID] = job
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	added := 0
	for id, job := range desired {
		if _, exists := s.byID[id]; !exists {
			added++
		}
		s.byID[id] = job
	}
	for id := range s.byID {
		if _, keep := desired[id]; !keep {
			delete(s.byID, id)
			delete(s.queued, id)
		}
	}
	s.jobs = make(map[string][]Job)
	for _, record := range records {
		job := desired[record.ID]
		s.jobs[job.Account] = append(s.jobs[job.Account], job)
	}
	for account, pointer := range s.pointer {
		if count := len(s.jobs[account]); count == 0 {
			delete(s.pointer, account)
		} else if pointer >= count {
			s.pointer[account] = pointer % count
		}
	}
	return added, nil
}

func (s *Scheduler) accountState(ctx context.Context, account string) (AccountState, error) {
	if s.store != nil {
		return s.store.AccountState(ctx, account)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory[account], nil
}

func (s *Scheduler) accountPaused(ctx context.Context, account string) (bool, error) {
	state, err := s.accountState(ctx, account)
	if err != nil {
		return false, err
	}
	if state.Reason == "" {
		return false, nil
	}
	return state.PauseUntil.IsZero() || state.PauseUntil.After(s.clock.Now()), nil
}

// RunOnce performs one deterministic round-robin tick and waits for its work,
// including any immediate requests queued behind those account polls.
func (s *Scheduler) RunOnce(ctx context.Context) error {
	s.mu.Lock()
	accounts := make([]string, 0, len(s.jobs))
	for account := range s.jobs {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)
	s.mu.Unlock()

	var jobs []Job
	var errs []error
	for _, account := range accounts {
		paused, err := s.accountPaused(ctx, account)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if paused {
			continue
		}
		s.mu.Lock()
		accountJobs := s.jobs[account]
		if len(accountJobs) > 0 && !s.inFlight[account] {
			index := s.pointer[account] % len(accountJobs)
			s.pointer[account]++
			s.inFlight[account] = true
			jobs = append(jobs, accountJobs[index])
		}
		s.mu.Unlock()
	}

	results := make(chan error, len(jobs))
	for _, job := range jobs {
		job := job
		s.wg.Add(1)
		go func() { defer s.wg.Done(); results <- s.runAccount(ctx, job) }()
	}
	for range jobs {
		if err := <-results; err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// PollNow starts an immediate poll. If the account is busy, distinct job IDs
// are queued in insertion order and duplicates are suppressed.
func (s *Scheduler) PollNow(ctx context.Context, jobID string) bool {
	s.mu.Lock()
	job, exists := s.byID[jobID]
	if !exists {
		s.mu.Unlock()
		return false
	}
	if s.inFlight[job.Account] {
		if s.queued[jobID] {
			s.mu.Unlock()
			return false
		}
		s.immediate[job.Account] = append(s.immediate[job.Account], jobID)
		s.queued[jobID] = true
		s.mu.Unlock()
		return true
	}
	s.inFlight[job.Account] = true
	s.wg.Add(1)
	s.mu.Unlock()
	go func() { defer s.wg.Done(); _ = s.runAccount(ctx, job) }()
	return true
}

func (s *Scheduler) Wait() { s.wg.Wait() }

func (s *Scheduler) runAccount(ctx context.Context, first Job) error {
	job := first
	var errs []error
	for {
		select {
		case s.sem <- struct{}{}:
		case <-ctx.Done():
			errs = append(errs, ctx.Err())
			s.releaseAccount(job.Account)
			return errors.Join(errs...)
		}
		outcome, err := job.Run(ctx)
		<-s.sem
		if err != nil {
			errs = append(errs, fmt.Errorf("job %s: %w", job.ID, err))
		} else if err = s.handleOutcome(ctx, job, outcome); err != nil {
			errs = append(errs, fmt.Errorf("job %s outcome: %w", job.ID, err))
		}

		s.mu.Lock()
		queue := s.immediate[job.Account]
		if len(queue) == 0 {
			s.inFlight[job.Account] = false
			s.mu.Unlock()
			return errors.Join(errs...)
		}
		nextID := queue[0]
		s.immediate[job.Account] = queue[1:]
		delete(s.queued, nextID)
		job = s.byID[nextID]
		s.mu.Unlock()
	}
}

func (s *Scheduler) releaseAccount(account string) {
	s.mu.Lock()
	s.inFlight[account] = false
	s.mu.Unlock()
}

func (s *Scheduler) handleOutcome(ctx context.Context, job Job, outcome Outcome) error {
	if s.shadow {
		return nil
	}
	now := s.clock.Now()
	if s.store != nil {
		if err := s.store.SaveOutcome(ctx, job.ID, outcome, now); err != nil {
			return err
		}
	}
	state, err := s.accountState(ctx, job.Account)
	if err != nil {
		return err
	}
	next := NextBackoff(state, signalFor(outcome.Code), now)
	if s.store != nil {
		err = s.store.SaveAccountState(ctx, job.Account, next)
	} else {
		s.mu.Lock()
		s.memory[job.Account] = next
		s.mu.Unlock()
	}
	if err != nil {
		return err
	}
	if err = s.dispatchPause(ctx, job, state, next); err != nil {
		return err
	}
	return s.dispatchVacancy(ctx, job, outcome)
}

func signalFor(code string) string {
	switch code {
	case "invalid_credentials", "rate_limited", "stale_start_url":
		return code
	default:
		return "success"
	}
}

var backoffSequence = [...]time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute}

func NextBackoff(current AccountState, signal string, now time.Time) AccountState {
	switch signal {
	case "invalid_credentials":
		return AccountState{Reason: "needs_credentials", LastPauseNotifiedReason: current.LastPauseNotifiedReason}
	case "stale_start_url":
		return AccountState{Reason: "needs_new_start_url", LastPauseNotifiedReason: current.LastPauseNotifiedReason}
	case "rate_limited":
		attempt := 1
		if current.Reason == "rate_limited" {
			attempt = current.BackoffAttempt + 1
		}
		index := attempt - 1
		if index >= len(backoffSequence) {
			index = len(backoffSequence) - 1
		}
		return AccountState{Reason: "rate_limited", PauseUntil: now.Add(backoffSequence[index]), BackoffAttempt: attempt, LastPauseNotifiedReason: current.LastPauseNotifiedReason}
	default:
		return AccountState{}
	}
}

func (s *Scheduler) dispatchPause(ctx context.Context, job Job, previous, next AccountState) error {
	actionable := next.Reason == "needs_credentials" || next.Reason == "needs_new_start_url"
	if !actionable {
		if previous.LastPauseNotifiedReason != "" && s.store != nil {
			return s.store.MarkPauseNotification(ctx, job.Account, "")
		}
		return nil
	}
	if previous.LastPauseNotifiedReason == next.Reason || s.notifier == nil {
		return nil
	}
	if err := s.notifier.Notify(ctx, Event{Kind: "account_pause", Job: job, Outcome: Outcome{Code: next.Reason}, Reason: next.Reason}); err != nil {
		return err
	}
	if s.store != nil {
		return s.store.MarkPauseNotification(ctx, job.Account, next.Reason)
	}
	s.mu.Lock()
	state := s.memory[job.Account]
	state.LastPauseNotifiedReason = next.Reason
	s.memory[job.Account] = state
	s.mu.Unlock()
	return nil
}

func vacancyFingerprint(vacancies []Vacancy) (string, int) {
	keys := make([]string, 0, len(vacancies))
	max := 0
	for _, vacancy := range vacancies {
		days := append([]string(nil), vacancy.Dias...)
		keys = append(keys, strings.Join([]string{vacancy.Turno, vacancy.Sede, vacancy.Horario, strings.Join(days, ",")}, "|"))
		if vacancy.Cupos > max {
			max = vacancy.Cupos
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, "\n"), max
}

func (s *Scheduler) notificationState(ctx context.Context, id string) (NotificationState, error) {
	if s.store != nil {
		return s.store.NotificationState(ctx, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notifyMem[id], nil
}

func (s *Scheduler) saveNotificationState(ctx context.Context, id string, state NotificationState) error {
	if s.store != nil {
		return s.store.SaveNotificationState(ctx, id, state)
	}
	s.mu.Lock()
	s.notifyMem[id] = state
	s.mu.Unlock()
	return nil
}

func (s *Scheduler) dispatchVacancy(ctx context.Context, job Job, outcome Outcome) error {
	prior, err := s.notificationState(ctx, job.ID)
	if err != nil {
		return err
	}
	if outcome.Code == "no_vacancies" {
		if prior.VacancyKey != "" {
			return s.saveNotificationState(ctx, job.ID, NotificationState{})
		}
		return nil
	}
	if outcome.Code != "found" || len(outcome.Vacancies) == 0 {
		return nil
	}
	key, max := vacancyFingerprint(outcome.Vacancies)
	if prior.VacancyKey == key && max <= prior.MaxCupos {
		return nil
	}
	if s.notifier == nil {
		return nil
	}
	if err = s.notifier.Notify(ctx, Event{Kind: "vacancy", Job: job, Outcome: outcome}); err != nil {
		return err
	}
	return s.saveNotificationState(ctx, job.ID, NotificationState{VacancyKey: key, MaxCupos: max})
}

// ThrottledNotifier serializes outbound REST calls and applies the delay even
// after a failed call; one rejection never poisons later notifications.
type ThrottledNotifier struct {
	Next  Notifier
	Clock Clock
	Delay time.Duration
	mu    sync.Mutex
}

func (n *ThrottledNotifier) Notify(ctx context.Context, event Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	clock := n.Clock
	if clock == nil {
		clock = realClock{}
	}
	if n.Delay > 0 {
		if err := clock.Sleep(ctx, n.Delay); err != nil {
			return err
		}
	}
	if n.Next == nil {
		return errors.New("scheduler: notifier is required")
	}
	return n.Next.Notify(ctx, event)
}
