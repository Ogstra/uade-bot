package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSearchIdentityMigrationDeduplicatesAndPreservesScopes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-searches.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`
		PRAGMA foreign_keys=ON;
		CREATE TABLE users (discord_user_id TEXT PRIMARY KEY, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
		CREATE TABLE jobs (id INTEGER PRIMARY KEY, discord_user_id TEXT NOT NULL REFERENCES users(discord_user_id), filtros_json TEXT NOT NULL, channel_id TEXT, guild_id TEXT, label TEXT, status TEXT, created_at INTEGER);
		CREATE TABLE poll_outcome_history (id INTEGER PRIMARY KEY, job_id INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE, recorded_at INTEGER NOT NULL, outcome_code TEXT NOT NULL);
		INSERT INTO users VALUES ('u1',1,1),('u2',1,1);
		INSERT INTO jobs VALUES
		 (1,'u1','{"materiaCodigo":" 3.1.050 "}',NULL,NULL,'first','active',1),
		 (2,'u1','{"materiaCodigo":"3.1.050"}',NULL,NULL,'duplicate','active',2),
		 (3,'u1','{"materiaCodigo":"3.1.050"}',NULL,'g1','guild','active',3),
		 (4,'u2','{"materiaCodigo":"3.1.050"}',NULL,NULL,'other-user','active',4),
		 (5,'u1','not-json',NULL,NULL,'corrupt','active',5);
		INSERT INTO poll_outcome_history VALUES (1,2,1,'found');`)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var kept, duplicate, scoped, invalid int
	if err = db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id=1 AND materia_code='3.1.050'`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id=2`).Scan(&duplicate); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id IN (3,4) AND materia_code='3.1.050'`).Scan(&scoped); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id=5 AND materia_code IS NULL`).Scan(&invalid); err != nil {
		t.Fatal(err)
	}
	if kept != 1 || duplicate != 0 || scoped != 2 || invalid != 1 {
		t.Fatalf("kept=%d duplicate=%d scoped=%d invalid=%d", kept, duplicate, scoped, invalid)
	}
	var history int
	if err = db.QueryRow(`SELECT COUNT(*) FROM poll_outcome_history`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if history != 0 {
		t.Fatalf("duplicate history survived cascade: %d", history)
	}
	if _, err = db.Exec(`INSERT INTO jobs(discord_user_id,filtros_json,materia_code,guild_id,status,created_at) VALUES('u1','{}','3.1.050',NULL,'active',9)`); err == nil {
		t.Fatal("duplicate search identity insert unexpectedly succeeded")
	}
}

func TestSearchIdentityUniqueUnderConcurrentInsert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO users(discord_user_id,created_at,updated_at) VALUES('u',1,1)`); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, e := db.Exec(`INSERT INTO jobs(discord_user_id,filtros_json,materia_code,guild_id,status,created_at) VALUES('u',?,'3.1.050',NULL,'active',?)`, fmt.Sprintf(`{"n":%d}`, i), i)
			errs <- e
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	successes := 0
	for e := range errs {
		if e == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful inserts=%d, want 1", successes)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE discord_user_id='u' AND materia_code='3.1.050'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("durable rows=%d, want 1", count)
	}
}

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
