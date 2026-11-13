// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

// The policy of the stored text (decision 46): which blobs have text, what the text may cost, and which
// rows wait. The rules are kept by the writes that change a node, and the worker reads them before it sends anything.

// Error texts of a skipped row that the policy chose. ReleaseTextSkipped and ReleaseTextCapped look for them.
const (
	TextErrOfficeTextOff = "office text search is off"
	TextErrAccountFull   = "the account's text limit is full"
	TextErrInstallFull   = "the installation's text limit is full"
	// TextErrParked is the error of a pending row whose file is in the trash. It is not a skip: the row waits, and it is
	// read when the file is out of the trash.
	TextErrParked = "the file is in the trash"
)

// textNever is the next_try of a refusal. It is far in the future, so ReleaseTextRetries never makes the row pending;
// only a change of converter key does (ReleaseTextKey).
const textNever = math.MaxInt64

// dropTextIfUnused removes the content rows of blobs that no node holds any more. A version may still hold a blob, and
// its text is not kept for a version (decision 46). It is called where a node stops holding a blob.
func (tx *Tx) dropTextIfUnused(ctx context.Context, accountID string, blobs ...string) error {
	for _, b := range blobs {
		if b == "" {
			continue
		}
		var used int
		err := tx.q.QueryRowContext(ctx, "SELECT 1 FROM nodes WHERE account_id = ? AND blob_id = ? LIMIT 1", accountID, b).Scan(&used)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := tx.dropText(ctx, accountID, b); err != nil {
			return err
		}
	}
	return nil
}

// textBlobsOf returns the blobs held by the nodes of accountID that match cond, and by their versions. cond is a fixed SQL
// condition on the alias n, and args are its values. It is read before the nodes are deleted, so that the text of a blob
// no other node holds goes in the same write (decision 46).
func (tx *Tx) textBlobsOf(ctx context.Context, accountID, cond string, args ...any) ([]string, error) {
	q := `SELECT n.blob_id FROM nodes n WHERE n.account_id = ? AND n.blob_id IS NOT NULL AND (` + cond + `)
		UNION SELECT v.blob_id FROM versions v JOIN nodes n ON n.id = v.node_id WHERE n.account_id = ? AND (` + cond + `)`
	all := append([]any{accountID}, args...)
	all = append(all, accountID)
	all = append(all, args...)
	rows, err := tx.q.QueryContext(ctx, q, all...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var blobs []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		blobs = append(blobs, b)
	}
	return blobs, rows.Err()
}

