// Package blob keeps file contents on local disk. Each file's contents is one immutable blob,
// named by a random ID the server makes; no path is ever built from a name a user typed.
//
// Layout under the data directory (design section 4):
//
//	blobs/<account-id>/<b1>/<b2>/<blob-id>   committed contents, never modified in place
//	uploads/<account-id>/tmp-*               writes in progress, on the same file system
//
// A write goes to a temporary file, is synced, is renamed into place, and then its directory is
// synced. Only after Commit returns may the caller record the blob in the database.
package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"selfcloud/internal/id"
)

// ErrTooLarge is returned by Create's writer when more than the limit is written.
var ErrTooLarge = errors.New("blob: contents are larger than the limit")

// Store holds committed blobs. Callers name blobs only by the IDs the store returned.
type Store interface {
	// Create starts a new blob for accountID. The writer must end with Commit or Abort.
	Create(ctx context.Context, accountID string, limit int64) (Writer, error)
	// Open opens a committed blob for reading. It does not follow symbolic links.
	Open(accountID, blobID string) (*os.File, error)
	// Remove deletes a committed blob. A blob that is already gone is not an error.
	Remove(accountID, blobID string) error
	// RemoveAccount deletes everything stored for an account.
	RemoveAccount(accountID string) error
	// RemoveStaleUploads deletes temporary files in uploads/ last modified before the given time, and
	// returns how many. A temporary file is one a write began and never committed.
	RemoveStaleUploads(before time.Time) (int, error)
	// CreateUpload makes the file of a resumable upload, exclusively, and closes it.
	CreateUpload(accountID, uploadID string) error
	// OpenUpload opens an upload's file for reading and writing. It does not create it.
	OpenUpload(accountID, uploadID string) (*os.File, error)
	// RemoveUpload deletes an upload's file. A file that is already gone is not an error.
	RemoveUpload(accountID, uploadID string) error
	// FinishUpload moves a complete upload's file into blobs/ under blobID and syncs both
	// directories. It is idempotent: when the file is already in blobs/, it does nothing.
	FinishUpload(accountID, uploadID, blobID string) error
	// RemoveOrphanUploads deletes upload files last modified before the given time that keep
	// reports as having no upload, and returns how many.
	RemoveOrphanUploads(before time.Time, keep func(accountID, uploadID string) (bool, error)) (int, error)
}

// Writer streams one blob to disk.
type Writer interface {
	io.Writer
	// Commit makes the blob durable and returns its ID, size and SHA-256 in hex.
	Commit() (Committed, error)
	// Abort discards the partial blob. It is safe to call after Commit, and does nothing then.
	Abort() error
}

// Committed describes a blob that is on disk.
type Committed struct {
	ID     string
	Size   int64
	SHA256 string
}

// Dir is a Store on a local directory.
type Dir struct {
	dataDir string
}

// New returns a Store under dataDir, creating blobs/ and uploads/ if needed.
func New(dataDir string) (*Dir, error) {
	for _, sub := range []string{"blobs", "uploads"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o700); err != nil {
			return nil, fmt.Errorf("blob: %w", err)
		}
	}
	return &Dir{dataDir: dataDir}, nil
}

