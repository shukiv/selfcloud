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
	"github.com/shukiv/selfcloud/internal/derived"
	"github.com/shukiv/selfcloud/internal/store"
)

// batchSize is how many orphans are looked at per round.
const batchSize = 200

// staleUpload is how old a temporary file must be before it is taken to be abandoned. No write
// takes this long: request bodies have a read deadline far shorter.
const staleUpload = 24 * time.Hour

// Options configure a Sweeper.
type Options struct {
	Store *store.Store
	Blobs blob.Store
	// TextMax, CapAccount and CapTotal are search.text_max_bytes, search.max_text_per_account and search.max_text_total:
	// the hourly check releases capped text rows once their cap has room, and drops text no node holds (decision 46).
	TextMax, CapAccount, CapTotal int64
	DeleteGrace                   time.Duration // how long an orphan blob is kept before its file is removed
	Interval                      time.Duration // how often Run sweeps
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
	// AuditRetention is audit.retention_days as a duration: an audit entry older than this is removed. Zero keeps every entry.
	AuditRetention time.Duration
	// ImportRetention is imports.job_retention_days as a duration: the record of a finished import older than this is
	// removed. The files it imported stay. Zero keeps every record.
	ImportRetention time.Duration
	// ImportPausedExpiry is imports.paused_expiry_days as a duration: an import paused for longer is cancelled, and what it
	// staged and its archive are removed. Zero keeps paused imports.
	ImportPausedExpiry time.Duration
	// Derived is the cache of derived files (thumbnails). Nil means there are none to sweep, as in tests.
	Derived *derived.Cache
	// Zips holds the stored zip downloads (decision 35). Nil means there are none to sweep, as in tests.
	Zips   *derived.ZipFiles
	Logger *slog.Logger
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
	// auditRetention is how long an audit entry is kept; auditPruned is when the entries were last pruned (audit.go).
	auditRetention time.Duration
	// importRetention is how long the record of a finished import is kept (imports.job_retention_days, decision 92).
	importRetention time.Duration
	// importPausedExpiry is how long an import may stay paused (imports.paused_expiry_days, decision 92).
	importPausedExpiry time.Duration
	auditPruned        time.Time
	derived            *derived.Cache
	zips               *derived.ZipFiles
	// searchChecked is when the search rows were last checked; see searchCheckEvery.
	searchChecked time.Time
	// The text caps (see Options).
	textMax, capAccount, capTotal int64
	log                           *slog.Logger
	// webhookEventCursor is where the next pass over the webhook events starts (see pruneWebhooks).
	webhookEventCursor int64
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
		versionCount: o.VersionsMaxCount, versionAge: o.VersionsMaxAge, auditRetention: o.AuditRetention,
		importRetention:    o.ImportRetention,
		importPausedExpiry: o.ImportPausedExpiry,
		derived:            o.Derived, zips: o.Zips, log: log,
		textMax: o.TextMax, capAccount: o.CapAccount, capTotal: o.CapTotal}
}

