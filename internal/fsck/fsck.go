// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fsck checks the data directory against the database. It reports blobs that no row names, rows whose file is
// missing or the wrong size, and SQLite's own integrity checks. It reads the database and never writes to it, and it
// is safe beside a running server (decision 100 in docs/design.md). Its repair only moves orphan blobs into a dated
// quarantine folder.
//
// Layout it checks (the writers are internal/blob, internal/derived and internal/sweep):
//
//	blobs/<account-id>/<b1>/<b2>/<blob-id>   named by blobs, nodes, versions, uploads and import archives
//	derived/<account-id>/<derived-id>        named by derived rows (ready ones have a file)
//	derived/zips/<account-id>/<zip-id>.zip   named by zips rows (ready ones have a .zip)
//
// The uploads/ tree, temporary files and keys/ are not checked: the sweep owns the first two, and keys/ is not
// named by any row.
package fsck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// Options configure Run.
type Options struct {
	DataDir       string        // the data directory (blobs/, derived/, quarantine/)
	DBPath        string        // the SQLite database; it must exist
	MinAge        time.Duration // files modified within this long before the snapshot are not judged
	VerifyContent bool          // hash the contents against the rows
	Repair        bool          // move orphan blobs to quarantine
	DerivedDir    string        // the derived directory; "" means DataDir/derived (the server's default)
}

// Finding kinds.
const (
	KindOrphanBlob     = "orphan_blob"
	KindMissingBlob    = "missing_blob"
	KindSizeBlob       = "size_mismatch_blob"
	KindHashBlob       = "hash_mismatch_blob"
	KindOrphanDerived  = "orphan_derived"
	KindMissingDerived = "missing_derived"
	KindSizeDerived    = "size_mismatch_derived"
	KindHashDerived    = "hash_mismatch_derived"
	KindOrphanZip      = "orphan_zip"
	KindMissingZip     = "missing_zip"
	KindSizeZip        = "size_mismatch_zip"
	KindHashZip        = "hash_mismatch_zip"
	KindStray          = "stray"
	KindIntegrity      = "sqlite_integrity"
	KindForeignKey     = "foreign_key"
)

var allKinds = []string{KindOrphanBlob, KindMissingBlob, KindSizeBlob, KindHashBlob, KindOrphanDerived,
	KindMissingDerived, KindSizeDerived, KindHashDerived, KindOrphanZip, KindMissingZip, KindSizeZip, KindHashZip,
	KindStray, KindIntegrity, KindForeignKey}

// Finding is one thing that disagrees.
type Finding struct {
	Kind    string `json:"kind"`
	Account string `json:"account,omitempty"`
	ID      string `json:"id,omitempty"` // the blob, derived or zip ID
	Node    string `json:"node,omitempty"`
	Version string `json:"version,omitempty"`
	Path    string `json:"path,omitempty"` // relative to the data directory
	Detail  string `json:"detail,omitempty"`
	Outcome string `json:"outcome,omitempty"` // what repair did with it, when it did something
}

// Report is the result of one run.
type Report struct {
	DataDir       string         `json:"data_dir"`
	SnapshotAt    time.Time      `json:"snapshot_at"`
	MinAgeSeconds int64          `json:"min_age_seconds"`
	VerifyContent bool           `json:"verify_content"`
	Repair        bool           `json:"repair"`
	Integrity     string         `json:"integrity"`
	Counts        map[string]int `json:"counts"`
	Scanned       map[string]int `json:"scanned"`
	Findings      []Finding      `json:"findings"`
	Quarantine    string         `json:"quarantine,omitempty"`
	Moved         int            `json:"moved"`
}

