// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Search over file content (design section 14, slice q3, decision 46). The rows of search_texts are queued by the write
// that gives a node a file's blob (queueText), so every file has its row from the moment it has content. The content
// worker in internal/search takes a pending row, extracts the text outside any transaction, and writes the result in one
// short write (FinishText). Nothing here reads a blob or runs a converter: the store only keeps the rows in step.

// Content states of a search_texts row.
const (
	TextPending = "pending" // queued, or waiting for a retry at next_try
	TextReady   = "ready"   // the text is in search_text_fts
	TextFailed  = "failed"  // a sticky refusal by the converter, retried at next_try
	TextSkipped = "skipped" // a permanent answer: too large, not text, or the converter is off
)

// textRetryBatch is how many failed rows one retry step makes pending.
const textRetryBatch = 100

// TextErrConverterOff is the error of a row skipped because office.enabled is off. ReleaseTextSkipped looks for it, so the
// two must agree.
const TextErrConverterOff = "the converter is off"

// queueText makes the content row of blobID when it has none. It runs in the write that gives a node the blob, so a
// node with a file never lacks its row. A blob shared by copies has one row.
func (tx *Tx) queueText(ctx context.Context, accountID, blobID string) error {
	// Selecting from blobs makes this a no-op for a blob row that is not there, instead of a foreign key failure: a node may
	// name a blob the store does not have (a test fixture, a copy in progress), and that is not this write's business.
	_, err := tx.q.ExecContext(ctx, `INSERT OR IGNORE INTO search_texts (blob_id, account_id, state)
		SELECT id, account_id, 'pending' FROM blobs WHERE id = ? AND account_id = ?`, blobID, accountID)
	return err
}

// dropText removes the content row of blobID and its full-text row. The full-text row goes first: the store's own delete
// does not reach it, and a cascade from blobs removes the row without it. It is called only for a blob that is going.
func (tx *Tx) dropText(ctx context.Context, accountID, blobID string) error {
	// The account's stored bytes go down by the ready row's bytes, in the same write (review S3).
	if _, err := tx.q.ExecContext(ctx, `UPDATE search_text_usage SET bytes = bytes - (
		SELECT st.bytes FROM search_texts st WHERE st.account_id = ? AND st.blob_id = ? AND st.state = 'ready')
		WHERE account_id = ? AND EXISTS (SELECT 1 FROM search_texts st WHERE st.account_id = ? AND st.blob_id = ? AND st.state = 'ready')`,
		accountID, blobID, accountID, accountID, blobID); err != nil {
		return err
	}
	if _, err := tx.q.ExecContext(ctx, `DELETE FROM search_text_fts WHERE rowid IN (
		SELECT id FROM search_texts WHERE account_id = ? AND blob_id = ?)`, accountID, blobID); err != nil {
		return err
	}
	_, err := tx.q.ExecContext(ctx, "DELETE FROM search_texts WHERE account_id = ? AND blob_id = ?", accountID, blobID)
	return err
}

// dropTextAccount removes the full-text rows of an account, before the account is deleted. Its content rows go by the
// cascade.
func (tx *Tx) dropTextAccount(ctx context.Context, accountID string) error {
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM search_text_usage WHERE account_id = ?", accountID); err != nil {
		return err
	}
	_, err := tx.q.ExecContext(ctx, `DELETE FROM search_text_fts WHERE rowid IN (
		SELECT id FROM search_texts WHERE account_id = ?)`, accountID)
	return err
}

// TextBackfillStep queues the content rows of up to limit file blobs after the backfill's cursor, in one write. It reads
// the nodes of the instance in blob-ID order, so a blob shared by copies is read once. The cursor moves to the last blob
// read. When a batch comes back short, the backfill is done, and done is true from then on.
func (s *Store) TextBackfillStep(ctx context.Context, limit int) (done bool, err error) {
	err = s.Write(ctx, func(tx *Tx) error {
		var after string
		var fin int
		if err := tx.q.QueryRowContext(ctx, "SELECT after, done FROM search_text_backfill WHERE id = 1").Scan(&after, &fin); err != nil {
			return err
		}
		if fin == 1 {
			done = true
			return nil
		}
		rows, err := tx.q.QueryContext(ctx, `SELECT DISTINCT n.blob_id, n.account_id FROM nodes n
			WHERE n.kind = 'file' AND n.blob_id IS NOT NULL AND n.blob_id > ? ORDER BY n.blob_id LIMIT ?`, after, limit)
		if err != nil {
			return err
		}
		type pair struct{ blob, account string }
		var batch []pair
		for rows.Next() {
			var p pair
			if err := rows.Scan(&p.blob, &p.account); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, p)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, p := range batch {
			if _, err := tx.q.ExecContext(ctx, `INSERT OR IGNORE INTO search_texts (blob_id, account_id, state)
				SELECT b.id, b.account_id, 'pending' FROM blobs b WHERE b.id = ? AND b.account_id = ? AND b.state = 'live'`,
				p.blob, p.account); err != nil {
				return err
			}
		}
		if len(batch) < limit {
			done = true
			_, err := tx.q.ExecContext(ctx, "UPDATE search_text_backfill SET done = 1 WHERE id = 1")
			return err
		}
		_, err = tx.q.ExecContext(ctx, "UPDATE search_text_backfill SET after = ? WHERE id = 1", batch[len(batch)-1].blob)
		return err
	})
	return done, err
}

