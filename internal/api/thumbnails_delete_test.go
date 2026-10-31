// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// derivedFilesOf counts the derived files an account has under the derived directory.
func derivedFilesOf(t *testing.T, s *Server, accountID string) int {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(s.derivedPool.Cache().Files().Root(), accountID))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}

// TestAccountDeleteRemovesItsThumbnails: the account-delete job takes the account's thumbnails off disk, and leaves
// another account's alone.
func TestAccountDeleteRemovesItsThumbnails(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	other, otherKey := newAccount(t, s, admin, "Other")
	f := newFolder(t, s, key, acc.ID, "Pictures")
	g := newFolder(t, s, otherKey, other.ID, "Pictures")
	pic := putImage(t, s, key, f.ID, "/a.png", "image/png", pngBytes(t, 64, 64))
	opic := putImage(t, s, otherKey, g.ID, "/b.png", "image/png", pngBytes(t, 64, 48))
	wantStatus(t, waitThumb(t, s, key, nil, pic.ID, 0), http.StatusOK)
	wantStatus(t, waitThumb(t, s, otherKey, nil, opic.ID, 0), http.StatusOK)
	if n := derivedFilesOf(t, s, acc.ID); n != 1 {
		t.Fatalf("thumbnails before the delete = %d, want 1", n)
	}
	if err := s.runAccountDelete(context.Background(), &store.Job{ID: "job_test", Target: acc.ID}); err != nil {
		t.Fatal(err)
	}
	if n := derivedFilesOf(t, s, acc.ID); n != 0 {
		t.Errorf("thumbnails after the account delete = %d, want 0", n)
	}
	if n := derivedFilesOf(t, s, other.ID); n != 1 {
		t.Errorf("another account's thumbnails = %d, want 1", n)
	}
}
