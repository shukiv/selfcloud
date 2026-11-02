// SPDX-License-Identifier: AGPL-3.0-or-later

package audio

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// maxPacket bounds one reassembled Ogg packet. A comment packet with a picture in it is the largest one read.
const maxPacket = 16 << 20

// oggPage reads the page at off: its granule position, its segment table and its body, and the offset of the next page.
// A page whose header is not a page is an error.
func oggPage(d *reader, off int64) (granule int64, segs []byte, body []byte, next int64, err error) {
	h, err := d.at(off, 27)
	if err != nil {
		return 0, nil, nil, 0, err
	}
	if string(h[:4]) != "OggS" || h[4] != 0 {
		return 0, nil, nil, 0, ErrCorrupt
	}
	granule = int64(binary.LittleEndian.Uint64(h[6:14]))
	n := int(h[26])
	if segs, err = d.at(off+27, n); err != nil {
		return 0, nil, nil, 0, err
	}
	sum := 0
	for _, s := range segs {
		sum += int(s)
	}
	if body, err = d.at(off+27+int64(n), sum); err != nil {
		return 0, nil, nil, 0, err
	}
	return granule, segs, body, off + 27 + int64(n) + int64(sum), nil
}

// oggHeadPackets reads the first two packets of the stream: the identification header and the comment header. A packet is
// the bytes of its segments up to the first segment shorter than 255; one that spans pages is put back together.
func oggHeadPackets(d *reader) ([][]byte, error) {
	var packets [][]byte
	var carry []byte
	off := int64(0)
	for pages := 0; pages < maxPages && len(packets) < 2; pages++ {
		_, segs, body, next, err := oggPage(d, off)
		if err != nil {
			break
		}
		i := 0
		for _, s := range segs {
			carry = append(carry, body[i:i+int(s)]...)
			i += int(s)
			if len(carry) > maxPacket {
				return packets, ErrTooLarge
			}
			if s < 255 {
				packets = append(packets, carry)
				carry = nil
				if len(packets) == 2 {
					break
				}
			}
		}
		off = next
	}
	if len(packets) == 0 {
		return nil, ErrCorrupt
	}
	return packets, nil
}

// oggLastGranule returns the granule position of the last page that has one, found in the last tailWindow bytes. It returns
// -1 when no page is found there. A candidate "OggS" must be followed by a version byte of 0 and a full header, so a packet
// that happens to hold those bytes is passed over.
func oggLastGranule(d *reader) int64 {
	start := max(int64(0), d.size-tailWindow)
	tail, err := d.at(start, int(d.size-start))
	if err != nil {
		return -1
	}
	for i := len(tail) - 27; i >= 0; i-- {
		if tail[i] != 'O' || !bytes.HasPrefix(tail[i:], []byte("OggS")) {
			continue
		}
		if tail[i+4] != 0 {
			continue
		}
		// A granule is a sample count: one over 2^40 is not a count of any real file, and would overflow the millisecond sum.
		g := int64(binary.LittleEndian.Uint64(tail[i+6 : i+14]))
		if g >= 0 && g < 1<<40 {
			return g
		}
	}
	return -1
}

// readOgg reads an Ogg Vorbis or Ogg Opus stream. Any other Ogg stream (Theora, FLAC in Ogg) is unsupported.
func readOgg(d *reader, wantPicture bool) (Result, error) {
	packets, err := oggHeadPackets(d)
	if err != nil {
		return Result{}, err
	}
	if len(packets) < 1 {
		return Result{}, ErrCorrupt
	}
	ident := packets[0]
	var res Result
	var rate int64
	var preskip int64
	var commentPrefix int
	switch {
	case len(ident) >= 16 && bytes.HasPrefix(ident, []byte("\x01vorbis")):
		res.Format = "ogg"
		rate = int64(binary.LittleEndian.Uint32(ident[12:16]))
		commentPrefix = len("\x03vorbis")
	case len(ident) >= 19 && bytes.HasPrefix(ident, []byte("OpusHead")):
		res.Format = "opus"
		preskip = int64(binary.LittleEndian.Uint16(ident[10:12]))
		commentPrefix = len("OpusTags")
	default:
		return Result{}, ErrUnsupported
	}
	if len(packets) == 2 {
		comments := packets[1]
		if len(comments) >= commentPrefix {
			for _, c := range parseComments(comments[commentPrefix:]) {
				if strings.EqualFold(c.key, "METADATA_BLOCK_PICTURE") {
					if wantPicture && res.Picture == nil {
						if _, p, ok := parsePictureBlock(vorbisPictureComment(c.value)); ok {
							res.Picture = p
						}
					}
					continue
				}
				applyComment(&res.Tags, c.key, c.value)
			}
		}
	}
	// Vorbis counts samples at its own rate. Opus always counts 48 kHz samples, and the pre-skip is not played.
	if g := oggLastGranule(d); g >= 0 {
		switch {
		case res.Format == "ogg" && rate > 0:
			res.Tags.DurationMS = g * 1000 / rate
		case res.Format == "opus":
			res.Tags.DurationMS = max(g-preskip, 0) * 1000 / 48000
		}
	}
	return res, nil
}
