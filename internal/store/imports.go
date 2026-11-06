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

// Import states (migration 0048, decision 92). A job moves queued -> running -> done. A pause parks it as paused, a
// cancel as cancelled, and a failure as failed; the last two keep their staged tree until cleanup has removed it.
const (
	ImportQueued    = "queued"
	ImportRunning   = "running"
	ImportPaused    = "paused"
	ImportCancelled = "cancelled"
	ImportDone      = "done"
	ImportFailed    = "failed"
)

// Import phases. A run moves scan -> stage -> publish -> done; a failure or a cancel moves it to cleanup, which removes the
// staged tree and the archive, and then done.
const (
	ImportPhaseScan    = "scan"
	ImportPhaseStage   = "stage"
	ImportPhasePublish = "publish"
	ImportPhaseCleanup = "cleanup"
	ImportPhaseDone    = "done"
)

// SourceArchive is the source of an import from an uploaded zip or tar.
const SourceArchive = "archive"

// Import is one background import. The fields after Actor are the job's state; they change as the job runs, and each change
// is written in one statement with SaveImport, inside the transaction that read the row.
type Import struct {
	ID              string
	JobID           string // the jobs row that runs it now
	AccountID       string
	UserID          string // who started it
	Source          string
	FolderID        string // the target folder
	ParentID        string // the target directory; "" is the folder's root
	OnConflict      string
	State           string
	Phase           string
	PauseReason     string
	ErrorCode       string
	Failure         string
	StagingFolderID string
	StagingRootID   string
	ArchiveBlobID   string
	ArchiveSHA256   string
	ArchiveFormat   string
	Entries         int64
	DeclaredBytes   int64
	Cursor          int64 // archive entries finished; a run resumes after this one
	ReservedBytes   int64 // held against the quota by the entry being written
	// ArchiveReserved is the archive's length, held against the staging folder's quota from the upload's hand-over until
	// the import ends (published, failed, cancelled or expired). It is released once, by ReleaseArchiveReservation (F1).
	ArchiveReserved int64
	Files           int64
	Bytes           int64
	Skipped         int64
	Actor           string // the caller, as JSON, for the record and the policy checks
	CreatedAt       time.Time
	UpdatedAt       time.Time
	FinishedAt      *time.Time
}

const importCols = `id, job_id, account_id, user_id, source, folder_id, parent_id, on_conflict, state, phase, pause_reason,
	error_code, failure, staging_folder_id, staging_root_id, archive_blob_id, archive_sha256, archive_format, entries,
	declared_bytes, cursor, reserved_bytes, archive_reserved, files, bytes, skipped, actor, created_at, updated_at, finished_at`

