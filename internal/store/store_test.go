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
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('users','credentials','jobs','poll_outcome_history','materias','command_log')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("tables=%d", n)
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
