// Package jobactions holds the admin-scoped job mutation logic shared by
// every surface that lets an operator pausar/reanudar/detener a search job
// without going through a specific user's Discord session.
//
// Mutate deliberately mirrors the admin=true branch of
// internal/discordhttp.mutateJob (no `AND discord_user_id=?` clause) byte
// for byte in SQL shape, without importing internal/discordhttp. This is a
// leaf package -- it imports neither internal/discordhttp nor
// internal/dashboard -- specifically to avoid any risk of an import cycle
// between those two packages, and to avoid touching commands.go's already
// production-proven Discord code path.
package jobactions

import (
	"context"
	"database/sql"
	"errors"
)

// ErrInvalidAction is returned by Mutate when action is not one of
// "pausar", "reanudar" or "detener". No query is executed in this case.
var ErrInvalidAction = errors.New("jobactions: invalid action")

// Mutate applies action to the job identified by id, with no notion of a
// "owning" Discord user -- equivalent to admin=true in
// internal/discordhttp.mutateJob. It returns the number of rows affected
// (0 when no job with that id exists) and an error only for a genuine
// invalid action or a DB failure.
func Mutate(ctx context.Context, db *sql.DB, action string, id int64) (int64, error) {
	var query string
	var args []any
	switch action {
	case "pausar":
		query = `UPDATE jobs SET status=? WHERE id=?`
		args = []any{"paused_by_user", id}
	case "reanudar":
		query = `UPDATE jobs SET status=? WHERE id=?`
		args = []any{"active", id}
	case "detener":
		query = `DELETE FROM jobs WHERE id=?`
		args = []any{id}
	default:
		return 0, ErrInvalidAction
	}
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
