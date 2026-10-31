// SPDX-License-Identifier: AGPL-3.0-or-later

// Package derived keeps the files made from a blob's contents, such as thumbnails. A derived file is
// never part of the account's quota (design decision 33, proposal s section 5). It is kept under its own
// directory and budget, separately from blobs, so the upload path and usage accounting never see it.
//
// Layout under the derived directory:
//
//	<account-id>/<derived-id>   a ready derived file
//	tmp/<derived-id>.part       a file being made; removed at start-up and when stale
//
// A file's name is its row's ID, which the server makes. No path is built from a name a user typed.
package derived

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// EncoderVersion names the code that makes derived files. A change to the thumbnail code or its quality changes
// it, so rows made by the old code are not served as current. The converter key of a row adds the limits to it
// (converterFor).
const EncoderVersion = "thumb-go-2"

// Kind returns the kind of a thumbnail with its longer edge at size pixels.
func Kind(size int) string { return fmt.Sprintf("thumb_%d", size) }

// Files is the directory of derived files.
type Files struct {
	root string
}

// NewFiles returns the files under root, creating root and its tmp directory.
func NewFiles(root string) (*Files, error) {
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o700); err != nil {
		return nil, fmt.Errorf("derived: %w", err)
	}
	return &Files{root: root}, nil
}

// Root returns the directory.
func (f *Files) Root() string { return f.root }

// Path returns the path of a ready file. Both IDs are checked first, so only validated names become path elements.
func (f *Files) Path(accountID, derivedID string) (string, error) {
	if !id.Valid("acc", accountID) || !id.Valid("drv", derivedID) {
		return "", fmt.Errorf("derived: invalid ID %q %q", accountID, derivedID)
	}
	return filepath.Join(f.root, accountID, derivedID), nil
}

// TmpPath returns the path of a file being made.
func (f *Files) TmpPath(derivedID string) (string, error) {
	if !id.Valid("drv", derivedID) {
		return "", fmt.Errorf("derived: invalid ID %q", derivedID)
	}
	return filepath.Join(f.root, "tmp", derivedID+".part"), nil
}

// Remove deletes a ready file. A file that is already gone is not an error.
func (f *Files) Remove(accountID, derivedID string) error {
	p, err := f.Path(accountID, derivedID)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("derived: %w", err)
	}
	return nil
}

// RemoveAccount deletes everything an account has under the directory.
func (f *Files) RemoveAccount(accountID string) error {
	if !id.Valid("acc", accountID) {
		return fmt.Errorf("derived: invalid account ID %q", accountID)
	}
	if err := os.RemoveAll(filepath.Join(f.root, accountID)); err != nil {
		return fmt.Errorf("derived: %w", err)
	}
	return nil
}

// RemoveStaleTemp deletes files in tmp last modified before the given time. A worker is never that slow: its
// decode has a timeout far shorter than an hour.
func (f *Files) RemoveStaleTemp(before time.Time) (int, error) {
	dir := filepath.Join(f.root, "tmp")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("derived: %w", err)
	}
	n := 0
	for _, e := range ents {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(before) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			n++
		}
	}
	return n, nil
}

// Cache is the store of derived rows and files, and the budget on them.
type Cache struct {
	st       *store.Store
	files    *Files
	maxBytes int64
	log      *slog.Logger
	now      func() time.Time

	// uses holds the last time each ready row was used. Requests write here, not to the store, and Flush
	// writes the batch, so a page of tiles costs one write, not one per tile.
	mu   sync.Mutex
	uses map[string]usage
}

type usage struct {
	accountID string
	at        time.Time
}

// NewCache returns a cache whose ready files may total maxBytes.
func NewCache(st *store.Store, files *Files, maxBytes int64, log *slog.Logger) *Cache {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Cache{st: st, files: files, maxBytes: maxBytes, log: log, now: time.Now, uses: map[string]usage{}}
}

// Files returns the directory the cache keeps its files in.
func (c *Cache) Files() *Files { return c.files }

// Lookup returns the derived row of a blob for a kind and converter key, if any.
func (c *Cache) Lookup(ctx context.Context, accountID, blobID, kind, converter string) (*store.Derived, error) {
	d, err := c.st.DerivedFor(ctx, accountID, blobID, kind, converter)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return d, err
}

// Use records that a ready row was served. Flush writes the records.
func (c *Cache) Use(accountID, id string) {
	c.mu.Lock()
	c.uses[id] = usage{accountID: accountID, at: c.now()}
	c.mu.Unlock()
}

