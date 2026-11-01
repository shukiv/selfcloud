// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"bytes"
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

// minimalOLE builds a minimal OLE2 compound file with 512-byte sectors: the header, one FAT sector, one directory sector,
// and one stream entry per name. Its layout is the one the reader follows; it is not a file any Office program wrote.
func minimalOLE(names ...string) []byte {
	const ss = 512
	out := make([]byte, ss*3) // header, FAT sector (0), directory sector (1)
	hdr := out[:ss]
	copy(hdr, oleMagic)
	binary.LittleEndian.PutUint16(hdr[26:], 3)      // major version 3
	binary.LittleEndian.PutUint16(hdr[28:], 0xFFFE) // byte order
	binary.LittleEndian.PutUint16(hdr[30:], 9)      // sector shift: 512
	binary.LittleEndian.PutUint16(hdr[32:], 6)      // mini sector shift
	binary.LittleEndian.PutUint32(hdr[44:], 1)      // one FAT sector
	binary.LittleEndian.PutUint32(hdr[48:], 1)      // first directory sector
	binary.LittleEndian.PutUint32(hdr[56:], 4096)   // mini stream cutoff
	binary.LittleEndian.PutUint32(hdr[60:], oleEndOfChain)
	binary.LittleEndian.PutUint32(hdr[68:], oleEndOfChain)
	for i := range 109 {
		v := uint32(oleFree)
		if i == 0 {
			v = 0
		}
		binary.LittleEndian.PutUint32(hdr[76+4*i:], v)
	}
	fat := out[ss : 2*ss]
	for i := 0; i < ss/4; i++ {
		v := uint32(oleFree)
		switch i {
		case 0:
			v = 0xFFFFFFFD // the FAT itself
		case 1:
			v = oleEndOfChain // the directory
		}
		binary.LittleEndian.PutUint32(fat[4*i:], v)
	}
	dir := out[2*ss:]
	entries := append([]string{"Root Entry"}, names...)
	for i, name := range entries {
		e := dir[128*i : 128*i+128]
		u := utf16.Encode([]rune(name))
		for j, c := range u {
			binary.LittleEndian.PutUint16(e[2*j:], c)
		}
		binary.LittleEndian.PutUint16(e[64:], uint16(2*len(u)+2))
		e[66] = 2
		if i == 0 {
			e[66] = 5
		}
		for _, off := range []int{68, 72, 76} {
			binary.LittleEndian.PutUint32(e[off:], oleFree)
		}
	}
	return out
}

func TestOLEKindFromStreamNames(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		names []string
		want  string
	}{
		{"word", []string{"WordDocument", "1Table"}, "doc"},
		{"excel", []string{"Workbook"}, "xls"},
		{"oldest excel", []string{"Book"}, "xls"},
		{"powerpoint", []string{"PowerPoint Document", "Pictures"}, "ppt"},
		{"no office stream", []string{"SummaryInformation"}, ""},
	} {
		got, err := oleKind(bytes.NewReader(minimalOLE(c.names...)))
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

// TestOLECorruptIsRefused: a file with the magic and a sector size that cannot be followed, and a file cut short, are
// errors, so the worker skips them.
func TestOLECorruptIsRefused(t *testing.T) {
	t.Parallel()
	bad := minimalOLE("WordDocument")
	binary.LittleEndian.PutUint16(bad[30:], 7) // no such sector size
	if _, err := oleKind(bytes.NewReader(bad)); err == nil {
		t.Error("a bad sector size was read")
	}
	short := minimalOLE("WordDocument")[:300]
	if _, err := oleKind(bytes.NewReader(short)); err == nil {
		t.Error("a file cut inside the header was read")
	}
	notOLE := append([]byte("PK\x03\x04"), make([]byte, 600)...)
	if _, err := oleKind(bytes.NewReader(notOLE)); err == nil {
		t.Error("a file without the OLE magic was read")
	}
}

// TestOLEDirectoryLoopIsBounded: a directory chain that points back at itself ends at the sector bound, with no error and no
// type, instead of looping.
func TestOLEDirectoryLoopIsBounded(t *testing.T) {
	t.Parallel()
	f := minimalOLE("Other")
	// FAT entry 1 (the directory) points at itself.
	binary.LittleEndian.PutUint32(f[512+4:], 1)
	got, err := oleKind(bytes.NewReader(f))
	if err != nil || got != "" {
		t.Errorf("got %q, %v; want no type and no error", got, err)
	}
}
