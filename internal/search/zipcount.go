// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"encoding/binary"
	"io"
)

// The end-of-central-directory records of a zip, as archive/zip reads them. zipEntryCount reads only these, so the cap on
// a container's entries holds before zip.NewReader builds one entry for each record (sec1, decision 84, review F5).
const (
	eocdSig      = 0x06054b50 // end of central directory
	eocd64LocSig = 0x07064b50 // zip64 end of central directory locator
	eocd64Sig    = 0x06064b50 // zip64 end of central directory
	eocdLen      = 22
	eocd64LocLen = 20
	eocd64Len    = 56
)

// zipEntryCount is the number of entries the zip's central directory records. It finds the end record as archive/zip
// does, in the last 1 KiB and then in the last 65 KiB, and it reads the zip64 count when the 16-bit count is 0xFFFF,
// as archive/zip does. ok is false where there is no end record, or it cannot be read.
func zipEntryCount(r io.ReaderAt, size int64) (uint64, bool) {
	var (
		buf    []byte
		eocOff = int64(-1)
	)
	for _, want := range []int64{1024, 65 * 1024} {
		n := min(want, size)
		if n < eocdLen {
			return 0, false
		}
		b := make([]byte, n)
		if _, err := r.ReadAt(b, size-n); err != nil && err != io.EOF {
			return 0, false
		}
		if p := findEOCD(b); p >= 0 {
			eocOff = size - n + int64(p)
			buf = b[p:]
			break
		}
		if n == size {
			break
		}
	}
	if eocOff < 0 {
		return 0, false
	}
	count := uint64(binary.LittleEndian.Uint16(buf[10:12]))
	if count != 0xffff {
		return count, true
	}
	// A 16-bit count of 0xFFFF may be a zip64 file: its locator sits just before the end record.
	locOff := eocOff - eocd64LocLen
	if locOff < 0 {
		return count, true
	}
	loc := make([]byte, eocd64LocLen)
	if _, err := r.ReadAt(loc, locOff); err != nil {
		return 0, false
	}
	if binary.LittleEndian.Uint32(loc[0:4]) != eocd64LocSig ||
		binary.LittleEndian.Uint32(loc[4:8]) != 0 || binary.LittleEndian.Uint32(loc[16:20]) != 1 {
		return count, true
	}
	rec := make([]byte, eocd64Len)
	if _, err := r.ReadAt(rec, int64(binary.LittleEndian.Uint64(loc[8:16]))); err != nil {
		return 0, false
	}
	if binary.LittleEndian.Uint32(rec[0:4]) != eocd64Sig {
		return 0, false
	}
	return binary.LittleEndian.Uint64(rec[32:40]), true
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
