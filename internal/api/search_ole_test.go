// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/binary"
	"testing"
	"time"
	"unicode/utf16"
)

// oleFile builds a minimal OLE2 compound file (512-byte sectors) with one stream per name: the header, one FAT sector and
// one directory sector. It is the layout the content worker reads, not a file any Office program wrote.
func oleFile(names ...string) []byte {
	const ss = 512
	out := make([]byte, ss*3)
	hdr := out[:ss]
	copy(hdr, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	binary.LittleEndian.PutUint16(hdr[26:], 3)
	binary.LittleEndian.PutUint16(hdr[28:], 0xFFFE)
	binary.LittleEndian.PutUint16(hdr[30:], 9)
	binary.LittleEndian.PutUint32(hdr[44:], 1)
	binary.LittleEndian.PutUint32(hdr[48:], 1)
	binary.LittleEndian.PutUint32(hdr[56:], 4096)
	binary.LittleEndian.PutUint32(hdr[60:], 0xFFFFFFFE)
	binary.LittleEndian.PutUint32(hdr[68:], 0xFFFFFFFE)
	for i := range 109 {
		v := uint32(0xFFFFFFFF)
		if i == 0 {
			v = 0
		}
		binary.LittleEndian.PutUint32(hdr[76+4*i:], v)
	}
	fat := out[ss : 2*ss]
	for i := 0; i < ss/4; i++ {
		v := uint32(0xFFFFFFFF)
		switch i {
		case 0:
			v = 0xFFFFFFFD
		case 1:
			v = 0xFFFFFFFE
		}
		binary.LittleEndian.PutUint32(fat[4*i:], v)
	}
	dir := out[2*ss:]
	for i, name := range append([]string{"Root Entry"}, names...) {
		e := dir[128*i : 128*i+128]
		for j, c := range utf16.Encode([]rune(name)) {
			binary.LittleEndian.PutUint16(e[2*j:], c)
		}
		binary.LittleEndian.PutUint16(e[64:], uint16(2*len(utf16.Encode([]rune(name)))+2))
		e[66] = 2
		if i == 0 {
			e[66] = 5
		}
		for _, off := range []int{68, 72, 76} {
			binary.LittleEndian.PutUint32(e[off:], 0xFFFFFFFF)
		}
	}
	return out
}

// TestOldOfficeFileIsSentAsItsOwnType (review, decision 46): an OLE2 file is sent to the converter as the type its streams
// name (here an Excel file), not as .doc for every compound file.
func TestOldOfficeFileIsSentAsItsOwnType(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "old.xls", string(oleFile("Workbook")))
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	conv := &fakeConverter{out: []byte("sheetword")}
	drainText(t, fx.worker(conv, clock))
	if conv.calls != 1 || conv.ext != "xls" {
		t.Fatalf("the converter was told %q in %d requests, want xls in 1", conv.ext, conv.calls)
	}
	if r := rowOf(t, fx.s, n.ID); r.State != "ready" {
		t.Fatalf("row %+v, want ready", r)
	}
}

// TestOldOfficeFileWhoseTypeIsUnknownIsSkipped (review, decision 46): a compound file whose structure cannot be followed
// is skipped with a fixed reason, and no request is made.
func TestOldOfficeFileWhoseTypeIsUnknownIsSkipped(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	bad := oleFile("WordDocument")
	binary.LittleEndian.PutUint16(bad[30:], 7) // a sector size that does not exist
	n := fx.file(t, "bad.doc", string(bad))
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	conv := &fakeConverter{out: []byte("never")}
	drainText(t, fx.worker(conv, clock))
	if conv.calls != 0 {
		t.Fatalf("the converter was sent a file of unknown type (%d requests)", conv.calls)
	}
	if r := rowOf(t, fx.s, n.ID); r.State != "skipped" || r.Error != "the type of the old office file could not be told" {
		t.Fatalf("row %+v, want skipped with the fixed reason", r)
	}
}