func scanImport(row interface{ Scan(...any) error }) (*Import, error) {
	var im Import
	var parent sql.NullString
	var created, updated int64
	var finished sql.NullInt64
	err := row.Scan(&im.ID, &im.JobID, &im.AccountID, &im.UserID, &im.Source, &im.FolderID, &parent, &im.OnConflict,
		&im.State, &im.Phase, &im.PauseReason, &im.ErrorCode, &im.Failure, &im.StagingFolderID, &im.StagingRootID,
		&im.ArchiveBlobID, &im.ArchiveSHA256, &im.ArchiveFormat, &im.Entries, &im.DeclaredBytes, &im.Cursor,
		&im.ReservedBytes, &im.ArchiveReserved, &im.Files, &im.Bytes, &im.Skipped, &im.Actor, &created, &updated, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	im.ParentID = parent.String
	im.CreatedAt = fromMillis(created)
	im.UpdatedAt = fromMillis(updated)
	im.FinishedAt = fromNullMillis(finished)
	return &im, nil
}

// Import returns the import with the given ID, or ErrNotFound.
func (s *Store) Import(ctx context.Context, id string) (*Import, error) {
	return scanImport(s.r.QueryRowContext(ctx, "SELECT "+importCols+" FROM import_jobs WHERE id = ?", id))
}

// Import returns the import with the given ID, read inside the transaction.
func (tx *Tx) Import(ctx context.Context, id string) (*Import, error) {
	return scanImport(tx.q.QueryRowContext(ctx, "SELECT "+importCols+" FROM import_jobs WHERE id = ?", id))
}

// InsertImport records a new import.
func (tx *Tx) InsertImport(ctx context.Context, im *Import) error {
	_, err := tx.q.ExecContext(ctx, "INSERT INTO import_jobs ("+importCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		im.ID, im.JobID, im.AccountID, im.UserID, im.Source, im.FolderID, nullString(im.ParentID), im.OnConflict,
		im.State, im.Phase, im.PauseReason, im.ErrorCode, im.Failure, im.StagingFolderID, im.StagingRootID,
		im.ArchiveBlobID, im.ArchiveSHA256, im.ArchiveFormat, im.Entries, im.DeclaredBytes, im.Cursor,
		im.ReservedBytes, im.ArchiveReserved, im.Files, im.Bytes, im.Skipped, im.Actor, millis(im.CreatedAt), millis(im.UpdatedAt),
		nullMillis(im.FinishedAt))
	return err
}

// SaveImport writes every mutable field of im, and its update time, in one statement.
func (tx *Tx) SaveImport(ctx context.Context, im *Import, at time.Time) error {
	im.UpdatedAt = at
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE import_jobs SET job_id = ?, state = ?, phase = ?, pause_reason = ?,
		error_code = ?, failure = ?, staging_folder_id = ?, staging_root_id = ?, archive_blob_id = ?, archive_sha256 = ?,
		archive_format = ?, entries = ?, declared_bytes = ?, cursor = ?, reserved_bytes = ?, archive_reserved = ?, files = ?, bytes = ?,
		skipped = ?, updated_at = ?, finished_at = ? WHERE id = ?`,
		im.JobID, im.State, im.Phase, im.PauseReason, im.ErrorCode, im.Failure, im.StagingFolderID, im.StagingRootID,
		im.ArchiveBlobID, im.ArchiveSHA256, im.ArchiveFormat, im.Entries, im.DeclaredBytes, im.Cursor, im.ReservedBytes,
		im.ArchiveReserved, im.Files, im.Bytes, im.Skipped, millis(at), nullMillis(im.FinishedAt), im.ID))
}

// UnfinishedImport returns the import of a user from a source that is queued or running, or ErrNotFound. A paused import
// holds no worker, so it does not count: the user may start another.
func (tx *Tx) UnfinishedImport(ctx context.Context, accountID, userID, source string) (*Import, error) {
	return scanImport(tx.q.QueryRowContext(ctx, "SELECT "+importCols+` FROM import_jobs
		WHERE account_id = ? AND user_id = ? AND source = ? AND state IN ('queued', 'running') LIMIT 1`,
		accountID, userID, source))
}

// CountUnfinishedImports is how many imports of the user are queued, running or paused: the ones not yet finished or
// cancelled. A paused import holds its archive, so these are capped per user (imports.max_unfinished_per_user).
func (tx *Tx) CountUnfinishedImports(ctx context.Context, accountID, userID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_jobs WHERE account_id = ? AND user_id = ?
		AND state IN ('queued', 'running', 'paused')`, accountID, userID).Scan(&n)
	return n, err
}

