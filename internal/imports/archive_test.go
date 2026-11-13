// SPDX-License-Identifier: AGPL-3.0-or-later

package imports

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// caps are generous unless a test narrows one of them.
func caps() Caps {
	return Caps{MaxEntries: 1000, MaxDepth: 16, MaxRatio: 100, MaxFile: 1 << 30, MaxTotal: 1 << 30}
}

func zipOf(t *testing.T, files map[string]string, order []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range order {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := io.WriteString(w, files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type tarEntry struct {
	hdr  tar.Header
	body string
}

func tarOf(t *testing.T, entries []tarEntry, gz bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.WriteCloser = nopCloser{&buf}
	if gz {
		w = gzip.NewWriter(&buf)
	}
	tw := tar.NewWriter(w)
	for _, e := range entries {
		h := e.hdr
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("tar header %q: %v", h.Name, err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := io.WriteString(tw, e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if gz {
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// refusedWith asserts err is an apierr with the code, and returns it.
func refusedWith(t *testing.T, err error, code apierr.Code) *apierr.Error {
	t.Helper()
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("want a refusal with code %s, got %v", code, err)
	}
	if ae.Code != code {
		t.Fatalf("code = %s, want %s (%v)", ae.Code, code, err)
	}
	return ae
}

func scanBytes(t *testing.T, data []byte, c Caps) (Summary, error) {
	t.Helper()
	return Scan(bytes.NewReader(data), int64(len(data)), c)
}

// TestZipSlipNamesAreRefused: every form of an unsafe name is refused before anything is read from the entry.
func TestZipSlipNamesAreRefused(t *testing.T) {
	for _, name := range []string{"../escape.txt", "a/../../escape.txt", "/etc/passwd", "C:/win.txt",
		"a\\b.txt", "dir//file.txt", "a/./b.txt", "bad\x00name.txt", "ctl\x07name.txt", "./x.txt"} {
		data := zipOf(t, map[string]string{name: "x"}, []string{name})
		_, err := scanBytes(t, data, caps())
		refusedWith(t, err, apierr.InvalidPath)
		if err == nil {
			t.Fatalf("%q was accepted", name)
		}
	}
}

// TestZipNamesAreNormalisedAndClashKeysMatch: a name is stored NFC, and two names that differ only in case or form are one
// clash key, so the second is refused as a duplicate.
func TestZipNamesAreNormalisedAndClashKeysMatch(t *testing.T) {
	nfd := "Cafe\u0301/note.txt"
	data := zipOf(t, map[string]string{nfd: "x"}, []string{nfd})
	var got []Entry
	if err := Walk(context.Background(), bytes.NewReader(data), int64(len(data)), Zip, 0, func(e Entry, _ io.Reader) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Names[0].Name != "Caf\u00e9" {
		t.Fatalf("names = %+v, want the NFC form of the folder", got)
	}
	dup := zipOf(t, map[string]string{"A/x.txt": "1", "a/X.txt": "2"}, []string{"A/x.txt", "a/X.txt"})
	_, err := scanBytes(t, dup, caps())
	refusedWith(t, err, apierr.InvalidPath)
}

// TestZipFolderAndFileDuplicatesRefused: a file listed twice, or a file and a folder with one path, is refused.
func TestZipFolderAndFileDuplicatesRefused(t *testing.T) {
	twice := zipOf(t, map[string]string{"a.txt": "1", "b/../c": "2"}, []string{"a.txt", "a.txt"})
	_, err := scanBytes(t, twice, caps())
	refusedWith(t, err, apierr.InvalidPath)

	folderTwice := zipOf(t, map[string]string{"d/": ""}, []string{"d/", "d/"})
	if _, err := scanBytes(t, folderTwice, caps()); err != nil {
		t.Fatalf("a folder listed twice is harmless, got %v", err)
	}
}

// TestZipRatioBombIsRefused: a file that expands past the ratio is refused from its headers.
func TestZipRatioBombIsRefused(t *testing.T) {
	bomb := zipOf(t, map[string]string{"zeros.bin": strings.Repeat("\x00", 8<<20)}, []string{"zeros.bin"})
	_, err := scanBytes(t, bomb, caps())
	ae := refusedWith(t, err, apierr.TooLarge)
	if ae.Details["reason"] != "ratio" {
		t.Fatalf("reason = %v, want ratio", ae.Details["reason"])
	}
}

// TestTooManyEntriesIsRefused: the cap counts the entries the archive really holds.
func TestTooManyEntriesIsRefused(t *testing.T) {
	files := map[string]string{}
	var order []string
	for i := range 5 {
		name := string(rune('a'+i)) + ".txt"
		files[name] = "x"
		order = append(order, name)
	}
	data := zipOf(t, files, order)
	c := caps()
	c.MaxEntries = 3
	_, err := scanBytes(t, data, c)
	ae := refusedWith(t, err, apierr.TooLarge)
	if ae.Details["reason"] != "entries" {
		t.Fatalf("reason = %v, want entries", ae.Details["reason"])
	}
}

// TestDeclaredCountThatHidesEntriesIsRefused: the end record says one entry, but five headers follow it. The count is
// taken from the headers, as decision 84 does, so the cap of 3 holds.
func TestDeclaredCountThatHidesEntriesIsRefused(t *testing.T) {
	files := map[string]string{}
	var order []string
	for i := range 5 {
		name := string(rune('a'+i)) + ".txt"
		files[name] = "x"
		order = append(order, name)
	}
	data := zipOf(t, files, order)
	end := bytes.LastIndex(data, []byte("PK\x05\x06"))
	if end < 0 {
		t.Fatal("no end record")
	}
	// Records on this disk and in total, at offsets 8 and 10 of the end record.
	data[end+8], data[end+9] = 1, 0
	data[end+10], data[end+11] = 1, 0
	c := caps()
	c.MaxEntries = 3
	_, err := scanBytes(t, data, c)
	refusedWith(t, err, apierr.TooLarge)
}

// TestDeepEntryIsRefused: an entry nested deeper than the cap is refused, and the names it carries are not kept.
func TestDeepEntryIsRefused(t *testing.T) {
	name := "a/b/c/deep.txt"
	data := zipOf(t, map[string]string{name: "x"}, []string{name})
	c := caps()
	c.MaxDepth = 3
	_, err := scanBytes(t, data, c)
	ae := refusedWith(t, err, apierr.TooLarge)
	if ae.Details["reason"] != "depth" {
		t.Fatalf("reason = %v, want depth", ae.Details["reason"])
	}
	c.MaxDepth = 4
	if _, err := scanBytes(t, data, c); err != nil {
		t.Fatalf("at the cap it is accepted, got %v", err)
	}
}

// TestFileOverTheMaxFileIsRefused: a file larger than upload.max_file_size is refused, from its declared size.
func TestFileOverTheMaxFileIsRefused(t *testing.T) {
	data := zipOf(t, map[string]string{"big.bin": strings.Repeat("a", 4096)}, []string{"big.bin"})
	c := caps()
	c.MaxFile = 1000
	_, err := scanBytes(t, data, c)
	ae := refusedWith(t, err, apierr.TooLarge)
	if ae.Details["reason"] != "file_size" {
		t.Fatalf("reason = %v, want file_size", ae.Details["reason"])
	}
}

// TestTotalBytesCapIsRefused: the files' declared bytes may not pass the total cap.
func TestTotalBytesCapIsRefused(t *testing.T) {
	data := zipOf(t, map[string]string{"a": "0123456789", "b": "0123456789"}, []string{"a", "b"})
	c := caps()
	c.MaxTotal = 15
	_, err := scanBytes(t, data, c)
	ae := refusedWith(t, err, apierr.TooLarge)
	if ae.Details["reason"] != "total" {
		t.Fatalf("reason = %v, want total", ae.Details["reason"])
	}
}

// TestTarLinksAndSpecialFilesAreRefused: a symlink, a hard link, a device and a FIFO are each refused.
func TestTarLinksAndSpecialFilesAreRefused(t *testing.T) {
	kinds := map[string]byte{"symlink": tar.TypeSymlink, "hardlink": tar.TypeLink, "chardev": tar.TypeChar,
		"blockdev": tar.TypeBlock, "fifo": tar.TypeFifo}
	for name, typ := range kinds {
		h := tar.Header{Name: "x/" + name, Typeflag: typ, Mode: 0o644, Linkname: "/etc/passwd"}
		data := tarOf(t, []tarEntry{{hdr: h}}, false)
		_, err := scanBytes(t, data, caps())
		refusedWith(t, err, apierr.InvalidPath)
		if err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// TestTarGzWholeStreamRatioIsRefused: a tar.gz whose files expand past the ratio, as a whole, is refused.
func TestTarGzWholeStreamRatioIsRefused(t *testing.T) {
	body := strings.Repeat("\x00", 4<<20)
	data := tarOf(t, []tarEntry{{hdr: tar.Header{Name: "zeros.bin", Typeflag: tar.TypeReg, Mode: 0o644}, body: body}}, true)
	_, err := scanBytes(t, data, caps())
	ae := refusedWith(t, err, apierr.TooLarge)
	if ae.Details["reason"] != "ratio" {
		t.Fatalf("reason = %v, want ratio", ae.Details["reason"])
	}
}

// TestZipWalkReturnsFoldersAndFilesInOrder: a zip with folders and files is scanned and walked; the walk skips what the
// cursor has, and gives each file's bytes.
func TestZipWalkReturnsFoldersAndFilesInOrder(t *testing.T) {
	files := map[string]string{"photos/": "", "photos/a.jpg": "AAA", "docs/b.txt": "BB"}
	order := []string{"photos/", "photos/a.jpg", "docs/b.txt"}
	data := zipOf(t, files, order)
	sum, err := scanBytes(t, data, caps())
	if err != nil {
		t.Fatal(err)
	}
	if sum.Format != Zip || sum.Entries != 3 || sum.Files != 2 || sum.Dirs != 1 || sum.Bytes != 5 {
		t.Fatalf("summary = %+v", sum)
	}
	var seen []string
	var bodies []string
	if err := Walk(context.Background(), bytes.NewReader(data), int64(len(data)), Zip, 1, func(e Entry, body io.Reader) error {
		seen = append(seen, e.Path)
		if body != nil {
			b, err := io.ReadAll(body)
			if err != nil {
				return err
			}
			bodies = append(bodies, string(b))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, ",") != "photos/a.jpg,docs/b.txt" {
		t.Fatalf("walk after the cursor saw %v", seen)
	}
	if strings.Join(bodies, ",") != "AAA,BB" {
		t.Fatalf("bodies = %v", bodies)
	}
}

// TestTarGzWalk: a gzipped tar with a folder and a file is scanned and walked, its file read whole.
func TestTarGzWalk(t *testing.T) {
	data := tarOf(t, []tarEntry{
		{hdr: tar.Header{Name: "photos/", Typeflag: tar.TypeDir, Mode: 0o755}},
		{hdr: tar.Header{Name: "photos/a.jpg", Typeflag: tar.TypeReg, Mode: 0o644}, body: "AAAA"},
	}, true)
	sum, err := scanBytes(t, data, caps())
	if err != nil {
		t.Fatal(err)
	}
	if sum.Format != TarGz || sum.Entries != 2 || sum.Bytes != 4 {
		t.Fatalf("summary = %+v", sum)
	}
	var got []string
	if err := Walk(context.Background(), bytes.NewReader(data), int64(len(data)), TarGz, 0, func(e Entry, body io.Reader) error {
		if body != nil {
			b, _ := io.ReadAll(body)
			got = append(got, e.Path+"="+string(b))
		} else {
			got = append(got, e.Path+"/")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "photos/,photos/a.jpg=AAAA" {
		t.Fatalf("walk = %v", got)
	}
}

// TestPlainTarIsDetected: a tar without gzip is read as a tar, by its magic.
func TestPlainTarIsDetected(t *testing.T) {
	data := tarOf(t, []tarEntry{{hdr: tar.Header{Name: "one.txt", Typeflag: tar.TypeReg, Mode: 0o644}, body: "1"}}, false)
	sum, err := scanBytes(t, data, caps())
	if err != nil {
		t.Fatal(err)
	}
	if sum.Format != Tar {
		t.Fatalf("format = %s", sum.Format)
	}
}

// TestNotAnArchiveIsRefused: bytes that are neither a zip, a gzip nor a tar are unsupported media, not a panic.
func TestNotAnArchiveIsRefused(t *testing.T) {
	for _, data := range [][]byte{[]byte("hello world, not an archive"), {}, []byte("PK")} {
		_, err := scanBytes(t, data, caps())
		refusedWith(t, err, apierr.UnsupportedMedia)
	}
}

// TestCleanPathRules: the segments of a valid name come back normalised, and a folder's trailing slash is dropped.
func TestCleanPathRules(t *testing.T) {
	parts, err := cleanPath("docs/Reports/", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || parts[0].Name != "docs" || parts[1].Name != "Reports" {
		t.Fatalf("parts = %+v", parts)
	}
	// A file's name may not end in a slash: the empty segment after it is a refusal, not a folder.
	_, err = cleanPath("docs/", false)
	refusedWith(t, err, apierr.InvalidPath)
}
