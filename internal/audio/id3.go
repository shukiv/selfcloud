// SPDX-License-Identifier: AGPL-3.0-or-later

package audio

import (
	"bytes"
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// id3Picture is one picture frame of an ID3v2 tag: its type (3 is the front cover) and its bytes.
type id3Picture struct {
	typ  byte
	data []byte
}

// id3Head is what an ID3v2 tag gives: its three text values, its pictures, and the offset where its audio starts.
type id3Head struct {
	title, artist, album string
	pictures             []id3Picture
	end                  int64
}

// readID3v2 reads the ID3v2 tag at the start of d, if there is one. found is false when there is none, or when its version
// is one this reader does not know. A tag whose declared size is past the end of the file is read as far as the file goes.
func readID3v2(d *reader, wantPicture bool) (h id3Head, found bool) {
	hdr, err := d.at(0, 10)
	if err != nil || string(hdr[:3]) != "ID3" {
		return id3Head{}, false
	}
	major, flags := hdr[3], hdr[5]
	if major < 2 || major > 4 {
		return id3Head{}, false
	}
	size := synchsafe(hdr[6:10])
	if size > maxRead {
		// A tag too large to read is skipped whole, so the audio after it is found and nothing inside it is taken for a
		// frame. Its tags are not read.
		return id3Head{end: int64(10 + size)}, true
	}
	avail := int64(min(size, int(min(d.size-10, maxRead))))
	body, err := d.at(10, int(avail))
	if err != nil {
		return id3Head{}, false
	}
	h.end = int64(10 + size)
	if major == 4 && flags&0x10 != 0 {
		h.end += 10 // a footer
	}
	if flags&0x80 != 0 && major < 4 {
		body = unsynchronise(body)
	}
	if flags&0x40 != 0 && len(body) >= 4 {
		// An extended header is skipped. In v2.4 its size is synchsafe and counts its own four bytes. In v2.3 the size is
		// plain and does not count them, so four are added.
		ext := int(binary.BigEndian.Uint32(body[:4]))
		skip := ext + 4
		if major == 4 {
			skip = int(synchsafe(body[:4]))
		}
		if skip < 0 || skip > len(body) {
			return h, true
		}
		body = body[skip:]
	}
	hdrLen, idLen := 10, 4
	if major == 2 {
		hdrLen, idLen = 6, 3
	}
	pos := 0
	for n := 0; n < maxFrames && pos+hdrLen <= len(body); n++ {
		fh := body[pos : pos+hdrLen]
		if fh[0] == 0 {
			break // padding
		}
		id := string(fh[:idLen])
		var fsize int
		var fflags byte
		switch major {
		case 2:
			fsize = int(fh[3])<<16 | int(fh[4])<<8 | int(fh[5])
		case 3:
			fsize = int(binary.BigEndian.Uint32(fh[4:8]))
			fflags = fh[9]
		case 4:
			fsize = int(synchsafe(fh[4:8]))
			fflags = fh[9]
		}
		start := pos + hdrLen
		if fsize <= 0 || fsize > len(body)-start {
			break // a frame cut short: nothing after it can be trusted
		}
		data := body[start : start+fsize]
		pos = start + fsize
		if major == 4 && fflags&0x02 != 0 {
			data = unsynchronise(data)
		}
		if (major == 3 && fflags&0xC0 != 0) || (major == 4 && fflags&0x0C != 0) {
			continue // compressed or encrypted: not read
		}
		if major == 4 && fflags&0x01 != 0 {
			if len(data) < 4 {
				continue
			}
			data = data[4:] // a data length indicator
		}
		switch id {
		case "TIT2", "TT2":
			setIfEmpty(&h.title, id3TextValue(data))
		case "TPE1", "TP1":
			setIfEmpty(&h.artist, id3TextValue(data))
		case "TALB", "TAL":
			setIfEmpty(&h.album, id3TextValue(data))
		case "APIC", "PIC":
			if wantPicture {
				if p, ok := id3PictureValue(id, data); ok {
					h.pictures = append(h.pictures, p)
				}
			}
		}
	}
	return h, true
}

// readID3v1 reads the 128-byte ID3v1 tag at the end of the file, if there is one. Its text is Latin-1.
func readID3v1(d *reader) (title, artist, album string, ok bool) {
	if d.size < 128 {
		return "", "", "", false
	}
	b, err := d.at(d.size-128, 128)
	if err != nil || string(b[:3]) != "TAG" {
		return "", "", "", false
	}
	field := func(p []byte) string {
		if i := bytes.IndexByte(p, 0); i >= 0 {
			p = p[:i]
		}
		return latin1(p)
	}
	return field(b[3:33]), field(b[33:63]), field(b[63:93]), true
}

// id3TextValue decodes a text frame: an encoding byte, then the text. Only the first value is kept, since ID3 separates
// several values with a NUL.
func id3TextValue(data []byte) string {
	if len(data) < 1 {
		return ""
	}
	return cleanText(decodeID3Text(data[0], data[1:]))
}

// decodeID3Text decodes text in one of the ID3 encodings: 0 Latin-1, 1 UTF-16 with a byte-order mark, 2 UTF-16BE,
// 3 UTF-8. The text stops at its first NUL.
func decodeID3Text(enc byte, b []byte) string {
	switch enc {
	case 0:
		if i := bytes.IndexByte(b, 0); i >= 0 {
			b = b[:i]
		}
		return latin1(b)
	case 1:
		if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
			return utf16Text(b[2:], binary.LittleEndian)
		}
		if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
			return utf16Text(b[2:], binary.BigEndian)
		}
		return utf16Text(b, binary.BigEndian)
	case 2:
		return utf16Text(b, binary.BigEndian)
	case 3:
		if i := bytes.IndexByte(b, 0); i >= 0 {
			b = b[:i]
		}
		return strings.ToValidUTF8(string(b), "�")
	}
	return ""
}