// ExpirePausedImports cancels up to limit imports that have been paused since before, and queues for each the job that
// removes what it staged and its archive. It returns how many it cancelled (imports.paused_expiry_days).
func (s *Store) ExpirePausedImports(ctx context.Context, before time.Time, limit int) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		rows, err := tx.q.QueryContext(ctx, `SELECT id, account_id FROM import_jobs
			WHERE state = 'paused' AND updated_at < ? ORDER BY updated_at, id LIMIT ?`, millis(before), limit)
		if err != nil {
			return err
		}
		type paused struct{ id, acc string }
		var list []paused
		for rows.Next() {
			var p paused
			if err := rows.Scan(&p.id, &p.acc); err != nil {
				rows.Close()
				return err
			}
			list = append(list, p)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		now := Now()
		for _, p := range list {
			job := &Job{ID: id.New("job"), AccountID: p.acc, Kind: "import", Target: p.id, Status: JobQueued,
				CreatedAt: now, UpdatedAt: now}
			if err := tx.InsertJob(ctx, job); err != nil {
				return err
			}
			if err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE import_jobs SET state = 'cancelled', phase = ?,
				pause_reason = 'expired', job_id = ?, updated_at = ? WHERE id = ? AND state = 'paused'`,
				ImportPhaseCleanup, job.ID, millis(now), p.id)); err != nil {
				return err
			}
		}
		n = int64(len(list))
		return nil
	})
	return n, err
}

// ReleaseArchiveReservation releases the reservation an import holds for its archive, from the staging folder, once: the
// row's amount goes to zero in the same write, so a second call releases nothing. It runs where the staging folder is
// dropped, published or cleaned up, while that folder still exists (round 3, F1). im is updated to match the row.
func (tx *Tx) ReleaseArchiveReservation(ctx context.Context, im *Import) error {
	held := im.ArchiveReserved
	im.ArchiveReserved = 0
	if held == 0 {
		return nil
	}
	if err := rowsAffected(tx.q.ExecContext(ctx, "UPDATE import_jobs SET archive_reserved = 0 WHERE id = ? AND archive_reserved = ?",
		im.ID, held)); err != nil {
		return err
	}
	return tx.AdjustFolders(ctx, im.AccountID, FolderChange{FolderID: im.StagingFolderID,
		Change: UsageChange{Reserved: -held}})
}

// RequeueAbandonedImports queues a job for each import that needs one and has none: a queued import whose job ended, a
// running one whose job died (a store error in the run), and a cancelled or failed one whose cleanup has not ended. A
// pause, a resume or a cancel that raced a job's end leaves one of these. It queues at most one batch of 100 a call and
// returns how many it queued (round 3, F10).
func (s *Store) RequeueAbandonedImports(ctx context.Context) (int, error) {
	var n int
	err := s.Write(ctx, func(tx *Tx) error {
		rows, err := tx.q.QueryContext(ctx, `SELECT i.id, i.account_id FROM import_jobs i
			WHERE (i.state IN ('queued', 'running') OR (i.state IN ('cancelled', 'failed') AND i.phase <> 'done'))
			AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.kind = 'import' AND j.target = i.id
				AND j.status IN ('queued', 'running'))
			ORDER BY i.updated_at, i.id LIMIT 100`)
		if err != nil {
			return err
		}
		type lost struct{ id, acc string }
		var list []lost
		for rows.Next() {
			var l lost
			if err := rows.Scan(&l.id, &l.acc); err != nil {
				rows.Close()
				return err
			}
			list = append(list, l)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		now := Now()
		for _, l := range list {
			job := &Job{ID: id.New("job"), AccountID: l.acc, Kind: "import", Target: l.id, Status: JobQueued,
				CreatedAt: now, UpdatedAt: now}
			if err := tx.InsertJob(ctx, job); err != nil {
				return err
			}
			if err := rowsAffected(tx.q.ExecContext(ctx, "UPDATE import_jobs SET job_id = ? WHERE id = ?",
				job.ID, l.id)); err != nil {
				return err
			}
		}
		n = len(list)
		return nil
	})
	return n, err
}

// ImportJobUnfinished reports whether a job of an import is queued or running. A resume or a cleanup queues a job only when
// none is, so one import never has two jobs at once.
func (tx *Tx) ImportJobUnfinished(ctx context.Context, importID string) (bool, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind = 'import' AND target = ?
		AND status IN ('queued', 'running')`, importID).Scan(&n)
	return n > 0, err
}

// ImportItem is one entry of an import's archive that was written or skipped. Path is the entry's clash keys joined with
// "/", and it is how a later entry finds the folder it sits in.
type ImportItem struct {
	ImportID  string
	Seq       int64
	Path      string
	Name      string
	Kind      string // file or dir
	State     string // done or skipped
	NodeID    string
	Bytes     int64
	ErrorCode string
	CreatedAt time.Time
}

// InsertImportItem records an entry of an import. Its seq is the next one for the import.
func (tx *Tx) InsertImportItem(ctx context.Context, it *ImportItem) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO import_items (import_id, seq, path, name, kind, state, node_id, bytes,
		error_code, created_at) VALUES (?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM import_items WHERE import_id = ?), ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.ImportID, it.ImportID, it.Path, it.Name, it.Kind, it.State, it.NodeID, it.Bytes, it.ErrorCode, millis(it.CreatedAt))
	return err
}

