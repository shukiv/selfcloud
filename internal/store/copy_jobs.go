// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// Chunked copy phases (migration 0027, decision 34). A job moves snapshot -> copy -> publish -> done. A failure, at any
// phase, moves it to cleanup, and cleanup removes the staged tree and releases the reservation. done keeps no staged
// nodes; it only lets the snapshot rows go in bounded steps.
const (
	CopyPhaseSnapshot = "snapshot"
	CopyPhaseCopy     = "copy"
	CopyPhasePublish  = "publish"
	CopyPhaseCleanup  = "cleanup"
	CopyPhaseDone     = "done"
)

// CopyJob is the state of one chunked copy. ReservedBytes is the amount held against the account's quota; it is zero
// once released, in the same write that releases it. Nodes, Files and Bytes count what the batches have copied.
type CopyJob struct {
	JobID           string
	AccountID       string
	StagingFolderID string
	StagingRootID   string
	RootID          string // the new root's ID, which the job's target names
	SourceID        string
	DestFolderID    string
	DestParentID    string
	Name            string // the new root's name, as the request gave it; "" keeps the source's name
	NameKey         string
	OnConflict      string
	KeepSharing     bool // whether the copies keep editors_can_share, decided when the job started
	KeepRestricted  int  // -1 until the first batch decides it; then 0 or 1, and it never changes (see the batches)
	Phase           string
	SnapshotCount   int
	Cursor          int64 // the seq of the last snapshot row the batches have handled
	ReservedBytes   int64
	Nodes           int64
	Files           int64
	Bytes           int64
	Failure         string
	Actor           string // the caller, as JSON, for the record; the policy checks use the job's actor fields
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const copyJobCols = `job_id, account_id, staging_folder_id, staging_root_id, root_id, source_id, dest_folder_id,
	dest_parent_id, name, name_key, on_conflict, keep_sharing, keep_restricted, phase, snapshot_count, cursor, reserved_bytes,
	nodes, files, bytes, failure, actor, created_at, updated_at`

func scanCopyJob(row interface{ Scan(...any) error }) (*CopyJob, error) {
	var j CopyJob
	var keep, keepRestricted int64
	var created, updated int64
	err := row.Scan(&j.JobID, &j.AccountID, &j.StagingFolderID, &j.StagingRootID, &j.RootID, &j.SourceID,
		&j.DestFolderID, &j.DestParentID, &j.Name, &j.NameKey, &j.OnConflict, &keep, &keepRestricted, &j.Phase, &j.SnapshotCount,
		&j.Cursor, &j.ReservedBytes, &j.Nodes, &j.Files, &j.Bytes, &j.Failure, &j.Actor, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	j.KeepSharing = keep == 1
	j.KeepRestricted = int(keepRestricted)
	j.CreatedAt = fromMillis(created)
	j.UpdatedAt = fromMillis(updated)
	return &j, nil
}

// CopyJobOf returns the chunked copy of a job, or ErrNotFound for a job that is not one (a copy queued before
// migration 0027, or any other job).
func (s *Store) CopyJobOf(ctx context.Context, jobID string) (*CopyJob, error) {
	return scanCopyJob(s.r.QueryRowContext(ctx, "SELECT "+copyJobCols+" FROM copy_jobs WHERE job_id = ?", jobID))
}

// CopyJobOf returns the chunked copy of a job, read inside the transaction.
func (tx *Tx) CopyJobOf(ctx context.Context, jobID string) (*CopyJob, error) {
	return scanCopyJob(tx.q.QueryRowContext(ctx, "SELECT "+copyJobCols+" FROM copy_jobs WHERE job_id = ?", jobID))
}

// CreateStagingFolder makes the staging folder of a copy and its root. The folder has staged = 1, so no folder read
// returns it, and its root has no parent, as every folder's root does.
func (tx *Tx) CreateStagingFolder(ctx context.Context, accountID, folderID, rootID string, at time.Time) error {
	if _, err := tx.q.ExecContext(ctx, `INSERT INTO folders (`+folderCols+`, staged)
		VALUES (?, ?, NULL, 'copy staging', ?, 'team', NULL, ?, ?, 0, 0, 0, 0, 1)`,
		folderID, accountID, "copy-staging-"+folderID, millis(at), millis(at)); err != nil {
		return err
	}
	_, err := tx.q.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind,
		created_at, modified_at) VALUES (?, ?, ?, NULL, '', '', 'dir', ?, ?)`, rootID, accountID, folderID, millis(at), millis(at))
	return err
}

// InsertCopyJob records a chunked copy. Its staging folder must exist already (CreateStagingFolder), in the same
// transaction, and the reservation must be made against that folder.
func (tx *Tx) InsertCopyJob(ctx context.Context, j *CopyJob) error {
	_, err := tx.q.ExecContext(ctx, "INSERT INTO copy_jobs ("+copyJobCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		j.JobID, j.AccountID, j.StagingFolderID, j.StagingRootID, j.RootID, j.SourceID, j.DestFolderID, j.DestParentID,
		j.Name, j.NameKey, j.OnConflict, boolInt(j.KeepSharing), j.KeepRestricted, j.Phase, j.SnapshotCount, j.Cursor, j.ReservedBytes,
		j.Nodes, j.Files, j.Bytes, j.Failure, j.Actor, millis(j.CreatedAt), millis(j.UpdatedAt))
	return err
}

// SetCopyPhase moves a copy to phase, with the failure text that led there ("" when none).
func (tx *Tx) SetCopyPhase(ctx context.Context, jobID, phase, failure string, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE copy_jobs SET phase = ?, failure = ?, updated_at = ? WHERE job_id = ?`,
		phase, failure, millis(at), jobID))
}

