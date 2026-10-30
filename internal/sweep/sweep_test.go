// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
	_ "modernc.org/sqlite"
)

// TestSweepWarnsOnReservationDriftAndKeepsNumbers: a sweep that finds the reservation counter out of
// step with the uploads says so, and leaves the number alone for `selfcloud admin recount-usage`.
func TestSweepWarnsOnReservationDriftAndKeepsNumbers(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "selfcloud.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	blobs, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	acc := &store.Account{ID: id.New("acc"), Name: "A", Status: store.AccountActive, CreatedAt: store.Now(), UpdatedAt: store.Now()}
	if err := st.Write(ctx, func(tx *store.Tx) error { return tx.InsertAccount(ctx, acc) }); err != nil {
		t.Fatal(err)
	}
	// A reservation with no upload behind it: the drift the check must notice.
	if err := st.Write(ctx, func(tx *store.Tx) error {
		fo := &store.Folder{ID: id.New("fld"), AccountID: acc.ID, Name: "Docs", NameKey: "docs",
			CreatedAt: store.Now(), UpdatedAt: store.Now()}
		if err := tx.InsertFolder(ctx, fo, "usr_test"); err != nil {
			return err
		}
		return tx.AdjustUsage(ctx, acc.ID, fo.ID, store.UsageChange{Reserved: 7})
	}); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	sw := New(Options{Store: st, Blobs: blobs, DeleteGrace: time.Hour, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if _, err := sw.Once(ctx, store.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "recount-usage") {
		t.Errorf("sweep did not warn about the drift; log:\n%s", logs.String())
	}
	a, err := st.Account(ctx, acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.ReservedBytes != 7 {
		t.Errorf("sweep changed the reserved counter to %d; it must only report", a.ReservedBytes)
	}
}

// TestSweepWarnsWhenAccountDisagreesWithItsFolders: an account whose counters differ from the sum of its folders'
// is reported by the sweep, and the sweep leaves the numbers as they are (repair is `admin recount-usage`).
func TestSweepWarnsWhenAccountDisagreesWithItsFolders(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "selfcloud.db")
	st, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	blobs, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	acc := &store.Account{ID: id.New("acc"), Name: "A", Status: store.AccountActive, CreatedAt: store.Now(), UpdatedAt: store.Now()}
	if err := st.Write(ctx, func(tx *store.Tx) error { return tx.InsertAccount(ctx, acc) }); err != nil {
		t.Fatal(err)
	}
	if err := st.Write(ctx, func(tx *store.Tx) error {
		fo := &store.Folder{ID: id.New("fld"), AccountID: acc.ID, Name: "Docs", NameKey: "docs",
			CreatedAt: store.Now(), UpdatedAt: store.Now()}
		return tx.InsertFolder(ctx, fo, "usr_test")
	}); err != nil {
		t.Fatal(err)
	}
	// The account's byte counter moves without its folders: the drift the check must notice.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE accounts SET used_bytes = used_bytes + 5 WHERE id = ?", acc.ID); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	sw := New(Options{Store: st, Blobs: blobs, DeleteGrace: time.Hour, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if _, err := sw.Once(ctx, store.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "disagree with their folders") {
		t.Errorf("sweep did not report the account against its folders; log:\n%s", logs.String())
	}
	a, err := st.Account(ctx, acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.UsedBytes != 5 {
		t.Errorf("sweep changed used_bytes to %d; it must only report", a.UsedBytes)
	}
}
