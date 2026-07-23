package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
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

func (s *Scheduler) runAccount(ctx context.Context, first Job) (err error) {
	job := first
	var errs []error
	panicked := false
	defer func() {
		if recover() != nil {
			panicked = true
			log.Print("scheduler account pipeline panic recovered")
			errs = append(errs, fmt.Errorf("job %s account pipeline panic recovered", job.ID))
		}
		s.finishAccount(first.Account, panicked)
		err = errors.Join(errs...)
	}()

	for {
		if pipelineErr := s.runJobPipelineSafely(ctx, job); pipelineErr != nil {
			errs = append(errs, pipelineErr)
		}

		next, ok := s.nextImmediate(job.Account)
		if !ok {
			return
		}
		job = next
	}
}

// runJobPipelineSafely owns one semaphore token for one complete job pipeline.
// Its fixed recovery contract deliberately excludes panic values, outcomes,
// credentials, and URLs while still allowing runAccount to drain queued work.
func (s *Scheduler) runJobPipelineSafely(ctx context.Context, job Job) (err error) {
	acquired := false
	defer func() {
		if acquired {
			<-s.sem
		}
		if recover() != nil {
			log.Print("scheduler job pipeline panic recovered")
			err = fmt.Errorf("job %s pipeline panic recovered", job.ID)
		}
	}()

	select {
	case s.sem <- struct{}{}:
		acquired = true
	case <-ctx.Done():
		return ctx.Err()
	}

	outcome, err := s.runJobSafely(ctx, job)
	if err != nil {
		return err
	}
	if err = s.handleOutcome(ctx, job, outcome); err != nil {
		return fmt.Errorf("job %s outcome: %w", job.ID, err)
	}
	return nil
}

// runJobSafely protects only job.Run. Its returned error may include the
// non-sensitive job ID, but the recovered panic value is never formatted,
// returned, logged, or passed to another helper.
func (s *Scheduler) runJobSafely(ctx context.Context, job Job) (outcome Outcome, err error) {
	defer func() {
		if recover() != nil {
			log.Print("scheduler job run panic recovered")
			err = fmt.Errorf("job %s run panic recovered", job.ID)
		}
	}()
	return job.Run(ctx)
}

// nextImmediate consumes accepted immediate polls in FIFO order. It removes
// every consumed ID from queued while holding the same lock used by PollNow.
func (s *Scheduler) nextImmediate(account string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.immediate[account]) > 0 {
		nextID := s.immediate[account][0]
		s.immediate[account] = s.immediate[account][1:]
		delete(s.queued, nextID)
		if job, ok := s.byID[nextID]; ok {
			return job, true
		}
	}
	delete(s.immediate, account)
	return Job{}, false
}

// finishAccount centralizes terminal bookkeeping. Unexpected panics outside
// the per-job boundary discard residual queue markers so later PollNow calls
// can accept those jobs again; normal completion has already drained FIFO.
func (s *Scheduler) finishAccount(account string, discardQueued bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if discardQueued {
		for _, id := range s.immediate[account] {
			delete(s.queued, id)
		}
		delete(s.immediate, account)
	}
	s.inFlight[account] = false
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