// DerivedNodeID is the ID a copy gives the node src of the source tree: the new root for the source, and a derived
// ID for every other node. A batch that runs again names the same nodes.
func (j *CopyJob) DerivedNodeID(src string) string {
	if src == j.SourceID {
		return j.RootID
	}
	return id.Derive("nod", j.JobID+"/"+src)
}

// ResetCopySnapshot deletes up to limit snapshot rows of a copy, so that a snapshot that was interrupted can start
// again. It returns true when no rows remain.
func (tx *Tx) ResetCopySnapshot(ctx context.Context, jobID string, limit int) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM copy_job_nodes WHERE job_id = ? AND seq IN
		(SELECT seq FROM copy_job_nodes WHERE job_id = ? ORDER BY seq LIMIT ?)`, jobID, jobID, limit)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n < int64(limit), err
}

// SubtreeRow is one node of a copy's snapshot: its ID, its parent's ID ("" for none) and its depth below the source.
type SubtreeRow struct {
	ID     string
	Parent string
	Depth  int
}

// ErrSourceChanged means the source tree changed in a way that makes the copy wrong: a node of the snapshot was
// trashed, deleted or moved, or a node's parent is not staged. The copy fails with nothing visible.
var ErrSourceChanged = errors.New("source_changed: the source changed during the copy; nothing was copied")

// CopyableTotals counts what a chunked copy of node id will make: the files of its untrashed subtree and their bytes,
// without versions (a copy carries no versions). It is the copy's reservation. SubtreeTotals counts trashed nodes and
// versions too, which a copy does not make, so a copy could be refused for quota it does not need.
func (s *Store) CopyableTotals(ctx context.Context, accountID, id string) (files, bytes int64, err error) {
	err = s.r.QueryRowContext(ctx, `WITH RECURSIVE sub(nid) AS (
		SELECT id FROM nodes WHERE account_id = ? AND id = ? AND trashed_at IS NULL
		UNION ALL
		SELECT c.id FROM nodes c JOIN sub ON c.parent_id = sub.nid WHERE c.account_id = ? AND c.trashed_at IS NULL
	)
	SELECT COUNT(*), COALESCE(SUM(n.size), 0) FROM sub JOIN nodes n ON n.id = sub.nid
	WHERE n.account_id = ? AND n.kind = 'file'`, accountID, id, accountID, accountID).Scan(&files, &bytes)
	return files, bytes, err
}

// SubtreeRows reads node id's untrashed subtree, the node itself included, in one statement, so the rows are one
// consistent view of the tree. It returns at most limit+1 rows, so a caller compares the count with its limit. The
// rows come in depth order, then by ID, so a parent always comes before its children.
func (s *Store) SubtreeRows(ctx context.Context, accountID, id string, limit int) ([]SubtreeRow, error) {
	rows, err := s.r.QueryContext(ctx, `WITH RECURSIVE sub(nid, pid, depth) AS (
		SELECT id, COALESCE(parent_id, ''), 0 FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT c.id, COALESCE(c.parent_id, ''), sub.depth + 1 FROM nodes c JOIN sub ON c.parent_id = sub.nid
		WHERE c.account_id = ? AND c.trashed_at IS NULL
	)
	SELECT nid, pid, depth FROM sub ORDER BY depth, nid LIMIT ?`,
		accountID, id, accountID, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubtreeRow
	for rows.Next() {
		var r SubtreeRow
		if err := rows.Scan(&r.ID, &r.Parent, &r.Depth); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetKeepRestricted records whether the copy's copies keep restricted values. It is decided by the first batch, and
// the store refuses to change it afterwards, so a copy's labels are all made by one rule.
func (tx *Tx) SetKeepRestricted(ctx context.Context, jobID string, keep bool, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE copy_jobs SET keep_restricted = ?, updated_at = ?
		WHERE job_id = ? AND keep_restricted = -1`, boolInt(keep), millis(at), jobID))
}