// Run checks the data directory against the database. On an error after the database was opened, the report still
// holds what was found before it.
func Run(ctx context.Context, opts Options) (*Report, error) {
	rep := &Report{DataDir: opts.DataDir, MinAgeSeconds: int64(opts.MinAge / time.Second),
		VerifyContent: opts.VerifyContent, Repair: opts.Repair, Integrity: "ok",
		Counts: map[string]int{}, Scanned: map[string]int{}, Findings: []Finding{}}
	if opts.MinAge < 0 {
		return rep, errors.New("fsck: the minimum age must not be negative")
	}
	if opts.DerivedDir == "" {
		opts.DerivedDir = filepath.Join(opts.DataDir, "derived")
	}
	want, err := store.LatestSchemaVersion()
	if err != nil {
		return rep, fmt.Errorf("fsck: %w", err)
	}
	cs, err := store.OpenCheck(ctx, opts.DBPath)
	if err != nil {
		return rep, fmt.Errorf("fsck: %w", err)
	}
	defer cs.Close()
	have, err := cs.SchemaVersion(ctx)
	if err != nil {
		// The file is there but cannot be read: that is a failed integrity check, not a usage error.
		unreadable(rep, err)
		return rep, fmt.Errorf("fsck: %w", err)
	}
	if have != want {
		return rep, fmt.Errorf("fsck: the database is at schema version %d and this selfcloud expects %d; "+
			"start the server that owns it, or run selfcloud migrate", have, want)
	}
	c := &checker{ctx: ctx, opts: opts, cs: cs, rep: rep}
	err = c.run()
	c.finish()
	if err != nil {
		return rep, fmt.Errorf("fsck: %w", err)
	}
	return rep, nil
}

// candidate is a row whose file was not found. It becomes a finding after a second look (recheck).
type candidate struct {
	kind    string
	account string
	id      string
}

// hashCheck is a file to hash after the snapshot is closed: its row says what the hash must be.
type hashCheck struct {
	kind string
	row  store.FileRow
	rel  string
}

type checker struct {
	ctx    context.Context
	opts   Options
	cs     *store.CheckStore
	snap   *store.Snapshot
	rep    *Report
	cands  []candidate
	hashes []hashCheck
}

// run makes every check in one read snapshot, so the checks agree with each other. Then it looks again at what it
// found as missing, in newer snapshots, and repairs if asked.
func (c *checker) run() error {
	snap, err := c.cs.Begin(c.ctx)
	if err != nil {
		unreadable(c.rep, err)
		return err
	}
	c.snap = snap
	c.rep.SnapshotAt = snap.At
	err = c.snapshotChecks()
	snap.Close()
	c.snap = nil
	if err != nil {
		return err
	}
	if err := c.hashPass(); err != nil {
		return err
	}
	if err := c.recheck(); err != nil {
		return err
	}
	if c.opts.Repair {
		return c.repair()
	}
	return nil
}

