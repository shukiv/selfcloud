// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"archive/zip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// The measurements of decision 35 run only when SELFCLOUD_MEASURE_ZIP names them. The data directory is
// SELFCLOUD_MEASURE_DIR (under /var/tmp: /tmp is a small tmpfs). Progress lines go to stderr at once.
//
// "files" creates SELFCLOUD_MEASURE_FILES nodes in one write, all naming one small blob, and then zips the team
// folder: set-up is one transaction, so the job's time is what is measured. "big" makes one blob of
// SELFCLOUD_MEASURE_GIB gibibytes as a sparse file (its size and checksum are set to match, so the job reads it as a
// whole file), and zips the folder.

// maxRSSMiB is the peak resident size of the test process so far, in MiB.
func maxRSSMiB() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return -1
	}
	return float64(ru.Maxrss) / 1024
}

// progress prints one line at once to stderr.
func progress(format string, args ...any) {
	fmt.Fprintf(os.Stderr, time.Now().Format("15:04:05")+" "+format+"\n", args...)
}

func measureServer(t *testing.T, mutate ...func(*config.Config)) *zipFixtureCtx {
	t.Helper()
	dir := os.Getenv("SELFCLOUD_MEASURE_DIR")
	if dir == "" {
		t.Skip("SELFCLOUD_MEASURE_DIR is not set")
	}
	muts := append([]func(*config.Config){func(c *config.Config) { c.DataDir = dir }}, mutate...)
	return &zipFixtureCtx{fx: zipFixture(t, muts...)}
}

type zipFixtureCtx struct{ fx *sharingFixture }