// Result counts what one pass did.
type Result struct {
	Orphaned       int64 // live blobs found unreferenced and marked
	Removed        int   // orphan blobs whose files and rows are gone
	Failed         int   // orphan blobs whose file could not be removed; they are retried next pass
	Sessions       int64 // expired sessions removed
	Verifications  int64 // expired or used verification and reset tokens removed
	SignInLinks    int64 // expired or used sign-in links removed
	DeviceConfirms int64 // expired or used device confirmations removed (sec2, decision 86)
	WOPISessions   int64 // office editing sessions past their end removed
	Locks          int   // office editing locks past their expiry removed and audited (decision 32)
	TempRemoved    int   // abandoned temporary upload files removed
	Uploads        int   // expired resumable uploads removed, rows and files
	UploadFiles    int   // resumable upload files with no row, removed
	Permissions    int   // permissions expired more than permissionGrace ago, removed and audited
	AccessRequests int64 // decided access requests older than accessRequestRetention, removed
	Purged         int   // trashed items past trash.retention, purged with their contents
	SearchRepaired bool  // the search rows were missing or extra, and the repair queued the missing ones (decision 40)
	// SearchFTSMismatch is set when a full-text table disagrees with the search rows; the admin rebuild fixes it.
	SearchFTSMismatch bool
	CopiesCleaned     int   // chunked copies whose staged rows were removed after their job finished
	Versions          int   // versions past versions.max_count or versions.max_age, pruned
	Links             int   // share links that ended more than a day ago, removed with their open uploads
	Recent            int64 // recent entries past recentRetention, or beyond recentPerUser for their user, removed
	// Webhook deliveries past their retention, events no delivery refers to, and expired previous secrets cleared.
	WebhookDeliveries int
	WebhookEvents     int
	WebhookSecrets    int64
	WebhookQuotaMarks int // quota marks that no longer hold (decision 30)
	// Notifications older than notificationKeep removed, and mail rows finished or expired (decision 43): a finished row
	// older than notifyMailKeep is removed, and a pending one older than it is expired as skipped.
	Notifications int64
	MailRows      int64
	MailExpired   int64
	// DerivedEvicted is the ready derived files removed to keep the derived budget; DerivedTemp the abandoned
	// temporary files of the derived workers; DerivedOrphans the derived files no row names; DerivedCleared the
	// failed rows of 30 days ago or more, which the next request queues again.
	DerivedEvicted int
	DerivedTemp    int
	DerivedOrphans int
	DerivedCleared int64
	// ZipsExpired is the stored zips removed past their expiry; ZipsAbandoned the unfinished ones whose job is gone.
	ZipsExpired   int
	ZipsAbandoned int
	// AuditPruned is the audit entries past audit.retention_days, removed (decision 48).
	AuditPruned int64
	// ImportsPruned is the records of finished imports past imports.job_retention_days, removed (decision 92).
	ImportsPruned int64
	// ImportsExpired is the paused imports past imports.paused_expiry_days that were cancelled (decision 92).
	ImportsExpired int64
}

// derivedFailedAge is how long a failed derived row is kept before the sweep clears it.
const derivedFailedAge = 30 * 24 * time.Hour

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
			res.SignInLinks > 0 || res.DeviceConfirms > 0):
			w.log.Info("housekeeping", "orphaned", res.Orphaned, "removed", res.Removed, "failed", res.Failed,
				"sessions", res.Sessions, "verifications", res.Verifications, "sign_in_links", res.SignInLinks,
				"device_confirms", res.DeviceConfirms)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// searchCheckEvery is how often the sweep checks the search rows against the nodes. It is low on purpose: a check counts
// every node of the installation.
const searchCheckEvery = time.Hour

