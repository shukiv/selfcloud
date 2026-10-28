package blob

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"selfcloud/internal/id"
)

// uploadPath returns the path of an upload's file under uploads/<account>. Both IDs are checked
// before any path is built.
func (d *Dir) uploadPath(accountID, uploadID string) (string, error) {
	if _, err := d.accountDir(accountID); err != nil {
		return "", err
	}
	if !id.Valid("upl", uploadID) {
		return "", fmt.Errorf("blob: invalid upload ID %q", uploadID)
	}
	return filepath.Join(d.dataDir, "uploads", accountID, uploadID), nil
}

// CreateUpload implements Store. The account's directory is made, then the file, exclusively with
// mode 0600; the directory is synced after the file's entry is made, so the file survives a crash.
func (d *Dir) CreateUpload(accountID, uploadID string) error {
	p, err := d.uploadPath(accountID, uploadID)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := mkdirAllSync(dir); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_EXCL|nofollow, 0o600)
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	if err := syncDirFn(dir); err != nil {
		return fmt.Errorf("blob: sync directory: %w", err)
	}
	return nil
}

// OpenUpload implements Store. It refuses symbolic links.
func (d *Dir) OpenUpload(accountID, uploadID string) (*os.File, error) {
	p, err := d.uploadPath(accountID, uploadID)
	if err != nil {
		return nil, err
	}
	return os.OpenFile(p, os.O_RDWR|nofollow, 0)
}

// RemoveUpload implements Store.
func (d *Dir) RemoveUpload(accountID, uploadID string) error {
	p, err := d.uploadPath(accountID, uploadID)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("blob: %w", err)
	}
	return nil
}

// FinishUpload implements Store. The file is synced, renamed to its blob path and both directories
// are synced. If the file is no longer under uploads/ but the blob exists, an earlier attempt
// already did the rename, and nothing is done.
func (d *Dir) FinishUpload(accountID, uploadID, blobID string) error {
	src, err := d.uploadPath(accountID, uploadID)
	if err != nil {
		return err
	}
	dst, err := d.blobPath(accountID, blobID)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(src, os.O_RDONLY|nofollow, 0)
	if errors.Is(err, fs.ErrNotExist) {
		if _, serr := os.Stat(dst); serr == nil {
			return nil
		}
		return fmt.Errorf("blob: upload %s has no file", uploadID)
	}
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("blob: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("blob: close: %w", err)
	}
	dir := filepath.Dir(dst)
	if err := mkdirAllSync(dir); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("blob: rename: %w", err)
	}
	if err := syncDirFn(dir); err != nil {
		return fmt.Errorf("blob: sync directory: %w", err)
	}
	if err := syncDirFn(filepath.Dir(src)); err != nil {
		return fmt.Errorf("blob: sync directory: %w", err)
	}
	return nil
}

// RemoveOrphanUploads implements Store. Only regular files named like upload IDs, last modified
// before the cutoff, are considered, so a file being created right now is never touched.
func (d *Dir) RemoveOrphanUploads(before time.Time, keep func(accountID, uploadID string) (bool, error)) (int, error) {
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
		if !acc.IsDir() || !validPart(acc.Name()) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, acc.Name()))
		if err != nil {
			return removed, fmt.Errorf("blob: %w", err)
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !strings.HasPrefix(e.Name(), "upl_") || !id.Valid("upl", e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.ModTime().Before(before) {
				continue
			}
			live, err := keep(acc.Name(), e.Name())
			if err != nil {
				return removed, err
			}
			if live {
				continue
			}
			if err := os.Remove(filepath.Join(root, acc.Name(), e.Name())); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}
