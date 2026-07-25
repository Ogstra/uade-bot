package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (discord_user_id TEXT PRIMARY KEY, pause_reason TEXT, pause_until INTEGER, backoff_attempt INTEGER NOT NULL DEFAULT 0, last_pause_notified_reason TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS credentials (discord_user_id TEXT PRIMARY KEY REFERENCES users(discord_user_id), ciphertext TEXT NOT NULL, iv TEXT NOT NULL, auth_tag TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS jobs (id INTEGER PRIMARY KEY AUTOINCREMENT, discord_user_id TEXT NOT NULL REFERENCES users(discord_user_id), filtros_json TEXT NOT NULL, channel_id TEXT, guild_id TEXT, label TEXT, status TEXT NOT NULL DEFAULT 'active', last_polled_at INTEGER, last_outcome TEXT, last_notified_state TEXT, last_notified_cupos INTEGER, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS poll_outcome_history (id INTEGER PRIMARY KEY AUTOINCREMENT, job_id INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE, recorded_at INTEGER NOT NULL, outcome_code TEXT NOT NULL, vacancy_count INTEGER, total_cupos INTEGER);
CREATE INDEX IF NOT EXISTS idx_poll_outcome_history_job_recorded ON poll_outcome_history (job_id, recorded_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS materias (codigo TEXT PRIMARY KEY, nombre TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS command_log (id INTEGER PRIMARY KEY AUTOINCREMENT, discord_user_id TEXT NOT NULL, command_name TEXT NOT NULL, guild_id TEXT, created_at INTEGER NOT NULL);
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
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}
	if err = ensureColumn(db, "jobs", "guild_id", "TEXT"); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	if err = migrateStatements(db, pendingSearchesStatements); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate pending_searches: %w", err)
	}
	return db, nil
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