// InsertCopySnapshot stores rows as the snapshot rows of a copy, from seq fromSeq on, in the order given, each with
// the parent it had when the snapshot was read.
func (tx *Tx) InsertCopySnapshot(ctx context.Context, jobID string, fromSeq int, rows []SubtreeRow) error {
	for i, r := range rows {
		var seen int
		if err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM copy_job_nodes WHERE job_id = ? AND node_id = ?`,
			jobID, r.ID).Scan(&seen); err != nil {
			return err
		}
		if seen > 0 {
			return ErrSourceChanged // a node listed twice: the tree changed between the reads of two pages
		}
		if _, err := tx.q.ExecContext(ctx, `INSERT INTO copy_job_nodes (job_id, seq, node_id, parent_id) VALUES (?, ?, ?, ?)`,
			jobID, fromSeq+i, r.ID, r.Parent); err != nil {
			return err
		}
	}
	return nil
}

// SetSnapshotDone records that the snapshot of count rows is complete, and moves the copy to the copy phase.
func (tx *Tx) SetSnapshotDone(ctx context.Context, jobID string, count int, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE copy_jobs SET snapshot_count = ?, phase = 'copy', updated_at = ?
		WHERE job_id = ?`, count, millis(at), jobID))
}

// nodesByIDs reads the nodes of accountID named by ids, in one statement.
func (tx *Tx) nodesByIDs(ctx context.Context, accountID string, ids []string) (map[string]*Node, error) {
	b, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := tx.q.QueryContext(ctx, nodeSelect+" WHERE n.account_id = ? AND n.id IN (SELECT value FROM json_each(?))",
		accountID, string(b))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]*Node, len(ids))
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out[n.ID] = n
	}
	return out, rows.Err()
}