// searchCheck compares the search rows with the nodes (store.SearchCheck). It repairs missing and extra rows, and
// reports a full-text table that disagrees with the rows, which only the admin rebuild can fix.
func (w *Sweeper) searchCheck(ctx context.Context, res *Result) error {
	// Text that no node holds goes, and capped text whose cap has room again is released, in bounded batches (decision 46).
	if _, err := w.st.DropUnusedText(ctx); err != nil {
		return err
	}
	if _, err := w.st.ReleaseTextCapped(ctx, w.textMax, w.capAccount, w.capTotal); err != nil {
		return err
	}
	// Deleted text leaves the write-ahead log within this hour: a passive checkpoint would keep it there (review, decision 46).
	// A busy log is not an error; the next hour tries again.
	if _, err := w.st.Checkpoint(ctx); err != nil {
		return err
	}
	d, err := w.st.SearchCheck(ctx)
	if err != nil {
		return err
	}
	if d.Missing > 0 || d.Extra > 0 || d.TextMissing > 0 {
		w.log.Warn("search rows disagree with the nodes; repairing", "missing", d.Missing, "extra", d.Extra,
			"text_missing", d.TextMissing)
		if err := w.st.SearchRepair(ctx); err != nil {
			return err
		}
		res.SearchRepaired = true
	}
	if d.FTSMismatch || d.TextFTSMismatch {
		w.log.Error("search full-text tables disagree with the search rows; run selfcloud admin search-rebuild")
		res.SearchFTSMismatch = true
	}
	return nil
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
	if res.CopiesCleaned, err = w.cleanupCopies(ctx); err != nil {
		return res, err
	}
	// The search rows are checked at most once every searchCheckEvery: a missing or extra row is repaired, and the
	// search job indexes what is missing. A full-text table that disagrees with its rows is only reported; the admin
	// command selfcloud admin search-rebuild makes it again. A check that fails is logged and counted, and the sweep goes on.
	if now.Sub(w.searchChecked) >= searchCheckEvery {
		w.searchChecked = now
		if serr := w.searchCheck(ctx, &res); serr != nil {
			w.log.Error("search check", "error", serr)
			res.Failed++
		}
	}
	if w.zips != nil {
		// A zip past its expiry, and a zip whose job is gone, are removed in bounded steps. A file that cannot be
		// removed is logged and counted, and the sweep goes on.
		zs, zerr := w.zips.Sweep(ctx, now, 500)
		res.ZipsExpired, res.ZipsAbandoned = zs.Expired, zs.Abandoned
		if zerr != nil {
			w.log.Error("stored zips", "error", zerr)
			res.Failed++
		}
	}
	if w.derived != nil {
		// Each derived step logs its own failure and carries on: one file that cannot be removed must not stop the
		// steps after it, for ever.
		var derr error
		if res.DerivedEvicted, derr = w.derived.Evict(ctx, 0); derr != nil {
			w.log.Error("derived eviction", "error", derr)
			res.Failed++
		}
		// A worker's temporary file lives for at most its decode's time limit, so an hour old is abandoned.
		if res.DerivedTemp, derr = w.derived.Files().RemoveStaleTemp(now.Add(-time.Hour)); derr != nil {
			w.log.Error("derived temporary files", "error", derr)
			res.Failed++
		}
		if res.DerivedOrphans, derr = w.derived.RemoveOrphans(ctx); derr != nil {
			w.log.Error("derived orphans", "error", derr)
			res.Failed++
		}
		if res.DerivedCleared, derr = w.derived.ClearFailed(ctx, now.Add(-derivedFailedAge)); derr != nil {
			w.log.Error("derived failures", "error", derr)
			res.Failed++
		}
	}
	// The audit log is pruned hourly. A failure is counted and logged, and the steps after it still run.
	if err := w.pruneAudit(ctx, now, &res); err != nil {
		w.log.Error("audit retention", "error", err)
		res.Failed++
	}
	// The records of finished imports are pruned with the same rule: a failure is counted and logged.
	if err := w.expireImports(ctx, now, &res); err != nil {
		return res, err
	}
	if err := w.pruneImports(ctx, now, &res); err != nil {
		w.log.Error("import retention", "error", err)
		res.Failed++
	}
	if res.Versions, err = w.pruneVersions(ctx, now); err != nil {
		return res, err
	}
	if res.Links, err = w.expireShareLinks(ctx, now); err != nil {
		return res, err
	}
	if err := w.pruneNotifications(ctx, now, &res); err != nil {
		return res, err
	}
	if err := w.pruneWebhooks(ctx, now, &res); err != nil {
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
		if res.DeviceConfirms, err = tx.DeleteSpentDeviceConfirms(ctx, now); err != nil {
			return err
		}
		if res.WOPISessions, err = tx.DeleteEndedWOPISessions(ctx, now); err != nil {
			return err
		}
		// A lock past its expiry is removed and audited, so a crashed editor leaves no lock behind (decision 32).
		// The file is free from the expiry on; the sweep only tidies the row.
		expiredLocks, err := tx.DeleteExpiredLocks(ctx, now)
		if err != nil {
			return err
		}
		for _, l := range expiredLocks {
			entry := &store.AuditEntry{Time: now, AccountID: l.AccountID, ActorKind: "system",
				Action: "node.unlock", Target: l.NodeID, Folders: []string{l.FolderID},
				Details: map[string]any{"reason": "expired"}}
			if err := tx.InsertAudit(ctx, entry); err != nil {
				return err
			}
		}
		res.Locks = len(expiredLocks)
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
			// The blob's derived files go first: a derived row cascades with its blob, and a cascade removes no
			// file, so the rows and their files are removed here, while the blob's id still names them.
			if w.derived != nil {
				if err := w.derived.RemoveBlob(ctx, b.AccountID, b.ID); err != nil {
					w.log.Error("cannot remove derived files of a blob", "blob", b.ID, "account", b.AccountID, "error", err)
					res.Failed++
					continue
				}
			}
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
