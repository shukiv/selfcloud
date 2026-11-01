// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf16"
)

// The old Office formats are OLE2 compound files. The converter must be told which one a file is (.doc, .xls or .ppt), and
// the file's first bytes do not say it: the type is in the names of its streams. The read is bounded: the header, the
// DIFAT and FAT sectors it lists (at most oleMaxDIFAT sectors of DIFAT), and at most oleMaxDirSectors sectors of the
// directory. A file whose type cannot be told is skipped and recorded, never sent as a guess (review, decision 46).

const (
	oleMaxDIFAT      = 64
	oleMaxDirSectors = 256
	oleFree          = 0xFFFFFFFF
	oleEndOfChain    = 0xFFFFFFFE
	oleDIFATChainEnd = 0xFFFFFFFE
)

// errNotOLE is an OLE2 file whose structure cannot be followed: a bad magic, sector size, or chain.
var errNotOLE = errors.New("not a readable OLE2 compound file")

// oleStreamKinds maps the stream names that tell the old formats apart to the converter's type for each.
var oleStreamKinds = map[string]string{
	"WordDocument":        "doc",
	"Workbook":            "xls",
	"Book":                "xls", // the oldest Excel files
	"PowerPoint Document": "ppt",
}

// oleKind returns "doc", "xls" or "ppt" for an OLE2 file, from the stream names in its directory, and "" when the file has
// none of those streams. An error means the structure could not be followed.
func oleKind(r io.ReaderAt) (string, error) {
	hdr := make([]byte, 512)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return "", errNotOLE
	}
	if !bytes.Equal(hdr[:8], oleMagic) {
		return "", errNotOLE
	}
	shift := binary.LittleEndian.Uint16(hdr[30:32])
	if shift != 9 && shift != 12 {
		return "", errNotOLE
	}
	ss := int64(1) << shift
	perSector := uint32(ss / 4)
	// sector n is at file offset (n+1)*ss: the header fills the first sector (512 bytes, or 4096 when the sector is that big).
	sectorAt := func(n uint32) int64 { return (int64(n) + 1) * ss }

	var fat []uint32
	for i := range 109 {
		id := binary.LittleEndian.Uint32(hdr[76+4*i:])
		if id == oleFree {
			break
		}
		fat = append(fat, id)
	}
	next := binary.LittleEndian.Uint32(hdr[68:72])
	sector := make([]byte, ss)
	for hops := 0; next != oleDIFATChainEnd && next != oleFree; hops++ {
		if hops >= oleMaxDIFAT {
			return "", errNotOLE
		}
		if _, err := r.ReadAt(sector, sectorAt(next)); err != nil {
			return "", errNotOLE
		}
		for i := uint32(0); i < perSector-1; i++ {
			id := binary.LittleEndian.Uint32(sector[4*i:])
			if id != oleFree {
				fat = append(fat, id)
			}
		}
		next = binary.LittleEndian.Uint32(sector[ss-4:])
	}

	fatEntry := func(id uint32) (uint32, error) {
		idx := id / perSector
		if int(idx) >= len(fat) {
			return 0, errNotOLE
		}
		var b [4]byte
		if _, err := r.ReadAt(b[:], sectorAt(fat[idx])+int64(id%perSector)*4); err != nil {
			return 0, errNotOLE
		}
		return binary.LittleEndian.Uint32(b[:]), nil
	}

	cur := binary.LittleEndian.Uint32(hdr[48:52]) // the first directory sector
	for range oleMaxDirSectors {
		if _, err := r.ReadAt(sector, sectorAt(cur)); err != nil {
			return "", errNotOLE
		}
		for off := int64(0); off+128 <= ss; off += 128 {
			e := sector[off : off+128]
			nameLen := int(binary.LittleEndian.Uint16(e[64:66]))
			if e[66] != 2 || nameLen < 2 || nameLen > 64 { // 2 is a stream; its name is nameLen bytes with the terminator
				continue
			}
			if kind, ok := oleStreamKinds[oleName(e[:nameLen-2])]; ok {
				return kind, nil
			}
		}
		after, err := fatEntry(cur)
		if err != nil {
			return "", err
		}
		if after == oleEndOfChain {
			return "", nil
		}
		cur = after
	}
	return "", nil
}

// oleName decodes a directory entry's name, UTF-16 little endian.
func oleName(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(b[i:]))
	}
	return string(utf16.Decode(u))
}
