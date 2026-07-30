package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (discord_user_id TEXT PRIMARY KEY, pause_reason TEXT, pause_until INTEGER, backoff_attempt INTEGER NOT NULL DEFAULT 0, last_pause_notified_reason TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS credentials (discord_user_id TEXT PRIMARY KEY REFERENCES users(discord_user_id), ciphertext TEXT NOT NULL, iv TEXT NOT NULL, auth_tag TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS jobs (id INTEGER PRIMARY KEY AUTOINCREMENT, discord_user_id TEXT NOT NULL REFERENCES users(discord_user_id), filtros_json TEXT NOT NULL, materia_code TEXT, channel_id TEXT, guild_id TEXT, label TEXT, status TEXT NOT NULL DEFAULT 'active', last_polled_at INTEGER, last_outcome TEXT, last_notified_state TEXT, last_notified_cupos INTEGER, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS poll_outcome_history (id INTEGER PRIMARY KEY AUTOINCREMENT, job_id INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE, recorded_at INTEGER NOT NULL, outcome_code TEXT NOT NULL, vacancy_count INTEGER, total_cupos INTEGER);
CREATE INDEX IF NOT EXISTS idx_poll_outcome_history_job_recorded ON poll_outcome_history (job_id, recorded_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS materias (codigo TEXT PRIMARY KEY, nombre TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS command_log (id INTEGER PRIMARY KEY AUTOINCREMENT, discord_user_id TEXT NOT NULL, command_name TEXT NOT NULL, guild_id TEXT, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS notification_delivery_progress (job_id INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE, delivery_fingerprint TEXT NOT NULL, route TEXT NOT NULL, fragment_index INTEGER NOT NULL, fragment_count INTEGER NOT NULL, fragment_fingerprint TEXT NOT NULL, delivered_at INTEGER NOT NULL, PRIMARY KEY (job_id, delivery_fingerprint, route, fragment_index));
`

// pendingSearchesStatements creates pending_searches, which holds the exact
// filters/channel/guild/label of a /buscar request made before the user has
// any saved credentials. Deliberately has no FK to users(discord_user_id):
// a pending search can exist BEFORE the users row does, since that row is
// only created inside submitCredentials' own transaction.
var pendingSearchesStatements = []string{
	"CREATE TABLE IF NOT EXISTS pending_searches (discord_user_id TEXT PRIMARY KEY, filtros_json TEXT NOT NULL, channel_id TEXT, guild_id TEXT, label TEXT, created_at INTEGER NOT NULL)",
}

// migrateStatements runs statements inside a single explicit transaction: if
// any statement fails, the deferred Rollback undoes everything executed so
// far in that same transaction, so a migration never leaves the schema
// half-applied (D-12).
func migrateStatements(db *sql.DB, statements []string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range statements {
		if _, err = tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func Open(path string) (*sql.DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, err
		}
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	db, err := sql.Open("sqlite", path+separator+"_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// modernc.org/sqlite's ResetSession only checks that the connection is
	// still open (sqlite.go conn.usable), not whether it is still mid an
	// uncommitted transaction. If a Tx.Commit fails (e.g. SQLITE_BUSY because
	// a concurrent connection on database/sql's default unbounded pool holds
	// a SHARED read lock while this one tries to escalate to EXCLUSIVE at
	// commit time), Go's database/sql already set tx.done=1 before invoking
	// the driver, so the safety-net `defer tx.Rollback()` becomes a no-op
	// (ErrTxDone) and never issues an actual ROLLBACK. The connection then
	// goes back into the idle pool still inside that open transaction, and
	// the next Begin() on it fails hard with "cannot start a transaction
	// within a transaction". Pinning the pool to a single physical
	// connection makes concurrent SQLite access from this process
	// impossible, so that race -- and the poisoned-connection state it
	// produces -- can never occur (see .planning/debug/resolved/nested-sqlite-tx-scheduler.md).
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}
	if err = ensureColumn(db, "jobs", "guild_id", "TEXT"); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	if err = migrateSearchIdentity(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate search identity: %w", err)
	}
	if err = migrateStatements(db, pendingSearchesStatements); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate pending_searches: %w", err)
	}
	return db, nil
}

// migrateSearchIdentity adds and backfills the durable product identity for a
// search. Every schema/data step is kept in one transaction so a legacy DB is
// either fully converged or left untouched. Invalid legacy JSON deliberately
// maps to NULL and is excluded from the partial unique index.
func migrateSearchIdentity(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(jobs)")
	if err != nil {
		return err
	}
	hasColumn := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		hasColumn = hasColumn || name == "materia_code"
	}
	if err = rows.Close(); err != nil {
		return err
	}

	statements := make([]string, 0, 4)
	if !hasColumn {
		statements = append(statements, "ALTER TABLE jobs ADD COLUMN materia_code TEXT")
	}
	statements = append(statements,
		`UPDATE jobs SET materia_code = CASE
			WHEN json_valid(filtros_json) THEN CASE
				WHEN json_type(filtros_json, '$.materiaCodigo') = 'text'
				 AND trim(json_extract(filtros_json, '$.materiaCodigo')) <> ''
				 AND trim(json_extract(filtros_json, '$.materiaCodigo')) NOT GLOB '*[^0-9.]*'
				 AND length(trim(json_extract(filtros_json, '$.materiaCodigo'))) - length(replace(trim(json_extract(filtros_json, '$.materiaCodigo')), '.', '')) = 2
				 AND trim(json_extract(filtros_json, '$.materiaCodigo')) NOT LIKE '.%'
				 AND trim(json_extract(filtros_json, '$.materiaCodigo')) NOT LIKE '%.'
				 AND trim(json_extract(filtros_json, '$.materiaCodigo')) NOT LIKE '%..%'
				THEN trim(json_extract(filtros_json, '$.materiaCodigo'))
			END
		END
		WHERE materia_code IS NULL`,
		`DELETE FROM jobs AS duplicate
		 WHERE duplicate.materia_code IS NOT NULL
		   AND EXISTS (
			SELECT 1 FROM jobs AS keeper
			 WHERE keeper.discord_user_id = duplicate.discord_user_id
			   AND COALESCE(keeper.guild_id, '') = COALESCE(duplicate.guild_id, '')
			   AND keeper.materia_code = duplicate.materia_code
			   AND keeper.id < duplicate.id
		   )`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_search_identity
		 ON jobs(discord_user_id, COALESCE(guild_id, ''), materia_code)
		 WHERE materia_code IS NOT NULL`,
	)
	return migrateStatements(db, statements)
}

func ensureColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var defaultValue any
		if err = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		found = found || name == column
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition)
	return err
}
