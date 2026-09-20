package scheduler

import (
	"context"
	"testing"
	"time"
)

// A closed enrollment period is not the user's fault and not theirs to fix,
// so it must never land on needs_new_start_url (the reason that drives the
// "relink your account" DM and the /credenciales prompt). It parks the
// account on a long retry instead, which is also what makes reopening
// self-healing: the pause simply expires and the next cycle polls again.
func TestNextBackoffParksClosedEnrollmentWithoutBlamingTheUser(t *testing.T) {
	now := time.Date(2027, 1, 15, 9, 0, 0, 0, time.UTC)

	got := NextBackoff(AccountState{}, "inscripciones_cerradas", now)

	if got.Reason != "inscripciones_cerradas" {
		t.Fatalf("Reason = %q, want inscripciones_cerradas", got.Reason)
	}
	if !got.PauseUntil.Equal(now.Add(ClosedEnrollmentRetry)) {
		t.Fatalf("PauseUntil = %v, want %v", got.PauseUntil, now.Add(ClosedEnrollmentRetry))
	}
	if got.BackoffAttempt != 0 {
		t.Fatalf("BackoffAttempt = %d, want 0: a closed period is not an escalating failure", got.BackoffAttempt)
	}
}

// The retry window must not creep upward the way rate_limited's does: every
// cycle while the portal stays closed re-parks the account for the same
// fixed interval, so reopening is noticed within one window no matter how
// many months the closure lasted.
func TestNextBackoffClosedEnrollmentDoesNotEscalate(t *testing.T) {
	now := time.Date(2027, 1, 15, 9, 0, 0, 0, time.UTC)
	state := AccountState{}
	for cycle := 0; cycle < 50; cycle++ {
		state = NextBackoff(state, "inscripciones_cerradas", now)
		if !state.PauseUntil.Equal(now.Add(ClosedEnrollmentRetry)) {
			t.Fatalf("cycle %d PauseUntil = %v, want a fixed %v window", cycle, state.PauseUntil, ClosedEnrollmentRetry)
		}
	}
}

// Reopening: once the portal answers normally again the success signal must
// clear the closed state completely, so the account resumes on its normal
// interval.
func TestNextBackoffSuccessClearsClosedEnrollment(t *testing.T) {
	now := time.Date(2027, 3, 1, 9, 0, 0, 0, time.UTC)
	closed := NextBackoff(AccountState{}, "inscripciones_cerradas", now)

	got := NextBackoff(closed, "success", now)

	if got.Reason != "" || !got.PauseUntil.IsZero() {
		t.Fatalf("state after reopening = %+v, want cleared", got)
	}
}

func TestSignalForClosedEnrollment(t *testing.T) {
	if got := signalFor("inscripciones_cerradas"); got != "inscripciones_cerradas" {
		t.Fatalf("signalFor = %q, want inscripciones_cerradas", got)
	}
}

// One DM when the period closes, and nothing more while it stays closed --
// the whole point is to stop the recurring nag. The dedupe reuses the same
// LastPauseNotifiedReason marker the actionable pauses already use.
func TestClosedEnrollmentNotifiesOnceThenStaysQuiet(t *testing.T) {
	store := newMemoryStore()
	notifier := &recordingNotifier{}
	clock := &fakeClock{now: time.Date(2027, 1, 15, 9, 0, 0, 0, time.UTC)}

	run := func() {
		s := New(1, WithStore(store), WithNotifier(notifier), WithClock(clock))
		s.Add(Job{Account: "a", ID: "1", Run: outcomeRun("inscripciones_cerradas")})
		if err := s.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	run()
	if notifier.count() != 1 {
		t.Fatalf("notifications after first closed poll = %d, want 1", notifier.count())
	}
	if event := notifier.last(); event.Kind != "account_pause" || event.Reason != "inscripciones_cerradas" {
		t.Fatalf("event = %+v, want account_pause/inscripciones_cerradas", event)
	}

	// Later cycles, after the pause window expires, keep finding the portal
	// closed. None of them may produce a second DM.
	for cycle := 0; cycle < 3; cycle++ {
		clock.now = clock.now.Add(ClosedEnrollmentRetry + time.Minute)
		run()
	}
	if notifier.count() != 1 {
		t.Fatalf("notifications while the period stayed closed = %d, want 1", notifier.count())
	}
}

// When enrollment reopens the user gets told once, so someone who stopped
// checking Discord in December knows their searches are live again.
func TestReopeningNotifiesOnce(t *testing.T) {
	store := newMemoryStore()
	notifier := &recordingNotifier{}
	clock := &fakeClock{now: time.Date(2027, 1, 15, 9, 0, 0, 0, time.UTC)}

	closedRun := func() {
		s := New(1, WithStore(store), WithNotifier(notifier), WithClock(clock))
		s.Add(Job{Account: "a", ID: "1", Run: outcomeRun("inscripciones_cerradas")})
		if err := s.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	openRun := func() {
		s := New(1, WithStore(store), WithNotifier(notifier), WithClock(clock))
		s.Add(Job{Account: "a", ID: "1", Run: outcomeRun("no_vacancies")})
		if err := s.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	closedRun()
	clock.now = clock.now.Add(ClosedEnrollmentRetry + time.Minute)
	openRun()

	if notifier.count() != 2 {
		t.Fatalf("notifications = %d, want 2 (closed + reopened)", notifier.count())
	}
	if event := notifier.last(); event.Kind != "account_resumed" || event.Reason != "inscripciones_cerradas" {
		t.Fatalf("reopen event = %+v, want account_resumed/inscripciones_cerradas", event)
	}

	// Staying open must not keep announcing it.
	openRun()
	if notifier.count() != 2 {
		t.Fatalf("notifications after a second open poll = %d, want 2", notifier.count())
	}
}

// A user-facing pause that was never announced (rate limiting, for example)
// must not produce a "your searches are back" message when it clears, since
// nothing was ever said about it stopping.
func TestUnannouncedPauseDoesNotAnnounceResume(t *testing.T) {
	store := newMemoryStore()
	notifier := &recordingNotifier{}
	clock := &fakeClock{now: time.Date(2027, 1, 15, 9, 0, 0, 0, time.UTC)}

	limited := New(1, WithStore(store), WithNotifier(notifier), WithClock(clock))
	limited.Add(Job{Account: "a", ID: "1", Run: outcomeRun("rate_limited")})
	if err := limited.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Hour)
	recovered := New(1, WithStore(store), WithNotifier(notifier), WithClock(clock))
	recovered.Add(Job{Account: "a", ID: "1", Run: outcomeRun("no_vacancies")})
	if err := recovered.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if notifier.count() != 0 {
		t.Fatalf("notifications = %d, want 0", notifier.count())
	}
}
