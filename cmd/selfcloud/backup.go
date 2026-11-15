// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/store"
)

// backupClock names the snapshot with the time it starts. Tests fix it to make the name predictable.
var backupClock = time.Now

// backupCommand snapshots the database (--to DIR) or verifies a snapshot (--check FILE). The snapshot holds the
// database only; blobs/ and keys/ are copied with other tools after it (design section 12). Exit 0 is good, 1 means
// the copy failed its check or the checked file has problems, 2 means the command could not run.
func backupCommand(args []string, stdout, stderr io.Writer) int {
	var (
		to, check string
		asJSON    bool
	)
	cfg, _, code := loadConfig("backup", args, stderr, 0, func(fs *flag.FlagSet) {
		fs.StringVar(&to, "to", "", "write an online snapshot of the database into this `directory` (the database only; copy blobs/ and keys/ with other tools after the snapshot)")
		fs.StringVar(&check, "check", "", "verify an existing snapshot `file` (read-only; writes nothing beside it)")
		fs.BoolVar(&asJSON, "json", false, "print one JSON document instead of text")
	})
	if cfg == nil {
		return code
	}
	switch {
	case to != "" && check != "":
		fmt.Fprintln(stderr, "selfcloud backup: --to and --check cannot be combined")
		return 2
	case to == "" && check == "":
		fmt.Fprintln(stderr, "selfcloud backup: one of --to DIR or --check FILE is required")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if check != "" {
		return backupCheck(ctx, check, asJSON, stdout, stderr)
	}
	return backupTo(ctx, cfg, to, asJSON, stdout, stderr)
}

// backupReport is the one document --json prints, and the fields the text summary names.
type backupReport struct {
	Path          string   `json:"path"`
	Bytes         int64    `json:"bytes"`
	SHA256        string   `json:"sha256"`
	SchemaVersion int      `json:"schema_version"`
	DurationMS    int64    `json:"duration_ms"`
	Integrity     string   `json:"integrity"`
	Problems      []string `json:"problems,omitempty"`
}

func backupCheck(ctx context.Context, path string, asJSON bool, stdout, stderr io.Writer) int {
	start := time.Now()
	rep, err := store.CheckSnapshot(ctx, path)
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud backup:", err)
		return 2
	}
	size, sum, err := backupHashFile(path)
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud backup:", err)
		return 2
	}
	out := backupReport{Path: path, Bytes: size, SHA256: sum, SchemaVersion: rep.SchemaVersion,
		DurationMS: time.Since(start).Milliseconds(), Integrity: "ok", Problems: rep.Problems}
	if len(rep.Problems) > 0 {
		out.Integrity = "failed"
	}
	if werr := writeBackupReport(out, asJSON, stdout); werr != nil {
		fmt.Fprintln(stderr, "selfcloud backup:", werr)
		return 2
	}
	if len(rep.Problems) > 0 {
		return 1
	}
	return 0
}

func backupTo(ctx context.Context, cfg *config.Config, dir string, asJSON bool, stdout, stderr io.Writer) int {
	fail := func(code int, format string, args ...any) int {
		fmt.Fprintf(stderr, "selfcloud backup: "+format+"\n", args...)
		return code
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fail(2, "%v", err)
	}
	if !fi.IsDir() {
		return fail(2, "%s is not a directory", dir)
	}
	if backupInsideBlobs(cfg.DataDir, dir) {
		return fail(2, "%s is inside the data directory's blobs/; choose a directory outside it", dir)
	}
	stamp := backupClock().UTC().Format("20060102T150405") + "Z"
	final := filepath.Join(dir, "selfcloud-"+stamp+".db")
	tmp := filepath.Join(dir, ".selfcloud-"+stamp+".db.tmp")
	if _, err := os.Lstat(final); err == nil {
		return fail(2, "%s already exists; refusing to overwrite it", final)
	}
	if _, err := os.Lstat(tmp); err == nil {
		return fail(2, "%s already exists", tmp)
	}
	start := time.Now()
	// SQLite creates the snapshot with the process umask; 0077 makes it owner-only from the first byte.
	old := syscall.Umask(0o077)
	err = store.SnapshotTo(ctx, cfg.Store.Path, tmp, 0)
	syscall.Umask(old)
	if err != nil {
		os.Remove(tmp) // VACUUM INTO may leave a partial file behind
		return fail(2, "%v", err)
	}
	rep, err := store.CheckSnapshot(ctx, tmp)
	if err != nil {
		os.Remove(tmp)
		return fail(2, "%v", err)
	}
	if len(rep.Problems) > 0 {
		os.Remove(tmp)
		for _, p := range rep.Problems {
			fmt.Fprintln(stderr, "selfcloud backup:", p)
		}
		return 1
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return fail(2, "%v", err)
	}
	size, sum, err := backupHashFile(tmp)
	if err != nil {
		os.Remove(tmp)
		return fail(2, "%v", err)
	}
	if err := backupFsync(tmp); err != nil {
		os.Remove(tmp)
		return fail(2, "%v", err)
	}
	// Publish under the final name only now that the copy is whole, checked and synced. A link refuses a name that
	// appeared meanwhile, so an existing file is never overwritten; the temporary name then goes.
	if err := os.Link(tmp, final); err != nil {
		os.Remove(tmp)
		if errors.Is(err, os.ErrExist) {
			return fail(2, "%s already exists; refusing to overwrite it", final)
		}
		return fail(2, "%v", err)
	}
	if err := os.Remove(tmp); err != nil {
		return fail(2, "%v", err)
	}
	if err := backupFsync(dir); err != nil {
		return fail(2, "%v", err)
	}
	out := backupReport{Path: final, Bytes: size, SHA256: sum, SchemaVersion: rep.SchemaVersion,
		DurationMS: time.Since(start).Milliseconds(), Integrity: "ok"}
	if werr := writeBackupReport(out, asJSON, stdout); werr != nil {
		return fail(2, "%v", werr)
	}
	return 0
}

// backupInsideBlobs reports whether dir is the data directory's blobs/ or sits inside it, following symlinks.
func backupInsideBlobs(dataDir, dir string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absBlobs, err := filepath.Abs(filepath.Join(dataDir, "blobs"))
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(absDir); err == nil {
		absDir = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absBlobs); err == nil {
		absBlobs = resolved
	}
	return absDir == absBlobs || strings.HasPrefix(absDir, absBlobs+string(os.PathSeparator))
}

// backupHashFile returns the file's size and the SHA-256 of its bytes.
func backupHashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// backupFsync syncs a file or a directory, so the bytes (or the rename) survive a power cut.
func backupFsync(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func writeBackupReport(rep backupReport, asJSON bool, stdout io.Writer) error {
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	fmt.Fprintf(stdout, "snapshot: %s\nintegrity: %s\nbytes: %d\nsha256: %s\nschema version: %d\nduration: %s\n",
		rep.Path, rep.Integrity, rep.Bytes, rep.SHA256, rep.SchemaVersion,
		time.Duration(rep.DurationMS)*time.Millisecond)
	for _, p := range rep.Problems {
		fmt.Fprintln(stdout, "problem:", p)
	}
	return nil
}
