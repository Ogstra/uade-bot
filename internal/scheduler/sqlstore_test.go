package scheduler

import (
	"context"
	"database/sql"
	"strings"
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

func TestSQLStoreNotificationDeliveryProgressIsIdempotentAndFailsClosed(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedNotificationJob(t, db)
	repo := SQLStore{DB: db}
	ctx := context.Background()
	fragment := NotificationFragment{Route: "channel:123", FragmentIndex: 0, FragmentCount: 2, FragmentFingerprint: "hash-a", DeliveredAt: time.UnixMilli(100)}
	if err = repo.MarkNotificationFragmentDelivered(ctx, "1", "delivery-a", fragment); err != nil {
		t.Fatal(err)
	}
	if err = repo.MarkNotificationFragmentDelivered(ctx, "1", "delivery-a", fragment); err != nil {
		t.Fatalf("identical mark must be idempotent: %v", err)
	}
	for _, conflicting := range []NotificationFragment{
		{Route: fragment.Route, FragmentIndex: 0, FragmentCount: 3, FragmentFingerprint: fragment.FragmentFingerprint},
		{Route: fragment.Route, FragmentIndex: 0, FragmentCount: 2, FragmentFingerprint: "hash-b"},
	} {
		if err = repo.MarkNotificationFragmentDelivered(ctx, "1", "delivery-a", conflicting); err == nil {
			t.Fatalf("conflicting mark %+v succeeded", conflicting)
		}
	}
	got, err := repo.DeliveredNotificationFragments(ctx, "1", "delivery-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].FragmentCount != 2 || got[0].FragmentFingerprint != "hash-a" || !got[0].DeliveredAt.Equal(time.UnixMilli(100)) {
		t.Fatalf("fragments=%+v", got)
	}
}

func TestSQLStoreCommitNotificationDeliveryUpdatesAndCleansOnlyMatchingDelivery(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedNotificationJob(t, db)
	repo := SQLStore{DB: db}
	ctx := context.Background()
	for _, delivery := range []string{"delivery-a", "delivery-b"} {
		if err = repo.MarkNotificationFragmentDelivered(ctx, "1", delivery, NotificationFragment{Route: "channel:123", FragmentIndex: 0, FragmentCount: 1, FragmentFingerprint: delivery, DeliveredAt: time.UnixMilli(100)}); err != nil {
			t.Fatal(err)
		}
	}
	if err = repo.CommitNotificationDelivery(ctx, "1", "delivery-a", NotificationState{VacancyKey: "vacancies", MaxCupos: 4}); err != nil {
		t.Fatal(err)
	}
	state, err := repo.NotificationState(ctx, "1")
	if err != nil || state != (NotificationState{VacancyKey: "vacancies", MaxCupos: 4}) {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if fragments, err := repo.DeliveredNotificationFragments(ctx, "1", "delivery-a"); err != nil || len(fragments) != 0 {
		t.Fatalf("committed fragments=%+v err=%v", fragments, err)
	}
	if fragments, err := repo.DeliveredNotificationFragments(ctx, "1", "delivery-b"); err != nil || len(fragments) != 1 {
		t.Fatalf("other delivery fragments=%+v err=%v", fragments, err)
	}
}

func TestSQLStoreCommitNotificationDeliveryRollsBackStateAndProgress(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedNotificationJob(t, db)
	repo := SQLStore{DB: db}
	ctx := context.Background()
	fragment := NotificationFragment{Route: "channel:123", FragmentIndex: 0, FragmentCount: 1, FragmentFingerprint: "hash", DeliveredAt: time.UnixMilli(100)}
	if err = repo.MarkNotificationFragmentDelivered(ctx, "1", "delivery-a", fragment); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TRIGGER fail_delivery_cleanup BEFORE DELETE ON notification_delivery_progress BEGIN SELECT RAISE(ABORT, 'injected cleanup failure'); END`); err != nil {
		t.Fatal(err)
	}
	err = repo.CommitNotificationDelivery(ctx, "1", "delivery-a", NotificationState{VacancyKey: "new", MaxCupos: 9})
	if err == nil || !strings.Contains(err.Error(), "injected cleanup failure") {
		t.Fatalf("commit error=%v", err)
	}
	state, err := repo.NotificationState(ctx, "1")
	if err != nil || state != (NotificationState{}) {
		t.Fatalf("state after rollback=%+v err=%v", state, err)
	}
	if fragments, readErr := repo.DeliveredNotificationFragments(ctx, "1", "delivery-a"); readErr != nil || len(fragments) != 1 {
		t.Fatalf("fragments after rollback=%+v err=%v", fragments, readErr)
	}
}

func seedNotificationJob(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(discord_user_id,created_at,updated_at) VALUES('u',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO jobs(id,discord_user_id,filtros_json,status,created_at) VALUES(1,'u','{}','active',1)`); err != nil {
		t.Fatal(err)
	}
}