func (c *checker) snapshotChecks() error {
	ok, problems, err := c.snap.IntegrityCheck(c.ctx)
	if err != nil {
		// A damaged file can make the check itself fail rather than list its problems. That is a failure too.
		c.rep.Integrity = "failed"
		c.add(Finding{Kind: KindIntegrity, Detail: err.Error()})
		return err
	}
	if !ok {
		c.rep.Integrity = "failed"
		for _, p := range problems {
			c.add(Finding{Kind: KindIntegrity, Detail: p})
		}
	}
	fks, err := c.snap.ForeignKeyViolations(c.ctx)
	if err != nil {
		return err
	}
	for _, p := range fks {
		c.add(Finding{Kind: KindForeignKey, Detail: p})
	}
	for _, step := range []func() error{c.walkBlobs, c.missingBlobs, c.walkDerived, c.missingDerived,
		c.walkZips, c.missingZips} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func (c *checker) add(f Finding) { c.rep.Findings = append(c.rep.Findings, f) }

func (c *checker) count(key string) { c.rep.Scanned[key]++ }

// tooRecent reports whether a file was modified within MinAge before the snapshot. Such a file may be a blob whose row
// is not written yet, so it is not judged. The rule's bound is in decision 100.
func (c *checker) tooRecent(info fs.FileInfo) bool {
	return info.ModTime().After(c.snap.At.Add(-c.opts.MinAge))
}

// walkBlobs judges every file under blobs/ that has the layout of a blob.
func (c *checker) walkBlobs() error {
	root := filepath.Join(c.opts.DataDir, "blobs")
	accs, err := readDir(root)
	if err != nil {
		return err
	}
	for _, a := range accs {
		accRel := filepath.Join("blobs", a.Name())
		if !a.IsDir() || !id.Valid("acc", a.Name()) {
			c.add(Finding{Kind: KindStray, Path: accRel, Detail: "not an account directory"})
			continue
		}
		b1s, err := readDir(filepath.Join(root, a.Name()))
		if err != nil {
			return err
		}
		for _, b1 := range b1s {
			b1Rel := filepath.Join(accRel, b1.Name())
			if !b1.IsDir() || len(b1.Name()) != 2 {
				c.add(Finding{Kind: KindStray, Path: b1Rel, Detail: "not a blob directory"})
				continue
			}
			b2s, err := readDir(filepath.Join(root, a.Name(), b1.Name()))
			if err != nil {
				return err
			}
			for _, b2 := range b2s {
				b2Rel := filepath.Join(b1Rel, b2.Name())
				if !b2.IsDir() || len(b2.Name()) != 2 {
					c.add(Finding{Kind: KindStray, Path: b2Rel, Detail: "not a blob directory"})
					continue
				}
				files, err := readDir(filepath.Join(root, a.Name(), b1.Name(), b2.Name()))
				if err != nil {
					return err
				}
				for _, f := range files {
					rel := filepath.Join(b2Rel, f.Name())
					name := f.Name()
					if !f.Type().IsRegular() || !id.Valid("blb", name) || name[4:6] != b1.Name() || name[6:8] != b2.Name() {
						c.add(Finding{Kind: KindStray, Path: rel, Detail: "not a blob file"})
						continue
					}
					info, err := f.Info()
					if err != nil {
						if errors.Is(err, fs.ErrNotExist) {
							continue // removed by the server since the listing
						}
						return err
					}
					if err := c.judgeBlob(a.Name(), name, rel, info); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// judgeBlob checks one blob file against its row, or, with no row, against the rows that name it.
func (c *checker) judgeBlob(acc, bid, rel string, info fs.FileInfo) error {
	c.count("blob_files")
	row, found, err := c.snap.BlobRow(c.ctx, bid)
	if err != nil {
		return err
	}
	if found {
		if row.AccountID != acc {
			c.add(Finding{Kind: KindStray, Account: acc, ID: bid, Path: rel, Detail: "its row names another account"})
			return nil
		}
		if info.Size() != row.Size {
			c.add(Finding{Kind: KindSizeBlob, Account: acc, ID: bid, Path: rel,
				Detail: fmt.Sprintf("the file has %d bytes, its row says %d", info.Size(), row.Size)})
		}
		if c.opts.VerifyContent && row.SHA256 != "" {
			c.hashes = append(c.hashes, hashCheck{kind: KindHashBlob,
				row: store.FileRow{ID: bid, AccountID: acc, SHA256: row.SHA256}, rel: rel})
		}
		return nil
	}
	named, err := c.snap.BlobNamed(c.ctx, bid)
	if err != nil {
		return err
	}
	if named {
		c.count("blob_named_without_row")
		return nil
	}
	if c.tooRecent(info) {
		c.count("blob_too_recent")
		return nil
	}
	c.add(Finding{Kind: KindOrphanBlob, Account: acc, ID: bid, Path: rel,
		Detail: fmt.Sprintf("%d bytes; no row names it", info.Size())})
	return nil
}

// missingBlobs lists the blob rows that are live, and the blobs that a node or version names with no row, whose files
// are not there. They are candidates until recheck.
func (c *checker) missingBlobs() error {
	err := c.snap.EachLiveBlob(c.ctx, func(r store.FileRow) error {
		c.count("live_blob_rows")
		if !id.Valid("acc", r.AccountID) || !id.Valid("blb", r.ID) {
			c.count("blob_rows_bad_id")
			return nil
		}
		gone, err := absent(blobPath(c.opts.DataDir, r.AccountID, r.ID))
		if err != nil {
			return err
		}
		if gone {
			c.cands = append(c.cands, candidate{kind: KindMissingBlob, account: r.AccountID, id: r.ID})
		}
		return nil
	})
	if err != nil {
		return err
	}
	return c.snap.EachUnrecordedBlob(c.ctx, func(acc, bid string) error {
		if !id.Valid("acc", acc) || !id.Valid("blb", bid) {
			c.count("blob_rows_bad_id")
			return nil
		}
		gone, err := absent(blobPath(c.opts.DataDir, acc, bid))
		if err != nil {
			return err
		}
		if gone {
			c.cands = append(c.cands, candidate{kind: KindMissingBlob, account: acc, id: bid})
		}
		return nil
	})
}

// walkDerived judges the files of derived rows. A file no row names is an orphan; a ready row's file is checked
// against the row's size, and with --verify-content against its hash.
func (c *checker) walkDerived() error {
	root := c.derivedRoot()
	accs, err := readDir(root)
	if err != nil {
		return err
	}
	for _, a := range accs {
		if !a.IsDir() || !id.Valid("acc", a.Name()) {
			continue // tmp/, zips/ and anything else are not derived files
		}
		files, err := readDir(filepath.Join(root, a.Name()))
		if err != nil {
			return err
		}
		for _, f := range files {
			name := f.Name()
			if !f.Type().IsRegular() || !id.Valid("drv", name) {
				continue
			}
			info, err := f.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return err
			}
			rel := c.rel(filepath.Join(root, a.Name(), name))
			c.count("derived_files")
			row, found, err := c.snap.DerivedRow(c.ctx, name)
			if err != nil {
				return err
			}
			if found {
				if row.AccountID != a.Name() || row.State != "ready" {
					continue // another account's row, or one still being made: not judged here
				}
				if err := c.checkFile(row, rel, info, KindSizeDerived, KindHashDerived); err != nil {
					return err
				}
				continue
			}
			if c.tooRecent(info) {
				c.count("derived_too_recent")
				continue
			}
			c.add(Finding{Kind: KindOrphanDerived, Account: a.Name(), ID: name, Path: rel,
				Detail: fmt.Sprintf("%d bytes; no derived row names it", info.Size())})
		}
	}
	return nil
}

// checkFile compares a ready row's file with the row. A size mismatch is always reported; a hash mismatch only with
// --verify-content.
func (c *checker) checkFile(row store.FileRow, rel string, info fs.FileInfo, sizeKind, hashKind string) error {
	if info.Size() != row.Size {
		c.add(Finding{Kind: sizeKind, Account: row.AccountID, ID: row.ID, Path: rel,
			Detail: fmt.Sprintf("the file has %d bytes, its row says %d", info.Size(), row.Size)})
	}
	if c.opts.VerifyContent && row.SHA256 != "" {
		c.hashes = append(c.hashes, hashCheck{kind: hashKind, row: row, rel: rel})
	}
	return nil
}

// hashPass reads the contents for --verify-content. It runs after the snapshot is closed, so a long hash does not hold
// a read transaction open. That is safe because a committed blob, derived file or zip is never changed, so its bytes
// are the bytes its row describes whenever they are read. The file may be gone by now; that is not a finding.
func (c *checker) hashPass() error {
	for _, h := range c.hashes {
		c.count("hashed_files")
		got, err := hashFile(filepath.Join(c.opts.DataDir, h.rel))
		if errors.Is(err, fs.ErrNotExist) {
			c.count("hash_removed_since_listing")
			continue
		}
		if err != nil {
			return err
		}
		if got != h.row.SHA256 {
			c.add(Finding{Kind: h.kind, Account: h.row.AccountID, ID: h.row.ID, Path: h.rel,
				Detail: "the contents do not hash to the row's SHA-256"})
		}
	}
	return nil
}

// missingDerived lists ready derived rows whose files are not there.
func (c *checker) missingDerived() error {
	root := c.derivedRoot()
	return c.snap.EachReadyDerived(c.ctx, func(r store.FileRow) error {
		c.count("ready_derived_rows")
		if !id.Valid("acc", r.AccountID) || !id.Valid("drv", r.ID) {
			c.count("derived_rows_bad_id")
			return nil
		}
		gone, err := absent(filepath.Join(root, r.AccountID, r.ID))
		if err != nil {
			return err
		}
		if gone {
			c.cands = append(c.cands, candidate{kind: KindMissingDerived, account: r.AccountID, id: r.ID})
		}
		return nil
	})
}

// walkZips judges the zip files. A zip file no row names is an orphan; a ready zip's .zip is checked against its row.
func (c *checker) walkZips() error {
	root := filepath.Join(c.derivedRoot(), "zips")
	accs, err := readDir(root)
	if err != nil {
		return err
	}
	for _, a := range accs {
		if !a.IsDir() || !id.Valid("acc", a.Name()) {
			continue
		}
		files, err := readDir(filepath.Join(root, a.Name()))
		if err != nil {
			return err
		}
		for _, f := range files {
			name := f.Name()
			ext := filepath.Ext(name)
			zid := strings.TrimSuffix(name, ext)
			if !f.Type().IsRegular() || (ext != ".zip" && ext != ".part") || !id.Valid("zip", zid) {
				continue
			}
			info, err := f.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return err
			}
			rel := c.rel(filepath.Join(root, a.Name(), name))
			c.count("zip_files")
			row, found, err := c.snap.ZipRow(c.ctx, zid)
			if err != nil {
				return err
			}
			if found {
				if ext == ".zip" && row.State == "ready" && row.AccountID == a.Name() {
					if err := c.checkFile(row, rel, info, KindSizeZip, KindHashZip); err != nil {
						return err
					}
				}
				continue
			}
			if c.tooRecent(info) {
				c.count("zip_too_recent")
				continue
			}
			c.add(Finding{Kind: KindOrphanZip, Account: a.Name(), ID: zid, Path: rel,
				Detail: fmt.Sprintf("%d bytes; no zip row names it", info.Size())})
		}
	}
	return nil
}

// missingZips lists ready zip rows whose .zip is not there.
func (c *checker) missingZips() error {
	root := filepath.Join(c.derivedRoot(), "zips")
	return c.snap.EachReadyZip(c.ctx, func(r store.FileRow) error {
		c.count("ready_zip_rows")
		if !id.Valid("acc", r.AccountID) || !id.Valid("zip", r.ID) {
			c.count("zip_rows_bad_id")
			return nil
		}
		gone, err := absent(filepath.Join(root, r.AccountID, r.ID+".zip"))
		if err != nil {
			return err
		}
		if gone {
			c.cands = append(c.cands, candidate{kind: KindMissingZip, account: r.AccountID, id: r.ID})
		}
		return nil
	})
}

// recheck looks at each missing candidate again, in a newer snapshot, and keeps it only if it is still missing.
//
// A blob is kept when its row is live or a node, version, upload or archive still names it. The sweep removes a blob's
// file only after it has set the row to orphan and then only when nothing names it, so a row that is no longer live
// and unnamed means the sweep got there first. A derived file is kept when its row is still ready and its blob is
// still live or named. A zip is kept when its row is still ready: the sweep removes a zip's row before its file.
func (c *checker) recheck() error {
	for _, cand := range c.cands {
		snap, err := c.cs.Begin(c.ctx)
		if err != nil {
			return err
		}
		keep, err := c.stillMissing(snap, cand)
		if err != nil {
			snap.Close()
			return err
		}
		if !keep {
			snap.Close()
			c.count("recheck_dropped")
			continue
		}
		f := Finding{Kind: cand.kind, Account: cand.account, ID: cand.id, Detail: "the file is not there"}
		switch cand.kind {
		case KindMissingBlob:
			f.Path = filepath.Join("blobs", cand.account, cand.id[4:6], cand.id[6:8], cand.id)
			f.Node, f.Version, err = snap.BlobReferrers(c.ctx, cand.id)
		case KindMissingDerived:
			f.Path = c.rel(filepath.Join(c.derivedRoot(), cand.account, cand.id))
		case KindMissingZip:
			f.Path = c.rel(filepath.Join(c.derivedRoot(), "zips", cand.account, cand.id+".zip"))
		}
		snap.Close()
		if err != nil {
			return err
		}
		c.add(f)
	}
	return nil
}

func (c *checker) stillMissing(snap *store.Snapshot, cand candidate) (bool, error) {
	ctx := c.ctx
	switch cand.kind {
	case KindMissingBlob:
		row, found, err := snap.BlobRow(ctx, cand.id)
		if err != nil {
			return false, err
		}
		if found && row.State == store.BlobLive {
			return true, nil
		}
		return snap.BlobNamed(ctx, cand.id)
	case KindMissingDerived:
		return snap.DerivedKept(ctx, cand.id)
	case KindMissingZip:
		row, found, err := snap.ZipRow(ctx, cand.id)
		if err != nil || !found {
			return false, err
		}
		return row.State == "ready", nil
	}
	return false, nil
}

// repair moves each orphan blob into the quarantine folder, once a fresh snapshot says no row names it and it is old
// enough. It renames and never deletes. A manifest lists what moved.
func (c *checker) repair() error {
	day := time.Now().UTC().Format("2006-01-02")
	qRel := filepath.Join("quarantine", day)
	qAbs := filepath.Join(c.opts.DataDir, qRel)
	type moved struct {
		Account  string    `json:"account"`
		Blob     string    `json:"blob"`
		From     string    `json:"from"`
		To       string    `json:"to"`
		Size     int64     `json:"size"`
		Modified time.Time `json:"modified"`
		Reason   string    `json:"reason"`
	}
	var entries []moved
	for i := range c.rep.Findings {
		f := &c.rep.Findings[i]
		if f.Kind != KindOrphanBlob {
			continue
		}
		done, err := c.moveOrphan(f, qRel, qAbs)
		if err != nil {
			return err
		}
		if done == nil {
			continue
		}
		entries = append(entries, moved{Account: f.Account, Blob: f.ID, From: f.Path, To: filepath.Join(qRel, f.Path),
			Size: done.Size(), Modified: done.ModTime().UTC(), Reason: "no row names this blob"})
		c.rep.Moved++
	}
	if len(entries) == 0 {
		return nil
	}
	c.rep.Quarantine = qRel
	manifest := struct {
		RunAt time.Time `json:"run_at"`
		Moved []moved   `json:"moved"`
	}{RunAt: time.Now().UTC(), Moved: entries}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	name := "manifest-" + time.Now().UTC().Format("150405.000000000") + ".json"
	if err := os.WriteFile(filepath.Join(qAbs, name), data, 0o600); err != nil {
		return fmt.Errorf("writing the manifest: %w", err)
	}
	return nil
}

// moveOrphan renames one orphan into quarantine when it is still an orphan. It returns the file's info when it moved,
// and nil with f.Outcome saying why not.
func (c *checker) moveOrphan(f *Finding, qRel, qAbs string) (fs.FileInfo, error) {
	snap, err := c.cs.Begin(c.ctx)
	if err != nil {
		return nil, err
	}
	named, err := snap.BlobNamed(c.ctx, f.ID)
	at := snap.At
	snap.Close()
	if err != nil {
		return nil, err
	}
	if named {
		f.Outcome = "not moved: a row names it now"
		return nil, nil
	}
	src := filepath.Join(c.opts.DataDir, f.Path)
	info, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		f.Outcome = "not moved: gone"
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.ModTime().After(at.Add(-c.opts.MinAge)) {
		f.Outcome = "not moved: too recent"
		return nil, nil
	}
	dst := filepath.Join(qAbs, f.Path)
	if _, err := os.Lstat(dst); err == nil {
		f.Outcome = "not moved: already in quarantine"
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return nil, fmt.Errorf("quarantine: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		f.Outcome = "not moved: " + err.Error()
		return nil, nil
	}
	f.Outcome = "moved to " + filepath.Join(qRel, f.Path)
	return info, nil
}

// finish fills in the counts from the findings. Every kind has a count, zero included.
func (c *checker) finish() { countKinds(c.rep) }

func countKinds(rep *Report) {
	for _, k := range allKinds {
		rep.Counts[k] = 0
	}
	for _, f := range rep.Findings {
		rep.Counts[f.Kind]++
	}
}

// unreadable records that the database could not be read at all. That fails the integrity check.
func unreadable(rep *Report, err error) {
	rep.Integrity = "failed"
	rep.Findings = append(rep.Findings, Finding{Kind: KindIntegrity, Detail: "the database cannot be read: " + err.Error()})
	countKinds(rep)
}

// WriteJSON writes the report as one JSON document.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText writes a short human-readable summary: the integrity result, each finding and the counts.
func (r *Report) WriteText(w io.Writer) {
	fmt.Fprintf(w, "data directory: %s\nsnapshot: %s\nintegrity: %s\n", r.DataDir,
		r.SnapshotAt.UTC().Format(time.RFC3339), r.Integrity)
	for _, f := range r.Findings {
		fmt.Fprintf(w, "%s", f.Kind)
		if f.Account != "" {
			fmt.Fprintf(w, " account=%s", f.Account)
		}
		if f.ID != "" {
			fmt.Fprintf(w, " id=%s", f.ID)
		}
		if f.Node != "" {
			fmt.Fprintf(w, " node=%s", f.Node)
		}
		if f.Version != "" {
			fmt.Fprintf(w, " version=%s", f.Version)
		}
		if f.Detail != "" {
			fmt.Fprintf(w, ": %s", f.Detail)
		}
		if f.Outcome != "" {
			fmt.Fprintf(w, " (%s)", f.Outcome)
		}
		fmt.Fprintln(w)
	}
	kinds := make([]string, 0, len(r.Counts))
	for k := range r.Counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	fmt.Fprintf(w, "findings: %d\n", len(r.Findings))
	for _, k := range kinds {
		if r.Counts[k] > 0 {
			fmt.Fprintf(w, "  %s: %d\n", k, r.Counts[k])
		}
	}
	if r.Moved > 0 {
		fmt.Fprintf(w, "moved to quarantine: %d (%s)\n", r.Moved, r.Quarantine)
	}
}

// derivedRoot is the derived directory the server writes to: the configured one, or data_dir/derived.
func (c *checker) derivedRoot() string { return c.opts.DerivedDir }

// rel names a path for a finding: relative to the data directory when it is inside it, absolute otherwise.
func (c *checker) rel(p string) string {
	r, err := filepath.Rel(c.opts.DataDir, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return p
	}
	return r
}

func blobPath(dataDir, acc, bid string) string {
	return filepath.Join(dataDir, "blobs", acc, bid[4:6], bid[6:8], bid)
}

// absent reports whether nothing is at path. A path that cannot be examined is an error, not absent.
func absent(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	return false, err
}

// readDir lists a directory in name order. A directory that is not there is empty, and one that disappears during the
// listing is empty too, so a server removing an account beside the check does not stop it.
func readDir(path string) ([]fs.DirEntry, error) {
	ents, err := os.ReadDir(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return ents, err
}

// hashFile returns the SHA-256 of a file's contents, in lowercase hex. The walk has already seen the entry as a regular
// file, and the files are immutable once committed, so the bytes read are the bytes the row describes.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