// resetTextBackfill starts the backfill again from the first blob. Rows that exist are skipped by the insert, so a repeat
// adds only the missing ones.
func (tx *Tx) resetTextBackfill(ctx context.Context) error {
	_, err := tx.q.ExecContext(ctx, "UPDATE search_text_backfill SET after = '', done = 0 WHERE id = 1")
	return err
}

// TextBackfillDone reports whether the backfill has walked every file blob.
func (s *Store) TextBackfillDone(ctx context.Context) (bool, error) {
	var fin int
	err := s.r.QueryRowContext(ctx, "SELECT done FROM search_text_backfill WHERE id = 1").Scan(&fin)
	return fin == 1, err
}

// TextJob is one pending content row the worker takes.
type TextJob struct {
	ID        int64
	AccountID string
	BlobID    string
	Size      int64
	Attempts  int64
}

// ClaimText returns the next due pending row, round robin across accounts: the first one whose account key sorts after
// after, and within an account the oldest. When no account is after it, the search wraps to the first. ok is false when
// nothing is due. It only reads: FinishText checks the row again before it writes.
func (s *Store) ClaimText(ctx context.Context, after string, now time.Time) (job TextJob, ok bool, err error) {
	const pick = `SELECT st.id, st.account_id, st.blob_id, b.size, st.attempts FROM search_texts st
		JOIN blobs b ON b.id = st.blob_id
		WHERE st.state = 'pending' AND st.account_id > ? AND st.next_try <= ?
		ORDER BY st.account_id, st.id LIMIT 1`
	scan := func(q string, args ...any) (TextJob, bool, error) {
		var j TextJob
		err := s.r.QueryRowContext(ctx, q, args...).Scan(&j.ID, &j.AccountID, &j.BlobID, &j.Size, &j.Attempts)
		if errors.Is(err, sql.ErrNoRows) {
			return TextJob{}, false, nil
		}
		return j, err == nil, err
	}
	job, ok, err = scan(pick, after, millis(now))
	if err != nil || ok {
		return job, ok, err
	}
	return scan(pick, "", millis(now))
}

// StartText counts an attempt in its own write, before a document is sent to the converter (review, decision 46). A
// conversion cut off by a shutdown or a crash keeps the count, so a document that kills the converter runs out of tries.
// The count is only for a row that is still pending.
func (s *Store) StartText(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.q.ExecContext(ctx, "UPDATE search_texts SET attempts = attempts + 1 WHERE id = ? AND state = 'pending'", id)
		return err
	})
}

// RefundText takes back the attempt of StartText when the converter could not be reached: no request reached it, so
// nothing was tried.
func (s *Store) RefundText(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.q.ExecContext(ctx, "UPDATE search_texts SET attempts = attempts - 1 WHERE id = ? AND state = 'pending' AND attempts > 0", id)
		return err
	})
}

// TextResult is what the worker found for one row.
type TextResult struct {
	ID        int64
	AccountID string
	BlobID    string
	// State is pending (a transient outage: the row waits at NextTry, and Attempts is unchanged), failed, skipped or ready.
	State     string
	Source    string // plain or converter; "" for a row that never reached a reader
	Converter string
	Error     string // a fixed message of the worker, never a converter's text or a file name
	// Text is the search form of the extracted text. Only a ready row has text.
	Text      string
	Bytes     int64
	Truncated bool
	Attempts  int64
	NextTry   time.Time
	// Sticky marks a refusal: its row waits for a new converter key (ReleaseTextKey), and the clock does not release it.
	Sticky bool
	// CapAccount and CapTotal are the caps the write checks a ready text against: search.max_text_per_account and
	// search.max_text_total, 0 for none. A text over a cap is recorded as skipped, with the cap's reason.
	CapAccount, CapTotal int64
}