// StagedCopyBatch copies up to limit snapshot rows of a copy after its cursor into the staging folder, in this
// transaction, and advances the cursor in the same write, so a batch either happens whole or not at all. A snapshot
// node that was deleted, trashed or moved since the snapshot fails the copy with ErrSourceChanged, and so does a node
// whose parent is not staged. A rename or a new content is not a change of structure: a source node is read as it is
// now, so a file replaced after the snapshot copies the new content. A node created after the snapshot is not in it,
// and is not copied. by is the caller who runs the copy; it is the
// creator of every copy. more reports whether rows remain after this batch.
func (tx *Tx) StagedCopyBatch(ctx context.Context, j *CopyJob, limit int, keepRestricted bool, by string,
	at time.Time) (copied int, more bool, err error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT seq, node_id, parent_id FROM copy_job_nodes WHERE job_id = ? AND seq > ?
		ORDER BY seq LIMIT ?`, j.JobID, j.Cursor, limit)
	if err != nil {
		return 0, false, err
	}
	var seqs []int64
	var ids, parents []string
	for rows.Next() {
		var seq int64
		var nid, pid string
		if err := rows.Scan(&seq, &nid, &pid); err != nil {
			rows.Close()
			return 0, false, err
		}
		seqs = append(seqs, seq)
		ids = append(ids, nid)
		parents = append(parents, pid)
	}
	if err := rows.Close(); err != nil {
		return 0, false, err
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	if len(ids) == 0 {
		return 0, false, nil
	}
	srcs, err := tx.nodesByIDs(ctx, j.AccountID, ids)
	if err != nil {
		return 0, false, err
	}
	var pairs []LabelCopy
	var files, bytes int64
	for i, sid := range ids {
		src, ok := srcs[sid]
		// A snapshot node that is gone, trashed, or under another parent now is a structural change: the copy would
		// differ from the snapshot. A rename or a new content is not; those are taken as they are read here.
		if !ok || src.TrashedAt != nil || src.ParentID != parents[i] {
			return 0, false, ErrSourceChanged
		}
		c := *src
		c.ID = j.DerivedNodeID(sid)
		c.FolderID = j.StagingFolderID
		if sid == j.SourceID {
			c.ParentID, c.Name, c.NameKey = j.StagingRootID, j.Name, j.NameKey
			if j.Name == "" {
				c.Name, c.NameKey = src.Name, src.NameKey
			}
		} else {
			c.ParentID = j.DerivedNodeID(src.ParentID)
			if !j.parentStaged(ctx, tx, c.ParentID) {
				return 0, false, ErrSourceChanged // the parent is in the snapshot and not copied yet: it changed
			}
		}
		c.CreatedAt, c.ModifiedAt = at, at
		c.CreatedBy, c.ModifiedBy = by, by
		c.OwnerID = ""
		if !j.KeepSharing {
			c.EditorsCanShare = false
		}
		c.ETag = id.New("etg")
		if err := tx.InsertNode(ctx, &c); err != nil {
			return 0, false, err
		}
		copied++
		if c.Kind == KindFile {
			files++
			bytes += c.Size
		}
		pairs = append(pairs, LabelCopy{From: sid, To: c.ID})
	}
	has, err := tx.HasLabelFields(ctx, j.AccountID)
	if err != nil {
		return 0, false, err
	}
	if has && len(pairs) > 0 {
		if err := tx.CopyLabelValues(ctx, j.AccountID, pairs, keepRestricted); err != nil {
			return 0, false, err
		}
	}
	last := seqs[len(seqs)-1]
	if err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE copy_jobs SET cursor = ?, nodes = nodes + ?, files = files + ?,
		bytes = bytes + ?, updated_at = ? WHERE job_id = ?`, last, copied, files, bytes, millis(at), j.JobID)); err != nil {
		return 0, false, err
	}
	return copied, len(ids) == limit, nil
}

// parentStaged reports whether a node with the given ID is in the staging folder of j.
func (j *CopyJob) parentStaged(ctx context.Context, tx *Tx, nodeID string) bool {
	var ok int
	err := tx.q.QueryRowContext(ctx, "SELECT 1 FROM nodes WHERE account_id = ? AND id = ? AND folder_id = ?",
		j.AccountID, nodeID, j.StagingFolderID).Scan(&ok)
	return err == nil
}

// CountUnfinishedCopies counts the chunked copies of accountID whose job is queued or running.
func (tx *Tx) CountUnfinishedCopies(ctx context.Context, accountID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM copy_jobs c JOIN jobs j ON j.id = c.job_id
		WHERE c.account_id = ? AND j.status IN ('queued', 'running')`, accountID).Scan(&n)
	return n, err
}

// CopySnapshotChanged reports whether the source's subtree no longer matches the snapshot: a snapshot node is gone,
// trashed, or has another parent. The publish asks it inside its write, so a change after the last batch fails the
// copy too: a directory moved after its batch is seen here, through its own row.
func (tx *Tx) CopySnapshotChanged(ctx context.Context, j *CopyJob) (bool, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM copy_job_nodes c
		LEFT JOIN nodes n ON n.account_id = ? AND n.id = c.node_id
		WHERE c.job_id = ? AND (n.id IS NULL OR n.trashed_at IS NOT NULL OR COALESCE(n.parent_id, '') <> c.parent_id)`,
		j.AccountID, j.JobID).Scan(&n)
	return n > 0, err
}