// ImportItems returns up to limit items of an import with a seq after after, in seq order.
func (s *Store) ImportItems(ctx context.Context, importID string, after int64, limit int) ([]ImportItem, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT import_id, seq, path, name, kind, state, node_id, bytes, error_code, created_at
		FROM import_items WHERE import_id = ? AND seq > ? ORDER BY seq LIMIT ?`, importID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImportItem
	for rows.Next() {
		var it ImportItem
		var created int64
		if err := rows.Scan(&it.ImportID, &it.Seq, &it.Path, &it.Name, &it.Kind, &it.State, &it.NodeID, &it.Bytes,
			&it.ErrorCode, &created); err != nil {
			return nil, err
		}
		it.CreatedAt = fromMillis(created)
		out = append(out, it)
	}
	return out, rows.Err()
}

// ImportFileItems reads every file item an import finished, in order, for the audit entries of its publish.
func (tx *Tx) ImportFileItems(ctx context.Context, importID string) ([]ImportItem, error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT import_id, seq, path, name, kind, state, node_id, bytes, error_code, created_at
		FROM import_items WHERE import_id = ? AND kind = 'file' AND state = 'done' ORDER BY seq`, importID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImportItem
	for rows.Next() {
		var it ImportItem
		var created int64
		if err := rows.Scan(&it.ImportID, &it.Seq, &it.Path, &it.Name, &it.Kind, &it.State, &it.NodeID, &it.Bytes,
			&it.ErrorCode, &created); err != nil {
			return nil, err
		}
		it.CreatedAt = fromMillis(created)
		out = append(out, it)
	}
	return out, rows.Err()
}

// ImportDirNode returns the node of the folder an import made or reused at path (the clash keys joined with "/"), or ""
// when there is none. A folder's node is the parent of the entries below it.
func (tx *Tx) ImportDirNode(ctx context.Context, importID, path string) (string, error) {
	var nodeID string
	err := tx.q.QueryRowContext(ctx, `SELECT node_id FROM import_items WHERE import_id = ? AND path = ? AND kind = 'dir'
		AND node_id <> '' LIMIT 1`, importID, path).Scan(&nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return nodeID, err
}

// ImportSourceNode returns the node an import of a source made for sourceID, when that node is still there (not trashed),
// or "" when none is. A node that was deleted or trashed does not count: the entry is imported again.
// The match is only ever for a node still in folderID: a node that was moved out of the target is not a match, so the
// entry is imported again. The key already names the user and the target (api importSourceKey).
func (tx *Tx) ImportSourceNode(ctx context.Context, accountID, folderID, source, sourceID string) (string, error) {
	return importSourceNode(ctx, tx.q, accountID, folderID, source, sourceID)
}

// ImportSourceNode is ImportSourceNode read outside a transaction.
func (s *Store) ImportSourceNode(ctx context.Context, accountID, folderID, source, sourceID string) (string, error) {
	return importSourceNode(ctx, s.r, accountID, folderID, source, sourceID)
}

func importSourceNode(ctx context.Context, q querier, accountID, folderID, source, sourceID string) (string, error) {
	var nodeID string
	err := q.QueryRowContext(ctx, `SELECT s.node_id FROM import_sources s JOIN nodes n ON n.account_id = s.account_id
		AND n.id = s.node_id WHERE s.account_id = ? AND s.source = ? AND s.source_id = ? AND n.trashed_at IS NULL
		AND n.folder_id = ?`,
		accountID, source, sourceID, folderID).Scan(&nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return nodeID, err
}

// DropBlob removes a blob that no node refers to, such as an import's archive once the import no longer needs it.
func (tx *Tx) DropBlob(ctx context.Context, accountID, blobID string) error {
	_, err := tx.q.ExecContext(ctx, `DELETE FROM blobs WHERE account_id = ? AND id = ?
		AND NOT EXISTS (SELECT 1 FROM nodes WHERE nodes.blob_id = blobs.id)
		AND NOT EXISTS (SELECT 1 FROM versions WHERE versions.blob_id = blobs.id)`, accountID, blobID)
	return err
}

// PutImportSource records that an import of a source made nodeID for sourceID. A second import that replaces the entry
// takes the key over.
func (tx *Tx) PutImportSource(ctx context.Context, accountID, source, sourceID, nodeID, importID string) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO import_sources (account_id, source, source_id, node_id, import_id)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (account_id, source, source_id) DO UPDATE SET node_id = excluded.node_id,
		import_id = excluded.import_id`, accountID, source, sourceID, nodeID, importID)
	return err
}

// DropImportSources removes what an import remembered of the entries it made, when the import failed or was cancelled:
// its nodes are gone, so the next import must copy them.
func (tx *Tx) DropImportSources(ctx context.Context, importID string) error {
	_, err := tx.q.ExecContext(ctx, `DELETE FROM import_sources WHERE import_id = ?`, importID)
	return err
}

// ImportSwitchOn reports whether the archive source is on for an account. No row means on.
func (s *Store) ImportSwitchOn(ctx context.Context, accountID string) (bool, error) {
	return importSwitchOn(ctx, s.r, accountID)
}

// ImportSwitchOn reports whether the archive source is on for an account, read inside the transaction.
func (tx *Tx) ImportSwitchOn(ctx context.Context, accountID string) (bool, error) {
	return importSwitchOn(ctx, tx.q, accountID)
}

func importSwitchOn(ctx context.Context, q querier, accountID string) (bool, error) {
	var on int
	err := q.QueryRowContext(ctx, `SELECT archive FROM import_switches WHERE account_id = ?`, accountID).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return on == 1, err
}

// SetImportSwitch turns the archive source on or off for an account.
func (tx *Tx) SetImportSwitch(ctx context.Context, accountID string, on bool) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO import_switches (account_id, archive) VALUES (?, ?)
		ON CONFLICT (account_id) DO UPDATE SET archive = excluded.archive`, accountID, boolInt(on))
	return err
}

