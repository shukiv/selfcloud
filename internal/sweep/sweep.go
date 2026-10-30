// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sweep runs the server's housekeeping: it removes file contents that no node refers to
// any more, and sessions and verification tokens that have ended.
//
// Every step is safe to repeat and safe next to requests. Requests and sweeps take turns on the
// store's single writer, and a blob is only orphaned when no node refers to it, which nothing can
// change, because new references come only from existing nodes.
package sweep

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/store"
)

// batchSize is how many orphans are looked at per round.
const batchSize = 200

// staleUpload is how old a temporary file must be before it is taken to be abandoned. No write
// takes this long: request bodies have a read deadline far shorter.
const staleUpload = 24 * time.Hour

// Options configure a Sweeper.
type Options struct {
	Store       *store.Store
	Blobs       blob.Store
	DeleteGrace time.Duration // how long an orphan blob is kept before its file is removed
	Interval    time.Duration // how often Run sweeps
	// UploadExpiry is upload.expiry: an upload file with no row is removed once it is this old.
	// Zero means defaultUploadExpiry.
	UploadExpiry time.Duration
	// UploadMaxLifetime is upload.max_lifetime: an upload is expired this long after it was created,
	// whatever its activity. Zero means defaultUploadMaxLifetime.
	UploadMaxLifetime time.Duration
	// TrashRetention is trash.retention: a deleted item is purged once it has been in the trash this long.
	// Zero means defaultTrashRetention.
	TrashRetention time.Duration
	// VersionsMaxCount and VersionsMaxAge are versions.max_count and versions.max_age. Zero means no limit.
	VersionsMaxCount int
	VersionsMaxAge   time.Duration
	Logger           *slog.Logger
}

// permissionGrace is how long an expired permission keeps its row. A permission grants nothing once it
// expires, since every read filters it; the row is kept a day so the audit shows the access ending.
const permissionGrace = 24 * time.Hour

// accessRequestRetention is how long a decided access request is kept. It is kept for a month so the
// people who decided and the requester can still see what was asked, and then it is removed.
const accessRequestRetention = 30 * 24 * time.Hour

// defaultTrashRetention is trash.retention's default, used when none is given.
const defaultTrashRetention = 30 * 24 * time.Hour

// trashBatch is how many trashed items one pass purges at most, so a large trash is emptied over passes
// and never holds the write lock for long.
const trashBatch = 200

// versionBatch is how many nodes one pass prunes versions on at most.
const versionBatch = 200

// defaultUploadExpiry is upload.expiry's default, used when none is given.
const defaultUploadExpiry = 24 * time.Hour

// defaultUploadMaxLifetime is upload.max_lifetime's default, used when none is given.
const defaultUploadMaxLifetime = 7 * 24 * time.Hour

// Sweeper runs the housekeeping.
type Sweeper struct {
	st           *store.Store
	blobs        blob.Store
	grace        time.Duration
	interval     time.Duration
	uploadExpiry time.Duration
	maxLifetime  time.Duration
	trashKeep    time.Duration
	versionCount int
	versionAge   time.Duration
	log          *slog.Logger
}

// New returns a Sweeper. It does nothing until Run or Once is called.
func New(o Options) *Sweeper {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if o.UploadExpiry <= 0 {
		o.UploadExpiry = defaultUploadExpiry
	}
	if o.UploadMaxLifetime <= 0 {
		o.UploadMaxLifetime = defaultUploadMaxLifetime
	}
	if o.TrashRetention <= 0 {
		o.TrashRetention = defaultTrashRetention
	}
	return &Sweeper{st: o.Store, blobs: o.Blobs, grace: o.DeleteGrace, interval: o.Interval,
		uploadExpiry: o.UploadExpiry, maxLifetime: o.UploadMaxLifetime, trashKeep: o.TrashRetention,
		versionCount: o.VersionsMaxCount, versionAge: o.VersionsMaxAge, log: log}
}

// Result counts what one pass did.
type Result struct {
	Orphaned       int64 // live blobs found unreferenced and marked
	Removed        int   // orphan blobs whose files and rows are gone
	Failed         int   // orphan blobs whose file could not be removed; they are retried next pass
	Sessions       int64 // expired sessions removed
	Verifications  int64 // expired or used verification and reset tokens removed
	SignInLinks    int64 // expired or used sign-in links removed
	TempRemoved    int   // abandoned temporary upload files removed
	Uploads        int   // expired resumable uploads removed, rows and files
	UploadFiles    int   // resumable upload files with no row, removed
	Permissions    int   // permissions expired more than permissionGrace ago, removed and audited
	AccessRequests int64 // decided access requests older than accessRequestRetention, removed
	Purged         int   // trashed items past trash.retention, purged with their contents
	Versions       int   // versions past versions.max_count or versions.max_age, pruned
	Links          int   // share links that ended more than a day ago, removed with their open uploads
	Recent         int64 // recent entries past recentRetention, or beyond recentPerUser for their user, removed
}