// StagedTotals returns the files and bytes the staged tree holds now, which the publish charges to the destination.
func (tx *Tx) StagedTotals(ctx context.Context, j *CopyJob) (files, bytes int64, err error) {
	err = tx.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN kind = 'file' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN kind = 'file' THEN size ELSE 0 END), 0)
		FROM nodes WHERE account_id = ? AND folder_id = ? AND parent_id IS NOT NULL`, j.AccountID, j.StagingFolderID).
		Scan(&files, &bytes)
	return files, bytes, err
}

// StagedItems counts the staged nodes that are not folders (files and shortcuts): the nodes the one-write copy reports.
func (tx *Tx) StagedItems(ctx context.Context, j *CopyJob) (int64, error) {
	var n int64
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE account_id = ? AND folder_id = ?
		AND parent_id IS NOT NULL AND kind <> 'dir'`, j.AccountID, j.StagingFolderID).Scan(&n)
	return n, err
}

// StagedFiles returns up to limit staged nodes that are not folders, for the per-file events of a small copy.
func (tx *Tx) StagedFiles(ctx context.Context, j *CopyJob, limit int) ([]*Node, error) {
	rows, err := tx.q.QueryContext(ctx, nodeSelect+` WHERE n.account_id = ? AND n.folder_id = ? AND n.parent_id IS NOT NULL
		AND n.kind <> 'dir' ORDER BY n.id LIMIT ?`, j.AccountID, j.StagingFolderID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// PublishStaged moves the staged tree into folderID under parentID, named name, in this transaction. Every node of
// the tree moves, the new root included, and it is the only node whose parent and name change. The staging folder and
// its root go, the reservation is released (the caller charges the destination with applyUsage first), and the copy
// is done. One statement moves the rows, so the write is bounded by the tree's size and not by a batch's.
func (tx *Tx) PublishStaged(ctx context.Context, j *CopyJob, folderID, parentID, name, key string, at time.Time) error {
	if _, err := tx.q.ExecContext(ctx, `UPDATE nodes SET folder_id = ?,
		owner_id = COALESCE((SELECT owner_user_id FROM folders WHERE id = ? AND kind = 'personal'), owner_id)
		WHERE account_id = ? AND folder_id = ? AND parent_id IS NOT NULL`,
		folderID, folderID, j.AccountID, j.StagingFolderID); err != nil {
		return err
	}
	if err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE nodes SET parent_id = ?, name = ?, name_key = ?, modified_at = ?
		WHERE account_id = ? AND id = ?`, parentID, name, key, millis(at), j.AccountID, j.RootID)); err != nil {
		return err
	}
	if _, err := tx.q.ExecContext(ctx, `DELETE FROM nodes WHERE account_id = ? AND folder_id = ?`,
		j.AccountID, j.StagingFolderID); err != nil {
		return err
	}
	if _, err := tx.q.ExecContext(ctx, `DELETE FROM folders WHERE account_id = ? AND id = ? AND staged = 1`,
		j.AccountID, j.StagingFolderID); err != nil {
		return err
	}
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE copy_jobs SET phase = 'done', reserved_bytes = 0, updated_at = ?
		WHERE job_id = ?`, millis(at), j.JobID))
}