// zipOf zips the team folder's root, as the account key. It reports the job's own wall time, which excludes the set-up,
// and the size of the zip; then it reads every entry back, checking each CRC.
func (z *zipFixtureCtx) zipOf(t *testing.T, label string) {
	t.Helper()
	fx := z.fx
	root := rootOf(t, fx, fx.fld.ID)
	start := time.Now()
	job := createZip(t, fx, nil, root)
	j := runZip(t, fx, job.ID)
	made := time.Since(start)
	if j.Status != "succeeded" {
		t.Fatalf("%s: job %s: %s", label, j.Status, j.Error)
	}
	rec := getZip(t, fx, nil, job.Target, nil)
	wantStatus(t, rec, 200)
	progress("%s: JOB wall %s, zip %d bytes, peak RSS %.0f MiB", label, made.Round(time.Millisecond), rec.Body.Len(), maxRSSMiB())
	readStart := time.Now()
	zr, err := zip.NewReader(strings.NewReader(rec.Body.String()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var n, bytes int64
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		c, err := io.Copy(io.Discard, rc) // reads every entry, so the CRC of each is checked
		rc.Close()
		if err != nil {
			t.Fatalf("entry %s: %v", f.Name, err)
		}
		n++
		bytes += c
	}
	progress("%s: read back %d entries, %d bytes, CRC checked, in %s", label, n, bytes, time.Since(readStart).Round(time.Millisecond))
}

func TestMeasureZipFiles(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_ZIP") != "files" {
		t.Skip("SELFCLOUD_MEASURE_ZIP is not files")
	}
	count, err := strconv.Atoi(os.Getenv("SELFCLOUD_MEASURE_FILES"))
	if err != nil || count < 1 {
		t.Skip("SELFCLOUD_MEASURE_FILES must be a count of files")
	}
	z := measureServer(t)
	fx := z.fx
	seed := putNode(t, fx.s, fx.key, fx.fld.ID, "/seed.txt", "hello")
	many := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/many")
	sn, err := fx.s.store.Node(t.Context(), fx.acc.ID, seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	now := store.Now()
	if err := fx.s.store.Write(t.Context(), func(tx *store.Tx) error {
		for i := 0; i < count; i++ {
			name := "f" + strconv.Itoa(i) + ".txt"
			n := &store.Node{ID: id.New("nod"), AccountID: fx.acc.ID, FolderID: fx.fld.ID, ParentID: many.ID,
				Name: name, NameKey: name, Kind: store.KindFile, Size: sn.Size, MimeType: sn.MimeType, BlobID: sn.BlobID,
				ETag: sn.ETag, CreatedAt: now, ModifiedAt: now, CreatedBy: sn.CreatedBy, ModifiedBy: sn.ModifiedBy}
			if err := tx.InsertNode(t.Context(), n); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	progress("set-up: %d nodes in one write, %s, peak RSS %.0f MiB", count, time.Since(start).Round(time.Millisecond), maxRSSMiB())
	z.zipOf(t, fmt.Sprintf("%d files", count))
}

func TestMeasureZipBigFile(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_ZIP") != "big" {
		t.Skip("SELFCLOUD_MEASURE_ZIP is not big")
	}
	gib, err := strconv.Atoi(os.Getenv("SELFCLOUD_MEASURE_GIB"))
	if err != nil || gib < 1 {
		t.Skip("SELFCLOUD_MEASURE_GIB must be a whole number of GiB")
	}
	z := measureServer(t, func(c *config.Config) {
		c.Zip.MaxBytes = config.ByteSize(int64(gib+1) << 30)
		c.Zip.MaxDisk = config.ByteSize(int64(gib+2) << 30)
	})
	fx := z.fx
	seed := putNode(t, fx.s, fx.key, fx.fld.ID, "/big.bin", "hello")
	sn, err := fx.s.store.Node(t.Context(), fx.acc.ID, seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(gib) << 30
	// The blob's bytes are zeros: the checksum is that of zeros of the new size.
	start := time.Now()
	h := sha256.New()
	if _, err := io.CopyN(h, zipZeros{}, size); err != nil {
		t.Fatal(err)
	}
	progress("checksum of %d GiB of zeros in %s", gib, time.Since(start).Round(time.Second))
	var path string
	_ = filepath.WalkDir(fx.s.cfg.DataDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == sn.BlobID {
			path = p
		}
		return nil
	})
	if path == "" {
		t.Fatal("blob file not found")
	}
	// Extend the blob to a sparse file of the new size, and record the new size and checksum in the database.
	// Cut the seed's bytes first: the file must be zeros throughout, as the checksum says.
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+fx.s.cfg.Store.Path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE blobs SET size = ?, sha256 = ? WHERE id = ?`, size, hex.EncodeToString(h.Sum(nil)), sn.BlobID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE nodes SET size = ? WHERE blob_id = ?`, size, sn.BlobID); err != nil {
		t.Fatal(err)
	}
	progress("sparse blob of %d GiB ready at %s", gib, path)
	z.zipOf(t, fmt.Sprintf("%d GiB sparse file", gib))
}

// TestZip64ThroughTheJob makes a zip of one file of SELFCLOUD_ZIP64_BYTES bytes (above 4 GiB) through the job's own path:
// the snapshot, writeZipEntry with its checksum, the publish, and the download. The file is a sparse blob of zeros, so no
// such size is stored; the zip is deflated, so it is small. It needs SELFCLOUD_MEASURE_DIR, and it is skipped without it.
func TestZip64ThroughTheJob(t *testing.T) {
	size, err := strconv.ParseInt(os.Getenv("SELFCLOUD_ZIP64_BYTES"), 10, 64)
	if err != nil || size <= 4<<30 {
		t.Skip("SELFCLOUD_ZIP64_BYTES must be a size above 4 GiB")
	}
	z := measureServer(t, func(c *config.Config) {
		c.Zip.MaxBytes = config.ByteSize(size + 1)
		c.Zip.MaxDisk = config.ByteSize(size + 2<<30)
		c.Zip.MaxDiskPerAccount = config.ByteSize(size + 2<<30)
	})
	fx := z.fx
	seed := putNode(t, fx.s, fx.key, fx.fld.ID, "/z64.bin", "hello")
	sn, err := fx.s.store.Node(t.Context(), fx.acc.ID, seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	h := sha256.New()
	if _, err := io.CopyN(h, zipZeros{}, size); err != nil {
		t.Fatal(err)
	}
	progress("checksum of %d bytes of zeros in %s", size, time.Since(start).Round(time.Second))
	var path string
	_ = filepath.WalkDir(fx.s.cfg.DataDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == sn.BlobID {
			path = p
		}
		return nil
	})
	if path == "" {
		t.Fatal("blob file not found")
	}
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+fx.s.cfg.Store.Path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE blobs SET size = ?, sha256 = ? WHERE id = ?`, size, hex.EncodeToString(h.Sum(nil)), sn.BlobID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE nodes SET size = ? WHERE blob_id = ?`, size, sn.BlobID); err != nil {
		t.Fatal(err)
	}
	job := createZip(t, fx, nil, seed.ID)
	jobStart := time.Now()
	if j := runZip(t, fx, job.ID); j.Status != "succeeded" {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	progress("zip64 through the job: JOB wall %s, peak RSS %.0f MiB", time.Since(jobStart).Round(time.Millisecond), maxRSSMiB())
	rec := getZip(t, fx, nil, job.Target, nil)
	wantStatus(t, rec, 200)
	zr, err := zip.NewReader(strings.NewReader(rec.Body.String()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || zr.File[0].UncompressedSize64 != uint64(size) {
		t.Fatalf("zip holds %d entries, first of %d bytes, want one of %d", len(zr.File), zr.File[0].UncompressedSize64, size)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, rc) // reads the whole entry, so its CRC-32 is checked
	rc.Close()
	if err != nil || n != size {
		t.Fatalf("read %d bytes of %d: %v", n, size, err)
	}
	progress("zip64 through the job: %d bytes read back and CRC-checked, zip %d bytes", n, rec.Body.Len())
}
