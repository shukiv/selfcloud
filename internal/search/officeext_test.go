// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// sec1 (decision 84): a zip with a very large number of entries is not read entry by entry into a map. Before,
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

// sec1 (decision 84): the entry cap is read from the central directory's record before the container is
// parsed. zip.NewReader builds an entry for each record (about 200 bytes an entry, so 4 MB for these 20,000), and the cap
// of 10,000 must refuse the zip before that cost. The heap allocated while officeExt refuses it stays under 1 MiB. The
// test is not parallel: it reads the process's allocation counter.
func TestOfficeExtRefusesOverTheCapBeforeBuildingTheEntries(t *testing.T) {
	names := make([]string, 0, 20000)
	for i := 0; i < 20000; i++ {
		names = append(names, fmt.Sprintf("junk/%d.bin", i))
	}
	f, size := zipWith(t, names, "")
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got := officeExt(f, size)
	runtime.ReadMemStats(&after)
	if got != "" {
		t.Errorf("officeExt over the cap = %q, want \"\"", got)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 1<<20 {
		t.Errorf("officeExt allocated %d bytes for a refused zip of 20,000 entries; the cap should refuse it before the entries are built", alloc)
	}
}

// sec1 (decision 84): the entry count is read for a zip64 container too, so a zip64 zip over the cap is
// refused by the same count, and a file that is not a zip has none.
func TestZipEntryCountReadsZip64AndRejectsOthers(t *testing.T) {
	f, size := zipWith(t, zip64Names(70000), "")
	n, ok := zipEntryCount(f, size, 1<<20)
	if !ok || n != 70000 {
		t.Errorf("zipEntryCount of a zip64 zip = %d, %v; want 70000, true", n, ok)
	}
	path := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(path, []byte("not a zip at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, ok := zipEntryCount(p, 16, 10); ok {
		t.Errorf("zipEntryCount of a file that is not a zip reported a count")
	}
}

// sec1 (decision 84): a zip whose end record declares 100 entries, but whose
// central directory really holds 65,636 (100 + 65,536), passed the declared count and then made zip.NewReader build all
// 65,636, since archive/zip reads headers until one fails and checks only the count modulo 65,536. The walk counts the
// real headers before zip.NewReader, so this zip is refused, and the heap allocated while it is refused stays under 1 MiB.
// Mutation: walking to the declared count instead passes this zip and fails the allocation check.
func TestOfficeExtRefusesADeclaredCountThatHidesRealHeaders(t *testing.T) {
	names := zip64Names(65636)
	f, size := zipWith(t, names, "")
	// The end record is the last 22 bytes (no comment). Its 16-bit entry count sits at offset 10: declare 100.
	path := f.Name()
	data := make([]byte, size)
	if _, err := f.ReadAt(data, 0); err != nil {
		t.Fatal(err)
	}
	eoc := len(data) - eocdLen
	if binary.LittleEndian.Uint32(data[eoc:eoc+4]) != eocdSig {
		t.Fatal("the zip does not end in an end record")
	}
	// Go's writer marks the directory's size and offset as 0xFFFFFFFF for a zip this big, which sends the reader to the
	// zip64 record. Copy the real size and offset into the end record, so the declared count is the 16-bit one.
	zip64At := binary.LittleEndian.Uint64(data[eoc-20+8 : eoc-20+16])
	if binary.LittleEndian.Uint32(data[zip64At:zip64At+4]) != eocd64Sig {
		t.Fatal("no zip64 end record")
	}
	realSize := binary.LittleEndian.Uint64(data[zip64At+40 : zip64At+48])
	realOffset := binary.LittleEndian.Uint64(data[zip64At+48 : zip64At+56])
	binary.LittleEndian.PutUint16(data[eoc+10:eoc+12], 100)
	binary.LittleEndian.PutUint16(data[eoc+8:eoc+10], 100)
	binary.LittleEndian.PutUint32(data[eoc+12:eoc+16], uint32(realSize))
	binary.LittleEndian.PutUint32(data[eoc+16:eoc+20], uint32(realOffset))
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got := officeExt(f, size)
	runtime.ReadMemStats(&after)
	if got != "" {
		t.Errorf("officeExt over a hidden count = %q, want \"\"", got)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 1<<20 {
		t.Errorf("officeExt allocated %d bytes for a zip whose real headers are 65,636 and declared 100", alloc)
	}
}

// sec1 (decision 84): a zip64 zip over the cap is refused before its entries are built, as the
// zip with a 16-bit count is.
func TestOfficeExtRefusesAZip64OverTheCapBeforeBuildingTheEntries(t *testing.T) {
	f, size := zipWith(t, zip64Names(70000), "")
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got := officeExt(f, size)
	runtime.ReadMemStats(&after)
	if got != "" {
		t.Errorf("officeExt over the zip64 cap = %q, want \"\"", got)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 1<<20 {
		t.Errorf("officeExt allocated %d bytes for a refused zip64 of 70,000 entries", alloc)
	}
}

// zipData is a zip of names, in memory.
func zipData(t *testing.T, names []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		if _, err := zw.Create(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// openBytes writes data to a file and opens it.
func openBytes(t *testing.T, data []byte) (*os.File, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.zip")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f, int64(len(data))
}

// (decision 84): a zip whose end record declares more entries than the cap, with two real
// headers, is refused before its entries are read. The declaration is a zip64 count of 65,538, which archive/zip accepts
// for two headers (the count is compared modulo 65,536). Mutation: without the declared-count check the zip is read and
// decided as a document.
func TestOfficeExtRefusesADeclaredCountOverTheCap(t *testing.T) {
	data := zipData(t, []string{"junk.txt", "word/document.xml"})
	eoc := len(data) - eocdLen
	dirSize := binary.LittleEndian.Uint32(data[eoc+12 : eoc+16])
	dirOff := binary.LittleEndian.Uint32(data[eoc+16 : eoc+20])
	// The zip64 record and its locator go where the end record is; the end record follows them, with a 16-bit count of
	// 0xFFFF so the zip64 count is used.
	z64 := make([]byte, eocd64Len)
	binary.LittleEndian.PutUint32(z64[0:4], eocd64Sig)
	binary.LittleEndian.PutUint64(z64[4:12], eocd64Len-12)
	binary.LittleEndian.PutUint16(z64[12:14], 45)
	binary.LittleEndian.PutUint16(z64[14:16], 45)
	binary.LittleEndian.PutUint64(z64[24:32], 2)
	binary.LittleEndian.PutUint64(z64[32:40], 65538)
	binary.LittleEndian.PutUint64(z64[40:48], uint64(dirSize))
	binary.LittleEndian.PutUint64(z64[48:56], uint64(dirOff))
	loc := make([]byte, eocd64LocLen)
	binary.LittleEndian.PutUint32(loc[0:4], eocd64LocSig)
	binary.LittleEndian.PutUint64(loc[8:16], uint64(eoc))
	binary.LittleEndian.PutUint32(loc[16:20], 1)
	end := append([]byte{}, data[eoc:]...)
	binary.LittleEndian.PutUint16(end[10:12], 0xffff)
	binary.LittleEndian.PutUint16(end[8:10], 0xffff)
	out := bytes.Join([][]byte{data[:eoc], z64, loc, end}, nil)
	f, size := openBytes(t, out)
	if got := officeExt(f, size); got != "" {
		t.Errorf("officeExt with a declared count over the cap = %q, want \"\" (refused)", got)
	}
}

// (decision 84): a zip with data in front of it (base above zero) and more real headers than
// the cap is refused, whichever directory start archive/zip takes. Mutation: walking only from dirOff, where this zip's
// headers are not, lets the zip through to zip.NewReader, which builds all 10,001 entries.
func TestOfficeExtRefusesAPrependedZipOverTheCap(t *testing.T) {
	// 65,541 real headers, declared as 5: archive/zip accepts the count, which it compares modulo 65,536. The declared
	// count is under the cap, so the walk decides. The real size and offset go into the end record, so the 16-bit count
	// is the one used.
	names := make([]string, 0, 65541)
	for i := 0; i < 65541; i++ {
		names = append(names, fmt.Sprintf("j%d", i))
	}
	z := zipData(t, names)
	eoc := len(z) - eocdLen
	// Go's writer put a zip64 record and locator between the directory and the end record. Remove them, so the end
	// record follows the directory directly, as a 16-bit zip has it.
	zip64At := int(binary.LittleEndian.Uint64(z[eoc-eocd64LocLen+8 : eoc-eocd64LocLen+16]))
	realSize := binary.LittleEndian.Uint64(z[zip64At+40 : zip64At+48])
	realOffset := binary.LittleEndian.Uint64(z[zip64At+48 : zip64At+56])
	end := append([]byte{}, z[eoc:]...)
	binary.LittleEndian.PutUint32(end[12:16], uint32(realSize))
	binary.LittleEndian.PutUint32(end[16:20], uint32(realOffset))
	binary.LittleEndian.PutUint16(end[10:12], 5)
	binary.LittleEndian.PutUint16(end[8:10], 5)
	d := bytes.Join([][]byte{z[:zip64At], end}, nil)
	data := append(make([]byte, 1000), d...)
	f, size := openBytes(t, data)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got := officeExt(f, size)
	runtime.ReadMemStats(&after)
	if got != "" {
		t.Errorf("officeExt over the cap in a prepended zip = %q, want \"\"", got)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 1<<20 {
		t.Errorf("officeExt allocated %d bytes for a prepended zip of 65,541 real headers declared as 5", alloc)
	}
}

// (decision 84): a normal office file with data in front of it is still decided.
func TestOfficeExtDecidesAPrependedOfficeFile(t *testing.T) {
	data := append(make([]byte, 1000), zipData(t, []string{"word/document.xml"})...)
	f, size := openBytes(t, data)
	if got := officeExt(f, size); got != "docx" {
		t.Errorf("officeExt of a prepended docx = %q, want docx", got)
	}
}

// zip64Names is n entry names. A zip with more than 65,534 entries is written with a zip64 directory end.
func zip64Names(n int) []string {
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		names = append(names, fmt.Sprintf("e%d", i))
	}
	return names
}

// sec1 (decision 84): a zip under the cap is still decided, with the entry the document type is read from
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