// ReleaseCopyReservation takes the reservation of a copy off its staging folder's counters, and sets the job's
// reserved_bytes to zero in the same write. It returns the amount released, which is zero when none was held.
func (tx *Tx) ReleaseCopyReservation(ctx context.Context, j *CopyJob, at time.Time) (int64, error) {
	if j.ReservedBytes == 0 {
		return 0, nil
	}
	if err := tx.AdjustFolders(ctx, j.AccountID, FolderChange{FolderID: j.StagingFolderID,
		Change: UsageChange{Reserved: -j.ReservedBytes}}); err != nil {
		return 0, err
	}
	if err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE copy_jobs SET reserved_bytes = 0, updated_at = ? WHERE job_id = ?`,
		millis(at), j.JobID)); err != nil {
		return 0, err
	}
	return j.ReservedBytes, nil
}

// CleanupCopyStep does one bounded step of the cleanup of a copy, in its own write. For a copy that did not publish, it
// deletes up to limit staged nodes, deepest first, so no delete cascades beyond the batch, and their snapshot rows with
// them. When none are left it releases the reservation, removes the staging folder, and removes the job's row. For a
// published copy it deletes up to limit snapshot rows, and never a node: those now belong to the destination. It
// returns true when the copy's rows are all gone. A job that is already gone counts as done.
func (s *Store) CleanupCopyStep(ctx context.Context, jobID string, limit int, at time.Time) (bool, error) {
	done := false
	err := s.Write(ctx, func(tx *Tx) error {
		j, err := tx.CopyJobOf(ctx, jobID)
		if errors.Is(err, ErrNotFound) {
			done = true
			return nil
		}
		if err != nil {
			return err
		}
		if j.Phase == CopyPhaseDone {
			return tx.dropSnapshotRows(ctx, j, limit, &done)
		}
		rows, err := tx.q.QueryContext(ctx, `SELECT seq, node_id FROM copy_job_nodes WHERE job_id = ?
			ORDER BY seq DESC LIMIT ?`, jobID, limit)
		if err != nil {
			return err
		}
		var minSeq int64
		var drop []string
		for rows.Next() {
			var seq int64
			var nid string
			if err := rows.Scan(&seq, &nid); err != nil {
				rows.Close()
				return err
			}
			minSeq = seq
			drop = append(drop, j.DerivedNodeID(nid))
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(drop) > 0 {
			b, err := json.Marshal(drop)
			if err != nil {
				return err
			}
			if _, err := tx.q.ExecContext(ctx, `DELETE FROM nodes WHERE account_id = ? AND folder_id = ? AND id IN
				(SELECT value FROM json_each(?))`, j.AccountID, j.StagingFolderID, string(b)); err != nil {
				return err
			}
			if _, err := tx.q.ExecContext(ctx, `DELETE FROM copy_job_nodes WHERE job_id = ? AND seq >= ?`,
				jobID, minSeq); err != nil {
				return err
			}
		}
		if len(drop) == limit {
			return nil // more snapshot rows may remain
		}
		// The snapshot is consumed: release, remove what is left of the staging folder, and the job's row.
		if _, err := tx.ReleaseCopyReservation(ctx, j, at); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM nodes WHERE account_id = ? AND folder_id = ?`,
			j.AccountID, j.StagingFolderID); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM folders WHERE account_id = ? AND id = ? AND staged = 1`,
			j.AccountID, j.StagingFolderID); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM copy_jobs WHERE job_id = ?`, jobID); err != nil {
			return err
		}
		done = true
		return nil
	})
	return done, err
}

// dropSnapshotRows deletes up to limit snapshot rows of a published copy, and its job row when none are left.
func (tx *Tx) dropSnapshotRows(ctx context.Context, j *CopyJob, limit int, done *bool) error {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM copy_job_nodes WHERE job_id = ? AND seq IN
		(SELECT seq FROM copy_job_nodes WHERE job_id = ? ORDER BY seq LIMIT ?)`, j.JobID, j.JobID, limit)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n >= int64(limit) {
		return nil
	}
	if _, err := tx.q.ExecContext(ctx, `DELETE FROM copy_jobs WHERE job_id = ?`, j.JobID); err != nil {
		return err
	}
	*done = true
	return nil
}

// AbandonedCopyJobs returns up to limit jobs whose copy has rows left but whose job has finished (succeeded or failed).
// A running or queued job is never listed: its runner owns the copy. The sweep cleans these up.
func (s *Store) AbandonedCopyJobs(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT c.job_id FROM copy_jobs c JOIN jobs j ON j.id = c.job_id
		WHERE j.status IN ('succeeded', 'failed') ORDER BY c.job_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err != nil {
			return nil, err
		}
		out = append(out, jid)
	}
	return out, rows.Err()
}
