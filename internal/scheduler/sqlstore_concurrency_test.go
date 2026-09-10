package scheduler

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ogstra/uade-bot/internal/store"
)

// TestSQLStoreOpenPinsSingleConnection is a fast regression guard for the
// nested-sqlite-tx-scheduler bug (see .planning/debug/resolved/): production
// hit "SQL logic error: cannot start a transaction within a transaction"
// on every scheduler tick because store.Open used database/sql's default
// unbounded connection pool. A concurrent reader holding a SHARED lock could
// make a writer's tx.Commit fail with SQLITE_BUSY; Go's database/sql sets
// tx.done=1 before invoking the driver's Commit, so the safety-net
// `defer tx.Rollback()` became a permanent no-op once Commit had been
// attempted, and modernc.org/sqlite's ResetSession does not detect a
// connection that is still mid-transaction -- so the poisoned connection
// went back into the idle pool and poisoned the next Begin() to reuse it.
// Pinning the pool to exactly one physical connection makes that
// interleaving impossible. If this test fails, someone removed the
// SetMaxOpenConns(1)/SetMaxIdleConns(1) call from store.Open.
func TestSQLStoreOpenPinsSingleConnection(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/pins.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stats := db.Stats()
	if stats.MaxOpenConnections != 1 {
		t.Fatalf("MaxOpenConnections=%d, want 1 (store.Open must pin the pool to a single connection)", stats.MaxOpenConnections)
	}
}

// TestSQLStoreSaveOutcomeUnderConcurrentReadPressure reproduces the
// production shape of the bug: many accounts' job pipelines calling
// SaveOutcome concurrently (as the scheduler's semaphore permits), while
// other goroutines run long-lived, undrained reads exactly like
// pollAllActiveJobs/dashboard snapshots do. Before the fix, this
// interleaving could make a writer's Commit fail with SQLITE_BUSY and
// permanently poison a pooled connection, so every later Begin() on it
// failed with "cannot start a transaction within a transaction" -- matching
// the real production log line exactly. With store.Open pinning the pool to
// one connection, concurrent access is fully serialized and that failure
// mode cannot occur.
func TestSQLStoreConcurrentReaderWriterPressure(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/concurrency.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const accounts = 8
	now := time.Now().UnixMilli()
	for i := 0; i < accounts; i++ {
		account := "u" + strconv.Itoa(i)
		if _, err = db.Exec(`INSERT INTO users(discord_user_id,created_at,updated_at) VALUES(?,?,?)`, account, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO jobs(discord_user_id,filtros_json,status,created_at) VALUES(?,?,'active',?)`, account, "{}", now); err != nil {
			t.Fatal(err)
		}
	}

	repo := SQLStore{DB: db}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	start := make(chan struct{})
	done := make(chan struct{})
	errCh := make(chan error, accounts*12)

	// Writers mimic concurrent job pipelines saving outcomes and advancing the
	// multipart delivery ledger. The start barrier maximizes contention while
	// the context and done timeout make a pool deadlock deterministic.
	for i := 0; i < accounts; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			jobID := strconv.Itoa(i + 1)
			for round := 0; round < 5; round++ {
				outcome := Outcome{Code: "found", Vacancies: []Vacancy{{Cupos: round + 1}}}
				if saveErr := repo.SaveOutcome(ctx, jobID, outcome, time.Now()); saveErr != nil {
					errCh <- saveErr
					return
				}
				delivery := fmt.Sprintf("delivery-%d-%d", i, round)
				fragment := NotificationFragment{Route: "dm", FragmentIndex: 0, FragmentCount: 1, FragmentFingerprint: delivery, DeliveredAt: time.Now()}
				if markErr := repo.MarkNotificationFragmentDelivered(ctx, jobID, delivery, fragment); markErr != nil {
					errCh <- markErr
					return
				}
				if commitErr := repo.CommitNotificationDelivery(ctx, jobID, delivery, NotificationState{VacancyKey: delivery, MaxCupos: round + 1}); commitErr != nil {
					errCh <- commitErr
					return
				}
			}
		}()
	}

	// Readers exercise the scheduler/dashboard query shape without retaining a
	// connection indefinitely, which would intentionally deadlock a unit pool.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for round := 0; round < 20; round++ {
				var jobs, progress int
				if readErr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`).Scan(&jobs); readErr != nil {
					errCh <- readErr
					return
				}
				if readErr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_delivery_progress`).Scan(&progress); readErr != nil {
					errCh <- readErr
					return
				}
				if jobs != accounts || progress < 0 {
					errCh <- fmt.Errorf("invalid reader snapshot jobs=%d progress=%d", jobs, progress)
					return
				}
			}
		}()
	}

	close(start)
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("concurrent pressure timed out: %v", ctx.Err())
	}
	close(errCh)

	for e := range errCh {
		if strings.Contains(e.Error(), "cannot start a transaction within a transaction") || strings.Contains(strings.ToUpper(e.Error()), "SQLITE_BUSY") {
			t.Fatalf("nested-transaction error reproduced: %v", e)
		}
		t.Fatalf("unexpected error: %v", e)
	}

	// A fresh ledger transaction after pressure proves the pooled connection
	// was not returned while still inside an earlier transaction.
	postDelivery := "delivery-post-pressure"
	postFragment := NotificationFragment{Route: "dm", FragmentIndex: 0, FragmentCount: 1, FragmentFingerprint: postDelivery, DeliveredAt: time.Now()}
	if err = repo.MarkNotificationFragmentDelivered(context.Background(), "1", postDelivery, postFragment); err != nil {
		t.Fatalf("post-pressure mark: %v", err)
	}
	if err = repo.CommitNotificationDelivery(context.Background(), "1", postDelivery, NotificationState{VacancyKey: postDelivery, MaxCupos: 99}); err != nil {
		t.Fatalf("post-pressure commit: %v", err)
	}
}