// validPart reports whether s may be a path element: an ID, lowercase letters, digits and
// underscores, short. Anything else is refused before a path is built.
func validPart(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func (d *Dir) accountDir(accountID string) (string, error) {
	if !validPart(accountID) {
		return "", fmt.Errorf("blob: invalid account ID %q", accountID)
	}
	return filepath.Join(d.dataDir, "blobs", accountID), nil
}

// blobPath returns the path of a committed blob. The ID is checked first, so only its
// two-character prefixes become directory names.
func (d *Dir) blobPath(accountID, blobID string) (string, error) {
	acc, err := d.accountDir(accountID)
	if err != nil {
		return "", err
	}
	if !id.Valid("blb", blobID) {
		return "", fmt.Errorf("blob: invalid blob ID %q", blobID)
	}
	// "blb_" is four bytes; the next four characters are the two directory levels.
	return filepath.Join(acc, blobID[4:6], blobID[6:8], blobID), nil
}

// Create starts a blob. The file is made in uploads/<account> with O_EXCL and mode 0600.
func (d *Dir) Create(ctx context.Context, accountID string, limit int64) (Writer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := d.accountDir(accountID); err != nil {
		return nil, err
	}
	up := filepath.Join(d.dataDir, "uploads", accountID)
	if err := os.MkdirAll(up, 0o700); err != nil {
		return nil, fmt.Errorf("blob: %w", err)
	}
	f, err := os.CreateTemp(up, "tmp-*")
	if err != nil {
		return nil, fmt.Errorf("blob: %w", err)
	}
	return &writer{d: d, accountID: accountID, f: f, h: sha256.New(), limit: limit}, nil
}

type writer struct {
	d         *Dir
	accountID string
	f         *os.File
	h         hash.Hash
	n         int64
	limit     int64
	done      bool
}

func (w *writer) Write(p []byte) (int, error) {
	if w.done {
		return 0, errors.New("blob: write after commit or abort")
	}
	if w.n+int64(len(p)) > w.limit {
		return 0, ErrTooLarge
	}
	n, err := w.f.Write(p)
	w.h.Write(p[:n])
	w.n += int64(n)
	return n, err
}

// Commit syncs the file, renames it to its final name and syncs the directories that changed.
func (w *writer) Commit() (Committed, error) {
	if w.done {
		return Committed{}, errors.New("blob: already committed or aborted")
	}
	tmp := w.f.Name()
	defer w.Abort() //nolint:errcheck // removes the temporary file if anything below failed
	if err := w.f.Sync(); err != nil {
		return Committed{}, fmt.Errorf("blob: sync: %w", err)
	}
	blobID := id.New("blb")
	final, err := w.d.blobPath(w.accountID, blobID)
	if err != nil {
		return Committed{}, err
	}
	dir := filepath.Dir(final)
	if err := mkdirAllSync(dir); err != nil {
		return Committed{}, fmt.Errorf("blob: %w", err)
	}
	if err := w.f.Close(); err != nil {
		return Committed{}, fmt.Errorf("blob: close: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return Committed{}, fmt.Errorf("blob: rename: %w", err)
	}
	if err := syncDirFn(dir); err != nil {
		// The name may not be durable. Remove it rather than leave a file no row refers to.
		_ = os.Remove(final)
		return Committed{}, fmt.Errorf("blob: sync directory: %w", err)
	}
	w.done = true
	return Committed{ID: blobID, Size: w.n, SHA256: hex.EncodeToString(w.h.Sum(nil))}, nil
}

// Abort closes and removes the temporary file. After a successful Commit it does nothing.
func (w *writer) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	_ = w.f.Close() // already closed by Commit on success; the error is not useful here
	if err := os.Remove(w.f.Name()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Open opens a committed blob read-only, refusing symbolic links.
func (d *Dir) Open(accountID, blobID string) (*os.File, error) {
	p, err := d.blobPath(accountID, blobID)
	if err != nil {
		return nil, err
	}
	return os.OpenFile(p, os.O_RDONLY|nofollow, 0)
}

// Remove deletes a committed blob. A missing file is not an error, so a repeated removal is safe.
func (d *Dir) Remove(accountID, blobID string) error {
	p, err := d.blobPath(accountID, blobID)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("blob: %w", err)
	}
	return nil
}

// RemoveAccount deletes the blob and upload directories of an account.
func (d *Dir) RemoveAccount(accountID string) error {
	acc, err := d.accountDir(accountID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(acc); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(d.dataDir, "uploads", accountID)); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	return nil
}

// RemoveStaleUploads implements Store.
func (d *Dir) RemoveStaleUploads(before time.Time) (int, error) {
	root := filepath.Join(d.dataDir, "uploads")
	accounts, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("blob: %w", err)
	}
	removed := 0
	for _, acc := range accounts {
		if !acc.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, acc.Name()))
		if err != nil {
			return removed, fmt.Errorf("blob: %w", err)
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !strings.HasPrefix(e.Name(), "tmp-") {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.ModTime().Before(before) {
				continue
			}
			if err := os.Remove(filepath.Join(root, acc.Name(), e.Name())); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// mkdirAllSync creates dir and any missing parents. Each new directory's entry is synced into its
// parent, so a committed blob cannot be left behind an entry the disk forgot after a power cut.
func mkdirAllSync(dir string) error {
	var missing []string
	for p := dir; ; p = filepath.Dir(p) {
		_, err := os.Stat(p)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		missing = append(missing, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	for _, p := range missing {
		if err := syncDir(filepath.Dir(p)); err != nil {
			return err
		}
	}
	return nil
}

// syncDirFn is syncDir; a test replaces it to make a sync fail.
var syncDirFn = syncDir

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
