package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
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

func TestOpenCreatesNotificationDeliveryProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	for attempt := 0; attempt < 2; attempt++ {
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			if _, err = db.Exec(`INSERT INTO users(discord_user_id,created_at,updated_at) VALUES('u',1,1)`); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`INSERT INTO jobs(id,discord_user_id,filtros_json,status,created_at) VALUES(1,'u','{}','active',1)`); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`INSERT INTO notification_delivery_progress(job_id,delivery_fingerprint,route,fragment_index,fragment_count,fragment_fingerprint,delivered_at) VALUES(1,'delivery','channel:123',0,2,'fragment',1)`); err != nil {
				t.Fatal(err)
			}
		}
		assertNotificationDeliverySchema(t, db)
		var rows int
		if err = db.QueryRow(`SELECT COUNT(*) FROM notification_delivery_progress`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Fatalf("rows after open %d=%d, want 1", attempt+1, rows)
		}
		db.Close()
	}
}

func TestMigrationCreatesNotificationDeliveryProgressForLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`CREATE TABLE jobs (id INTEGER PRIMARY KEY, discord_user_id TEXT, filtros_json TEXT, channel_id TEXT, label TEXT, status TEXT, last_polled_at INTEGER, last_outcome TEXT, last_notified_state TEXT, last_notified_cupos INTEGER, created_at INTEGER)`); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertNotificationDeliverySchema(t, db)
}

func assertNotificationDeliverySchema(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(notification_delivery_progress)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	var primaryKeys = map[string]int{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
		if pk > 0 {
			primaryKeys[name] = pk
		}
	}
	wantColumns := []string{"job_id", "delivery_fingerprint", "route", "fragment_index", "fragment_count", "fragment_fingerprint", "delivered_at"}
	if !reflect.DeepEqual(columns, wantColumns) {
		t.Fatalf("columns=%v, want %v", columns, wantColumns)
	}
	wantPK := map[string]int{"job_id": 1, "delivery_fingerprint": 2, "route": 3, "fragment_index": 4}
	if !reflect.DeepEqual(primaryKeys, wantPK) {
		t.Fatalf("primary key=%v, want %v", primaryKeys, wantPK)
	}
	var table, from, to, onDelete string
	var id, seq int
	var onUpdate, match string
	if err = db.QueryRow(`PRAGMA foreign_key_list(notification_delivery_progress)`).Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
		t.Fatal(err)
	}
	if table != "jobs" || from != "job_id" || to != "id" || onDelete != "CASCADE" {
		t.Fatalf("foreign key table=%s from=%s to=%s delete=%s", table, from, to, onDelete)
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
