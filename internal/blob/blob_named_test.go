// SPDX-License-Identifier: AGPL-3.0-or-later

package blob

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBlobCreateNamedPartial: CreateNamed makes the partial under the name given, so a caller can find and remove it
// after a restart. Commit gives the blob an ID as Create does, and the partial's name is gone once it commits.
func TestBlobCreateNamedPartial(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(dir, "uploads", "acc_a", "bnd-bnd_1.part")
	w, err := d.CreateNamed(ctx, "acc_a", "bnd-bnd_1.part", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partial); err != nil {
		t.Fatalf("the named partial is not at its name: %v", err)
	}
	if _, err := io.WriteString(w, "hello"); err != nil {
		t.Fatal(err)
	}
	// A second create under the same name is refused: the name is exclusive.
	if _, err := d.CreateNamed(ctx, "acc_a", "bnd-bnd_1.part", 1<<20); err == nil {
		t.Error("a second CreateNamed under the same name was accepted")
	}
	c, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.ID, "blb_") || c.Size != 5 {
		t.Errorf("commit = %+v, want a blb_ ID and size 5", c)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Errorf("the partial remains after commit: %v", err)
	}
	f, err := d.Open("acc_a", c.ID)
	if err != nil {
		t.Fatalf("the committed blob cannot be opened: %v", err)
	}
	f.Close()
}

// TestBlobCreateNamedAbortRemovesIt: Abort removes the named partial, as it removes a random one.
func TestBlobCreateNamedAbortRemovesIt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	w, err := d.CreateNamed(ctx, "acc_a", "bnd-bnd_2.part", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "uploads", "acc_a", "bnd-bnd_2.part")); !os.IsNotExist(err) {
		t.Errorf("Abort left the named partial: %v", err)
	}
}

// TestBlobCreateNamedRefusesBadNames: a name is a file name in uploads/<account>, so a path, a dot file or an empty
// name is refused before any file is made.
func TestBlobCreateNamedRefusesBadNames(t *testing.T) {
	ctx := context.Background()
	d, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// tmp- is the prefix RemoveStaleUploads sweeps, so a named partial must never take it (review L1).
	for _, name := range []string{"", "../x.part", "a/b.part", ".hidden", "with space.part", strings.Repeat("a", 129), "tmp-bnd.part"} {
		if w, err := d.CreateNamed(ctx, "acc_a", name, 1<<20); err == nil {
			w.Abort()
			t.Errorf("CreateNamed(%q) was accepted", name)
		}
	}
}
