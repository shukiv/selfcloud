// SPDX-License-Identifier: AGPL-3.0-or-later

package audio

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
)

// readFLAC reads a FLAC stream: STREAMINFO for the duration, the Vorbis comment block for the tags, and a PICTURE block for
// the cover. The metadata blocks are walked from the start, each one checked against the file's size; a block past the end
// stops the walk.
func readFLAC(d *reader, wantPicture bool) (Result, error) {
	res := Result{Format: "flac"}
	off := int64(4)
	haveInfo := false
	var picture []byte
	var picType uint32 = 1 << 31 // no picture yet
	for n := 0; n < maxBlocks; n++ {
		h, err := d.at(off, 4)
		if err != nil {
			break
		}
		last := h[0]&0x80 != 0
		typ := h[0] & 0x7F
		length := int64(h[1])<<16 | int64(h[2])<<8 | int64(h[3])
		if off+4+length > d.size {
			break
		}
		switch typ {
		case 0:
			if length < 34 {
				return Result{}, ErrCorrupt
			}
			b, err := d.at(off+4, 34)
			if err != nil {
				return Result{}, ErrCorrupt
			}
			// STREAMINFO bytes 10 to 17 hold the sample rate (20 bits), the channels (3), the sample size (5) and the total
			// number of samples (36).
			rate := int64(b[10])<<12 | int64(b[11])<<4 | int64(b[12])>>4
			total := int64(b[13]&0x0F)<<32 | int64(binary.BigEndian.Uint32(b[14:18]))
			haveInfo = true
			if rate > 0 {
				res.Tags.DurationMS = total * 1000 / rate
			}
		case 4:
			if b, err := d.at(off+4, int(length)); err == nil {
				for _, c := range parseComments(b) {
					applyComment(&res.Tags, c.key, c.value)
				}
			}
		case 6:
			if wantPicture {
				if b, err := d.at(off+4, int(length)); err == nil {
					if t, p, ok := parsePictureBlock(b); ok && (picture == nil || (t == 3 && picType != 3)) {
						picture, picType = p, t
					}
				}
			}
		}
		if last {
			break
		}
		off += 4 + length
	}
	if !haveInfo {
		return Result{}, ErrCorrupt
	}
	res.Picture = picture
	return res, nil
}

// comment is one Vorbis comment: a field name and its value.
type comment struct {
	key, value string
}

// parseComments reads a Vorbis comment block: a vendor string, then a count and that many "NAME=value" strings. The lengths are
// little-endian. Ogg packets have the same block, after a short prefix the caller removes. Comments past maxComments are not
// read. Each string is decoded as UTF-8 and cleaned later by applyComment.
func parseComments(b []byte) []comment {
	var out []comment
	if len(b) < 4 {
		return nil
	}
	pos := 4 + int(binary.LittleEndian.Uint32(b[:4]))
	if pos < 4 || pos+4 > len(b) {
		return nil
	}
	count := int(binary.LittleEndian.Uint32(b[pos : pos+4]))
	pos += 4
	for i := 0; i < count && i < maxComments && pos+4 <= len(b); i++ {
		n := int(binary.LittleEndian.Uint32(b[pos : pos+4]))
		pos += 4
		if n < 0 || n > len(b)-pos {
			break
		}
		s := string(b[pos : pos+n])
		pos += n
		key, value, ok := strings.Cut(s, "=")
		if !ok {
			continue
		}
		out = append(out, comment{key: key, value: value})
	}
	return out
}

// applyComment sets a tag from one comment. The names are the common Vorbis names, in any case, and the first value wins.
func applyComment(t *Tags, key, value string) {
	switch strings.ToUpper(key) {
	case "TITLE":
		setIfEmpty(&t.Title, cleanText(value))
	case "ARTIST":
		setIfEmpty(&t.Artist, cleanText(value))
	case "ALBUM":
		setIfEmpty(&t.Album, cleanText(value))
	}
}

// parsePictureBlock reads a FLAC PICTURE block (big-endian fields) and returns its type and its bytes. The block's MIME type
// is not kept: the caller sniffs the bytes. A picture over MaxPicture is refused.
func parsePictureBlock(b []byte) (uint32, []byte, bool) {
	be := binary.BigEndian
	if len(b) < 32 {
		return 0, nil, false
	}
	typ := be.Uint32(b[0:4])
	p := 4
	mlen := int(be.Uint32(b[p : p+4]))
	p += 4
	if mlen < 0 || mlen > len(b)-p-4 {
		return 0, nil, false
	}
	p += mlen
	dlen := int(be.Uint32(b[p : p+4]))
	p += 4
	if dlen < 0 || dlen > len(b)-p-20 {
		return 0, nil, false
	}
	p += dlen + 16 // description, then width, height, depth and colours
	plen := int(be.Uint32(b[p : p+4]))
	p += 4
	if plen <= 0 || plen > MaxPicture || plen > len(b)-p {
		return 0, nil, false
	}
	return typ, b[p : p+plen], true
}

// vorbisPictureComment decodes the base64 METADATA_BLOCK_PICTURE value of an Ogg comment into a picture block's bytes.
func vorbisPictureComment(value string) []byte {
	if len(value) > base64.StdEncoding.EncodedLen(MaxPicture+1024) {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil
	}
	return raw
}
