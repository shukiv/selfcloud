// SPDX-License-Identifier: AGPL-3.0-or-later

package audio

import (
	"encoding/binary"
)

// readMP4 reads an MP4 or M4A file: the top-level boxes are walked from the start (a moov box may come last, and the boxes
// before it are skipped by their size, without being read). The moov box is read whole, up to maxRead, and holds the
// duration (mvhd) and the iTunes-style tags (udta/meta/ilst). A file with no moov box has no duration and no tags: it is
// corrupt.
func readMP4(d *reader, wantPicture bool) (Result, error) {
	res := Result{Format: "mp4"}
	var moov []byte
	off := int64(0)
	for n := 0; n < maxAtoms && off+8 <= d.size; n++ {
		h, err := d.at(off, 8)
		if err != nil {
			break
		}
		size := int64(binary.BigEndian.Uint32(h[:4]))
		typ := string(h[4:8])
		hl := int64(8)
		switch size {
		case 0:
			size = d.size - off // the box runs to the end of the file
		case 1:
			lh, err := d.at(off+8, 8)
			if err != nil {
				return Result{}, ErrCorrupt
			}
			size = int64(binary.BigEndian.Uint64(lh))
			hl = 16
		}
		if size < hl || size > d.size-off {
			return Result{}, ErrCorrupt
		}
		if typ == "moov" {
			if size-hl > maxRead {
				return Result{}, ErrTooLarge
			}
			body, err := d.at(off+hl, int(size-hl))
			if err != nil {
				return Result{}, ErrCorrupt
			}
			moov = body
			break
		}
		off += size
	}
	if moov == nil {
		return Result{}, ErrCorrupt
	}
	forEachBox(moov, func(typ string, body []byte) {
		switch typ {
		case "mvhd":
			res.Tags.DurationMS = mvhdDuration(body)
		case "udta":
			forEachBox(body, func(typ string, body []byte) {
				if typ == "meta" && len(body) >= 4 {
					// meta is a full box: four bytes of version and flags come before its children.
					forEachBox(body[4:], func(typ string, body []byte) {
						if typ == "ilst" {
							readIlst(body, &res, wantPicture)
						}
					})
				}
			})
		}
	})
	return res, nil
}

// forEachBox calls fn for each box that b holds, with the box's body. It stops at the first box whose size does not fit
// b, and after maxAtoms boxes.
func forEachBox(b []byte, fn func(typ string, body []byte)) {
	pos := 0
	for n := 0; n < maxAtoms && pos+8 <= len(b); n++ {
		size := int(binary.BigEndian.Uint32(b[pos : pos+4]))
		typ := string(b[pos+4 : pos+8])
		hl := 8
		if size == 1 {
			if pos+16 > len(b) {
				return
			}
			big := binary.BigEndian.Uint64(b[pos+8 : pos+16])
			if big > uint64(len(b)) {
				return
			}
			size, hl = int(big), 16
		} else if size == 0 {
			size = len(b) - pos
		}
		if size < hl || size > len(b)-pos {
			return
		}
		fn(typ, b[pos+hl:pos+size])
		pos += size
	}
}

// mvhdDuration reads the duration of a movie header box in milliseconds. Version 0 has 32-bit times, version 1 64-bit ones.
func mvhdDuration(b []byte) int64 {
	if len(b) < 4 {
		return 0
	}
	var timescale, duration uint64
	switch b[0] {
	case 0:
		if len(b) < 20 {
			return 0
		}
		timescale = uint64(binary.BigEndian.Uint32(b[12:16]))
		duration = uint64(binary.BigEndian.Uint32(b[16:20]))
	case 1:
		if len(b) < 32 {
			return 0
		}
		timescale = uint64(binary.BigEndian.Uint32(b[20:24]))
		duration = binary.BigEndian.Uint64(b[24:32])
	default:
		return 0
	}
	if timescale == 0 || duration > 1<<40 {
		return 0
	}
	return int64(duration * 1000 / timescale)
}

// readIlst reads the items of an iTunes metadata list. Each item holds a data box: the value (text, or the bytes of a
// picture in covr). The four-character names are the Latin-1 bytes 0xA9 (©) followed by nam, ART or alb.
func readIlst(b []byte, res *Result, wantPicture bool) {
	forEachBox(b, func(typ string, body []byte) {
		var value []byte
		forEachBox(body, func(dt string, data []byte) {
			if dt == "data" && value == nil && len(data) >= 8 {
				value = data[8:] // type indicator and locale come first
			}
		})
		if value == nil {
			return
		}
		switch typ {
		case "\xa9nam":
			setIfEmpty(&res.Tags.Title, cleanText(string(value)))
		case "\xa9ART":
			setIfEmpty(&res.Tags.Artist, cleanText(string(value)))
		case "\xa9alb":
			setIfEmpty(&res.Tags.Album, cleanText(string(value)))
		case "covr":
			if wantPicture && res.Picture == nil && len(value) > 0 && len(value) <= MaxPicture {
				res.Picture = value
			}
		}
	})
}
