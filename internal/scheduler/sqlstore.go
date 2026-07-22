package scheduler

import (
	"context"
	"database/sql"
	"strconv"
	"time"
)

type SQLStore struct{ DB *sql.DB }

func (s SQLStore) ActiveJobs(ctx context.Context) ([]PersistedJob, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, discord_user_id, COALESCE(channel_id, '') FROM jobs WHERE status = 'active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []PersistedJob
	for rows.Next() {
		var id int64
		var job PersistedJob
		if err = rows.Scan(&id, &job.Account, &job.Channel); err != nil {
			return nil, err
		}
		job.ID = strconv.FormatInt(id, 10)
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s SQLStore) AccountState(ctx context.Context, account string) (AccountState, error) {
	var reason, last sql.NullString
	var until sql.NullInt64
	var attempt int
	err := s.DB.QueryRowContext(ctx, `SELECT pause_reason, pause_until, backoff_attempt, last_pause_notified_reason FROM users WHERE discord_user_id = ?`, account).Scan(&reason, &until, &attempt, &last)
	if err == sql.ErrNoRows {
		return AccountState{}, nil
	}
	state := AccountState{Reason: reason.String, BackoffAttempt: attempt, LastPauseNotifiedReason: last.String}
	if until.Valid {
		state.PauseUntil = time.UnixMilli(until.Int64)
	}
	return state, err
}

func (s SQLStore) SaveAccountState(ctx context.Context, account string, state AccountState) error {
	var reason any
	if state.Reason != "" {
		reason = state.Reason
	}
	var until any
	if !state.PauseUntil.IsZero() {
		until = state.PauseUntil.UnixMilli()
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET pause_reason = ?, pause_until = ?, backoff_attempt = ?, updated_at = ? WHERE discord_user_id = ?`, reason, until, state.BackoffAttempt, time.Now().UnixMilli(), account)
	return err
}

func (s SQLStore) NotificationState(ctx context.Context, jobID string) (NotificationState, error) {
	var key sql.NullString
	var cupos sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT last_notified_state, last_notified_cupos FROM jobs WHERE id = ?`, jobID).Scan(&key, &cupos)
	if err == sql.ErrNoRows {
		return NotificationState{}, nil
	}
	return NotificationState{VacancyKey: key.String, MaxCupos: int(cupos.Int64)}, err
}

func (s SQLStore) SaveNotificationState(ctx context.Context, jobID string, state NotificationState) error {
	var key, cupos any
	if state.VacancyKey != "" {
		key, cupos = state.VacancyKey, state.MaxCupos
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET last_notified_state = ?, last_notified_cupos = ? WHERE id = ?`, key, cupos, jobID)
	return err
}

func (s SQLStore) SaveOutcome(ctx context.Context, jobID string, outcome Outcome, recordedAt time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET last_polled_at = ?, last_outcome = ? WHERE id = ?`, recordedAt.UnixMilli(), outcome.Code, jobID); err != nil {
		return err
	}
	total := 0
	for _, vacancy := range outcome.Vacancies {
		total += vacancy.Cupos
	}
	var count, cupos any
	if outcome.Code == "found" || outcome.Code == "no_vacancies" {
		count, cupos = len(outcome.Vacancies), total
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO poll_outcome_history (job_id, recorded_at, outcome_code, vacancy_count, total_cupos) VALUES (?, ?, ?, ?, ?)`, jobID, recordedAt.UnixMilli(), outcome.Code, count, cupos); err != nil {
		return err
	}
	return tx.Commit()
}

func (s SQLStore) MarkPauseNotification(ctx context.Context, account, reason string) error {
	var value any
	if reason != "" {
		value = reason
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET last_pause_notified_reason = ?, updated_at = ? WHERE discord_user_id = ?`, value, time.Now().UnixMilli(), account)
	return err
}