// recentRetention is how long a recent entry is kept after its last use, and recentPerUser how many a user keeps.
const (
	recentRetention = 30 * 24 * time.Hour
	recentPerUser   = 500
)

// Run sweeps at once and then every interval, until ctx is cancelled.
func (w *Sweeper) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		res, err := w.Once(ctx, store.Now())
		switch {
		case err != nil && ctx.Err() == nil:
			w.log.Error("housekeeping", "error", err)
		case err == nil && (res.Removed > 0 || res.Orphaned > 0 || res.Sessions > 0 || res.Verifications > 0 ||
			res.SignInLinks > 0):
			w.log.Info("housekeeping", "orphaned", res.Orphaned, "removed", res.Removed, "failed", res.Failed,
				"sessions", res.Sessions, "verifications", res.Verifications, "sign_in_links", res.SignInLinks)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once makes one pass, judging expiry and grace against now.
func (w *Sweeper) Once(ctx context.Context, now time.Time) (Result, error) {
	var res Result
	var err error
	if res.Orphaned, err = w.orphan(ctx, now); err != nil {
		return res, err
	}
	if err := w.removeDue(ctx, now, &res); err != nil {
		return res, err
	}
	// Files under blobs/ that no row refers to are not looked for here: that means walking the whole
	// tree. A future `selfcloud fsck` command does it.
	if res.TempRemoved, err = w.blobs.RemoveStaleUploads(now.Add(-staleUpload)); err != nil {
		return res, err
	}
	if res.Uploads, err = w.expireUploads(ctx, now); err != nil {
		return res, err
	}
	if res.UploadFiles, err = w.blobs.RemoveOrphanUploads(now.Add(-w.uploadExpiry), w.uploadRowExists); err != nil {
		return res, err
	}
	if res.Purged, err = w.purgeTrash(ctx, now); err != nil {
		return res, err
	}
	if res.Versions, err = w.pruneVersions(ctx, now); err != nil {
		return res, err
	}
	if res.Links, err = w.expireShareLinks(ctx, now); err != nil {
		return res, err
	}
	err = w.st.Write(ctx, func(tx *store.Tx) error {
		if res.Sessions, err = tx.DeleteExpiredSessions(ctx, now); err != nil {
			return err
		}
		if res.Verifications, err = tx.DeleteSpentVerifications(ctx, now); err != nil {
			return err
		}
		if res.SignInLinks, err = tx.DeleteSpentSignInLinks(ctx, now); err != nil {
			return err
		}
		// Recent entries older than recentRetention, and all but the recentPerUser latest of each user (slice p2).
		if res.Recent, err = tx.PruneRecent(ctx, now.Add(-recentRetention), recentPerUser); err != nil {
			return err
		}
		// Each expired permission is audited with the folder it was on, so one in a personal folder
		// stays private to that folder's user, as every entry about it does.
		expired, err := tx.DeleteExpiredPermissions(ctx, now.Add(-permissionGrace))
		if err != nil {
			return err
		}
		for _, pm := range expired {
			n, err := tx.Node(ctx, pm.AccountID, pm.NodeID)
			if err != nil {
				return err
			}
			entry := &store.AuditEntry{Time: now, AccountID: pm.AccountID, ActorKind: "system",
				Action: "permission.expired", Target: pm.NodeID, Folders: []string{n.FolderID},
				Details: map[string]any{"subject_kind": pm.SubjectKind, "subject_id": pm.SubjectID,
					"role": pm.Role, "expires_at": pm.ExpiresAt.UTC().Format(time.RFC3339)}}
			if err := tx.InsertAudit(ctx, entry); err != nil {
				return err
			}
		}
		res.Permissions = len(expired)
		if res.AccessRequests, err = tx.DeleteDecidedAccessRequests(ctx, now.Add(-accessRequestRetention)); err != nil {
			return err
		}
		// A cheap check of the reservation counters against the uploads. It only reports: the numbers
		// are repaired by `selfcloud admin recount-usage`, never here.
		counted, declared, err := tx.ReservationTotals(ctx)
		if err != nil {
			return err
		}
		if counted != declared {
			w.log.Warn("upload reservations disagree with the uploads; run selfcloud admin recount-usage",
				"reserved_bytes", counted, "upload_bytes", declared)
		}
		// The account's counters are the sum of its folders' (slice p1). A difference is reported, never repaired.
		drift, err := tx.CounterDrift(ctx, 100)
		if err != nil {
			return err
		}
		if len(drift) > 0 {
			w.log.Warn("account usage counters disagree with their folders; run selfcloud admin recount-usage",
				"accounts", len(drift), "first", drift[0])
		}
		return nil
	})
	return res, err
}

// orphan marks the live blobs that no node refers to, to be removed after the grace period.
func (w *Sweeper) orphan(ctx context.Context, now time.Time) (int64, error) {
	var n int64
	err := w.st.Write(ctx, func(tx *store.Tx) error {
		var err error
		n, err = tx.OrphanUnreferenced(ctx, now.Add(w.grace))
		return err
	})
	return n, err
}

// removeDue removes the files of orphans whose grace has passed, then their rows. A file that
// cannot be removed keeps its row, so the next pass tries again. The loop stops once a round
// removes nothing, so a file that never goes cannot hold it forever.
func (w *Sweeper) removeDue(ctx context.Context, now time.Time, res *Result) error {
	for ctx.Err() == nil {
		due, err := w.st.DueBlobs(ctx, now, batchSize)
		if err != nil {
			return err
		}
		if len(due) == 0 {
			return nil
		}
		progress := 0
		for _, b := range due {
			if err := w.blobs.Remove(b.AccountID, b.ID); err != nil {
				w.log.Error("cannot remove blob file", "blob", b.ID, "account", b.AccountID, "error", err)
				res.Failed++
				continue
			}
			var removed bool
			err := w.st.Write(ctx, func(tx *store.Tx) error {
				var err error
				removed, err = tx.DeleteOrphan(ctx, b.AccountID, b.ID)
				return err
			})
			if err != nil {
				return err
			}
			if removed {
				res.Removed++
				progress++
			}
		}
		if progress == 0 {
			return nil
		}
	}
	return ctx.Err()
}

// expireUploads removes resumable uploads that have expired, or passed upload.max_lifetime: each row
// goes in its own transaction, which checks the expiry again and releases the upload's reservation,
// and then its file. A file that outlives its row is
// collected by RemoveOrphanUploads.
func (w *Sweeper) expireUploads(ctx context.Context, now time.Time) (int, error) {
	removed := 0
	createdBefore := now.Add(-w.maxLifetime)
	for ctx.Err() == nil {
		due, err := w.st.ExpiredUploads(ctx, now, createdBefore, batchSize)
		if err != nil {
			return removed, err
		}
		if len(due) == 0 {
			return removed, nil
		}
		progress := 0
		for _, u := range due {
			var gone bool
			err := w.st.Write(ctx, func(tx *store.Tx) error {
				var err error
				gone, err = tx.DeleteExpiredUpload(ctx, u.AccountID, u.ID, now, createdBefore)
				if err != nil || !gone {
					return err
				}
				return tx.InsertAudit(ctx, &store.AuditEntry{Time: now, AccountID: u.AccountID, ActorKind: "system",
					ActorID: "sweep", Action: "upload.expire", Target: u.ID,
					Details: map[string]any{"name": u.Name, "length": u.Length, "durable_offset": u.DurableOffset}})
			})
			if err != nil {
				return removed, err
			}
			if !gone {
				continue
			}
			// The row is gone and so is any commit that could have run: the upload's file, and a blob
			// a completion moved but never committed, both go.
			if err := w.blobs.RemoveUpload(u.AccountID, u.ID); err != nil {
				w.log.Error("cannot remove upload file", "upload", u.ID, "account", u.AccountID, "error", err)
			}
			if err := w.blobs.Remove(u.AccountID, u.BlobID); err != nil {
				w.log.Error("cannot remove uncommitted blob", "blob", u.BlobID, "account", u.AccountID, "error", err)
			}
			removed++
			progress++
		}
		if progress == 0 {
			return removed, nil
		}
	}
	return removed, ctx.Err()
}

// uploadRowExists reports whether an upload has a row. It is the keep test for RemoveOrphanUploads.
func (w *Sweeper) uploadRowExists(accountID, uploadID string) (bool, error) {
	_, err := w.st.Upload(context.Background(), accountID, uploadID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
