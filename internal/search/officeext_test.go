// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// zipWith writes a zip with the given entry names, each empty except for mimetype's content, and returns it opened.
func zipWith(t *testing.T, names []string, mimetype string) (*os.File, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.zip")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if n == "mimetype" {
			if _, err := w.Write([]byte(mimetype)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return f, info.Size()
}

// sec1 item 6 (decision 84): a zip with a very large number of entries is not read entry by entry into a map. Before,
// every entry was kept, so a zip of many small entries cost memory in proportion to its entries. A container over the
// entry cap is not decided, and the document is not extracted as an office file.
func TestOfficeExtRefusesAZipOverTheEntryCap(t *testing.T) {
	t.Parallel()
	names := make([]string, 0, 12001)
	for i := 0; i < 12000; i++ {
		names = append(names, fmt.Sprintf("junk/%d.bin", i))
	}
	names = append(names, "word/document.xml")
	f, size := zipWith(t, names, "")
	if got := officeExt(f, size); got != "" {
		t.Errorf("officeExt over the cap = %q, want \"\" (not decided)", got)
	}
}

// sec1 item 6 (decision 84): a zip under the cap is still decided, with the entry the document type is read from
// (the last entry of the container is found, as before).
func TestOfficeExtDecidesAZipUnderTheEntryCap(t *testing.T) {
	t.Parallel()
	names := make([]string, 0, 9001)
	for i := 0; i < 9000; i++ {
		names = append(names, fmt.Sprintf("junk/%d.bin", i))
	}
	names = append(names, "word/document.xml")
	f, size := zipWith(t, names, "")
	if got := officeExt(f, size); got != "docx" {
		t.Errorf("officeExt = %q, want docx", got)
	}

	f2, size2 := zipWith(t, []string{"content.xml", "mimetype"}, "application/vnd.oasis.opendocument.text")
	if got := officeExt(f2, size2); got != "odt" {
		t.Errorf("mimetype officeExt = %q, want odt", got)
	}
}