// id3PictureValue reads an attached picture frame: APIC in v2.3 and v2.4, PIC in v2.2. The MIME type is not kept, since the
// caller sniffs the bytes. The picture's bytes are cut out of the frame and capped at MaxPicture.
func id3PictureValue(id string, data []byte) (id3Picture, bool) {
	if len(data) < 2 {
		return id3Picture{}, false
	}
	enc := data[0]
	p := data[1:]
	if id == "APIC" {
		i := bytes.IndexByte(p, 0)
		if i < 0 {
			return id3Picture{}, false
		}
		p = p[i+1:]
	} else {
		if len(p) < 3 {
			return id3Picture{}, false
		}
		p = p[3:] // the three-character image format
	}
	if len(p) < 1 {
		return id3Picture{}, false
	}
	typ := p[0]
	p = p[1:]
	// The description ends with one NUL in Latin-1 and UTF-8, and with two in the UTF-16 encodings.
	termLen := 1
	wide := enc == 1 || enc == 2
	if wide {
		termLen = 2
	}
	i := indexTerm(p, wide)
	if i < 0 {
		return id3Picture{}, false
	}
	data = p[i+termLen:]
	if len(data) == 0 || len(data) > MaxPicture {
		return id3Picture{}, false
	}
	return id3Picture{typ: typ, data: data}, true
}

// indexTerm finds the NUL that ends a picture description. In UTF-16 the terminator is a NUL unit, two bytes long, that
// starts at an even offset: a NUL pair that straddles two characters is part of the text, not its end.
func indexTerm(p []byte, wide bool) int {
	if !wide {
		return bytes.IndexByte(p, 0)
	}
	for i := 0; i+1 < len(p); i += 2 {
		if p[i] == 0 && p[i+1] == 0 {
			return i
		}
	}
	return -1
}

// bestPicture chooses the front cover (type 3) when there is one, and otherwise the first picture.
func bestPicture(ps []id3Picture) []byte {
	for _, p := range ps {
		if p.typ == 3 {
			return p.data
		}
	}
	if len(ps) > 0 {
		return ps[0].data
	}
	return nil
}

// synchsafe reads a four-byte size whose top bit of each byte is zero: 7 bits per byte. A byte with its top bit set is read
// with that bit ignored, the way the common readers do.
func synchsafe(b []byte) int {
	return int(b[0]&0x7F)<<21 | int(b[1]&0x7F)<<14 | int(b[2]&0x7F)<<7 | int(b[3]&0x7F)
}

// unsynchronise undoes ID3's unsynchronisation: each 0xFF 0x00 becomes 0xFF.
func unsynchronise(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte{0xFF, 0x00}, []byte{0xFF})
}

// latin1 turns Latin-1 bytes into a UTF-8 string.
func latin1(b []byte) string {
	rs := make([]rune, len(b))
	for i, c := range b {
		rs[i] = rune(c)
	}
	return string(rs)
}

// utf16Text decodes UTF-16 in the given byte order, stopping at a NUL unit.
func utf16Text(b []byte, order binary.ByteOrder) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := order.Uint16(b[i : i+2])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}
