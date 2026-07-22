package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/ogs/uade-bot/internal/store"
)

func TestSQLStoreReconstructsAndPersistsNodeCompatibleState(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC).UnixMilli()
	if _, err = db.Exec(`INSERT INTO users(discord_user_id,created_at,updated_at) VALUES('a',?,?); INSERT INTO jobs(discord_user_id,filtros_json,status,created_at) VALUES('a','{}','active',?),('a','{}','paused_by_user',?)`, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	repo := SQLStore{DB: db}
	jobs, err := repo.ActiveJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != "1" {
		t.Fatalf("active=%v", jobs)
	}
	state := AccountState{Reason: "rate_limited", PauseUntil: time.UnixMilli(now + 60000), BackoffAttempt: 1}
	if err = repo.SaveAccountState(context.Background(), "a", state); err != nil {
		t.Fatal(err)
	}
	got, err := repo.AccountState(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Reason != state.Reason || got.BackoffAttempt != 1 || !got.PauseUntil.Equal(state.PauseUntil) {
		t.Fatalf("state=%+v", got)
	}
	outcome := Outcome{Code: "found", Vacancies: []Vacancy{{Cupos: 2}, {Cupos: 3}}}
	if err = repo.SaveOutcome(context.Background(), "1", outcome, time.UnixMilli(now)); err != nil {
		t.Fatal(err)
	}
	if err = repo.SaveNotificationState(context.Background(), "1", NotificationState{VacancyKey: "key", MaxCupos: 3}); err != nil {
		t.Fatal(err)
	}
	notified, err := repo.NotificationState(context.Background(), "1")
	if err != nil || notified.VacancyKey != "key" || notified.MaxCupos != 3 {
		t.Fatalf("notification=%+v err=%v", notified, err)
	}
	var history, total int
	if err = db.QueryRow(`SELECT COUNT(*), total_cupos FROM poll_outcome_history WHERE job_id=1`).Scan(&history, &total); err != nil {
		t.Fatal(err)
	}
	if history != 1 || total != 5 {
		t.Fatalf("history=%d total=%d", history, total)
	}
}
