// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"bufio"
	"encoding/binary"
	"io"
)

// The records of a zip's central directory, as archive/zip reads them. zipEntryCount reads them, so the cap on a
// container's entries holds before zip.NewReader builds one entry for each header (sec1, decision 84).
const (
	eocdSig          = 0x06054b50 // end of central directory
	eocd64LocSig     = 0x07064b50 // zip64 end of central directory locator
	eocd64Sig        = 0x06064b50 // zip64 end of central directory
	dirHeaderSig     = 0x02014b50 // central directory file header
	eocdLen          = 22
	eocd64LocLen     = 20
	eocd64Len        = 56
	dirHeaderLen     = 46
	maxInt64Unsigned = 1<<63 - 1
)

// ZipEntryCount is zipEntryCount for the import of an archive (decision 92): the same count, with the same refusal, so an
// archive's entries are counted before archive/zip builds one entry for each header.
func ZipEntryCount(r io.ReaderAt, size int64, limit int) (uint64, bool) {
	return zipEntryCount(r, size, limit)
}

// zipEntryCount is the number of central directory headers the zip really holds, counted as archive/zip counts them:
// from the directory's offset, headers are read until one fails, so the declared count is not trusted. The count stops
// at limit+1, and the walk keeps no entry, only a buffered reader and the fixed part of one header, so its cost does not
// grow with the zip. ok is false where archive/zip would refuse the zip's structure.
func zipEntryCount(r io.ReaderAt, size int64, limit int) (uint64, bool) {
	eocOff, rec, ok := findEnd(r, size)
	if !ok {
		return 0, false
	}
	dirRecords := uint64(binary.LittleEndian.Uint16(rec[10:12]))
	dirSize := uint64(binary.LittleEndian.Uint32(rec[12:16]))
	dirOff := uint64(binary.LittleEndian.Uint32(rec[16:20]))
	endOff := eocOff
	if dirRecords == 0xffff || dirSize == 0xffff || dirOff == 0xffffffff {
		p, ok, err := zip64End(r, eocOff)
		if err != nil {
			return 0, false
		}
		if ok {
			zrec := make([]byte, eocd64Len)
			if _, err := r.ReadAt(zrec, p); err != nil || binary.LittleEndian.Uint32(zrec[0:4]) != eocd64Sig {
				return 0, false
			}
			dirRecords = binary.LittleEndian.Uint64(zrec[32:40])
			dirSize = binary.LittleEndian.Uint64(zrec[40:48])
			dirOff = binary.LittleEndian.Uint64(zrec[48:56])
			endOff = p
		}
	}
	// The declared count sizes archive/zip's allocation for its entries, so a declared count over the cap is refused
	// before anything is read from the directory.
	if dirRecords > uint64(limit) {
		return dirRecords, true
	}
	// The directory's bytes are bounded too: archive/zip keeps every header's name, extra and comment in memory, so a
	// directory of many large headers costs its own size. A legitimate header is well under 4 KiB, so a directory over
	// limit times 4 KiB cannot hold limit legitimate entries, and it is refused as one more than the cap.
	if limit >= 0 && dirSize > uint64(limit)*4096 {
		return uint64(limit) + 1, true
	}
	if dirSize > maxInt64Unsigned || dirOff > maxInt64Unsigned {
		return 0, false
	}
	// archive/zip's choice of the directory's start is not reproduced: the walk is made from each start it could take,
	// and the larger count stands. A base above zero gives two starts: base+dirOff, and dirOff itself (base 0).
	base := endOff - int64(dirSize) - int64(dirOff)
	starts := []int64{base + int64(dirOff)}
	if base > 0 {
		starts = append(starts, int64(dirOff))
	}
	var most uint64
	valid := false
	for _, start := range starts {
		if start < 0 || start >= size {
			continue
		}
		valid = true
		most = max(most, walkHeaders(r, size, start, limit))
	}
	if !valid {
		return 0, false
	}
	return most, true
}

// findEnd is the offset and the 22 bytes of the last end-of-central-directory record, found as archive/zip finds it:
// in the last 1 KiB, then in the last 65 KiB, with the same comment rule.
func findEnd(r io.ReaderAt, size int64) (int64, []byte, bool) {
	for _, want := range []int64{1024, 65 * 1024} {
		n := min(want, size)
		if n < eocdLen {
			return 0, nil, false
		}
		b := make([]byte, n)
		if _, err := r.ReadAt(b, size-n); err != nil && err != io.EOF {
			return 0, nil, false
		}
		if p := findEOCD(b); p >= 0 {
			return size - n + int64(p), b[p : p+eocdLen], true
		}
		if n == size {
			break
		}
	}
	return 0, nil, false
}

// zip64End is the offset of the zip64 end record, when the locator before the end record names one. ok is false with no
// locator; err is set when the locator's disk fields are wrong (archive/zip refuses those).
func zip64End(r io.ReaderAt, eocOff int64) (int64, bool, error) {
	locOff := eocOff - eocd64LocLen
	if locOff < 0 {
		return 0, false, nil
	}
	loc := make([]byte, eocd64LocLen)
	if _, err := r.ReadAt(loc, locOff); err != nil {
		return 0, false, err
	}
	if binary.LittleEndian.Uint32(loc[0:4]) != eocd64LocSig {
		return 0, false, nil
	}
	if binary.LittleEndian.Uint32(loc[4:8]) != 0 || binary.LittleEndian.Uint32(loc[16:20]) != 1 {
		return 0, false, nil
	}
	return int64(binary.LittleEndian.Uint64(loc[8:16])), true, nil
}

// walkHeaders counts the headers from start on, stopping at limit+1. A header that is not there (a bad signature, or the
// end of the data) ends the walk, as archive/zip's loop does.
func walkHeaders(r io.ReaderAt, size, start int64, limit int) uint64 {
	br := bufio.NewReaderSize(io.NewSectionReader(r, start, size-start), 1<<16)
	var n uint64
	for n <= uint64(limit) {
		if !readHeader(br) {
			break
		}
		n++
	}
	return n
}

// readHeader reads one central directory header from br and skips its variable part. It keeps only the fixed part, in
// a fixed array, so it makes no allocation per header.
func readHeader(br *bufio.Reader) bool {
	var fixed [dirHeaderLen]byte
	if _, err := io.ReadFull(br, fixed[:]); err != nil {
		return false
	}
	if binary.LittleEndian.Uint32(fixed[0:4]) != dirHeaderSig {
		return false
	}
	variable := int(binary.LittleEndian.Uint16(fixed[28:30])) + int(binary.LittleEndian.Uint16(fixed[30:32])) +
		int(binary.LittleEndian.Uint16(fixed[32:34]))
	if _, err := br.Discard(variable); err != nil {
		return false
	}
	return true
}

// findEOCD is the offset of the last end record in b that fits its comment, or -1. It is archive/zip's search: a record
// whose comment runs past the block is not taken, and the search stops there.
func findEOCD(b []byte) int {
	for i := len(b) - eocdLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(b[i:i+4]) != eocdSig {
			continue
		}
		n := int(binary.LittleEndian.Uint16(b[i+20 : i+22]))
		if n+eocdLen+i > len(b) {
			return -1
		}
		return i
	}
	return -1
}
