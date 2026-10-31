// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/shukiv/selfcloud/internal/fsutil"
	"github.com/shukiv/selfcloud/internal/store"
)

// installOutcome is what Install did with a finished file.
type installOutcome int

const (
	// installDone: the file is in place and its row is ready.
	installDone installOutcome = iota
	// installNoRoom: the disk, or the budget, has no room now. The row is failed as a host condition (retried).
	installNoRoom
	// installGone: the row went while the file was made, so the file was removed with nothing kept.
	installGone
	// installDropped: the file could not be placed or recorded, or the budget is full after eviction. The row is
	// dropped, so a later request makes the file again.
	installDropped
)

// Install moves a finished temporary file into place and records its row ready, if the disk and the budget allow. The
// budget is checked and the row completed in one write, so two finishing workers cannot both pass it. When the budget is
// full the least recently used files are evicted once, and the check is made again. This is the one rule for every kind
// of derived file (thumbnails and conversions alike).
func (c *Cache) Install(ctx context.Context, accountID, derivedID, tmp string, size int64, sha string, minFree uint64) installOutcome {
	log := c.log
	final, err := c.files.Path(accountID, derivedID)
	if err != nil {
		discardTemp(log, tmp)
		dropRowBy(ctx, c, accountID, derivedID, log)
		return installDropped
	}
	free, err := fsutil.FreeBytes(c.files.Root())
	if err != nil || free < minFree+uint64(size) {
		discardTemp(log, tmp)
		log.Warn("no room for a derived file; it will be made again on request", "derived", derivedID)
		return installNoRoom
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		discardTemp(log, tmp)
		log.Error("cannot create a derived directory", "error", err)
		dropRowBy(ctx, c, accountID, derivedID, log)
		return installDropped
	}
	if err := os.Rename(tmp, final); err != nil {
		discardTemp(log, tmp)
		log.Error("cannot place a derived file", "error", err)
		dropRowBy(ctx, c, accountID, derivedID, log)
		return installDropped
	}
	for attempt := 0; attempt < 2; attempt++ {
		var done, over bool
		err := c.st.Write(ctx, func(tx *store.Tx) error {
			total, err := tx.ReadyDerivedBytes(ctx)
			if err != nil {
				return err
			}
			if total+size > c.maxBytes {
				over = true
				return nil
			}
			done, err = tx.CompleteDerived(ctx, accountID, derivedID, size, sha, time.Now())
			return err
		})
		if err != nil {
			discardFinal(log, final)
			log.Error("cannot record a derived file", "error", err)
			dropRowBy(context.WithoutCancel(ctx), c, accountID, derivedID, log)
			return installDropped
		}
		if done {
			return installDone
		}
		if !over {
			discardFinal(log, final)
			return installGone
		}
		if attempt == 0 {
			if _, err := c.Evict(ctx, size); err != nil {
				log.Error("cannot evict derived files for the budget", "error", err)
				break
			}
		}
	}
	discardFinal(log, final)
	log.Warn("the derived budget is full; the file will be made again on request", "derived", derivedID)
	dropRowBy(ctx, c, accountID, derivedID, log)
	return installDropped
}

// dropRowBy removes a pending row by its IDs.
func dropRowBy(ctx context.Context, c *Cache, accountID, derivedID string, log *slog.Logger) {
	err := c.st.Write(ctx, func(tx *store.Tx) error {
		_, err := tx.DeletePendingDerived(ctx, accountID, derivedID)
		return err
	})
	if err != nil {
		log.Error("cannot remove a pending derived row", "error", err)
	}
}

// dropRow removes a pending row of a job, so a later request queues the work again.
func dropRow(ctx context.Context, st *store.Store, log *slog.Logger, j job) {
	err := st.Write(ctx, func(tx *store.Tx) error {
		_, err := tx.DeletePendingDerived(ctx, j.accountID, j.derivedID)
		return err
	})
	if err != nil {
		log.Error("cannot remove a pending derived row", "error", err)
	}
}

// failRow records a job's outcome as failed. A transient failure (the server's or the host's own) is retried after a
// wait, up to maxAttempts; any other is sticky for its key. Once the context is cancelled nothing is recorded.
func failRow(ctx context.Context, st *store.Store, now func() time.Time, log *slog.Logger, j job, reason string, transient bool) {
	if ctx.Err() != nil {
		return
	}
	at := now()
	row, err := st.DerivedByID(ctx, j.accountID, j.derivedID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		log.Error("cannot read a derived row", "error", err, "derived", j.derivedID)
		return
	}
	attempts := row.Attempts + 1
	var retryAt time.Time
	if transient {
		if attempts < maxAttempts {
			retryAt = at.Add(retryBackoff[attempts-1])
		} else {
			reason = fmt.Sprintf("%s (gave up after %d attempts)", reason, attempts)
		}
	}
	err = st.Write(ctx, func(tx *store.Tx) error {
		_, err := tx.FailDerived(ctx, j.accountID, j.derivedID, reason, at, attempts, retryAt)
		return err
	})
	if err != nil {
		log.Error("cannot record a failed derived file", "error", err)
	}
}

// discardTemp removes a temporary file that will not be kept.
func discardTemp(log *slog.Logger, tmp string) {
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Error("cannot remove a derived temporary file", "error", err)
	}
}

// discardFinal removes a file placed at its final path that will not be kept.
func discardFinal(log *slog.Logger, final string) {
	if err := os.Remove(final); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Error("cannot remove a derived file", "error", err)
	}
}
