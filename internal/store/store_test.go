package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpenInitializesCompatibleSchema(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('users','credentials','jobs','poll_outcome_history','materias','command_log','pending_searches')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 7 {
		t.Fatalf("tables=%d", n)
	}
}

// TestMigrateStatementsRollsBackAllStatementsOnFailure proves migrateStatements
// is genuinely transactional (D-12): a statement that succeeds earlier in the
// list must NOT be persisted if a later statement in the same call fails.
func TestMigrateStatementsRollsBackAllStatementsOnFailure(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statements := []string{
		"CREATE TABLE IF NOT EXISTS rollback_probe (id INTEGER)",
		"THIS IS NOT VALID SQL",
	}
	if err := migrateStatements(db, statements); err == nil {
		t.Fatal("expected an error from the invalid statement")
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='rollback_probe'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("rollback_probe table persisted despite rollback: count=%d", n)
	}
}

func TestOpenMigratesPreGuildJobsSchemaIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`CREATE TABLE jobs (id INTEGER PRIMARY KEY, discord_user_id TEXT, filtros_json TEXT, channel_id TEXT, label TEXT, status TEXT, last_polled_at INTEGER, last_outcome TEXT, last_notified_state TEXT, last_notified_cupos INTEGER, created_at INTEGER)`)
	legacy.Close()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		db, openErr := Open(path)
		if openErr != nil {
			t.Fatal(openErr)
		}
		if _, execErr := db.Exec(`INSERT INTO jobs(discord_user_id,filtros_json,guild_id,label,status,created_at) VALUES('u','{}','g','l','active',1)`); execErr != nil {
			db.Close()
			t.Fatal(execErr)
		}
		db.Close()
	}
}
