package jobactions

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Ogstra/uade-bot/internal/store"
)

// openSeeded returns a fresh SQLite DB (store.Open's schema) seeded with two
// jobs owned by different discord_user_id values -- user-a's job starts
// 'active', user-b's job starts 'paused_by_user' -- so cross-owner mutation
// (no WHERE discord_user_id) can be verified explicitly.
func openSeeded(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/jobactions.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`INSERT INTO users(discord_user_id,backoff_attempt,created_at,updated_at) VALUES ('user-a',0,1,1),('user-b',0,1,1);
INSERT INTO jobs(id,discord_user_id,filtros_json,status,created_at) VALUES
	(10,'user-a','{"materiaCodigo":"1.1.010"}','active',1),
	(20,'user-b','{"materiaCodigo":"1.1.020"}','paused_by_user',1)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func jobStatus(t *testing.T, db *sql.DB, id int64) (string, bool) {
	t.Helper()
	var status string
	err := db.QueryRow(`SELECT status FROM jobs WHERE id=?`, id).Scan(&status)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return status, true
}

func TestMutatePausarChangesStatusToPausedByUser(t *testing.T) {
	db := openSeeded(t)
	n, err := Mutate(context.Background(), db, "pausar", 10)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	status, ok := jobStatus(t, db, 10)
	if !ok || status != "paused_by_user" {
		t.Fatalf("status=%q ok=%v", status, ok)
	}
}

func TestMutateReanudarChangesStatusToActive(t *testing.T) {
	db := openSeeded(t)
	n, err := Mutate(context.Background(), db, "reanudar", 20)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	status, ok := jobStatus(t, db, 20)
	if !ok || status != "active" {
		t.Fatalf("status=%q ok=%v", status, ok)
	}
}

func TestMutateDetenerDeletesTheRow(t *testing.T) {
	db := openSeeded(t)
	n, err := Mutate(context.Background(), db, "detener", 10)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id=?`, 10).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count=%d, want 0", count)
	}
}

func TestMutateUnknownIDReturnsZeroWithoutError(t *testing.T) {
	db := openSeeded(t)
	for _, action := range []string{"pausar", "reanudar", "detener"} {
		n, err := Mutate(context.Background(), db, action, 999)
		if err != nil || n != 0 {
			t.Fatalf("action=%s n=%d err=%v", action, n, err)
		}
	}
}

func TestMutateInvalidActionReturnsErrInvalidActionWithoutTouchingDB(t *testing.T) {
	db := openSeeded(t)
	n, err := Mutate(context.Background(), db, "borrar-todo", 10)
	if n != 0 || !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("n=%d err=%v", n, err)
	}
	status, ok := jobStatus(t, db, 10)
	if !ok || status != "active" {
		t.Fatalf("job 10 mutated despite invalid action: status=%q ok=%v", status, ok)
	}
}

func TestMutateHasNoOwnerScopingAndWorksAcrossUsers(t *testing.T) {
	db := openSeeded(t)
	// job 20 belongs to user-b; Mutate has no notion of "session owner" at
	// all, so pausing/reanudar/eliminando it must succeed exactly the same
	// as if it belonged to the caller -- this is the admin=true behavior
	// jobactions mirrors, never the admin=false branch of mutateJob.
	n, err := Mutate(context.Background(), db, "pausar", 20)
	if err != nil || n != 1 {
		t.Fatalf("cross-owner pausar failed: n=%d err=%v", n, err)
	}
	status, ok := jobStatus(t, db, 20)
	if !ok || status != "paused_by_user" {
		t.Fatalf("status=%q ok=%v", status, ok)
	}
}