// DropUnusedText removes the content rows of every blob no node holds: the sweep's catch-all for a node that was purged
// while a version still held its blob. It returns how many rows it removed. A version alone never keeps text.
func (s *Store) DropUnusedText(ctx context.Context) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		rows, err := tx.q.QueryContext(ctx, `SELECT st.account_id, st.blob_id FROM search_texts st
			WHERE NOT EXISTS (SELECT 1 FROM nodes WHERE nodes.account_id = st.account_id AND nodes.blob_id = st.blob_id)`)
		if err != nil {
			return err
		}
		type pair struct{ acc, blob string }
		var gone []pair
		for rows.Next() {
			var p pair
			if err := rows.Scan(&p.acc, &p.blob); err != nil {
				rows.Close()
				return err
			}
			gone = append(gone, p)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, p := range gone {
			if err := tx.dropText(ctx, p.acc, p.blob); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// TextLive reports whether blobID is held by a node of accountID that is not in the trash, itself or through an ancestor.
// A trashed file's text is not sent to the converter; the worker parks the row until the file is out again.
func (s *Store) TextLive(ctx context.Context, accountID, blobID string) (bool, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT id FROM nodes WHERE account_id = ? AND blob_id = ?", accountID, blobID)
	if err != nil {
		return false, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, id := range ids {
		self, above, err := trashState(ctx, s.r, accountID, id)
		if err != nil {
			return false, err
		}
		if !self && !above {
			return true, nil
		}
	}
	return false, nil
}

// TextRoom says whether an account may store need more bytes of text, and which cap would stop it. A zero cap is none.
// The worker asks before it reads a file, so a capped account's files are not read at all.
func (s *Store) TextRoom(ctx context.Context, accountID string, need, perAccount, total int64) (bool, string, error) {
	return textRoomOn(ctx, s.r, accountID, need, perAccount, total)
}

// textRoomOn is TextRoom on a querier, so FinishText can check the caps inside its write.
func textRoomOn(ctx context.Context, q querier, accountID string, need, perAccount, total int64) (bool, string, error) {
	var acc, all int64
	if err := q.QueryRowContext(ctx, "SELECT COALESCE((SELECT bytes FROM search_text_usage WHERE account_id = ?), 0)", accountID).Scan(&acc); err != nil {
		return false, "", err
	}
	if err := q.QueryRowContext(ctx, "SELECT COALESCE(SUM(bytes), 0) FROM search_text_usage").Scan(&all); err != nil {
		return false, "", err
	}
	switch {
	case perAccount > 0 && acc+need > perAccount:
		return false, TextErrAccountFull, nil
	case total > 0 && all+need > total:
		return false, TextErrInstallFull, nil
	}
	return true, "", nil
}

// textBytes is the stored text of one account, from its usage row.
func (s *Store) textBytes(ctx context.Context, accountID string) (int64, error) {
	var n int64
	err := s.r.QueryRowContext(ctx, "SELECT COALESCE((SELECT bytes FROM search_text_usage WHERE account_id = ?), 0)", accountID).Scan(&n)
	return n, err
}

// TextUsage is the text an account holds, and how many of its files were skipped for a cap. It is the owner's view.
type TextUsage struct {
	Bytes       int64
	SkippedCaps int64
}

// TextUsageOf reads the text usage of an account.
func (s *Store) TextUsageOf(ctx context.Context, accountID string) (TextUsage, error) {
	var u TextUsage
	var err error
	if u.Bytes, err = s.textBytes(ctx, accountID); err != nil {
		return u, err
	}
	err = s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_texts WHERE account_id = ? AND state = 'skipped'
		AND error IN (?, ?)`, accountID, TextErrAccountFull, TextErrInstallFull).Scan(&u.SkippedCaps)
	return u, err
}

// ReleaseTextKey makes pending the refusals of an earlier converter key. A refusal is sticky until the converter's key
// changes (decision 37's rule); when the key differs, the refused rows are tried again, once.
func (s *Store) ReleaseTextKey(ctx context.Context, key string) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		res, err := tx.q.ExecContext(ctx, `UPDATE search_texts SET state = 'pending', next_try = 0, attempts = 0, error = ''
			WHERE state = 'failed' AND next_try = ? AND converter <> ?`, textNever, key)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// textReleaseBatch is how many capped rows one release makes pending. A release is bounded, so a full cap with a large
// backlog does not rewrite the backlog at once.
const textReleaseBatch = 1000

// ReleaseTextCapped makes pending, in batches, the rows skipped for a cap that now has room for one more file of textMax
// bytes. The sweep calls it, so a capped file is read again once its account (or the installation) has room, and not on
// every pass while it has none. It returns how many rows it released.
func (s *Store) ReleaseTextCapped(ctx context.Context, textMax, perAccount, total int64) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		if perAccount > 0 {
			res, err := tx.q.ExecContext(ctx, `UPDATE search_texts SET state = 'pending', error = '', next_try = 0
				WHERE id IN (SELECT st.id FROM search_texts st WHERE st.state = 'skipped' AND st.error = ?
					AND COALESCE((SELECT u.bytes FROM search_text_usage u WHERE u.account_id = st.account_id), 0) + ? <= ?
					LIMIT ?)`, TextErrAccountFull, textMax, perAccount, textReleaseBatch)
			if err != nil {
				return err
			}
			m, err := res.RowsAffected()
			if err != nil {
				return err
			}
			n += m
		}
		if total > 0 {
			var all int64
			if err := tx.q.QueryRowContext(ctx, "SELECT COALESCE(SUM(bytes), 0) FROM search_text_usage").Scan(&all); err != nil {
				return err
			}
			if all+textMax <= total {
				res, err := tx.q.ExecContext(ctx, `UPDATE search_texts SET state = 'pending', error = '', next_try = 0
					WHERE id IN (SELECT id FROM search_texts WHERE state = 'skipped' AND error = ? LIMIT ?)`,
					TextErrInstallFull, textReleaseBatch)
				if err != nil {
					return err
				}
				m, err := res.RowsAffected()
				if err != nil {
					return err
				}
				n += m
			}
		}
		return nil
	})
	return n, err
}