// StagedTopLevel returns the staged nodes whose parent is not staged: the staged roots of the import (children of its
// staging root) and the entries that sit in a folder the import reused, which are the ones a publish must place and check.
func (tx *Tx) StagedTopLevel(ctx context.Context, accountID, stagingFolder string) ([]*Node, error) {
	rows, err := tx.q.QueryContext(ctx, nodeSelect+` WHERE n.account_id = ? AND n.folder_id = ? AND n.parent_id IS NOT NULL
		AND n.parent_id NOT IN (SELECT m.id FROM nodes m WHERE m.account_id = ? AND m.folder_id = ?
			AND m.parent_id IS NOT NULL)
		ORDER BY n.id`, accountID, stagingFolder, accountID, stagingFolder)
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

// SetStagedParent places a staged top-level node under parentID, named name, in this transaction.
func (tx *Tx) SetStagedParent(ctx context.Context, accountID, nodeID, parentID, name, key string, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE nodes SET parent_id = ?, name = ?, name_key = ?, modified_at = ?
		WHERE account_id = ? AND id = ?`, parentID, name, key, millis(at), accountID, nodeID))
}

// MoveStagedTree moves every staged node of the staging folder into folderID, in this transaction, and queues the search
// index of the moved rows, as a copy's publish does (decision 40). The staging root and the staging folder are removed
// by DropStaging, which the caller runs after the parents are placed.
func (tx *Tx) MoveStagedTree(ctx context.Context, accountID, stagingFolder, folderID string, at time.Time) error {
	lo, hi, err := tx.FolderRowIDRange(ctx, accountID, stagingFolder)
	if err != nil {
		return err
	}
	if _, err := tx.q.ExecContext(ctx, `UPDATE nodes SET folder_id = ?,
		owner_id = COALESCE((SELECT owner_user_id FROM folders WHERE id = ? AND kind = 'personal'), owner_id)
		WHERE account_id = ? AND folder_id = ? AND parent_id IS NOT NULL`,
		folderID, folderID, accountID, stagingFolder); err != nil {
		return err
	}
	from := lo - 1
	if lo == 0 {
		from = hi
	}
	return tx.QueueSearchRange(ctx, accountID, folderID, from, hi)
}

// StagedCounts returns the nodes of the staging folder that are not its root, the bytes the files among them hold, and
// how many are files, as the publish charges them.
func (tx *Tx) StagedCounts(ctx context.Context, accountID, stagingFolder string) (files, bytes int64, err error) {
	err = tx.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN kind = 'file' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN kind = 'file' THEN size ELSE 0 END), 0)
		FROM nodes WHERE account_id = ? AND folder_id = ? AND parent_id IS NOT NULL`, accountID, stagingFolder).
		Scan(&files, &bytes)
	return files, bytes, err
}

