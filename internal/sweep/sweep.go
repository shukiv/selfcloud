// Package sweep runs the server's housekeeping: it removes file contents that no node refers to
// any more, and sessions and verification tokens that have ended.
//
// Every step is safe to repeat and safe next to requests. Requests and sweeps take turns on the
// store's single writer, and a blob is only orphaned when no node refers to it, which nothing can
// change, because new references come only from existing nodes.
package sweep

import (
	"context"
	"log/slog"
	"time"

	"selfcloud/internal/blob"
	"selfcloud/internal/store"
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
	Logger      *slog.Logger
}

// Sweeper runs the housekeeping.
type Sweeper struct {
	st       *store.Store
	blobs    blob.Store
	grace    time.Duration
	interval time.Duration
	log      *slog.Logger
}

// New returns a Sweeper. It does nothing until Run or Once is called.
func New(o Options) *Sweeper {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Sweeper{st: o.Store, blobs: o.Blobs, grace: o.DeleteGrace, interval: o.Interval, log: log}
}

// Result counts what one pass did.
type Result struct {
	Orphaned      int64 // live blobs found unreferenced and marked
	Removed       int   // orphan blobs whose files and rows are gone
	Failed        int   // orphan blobs whose file could not be removed; they are retried next pass
	Sessions      int64 // expired sessions removed
	Verifications int64 // expired or used verification and reset tokens removed
	TempRemoved   int   // abandoned temporary upload files removed
}

// Run sweeps at once and then every interval, until ctx is cancelled.
func (w *Sweeper) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		res, err := w.Once(ctx, store.Now())
		switch {
		case err != nil && ctx.Err() == nil:
			w.log.Error("housekeeping", "error", err)
		case err == nil && (res.Removed > 0 || res.Orphaned > 0 || res.Sessions > 0 || res.Verifications > 0):
			w.log.Info("housekeeping", "orphaned", res.Orphaned, "removed", res.Removed, "failed", res.Failed,
				"sessions", res.Sessions, "verifications", res.Verifications)
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
	err = w.st.Write(ctx, func(tx *store.Tx) error {
		if res.Sessions, err = tx.DeleteExpiredSessions(ctx, now); err != nil {
			return err
		}
		res.Verifications, err = tx.DeleteSpentVerifications(ctx, now)
		return err
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