// Flush writes the recorded uses to the store, in one transaction.
func (c *Cache) Flush(ctx context.Context) error {
	c.mu.Lock()
	batch := c.uses
	c.uses = map[string]usage{}
	c.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	err := c.st.Write(ctx, func(tx *store.Tx) error {
		for id, u := range batch {
			if err := tx.TouchDerived(ctx, u.accountID, id, u.at); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Put the batch back, so the next flush retries it, unless a newer use has since arrived.
		c.mu.Lock()
		for id, u := range batch {
			if _, ok := c.uses[id]; !ok {
				c.uses[id] = u
			}
		}
		c.mu.Unlock()
	}
	return err
}

// RemoveBlob removes the derived rows of a blob and then their files. It runs before the blob's own row is
// removed, so no derived file outlives its blob. A file that cannot be removed is logged; the row is already
// gone, so the file is an orphan that a later pass cannot see. That is the same trade the blobs make.
func (c *Cache) RemoveBlob(ctx context.Context, accountID, blobID string) error {
	var rows []*store.Derived
	err := c.st.Write(ctx, func(tx *store.Tx) error {
		var err error
		rows, err = tx.DeleteDerivedForBlob(ctx, accountID, blobID)
		return err
	})
	if err != nil {
		return err
	}
	for _, d := range rows {
		if err := c.files.Remove(d.AccountID, d.ID); err != nil {
			c.log.Error("cannot remove derived file", "derived", d.ID, "account", d.AccountID, "error", err)
		}
	}
	return nil
}

// Evict removes least recently used ready files until the ready files total no more than the budget less need,
// and returns how many it removed. It works in batches so that no writer hold is long. Files go before their rows:
// a crash in between leaves a row whose file is missing, which Request notices and clears.
func (c *Cache) Evict(ctx context.Context, need int64) (int, error) {
	removed := 0
	for ctx.Err() == nil {
		total, err := c.st.ReadyDerivedBytes(ctx)
		if err != nil {
			return removed, err
		}
		over := total + need - c.maxBytes
		if over <= 0 {
			return removed, nil
		}
		rows, err := c.st.LeastUsedDerived(ctx, evictBatch)
		if err != nil {
			return removed, err
		}
		if len(rows) == 0 {
			return removed, nil
		}
		// Only as many rows as the budget needs, least recently used first.
		for _, d := range rows {
			if over <= 0 {
				break
			}
			// A file that cannot be removed stops the pass: the row would be chosen again at once, and
			// the loop would never end. The next sweep tries again.
			if err := c.files.Remove(d.AccountID, d.ID); err != nil {
				return removed, err
			}
			err := c.st.Write(ctx, func(tx *store.Tx) error {
				_, err := tx.DeleteDerivedRow(ctx, d.AccountID, d.ID)
				return err
			})
			if err != nil {
				return removed, err
			}
			over -= d.Size
			removed++
		}
	}
	return removed, ctx.Err()
}

// evictBatch is how many rows one eviction round looks at.
const evictBatch = 100

// ClearFailed removes the failed rows that failed before the given time, so the next request for each queues it
// again. It is the sweep's step: a failure is not kept for ever, even a sticky one, because a fix elsewhere (a
// decoder, a limit) may have made the image work.
func (c *Cache) ClearFailed(ctx context.Context, before time.Time) (int64, error) {
	var n int64
	err := c.st.Write(ctx, func(tx *store.Tx) error {
		var err error
		n, err = tx.ClearFailedDerived(ctx, before)
		return err
	})
	return n, err
}

// orphanScan bounds how many files one pass looks at.
const orphanScan = 1000

// orphanAge is how old a file must be before it can be an orphan. A file being made is in tmp/, and a finished one is
// renamed in place with its row already pending, so a young file with no row is left for the next pass.
const orphanAge = time.Hour

// RemoveOrphans deletes ready-file names under the account directories that no row names, at most orphanScan
// per pass. It returns how many it removed. The row is checked for each file, so a file whose row exists is kept
// whatever its state.
func (c *Cache) RemoveOrphans(ctx context.Context) (int, error) {
	root := c.files.Root()
	accounts, err := os.ReadDir(root)
	if err != nil {
		return 0, fmt.Errorf("derived: %w", err)
	}
	removed, scanned := 0, 0
	for _, a := range accounts {
		if scanned >= orphanScan || ctx.Err() != nil {
			break
		}
		if !a.IsDir() || !id.Valid("acc", a.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, a.Name()))
		if err != nil {
			c.log.Error("cannot list derived files", "account", a.Name(), "error", err)
			continue
		}
		for _, f := range files {
			if scanned >= orphanScan || ctx.Err() != nil {
				break
			}
			if !f.Type().IsRegular() || !id.Valid("drv", f.Name()) {
				continue
			}
			scanned++
			info, err := f.Info()
			if err != nil || c.now().Sub(info.ModTime()) < orphanAge {
				continue
			}
			ok, err := c.st.DerivedExists(ctx, a.Name(), f.Name())
			if err != nil {
				return removed, err
			}
			if ok {
				continue
			}
			if err := c.files.Remove(a.Name(), f.Name()); err != nil {
				c.log.Error("cannot remove an orphan derived file", "derived", f.Name(), "error", err)
				continue
			}
			removed++
		}
	}
	return removed, nil
}

// Recover is the start-up step: pending rows are work the last process never finished, and the queue they were
// in is gone, so they are removed, and the temporary files with them.
func (c *Cache) Recover(ctx context.Context) (int64, error) {
	var n int64
	err := c.st.Write(ctx, func(tx *store.Tx) error {
		var err error
		n, err = tx.DeleteAllPendingDerived(ctx)
		return err
	})
	if err != nil {
		return 0, err
	}
	if _, err := c.files.RemoveStaleTemp(c.now().Add(time.Second)); err != nil {
		return n, err
	}
	return n, nil
}
