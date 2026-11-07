// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// fsckDataDir makes a data directory with an empty database and a configuration file that names it.
func fsckDataDir(t *testing.T) (dir, cfg string) {
	t.Helper()
	dir = t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "selfcloud.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	cfg = filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("data_dir: "+dir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, cfg
}

func TestFsckCleanExitsZero(t *testing.T) {
	_, cfg := fsckDataDir(t)
	code, out, errOut := runCmd(t, "fsck", "-config", cfg)
	if code != 0 {
		t.Fatalf("fsck on a clean store = %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

func TestFsckOrphanExitsOneAndJSONCountsIt(t *testing.T) {
	dir, cfg := fsckDataDir(t)
	bs, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	acc := id.New("acc")
	w, err := bs.Create(context.Background(), acc, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("nobody names me")); err != nil {
		t.Fatal(err)
	}
	c, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "blobs", acc, c.ID[4:6], c.ID[6:8], c.ID), old, old); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCmd(t, "fsck", "-config", cfg, "-json")
	if code != 1 {
		t.Fatalf("fsck with an orphan = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	var doc struct {
		Integrity string         `json:"integrity"`
		Counts    map[string]int `json:"counts"`
		Findings  []struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(bytes.TrimSpace([]byte(out)), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
	}
	if doc.Integrity != "ok" || doc.Counts["orphan_blob"] != 1 || len(doc.Findings) != 1 ||
		doc.Findings[0].Kind != "orphan_blob" || doc.Findings[0].ID != c.ID {
		t.Fatalf("JSON = %+v", doc)
	}
}

func TestFsckWithoutADatabaseExitsTwo(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("data_dir: "+dir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCmd(t, "fsck", "-config", cfg)
	if code != 2 {
		t.Fatalf("fsck with no database = %d, want 2", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "selfcloud.db")); err == nil {
		t.Fatal("fsck created a database")
	}
	if errOut == "" {
		t.Fatal("no error message")
	}
}
