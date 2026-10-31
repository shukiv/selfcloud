// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Job statuses.
const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
)

// Job is background work, such as deleting an account's files.
type Job struct {
	ID         string
	AccountID  string
	Kind       string
	Target     string
	Status     string
	Error      string
	Payload    string // JSON arguments; "" when the job needs none
	CreatedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt *time.Time
}

const jobCols = "id, account_id, kind, target, status, error, created_at, updated_at, finished_at, payload"

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var created, updated int64
	var finished sql.NullInt64
	err := row.Scan(&j.ID, &j.AccountID, &j.Kind, &j.Target, &j.Status, &j.Error, &created, &updated, &finished,
		&j.Payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	j.CreatedAt = fromMillis(created)
	j.UpdatedAt = fromMillis(updated)
	j.FinishedAt = fromNullMillis(finished)
	return &j, nil
}

// Job returns the job with the given ID.
func (s *Store) Job(ctx context.Context, id string) (*Job, error) {
	return scanJob(s.r.QueryRowContext(ctx, "SELECT "+jobCols+" FROM jobs WHERE id = ?", id))
}

// UnfinishedJob returns the queued or running job of a kind for an account and target.
func (tx *Tx) UnfinishedJob(ctx context.Context, accountID, kind, target string) (*Job, error) {
	return scanJob(tx.q.QueryRowContext(ctx, "SELECT "+jobCols+` FROM jobs
		WHERE account_id = ? AND kind = ? AND target = ? AND status IN ('queued', 'running')
		ORDER BY created_at LIMIT 1`, accountID, kind, target))
}

// InsertJob queues j.
func (tx *Tx) InsertJob(ctx context.Context, j *Job) error {
	_, err := tx.q.ExecContext(ctx, "INSERT INTO jobs ("+jobCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		j.ID, j.AccountID, j.Kind, j.Target, j.Status, j.Error, millis(j.CreatedAt),
		millis(j.UpdatedAt), nullMillis(j.FinishedAt), j.Payload)
	return err
}

// ClaimJob marks the oldest queued job running and returns it, or ErrNotFound.
func (s *Store) ClaimJob(ctx context.Context) (*Job, error) {
	var j *Job
	err := s.Write(ctx, func(tx *Tx) error {
		var err error
		j, err = scanJob(tx.q.QueryRowContext(ctx, `UPDATE jobs SET status = 'running', updated_at = ?
			WHERE id = (SELECT id FROM jobs WHERE status = 'queued' ORDER BY created_at, id LIMIT 1)
			RETURNING `+jobCols, millis(Now())))
		return err
	})
	return j, err
}

// FinishJob records a job's outcome; jobErr nil means success.
// YieldJob puts a running job back in the queue at the back: its created_at is now, so the queue, which runs the oldest
// first, runs the jobs queued meanwhile before it. The job keeps its state, and runs again from it.
func (s *Store) YieldJob(ctx context.Context, id string) error {
	return s.Write(ctx, func(tx *Tx) error {
		return rowsAffected(tx.q.ExecContext(ctx,
			"UPDATE jobs SET status = 'queued', created_at = ?, updated_at = ? WHERE id = ? AND status = 'running'",
			millis(Now()), millis(Now()), id))
	})
}

func (s *Store) FinishJob(ctx context.Context, id string, jobErr error) error {
	status, msg := JobSucceeded, ""
	if jobErr != nil {
		status, msg = JobFailed, jobErr.Error()
	}
	now := millis(Now())
	return s.Write(ctx, func(tx *Tx) error {
		return rowsAffected(tx.q.ExecContext(ctx,
			"UPDATE jobs SET status = ?, error = ?, updated_at = ?, finished_at = ? WHERE id = ?",
			status, msg, now, now, id))
	})
}

// RequeueRunningJobs puts jobs left running by a previous process back in the queue. Job
// handlers are written to be safe to run again.
func (s *Store) RequeueRunningJobs(ctx context.Context) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		res, err := tx.q.ExecContext(ctx, "UPDATE jobs SET status = 'queued', updated_at = ? WHERE status = 'running'",
			millis(Now()))
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}
