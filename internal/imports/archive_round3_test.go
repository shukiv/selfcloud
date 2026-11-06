// SPDX-License-Identifier: AGPL-3.0-or-later

package imports

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// randomText is n bytes that do not compress, so the stream's ratio stays near one.
func randomText(n int) string {
	b := make([]byte, n)
	rand.New(rand.NewSource(1)).Read(b)
	return string(b)
}

// A tar entry of a normal size is not a ratio bomb: a tar entry has no compressed size of its own (F12).
func TestTarEntryOfOneMebibyteImports(t *testing.T) {
	data := tarOf(t, []tarEntry{{hdr: tar.Header{Name: "big.bin", Typeflag: tar.TypeReg, Mode: 0o644,
		Size: 1 << 20}, body: randomText(1 << 20)}}, false)
	if _, err := scanBytes(t, data, caps()); err != nil {
		t.Fatalf("a 1 MiB tar entry was refused: %v", err)
	}
}

// A 50 MiB entry of a tar.gz imports too: the ratio is measured on the stream, which here does not expand.
func TestTarGzEntryOfFiftyMebibytesImports(t *testing.T) {
	data := tarOf(t, []tarEntry{{hdr: tar.Header{Name: "big.bin", Typeflag: tar.TypeReg, Mode: 0o644,
		Size: 50 << 20}, body: randomText(50 << 20)}}, true)
	c := caps()
	c.MaxFile = 1 << 30
	if _, err := scanBytes(t, data, c); err != nil {
		t.Fatalf("a 50 MiB tar.gz entry was refused: %v", err)
	}
}

// Entries that are each under 100 KiB are still refused when the stream they make expands past the ratio: the stream
// check fires, not the per-entry check (fix 6 re-proved without F12's path, F12).
func TestTarGzSmallEntriesStillHitTheStreamRatio(t *testing.T) {
	var entries []tarEntry
	for i := 0; i < 300; i++ {
		entries = append(entries, tarEntry{hdr: tar.Header{Name: fmt.Sprintf("e%03d.bin", i), Typeflag: tar.TypeReg,
			Mode: 0o644, Size: 50 << 10}, body: strings.Repeat("0", 50<<10)})
	}
	data := tarOf(t, entries, true)
	_, err := scanBytes(t, data, caps())
	ae := refusedWith(t, err, apierr.TooLarge)
	if ae.Details["reason"] != "ratio" {
		t.Fatalf("reason = %v, want ratio", ae.Details["reason"])
	}
}

// A file and an entry under a folder of the same name is refused at scan, in either order (F7).
func TestFileAndItsSubfolderRefusedAtScan(t *testing.T) {
	for _, order := range [][]string{{"a", "a/b"}, {"a/b", "a"}} {
		data := zipOf(t, map[string]string{"a": "x", "a/b": "y"}, order)
		_, err := scanBytes(t, data, caps())
		ae := refusedWith(t, err, apierr.InvalidPath)
		if ae.Details["reason"] != "duplicate" {
			t.Fatalf("order %v: reason = %v, want duplicate", order, ae.Details["reason"])
		}
	}
}

// A zip whose central directory is larger than its entry cap can hold is refused before archive/zip reads it: the
// directory's bytes, not only its count, are bounded (F18). Seventy headers of 60 KB extra data are about 4.2 MB, over
// 1000 entries times 4 KiB.
func TestZipCentralDirectoryOverTheBoundIsRefused(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := 0; i < 70; i++ {
		fh := &zip.FileHeader{Name: fmt.Sprintf("f%02d.txt", i), Method: zip.Store, Extra: make([]byte, 60000)}
		if _, err := w.CreateHeader(fh); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := scanBytes(t, buf.Bytes(), caps()); err == nil {
		t.Fatal("a zip whose central directory is over the bound was accepted")
	}
}
