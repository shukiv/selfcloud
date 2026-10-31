// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// zipOrphanAge is how old a zip file with no row must be before the sweep removes it. A file being published has its row
// (it was written when the zip was asked for), so this only guards the file against a clock step or a slow write.
const zipOrphanAge = time.Hour

// ZipFiles holds the stored zip downloads (decision 35). A zip is at zips/<account>/<zip>.zip, and while it is being
// made at zips/<account>/<zip>.part. The directory sits beside the thumbnail files, not inside them, so the thumbnail
// sweep never reads it. Account deletion removes the account's directory here.
type ZipFiles struct {
	root string
	st   *store.Store
	ttl  time.Duration
}

// NewZipFiles returns the zip files under dir/zips, creating the directory. ttl is zip.ttl: how long a failed zip's row
// is kept before the sweep removes it.
func NewZipFiles(dir string, st *store.Store, ttl time.Duration) (*ZipFiles, error) {
	root := filepath.Join(dir, "zips")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("zips: %w", err)
	}
	return &ZipFiles{root: root, st: st, ttl: ttl}, nil
}

// Path is the path of a finished zip. Both IDs are validated first, so only checked names become path elements.
func (z *ZipFiles) Path(accountID, zipID string) (string, error) {
	return z.pathWithExt(accountID, zipID, ".zip")
}

// PartPath is the path of a zip being made.
func (z *ZipFiles) PartPath(accountID, zipID string) (string, error) {
	return z.pathWithExt(accountID, zipID, ".part")
}

func (z *ZipFiles) pathWithExt(accountID, zipID, ext string) (string, error) {
	if !id.Valid("acc", accountID) || !id.Valid("zip", zipID) {
		return "", fmt.Errorf("zips: invalid ID %q %q", accountID, zipID)
	}
	return filepath.Join(z.root, accountID, zipID+ext), nil
}

// Remove deletes a zip's finished and partial files. A file that is not there is not an error.
func (z *ZipFiles) Remove(accountID, zipID string) error {
	for _, ext := range []string{".zip", ".part"} {
		p, err := z.pathWithExt(accountID, zipID, ext)
		if err != nil {
			return err
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("zips: %w", err)
		}
	}
	return nil
}

// RemoveAccount deletes every zip file of an account.
func (z *ZipFiles) RemoveAccount(accountID string) error {
	if !id.Valid("acc", accountID) {
		return fmt.Errorf("zips: invalid account ID %q", accountID)
	}
	if err := os.RemoveAll(filepath.Join(z.root, accountID)); err != nil {
		return fmt.Errorf("zips: %w", err)
	}
	return nil
}

// ZipSweep is what one pass of the zip sweep did.
type ZipSweep struct {
	Expired   int // ready zips past their expiry: row first, then file
	Abandoned int // pending zips whose job is gone: failed, then their partial file removed
	Failed    int // failed zips older than zip.ttl: their rows and roots are removed
	Orphans   int // zip files no row names, older than an hour: removed
}

// Sweep does four bounded steps, each in at most limit items. An expired zip's row goes first, in one write, and then
// its file. An abandoned zip is failed before its file goes. A failed zip's row goes once zip.ttl has passed since its
// creation. The orphan step removes a zip file that no row names and that is more than an hour old: a row is removed
// before its file in a delete, so a crash or a failed removal leaves a file no row names, and this step is the one that
// removes it. A file that cannot be removed is returned as the error, and the pass goes on.
func (z *ZipFiles) Sweep(ctx context.Context, now time.Time, limit int) (ZipSweep, error) {
	var out ZipSweep
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	expired, err := z.st.ExpiredZips(ctx, now, limit)
	if err != nil {
		return out, err
	}
	for _, zp := range expired {
		if err := z.st.Write(ctx, func(tx *store.Tx) error {
			_, err := tx.DeleteZip(ctx, zp.AccountID, zp.ID)
			return err
		}); err != nil {
			return out, err
		}
		out.Expired++
		keep(z.Remove(zp.AccountID, zp.ID))
	}
	abandoned, err := z.st.PendingZipsWithoutJob(ctx, limit)
	if err != nil {
		return out, err
	}
	for _, zp := range abandoned {
		if err := z.st.Write(ctx, func(tx *store.Tx) error {
			return tx.FailZip(ctx, zp.ID, "the job that made this zip did not finish")
		}); err != nil {
			return out, err
		}
		out.Abandoned++
		keep(z.Remove(zp.AccountID, zp.ID))
	}
	if err := z.st.Write(ctx, func(tx *store.Tx) error {
		n, err := tx.DeleteFailedZips(ctx, now.Add(-z.ttl), limit)
		out.Failed = int(n)
		return err
	}); err != nil {
		return out, err
	}
	orphans, err := z.removeOrphans(ctx, now, limit)
	out.Orphans = orphans
	keep(err)
	return out, firstErr
}

// removeOrphans removes up to limit zip files (<zip>.zip and <zip>.part) that no row names and that are more than
// zipOrphanAge old by their modification time. A file whose row exists is never removed, however old.
func (z *ZipFiles) removeOrphans(ctx context.Context, now time.Time, limit int) (int, error) {
	accounts, err := os.ReadDir(z.root)
	if err != nil {
		return 0, fmt.Errorf("zips: %w", err)
	}
	removed := 0
	for _, a := range accounts {
		if !a.IsDir() || !id.Valid("acc", a.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(z.root, a.Name()))
		if err != nil {
			return removed, fmt.Errorf("zips: %w", err)
		}
		for _, f := range files {
			if removed >= limit || ctx.Err() != nil {
				return removed, ctx.Err()
			}
			ext := filepath.Ext(f.Name())
			zipID := strings.TrimSuffix(f.Name(), ext)
			if f.IsDir() || (ext != ".zip" && ext != ".part") || !id.Valid("zip", zipID) {
				continue
			}
			info, err := f.Info()
			if err != nil || now.Sub(info.ModTime()) < zipOrphanAge {
				continue
			}
			if _, err := z.st.Zip(ctx, a.Name(), zipID); err == nil {
				continue // its row names it
			} else if !errors.Is(err, store.ErrNotFound) {
				return removed, err
			}
			if err := os.Remove(filepath.Join(z.root, a.Name(), f.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return removed, fmt.Errorf("zips: %w", err)
			}
			removed++
		}
	}
	return removed, nil
}