// FinishText records what the worker found for a row, in one write. The row must still be pending or failed for its
// blob, and the blob must still be used by a node or a version of the account. Otherwise nothing is kept: a row whose
// blob is no longer used is deleted, so a later restore queues it again. A ready row's text goes into search_text_fts in
// the same write.
func (s *Store) FinishText(ctx context.Context, r TextResult) error {
	return s.Write(ctx, func(tx *Tx) error {
		var state string
		err := tx.q.QueryRowContext(ctx, "SELECT state FROM search_texts WHERE id = ? AND blob_id = ? AND account_id = ?",
			r.ID, r.BlobID, r.AccountID).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // the row went while the worker read the file: its blob is gone
		}
		if err != nil {
			return err
		}
		if state != TextPending && state != TextFailed {
			return nil // another write has finished this row
		}
		// A node must hold the blob now. A version alone does not keep text (review S3): its blob's text is not wanted.
		var used int
		err = tx.q.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE account_id = ? AND blob_id = ? LIMIT 1`,
			r.AccountID, r.BlobID).Scan(&used)
		if errors.Is(err, sql.ErrNoRows) {
			_, err := tx.q.ExecContext(ctx, "DELETE FROM search_texts WHERE id = ?", r.ID)
			return err
		}
		if err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, "DELETE FROM search_text_fts WHERE rowid = ?", r.ID); err != nil {
			return err
		}
		if r.State == TextReady {
			// The caps are read in this write, so two workers cannot both take the last of the room (review S3).
			ok, reason, err := textRoomOn(ctx, tx.q, r.AccountID, r.Bytes, r.CapAccount, r.CapTotal)
			if err != nil {
				return err
			}
			if !ok {
				r.State, r.Error, r.Text, r.Bytes, r.Truncated = TextSkipped, reason, "", 0, false
			}
		}
		if r.State == TextReady {
			if _, err := tx.q.ExecContext(ctx, "INSERT INTO search_text_fts (rowid, text, account_id) VALUES (?, ?, ?)",
				r.ID, r.Text, r.AccountID); err != nil {
				return err
			}
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO search_text_usage (account_id, bytes) VALUES (?, ?)
				ON CONFLICT (account_id) DO UPDATE SET bytes = bytes + excluded.bytes`, r.AccountID, r.Bytes); err != nil {
				return err
			}
		}
		var next int64
		switch {
		case r.Sticky:
			next = textNever // a refusal waits for a new converter key, not for the clock
		case !r.NextTry.IsZero():
			next = millis(r.NextTry)
		}
		var truncated int64
		if r.Truncated {
			truncated = 1
		}
		_, err = tx.q.ExecContext(ctx, `UPDATE search_texts SET state = ?, source = ?, converter = ?, bytes = ?,
			truncated = ?, error = ?, attempts = ?, next_try = ? WHERE id = ?`,
			r.State, r.Source, r.Converter, r.Bytes, truncated, r.Error, r.Attempts, next, r.ID)
		return err
	})
}

// ReleaseTextRetries makes pending the failed rows whose wait has passed, at most textRetryBatch of them a call. It
// returns how many it made pending.
func (s *Store) ReleaseTextRetries(ctx context.Context, now time.Time) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		res, err := tx.q.ExecContext(ctx, `UPDATE search_texts SET state = 'pending', next_try = 0
			WHERE id IN (SELECT id FROM search_texts WHERE state = 'failed' AND next_try <= ? LIMIT ?)`,
			millis(now), textRetryBatch)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// ReleaseTextSkipped makes pending the rows skipped because the converter was off. It runs when office is turned on, so
// those files are converted without a rebuild. It returns how many it made pending.
func (s *Store) ReleaseTextSkipped(ctx context.Context) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		res, err := tx.q.ExecContext(ctx, `UPDATE search_texts SET state = 'pending', error = '', next_try = 0
			WHERE state = 'skipped' AND error IN (?, ?)`, TextErrConverterOff, TextErrOfficeTextOff)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// textCheck is what SearchCheck found in the content rows. It is counted only once the backfill is done, because until
// then missing rows are work on their way.
type textCheck struct {
	missing  int64
	ftsWrong bool
}

// checkText compares the content rows with the nodes and the full-text table. Callers hold a read.
func (s *Store) checkText(ctx context.Context) (textCheck, error) {
	var c textCheck
	done, err := s.TextBackfillDone(ctx)
	if err != nil {
		return c, err
	}
	if done {
		err = s.r.QueryRowContext(ctx, `SELECT COUNT(DISTINCT n.blob_id) FROM nodes n
			WHERE n.kind = 'file' AND n.blob_id IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM search_texts st WHERE st.blob_id = n.blob_id)`).Scan(&c.missing)
		if err != nil {
			return c, err
		}
	}
	var ready, fts int64
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_texts WHERE state = 'ready'").Scan(&ready); err != nil {
		return c, err
	}
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_text_fts").Scan(&fts); err != nil {
		return c, err
	}
	c.ftsWrong = ready != fts
	return c, nil
}

// clearText empties the content rows and their full-text table, for a rebuild, and starts the backfill again.
func (tx *Tx) clearText(ctx context.Context) error {
	// The usage goes with the text it counts, or a rebuild would double it and fill the cap (review, decision 46).
	for _, q := range []string{"DELETE FROM search_text_fts", "DELETE FROM search_texts", "DELETE FROM search_text_usage"} {
		if _, err := tx.q.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.resetTextBackfill(ctx)
}

// repairText makes the content rows agree with the nodes that need them: when rows are missing, the backfill starts
// again, and it adds only the missing rows.
func (tx *Tx) repairText(ctx context.Context, c textCheck) error {
	if c.missing == 0 {
		return nil
	}
	return tx.resetTextBackfill(ctx)
}
