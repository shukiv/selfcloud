package sweep

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"selfcloud/internal/blob"
	"selfcloud/internal/id"
	"selfcloud/internal/store"
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
		return tx.AdjustUsage(ctx, acc.ID, store.UsageChange{Reserved: 7})
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