// CleanupStagedBatch removes up to limit staged nodes of a staging folder in this transaction, and the blobs no node
// refers to any more. It returns the IDs of the blobs it removed, whose files the caller removes after the commit, and
// whether the folder has no nodes left (its root included).
func (tx *Tx) CleanupStagedBatch(ctx context.Context, accountID, stagingFolder string, limit int) (blobs []string, done bool, err error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT id, blob_id FROM nodes WHERE account_id = ? AND folder_id = ?
		AND parent_id IS NOT NULL ORDER BY id LIMIT ?`, accountID, stagingFolder, limit)
	if err != nil {
		return nil, false, err
	}
	var ids []string
	for rows.Next() {
		var nid, blob string
		if err := rows.Scan(&nid, &blob); err != nil {
			rows.Close()
			return nil, false, err
		}
		ids = append(ids, nid)
		if blob != "" {
			blobs = append(blobs, blob)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(ids) > 0 {
		b, err := json.Marshal(ids)
		if err != nil {
			return nil, false, err
		}
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM nodes WHERE account_id = ? AND folder_id = ? AND id IN
			(SELECT value FROM json_each(?))`, accountID, stagingFolder, string(b)); err != nil {
			return nil, false, err
		}
	}
	if len(blobs) > 0 {
		b, err := json.Marshal(blobs)
		if err != nil {
			return nil, false, err
		}
		// A blob a staged node held is removed only when nothing else refers to it.
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM blobs WHERE account_id = ? AND id IN (SELECT value FROM json_each(?))
			AND NOT EXISTS (SELECT 1 FROM nodes WHERE nodes.blob_id = blobs.id)
			AND NOT EXISTS (SELECT 1 FROM versions WHERE versions.blob_id = blobs.id)`, accountID, string(b)); err != nil {
			return nil, false, err
		}
	}
	var left int
	if err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE account_id = ? AND folder_id = ?
		AND parent_id IS NOT NULL`, accountID, stagingFolder).Scan(&left); err != nil {
		return nil, false, err
	}
	return blobs, left == 0, nil
}

// DropStaging removes the staging root and the staging folder, once their nodes are gone or moved.
func (tx *Tx) DropStaging(ctx context.Context, accountID, stagingFolder, rootID string) error {
	if _, err := tx.q.ExecContext(ctx, `DELETE FROM nodes WHERE account_id = ? AND folder_id = ? AND id = ?`,
		accountID, stagingFolder, rootID); err != nil {
		return err
	}
	_, err := tx.q.ExecContext(ctx, `DELETE FROM folders WHERE account_id = ? AND id = ? AND staged = 1`, accountID, stagingFolder)
	return err
}

// PurgeImports removes up to limit import records that finished before before, with their items and their jobs rows. The
// files they imported stay, and so does what the imports remember of them (import_sources). It returns how many it removed.
func (s *Store) PurgeImports(ctx context.Context, before time.Time, limit int) (int64, error) {
	var removed int64
	err := s.Write(ctx, func(tx *Tx) error {
		// The jobs rows of an import are all of those queued for it (a resume queues one), so they go with it.
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM jobs WHERE kind = 'import' AND status IN ('succeeded', 'failed')
			AND target IN (SELECT id FROM import_jobs
			WHERE finished_at IS NOT NULL AND finished_at < ? AND state IN ('done', 'failed', 'cancelled')
			AND phase = 'done' ORDER BY finished_at, id LIMIT ?)`,
			millis(before), limit); err != nil {
			return err
		}
		res, err := tx.q.ExecContext(ctx, `DELETE FROM import_jobs WHERE id IN (SELECT id FROM import_jobs
			WHERE finished_at IS NOT NULL AND finished_at < ? AND state IN ('done', 'failed', 'cancelled')
			AND phase = 'done' ORDER BY finished_at, id LIMIT ?)`,
			millis(before), limit)
		if err != nil {
			return err
		}
		removed, err = res.RowsAffected()
		return err
	})
	return removed, err
}
