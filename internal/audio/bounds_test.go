// SPDX-License-Identifier: AGPL-3.0-or-later

package audio

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

// countingReader counts the bytes read through it, so a test can prove how much a reader took from the file.
type countingReader struct {
	r    io.ReadSeeker
	read int64
}

// flacBlocks is a FLAC file of many metadata blocks, generated on demand: a 4-byte header for each block at its place, and
// zeros in each body. The file can be large without the test holding it.
type flacBlocks struct {
	prefix []byte // "fLaC" and STREAMINFO
	block  int64  // the body size of each block
	count  int64
	pos    int64
}

func (f *flacBlocks) size() int64 { return int64(len(f.prefix)) + f.count*(f.block+4) }

func (f *flacBlocks) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.pos = off
	case io.SeekCurrent:
		f.pos += off
	case io.SeekEnd:
		f.pos = f.size() + off
	}
	return f.pos, nil
}

func (f *flacBlocks) Read(p []byte) (int, error) {
	if f.pos >= f.size() {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && f.pos < f.size() {
		var c byte
		if f.pos < int64(len(f.prefix)) {
			c = f.prefix[f.pos]
		} else {
			rel := f.pos - int64(len(f.prefix))
			r := rel % (f.block + 4)
			if r < 4 {
				c = []byte{0x04, byte(f.block >> 16), byte(f.block >> 8), byte(f.block)}[r]
			}
		}
		p[n] = c
		n++
		f.pos++
	}
	return n, nil
}

func (c *countingReader) Seek(off int64, whence int) (int64, error) { return c.r.Seek(off, whence) }

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	return n, err
}

// synchsafeBytes encodes n as a four-byte synchsafe integer (7 bits a byte).
func synchsafeBytes(n int) []byte {
	return []byte{byte(n >> 21 & 0x7F), byte(n >> 14 & 0x7F), byte(n >> 7 & 0x7F), byte(n & 0x7F)}
}

// oggPages paginates one packet into Ogg pages of at most 255 segments each, as a muxer does. The packet is cut into
// 255-byte segments; the last segment is shorter than 255, which ends the packet.
func oggPages(packet []byte, serial uint32, granule uint64) []byte {
	var segs []byte
	for n := len(packet); ; {
		if n >= 255 {
			segs = append(segs, 255)
			n -= 255
			continue
		}
		segs = append(segs, byte(n))
		break
	}
	var out []byte
	pos := 0
	for seq := uint32(0); len(segs) > 0; seq++ {
		take := min(len(segs), 255)
		body := 0
		for _, s := range segs[:take] {
			body += int(s)
		}
		h := []byte("OggS\x00\x00")
		g := granule
		if len(segs) > take {
			g = 0xFFFFFFFFFFFFFFFF // the packet goes on: no granule yet
		}
		h = binary.LittleEndian.AppendUint64(h, g)
		h = binary.LittleEndian.AppendUint32(h, serial)
		h = binary.LittleEndian.AppendUint32(h, seq)
		h = append(h, 0, 0, 0, 0, byte(take))
		h = append(h, segs[:take]...)
		out = append(out, h...)
		out = append(out, packet[pos:pos+body]...)
		pos += body
		segs = segs[take:]
	}
	return out
}

// oggVorbisWithCover builds an Ogg Vorbis stream: an identification packet, then a comment packet with a title and a
// METADATA_BLOCK_PICTURE of picture bytes, which spans many pages.
func oggVorbisWithCover(title string, picture []byte) []byte {
	ident := append([]byte("\x01vorbis\x00\x00\x00\x00\x01"), binary.LittleEndian.AppendUint32(nil, 44100)...)
	ident = append(ident, make([]byte, 13)...)
	be := binary.BigEndian
	block := be.AppendUint32(nil, 3)
	block = be.AppendUint32(block, uint32(len("image/png")))
	block = append(block, "image/png"...)
	block = be.AppendUint32(block, 0)
	block = append(block, make([]byte, 16)...)
	block = be.AppendUint32(block, uint32(len(picture)))
	block = append(block, picture...)
	entries := []string{"TITLE=" + title, "METADATA_BLOCK_PICTURE=" + base64.StdEncoding.EncodeToString(block)}
	comments := append([]byte("\x03vorbis"), binary.LittleEndian.AppendUint32(nil, 0)...)
	comments = binary.LittleEndian.AppendUint32(comments, uint32(len(entries)))
	for _, e := range entries {
		comments = binary.LittleEndian.AppendUint32(comments, uint32(len(e)))
		comments = append(comments, e...)
	}
	out := oggPages(ident, 1, 0)
	out = append(out, oggPages(comments, 1, 0)...)
	return out
}

// A cover of 4 MiB in a comment packet spans about 80 pages. The title and the picture of such a file are read; the old
// limit of 64 pages lost them all.
func TestOggCoverSpanningManyPagesKeepsItsTags(t *testing.T) {
	picture := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x5A}, 4<<20)...)
	b := oggVorbisWithCover("Long Cover", picture)
	res := mustRead(t, b, true)
	if res.Tags.Title != "Long Cover" {
		t.Fatalf("title %q: the tags after a large cover were lost", res.Tags.Title)
	}
	if !bytes.Equal(res.Picture, picture) {
		t.Fatalf("the picture read is %d bytes, want %d", len(res.Picture), len(picture))
	}
}

// A tag over 16 MiB is skipped whole. The frame after it is found, and the duration is estimated from it. The old code
// started the scan inside the tag and found no frame.
func TestID3TagOverMaxReadIsSkippedWhole(t *testing.T) {
	const tagSize = 17 << 20
	const audio = 1 << 20
	head := append([]byte("ID3\x04\x00\x00"), synchsafeBytes(tagSize)...)
	// Pad the head to the end of the tag, then one MPEG 1 Layer III frame at 128 kbit/s.
	head = append(head, make([]byte, tagSize)...)
	head = append(head, 0xFF, 0xFB, 0x90, 0x64)
	z := &zeros{head: head, size: int64(len(head)) + audio}
	res, err := read(z, true)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if res.Format != "mp3" {
		t.Fatalf("format %q", res.Format)
	}
	want := int64(audio) * 8 / 128
	if res.Tags.DurationMS != want {
		t.Fatalf("duration %d ms, want %d (estimated from the frame after the tag)", res.Tags.DurationMS, want)
	}
}

// An ID3v2.3 extended header's size does not count its own four bytes. The title after it must still be read.
func TestID3v23ExtendedHeaderIsSkippedWithItsOwnSize(t *testing.T) {
	ext := binary.BigEndian.AppendUint32(nil, 6) // the size, not counting these four bytes
	ext = append(ext, make([]byte, 6)...)        // flags and padding
	frame := []byte("TIT2")
	frame = binary.BigEndian.AppendUint32(frame, uint32(1+len("Ext Title")))
	frame = append(frame, 0, 0, 0) // flags, then the Latin-1 encoding byte
	frame = append(frame, "Ext Title"...)
	body := append(ext, frame...)
	tag := []byte("ID3\x03\x00\x40")
	tag = append(tag, synchsafeBytes(len(body))...)
	b := append(tag, body...)
	res := read0(t, b)
	if res.Tags.Title != "Ext Title" {
		t.Fatalf("title %q, want %q: the extended header was not skipped with its own four bytes", res.Tags.Title, "Ext Title")
	}
}

// read0 reads the file and fails the test on any error.
func read0(t *testing.T, b []byte) Result {
	t.Helper()
	return mustRead(t, b, true)
}

// In a UTF-16BE description, a NUL pair that straddles two characters is not the terminator. The picture is the bytes after
// the real terminator.
func TestID3UTF16TerminatorIsFoundOnAnEvenOffset(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nimage")
	// The description holds U+4100 and U+0042: bytes 41 00 00 42. The terminator is 00 00, at an even offset (4).
	data := []byte{2}
	data = append(data, "image/png"...)
	data = append(data, 0, 3)
	data = append(data, 0x41, 0x00, 0x00, 0x42, 0x00, 0x00)
	data = append(data, png...)
	frame := []byte("APIC")
	frame = append(frame, synchsafeBytes(len(data))...)
	frame = append(frame, 0, 0)
	frame = append(frame, data...)
	tag := []byte("ID3\x04\x00\x00")
	tag = append(tag, synchsafeBytes(len(frame))...)
	b := append(tag, frame...)
	res := read0(t, b)
	if !bytes.Equal(res.Picture, png) {
		t.Fatalf("picture % x, want the PNG after the real terminator", res.Picture[:min(len(res.Picture), 12)])
	}
}

// A duration over a week is a bad header: the reader gives 0 and keeps the title. A tiny header over a huge file gives the
// estimate, which is over a week.
func TestImplausibleDurationBecomesZero(t *testing.T) {
	// MPEG 1 Layer III at 32 kbit/s over 2^40 bytes: about 2^38 milliseconds.
	z := &zeros{head: []byte{0xFF, 0xFB, 0x10, 0x00}, size: 1 << 40}
	res, err := read(z, false)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if res.Tags.DurationMS != 0 {
		t.Fatalf("duration %d ms for an implausible header, want 0", res.Tags.DurationMS)
	}
}

// A FLAC file of 256 blocks, each a megabyte, asks for 256 MiB. The reader takes no more than its total cap, however many
// blocks each one is under the per-read cap.
func TestTotalReadsAreCapped(t *testing.T) {
	info := make([]byte, 34)
	const rate = 44100
	info[10] = byte(rate >> 12 & 0xFF)
	info[11] = byte(rate >> 4 & 0xFF)
	info[12] = byte(rate & 0xF << 4)
	// The file: STREAMINFO, then 256 comment blocks of a megabyte each: 256 MiB, generated as it is read.
	prefix := append([]byte("fLaC\x00\x00\x00\x22"), info...)
	f := &flacBlocks{prefix: prefix, block: 1 << 20, count: 256}
	c := &countingReader{r: f}
	if _, err := read(c, false); err != nil {
		t.Fatalf("read: %v", err)
	}
	if c.read > maxTotalRead+(1<<20) {
		t.Fatalf("the reader took %d bytes from a file of %d blocks, over its cap of %d", c.read, f.count, maxTotalRead)
	}
}

// The tags of a title made only of Hangul fillers or a Braille blank are empty, so the listing falls back to the file name.
func TestBlankTitlesAreEmpty(t *testing.T) {
	for _, s := range []string{"ㅤㅤ", "ᅟᅠ", "⠀⠀", "ﾠ"} {
		if got := cleanText(s); got != "" {
			t.Errorf("cleanText(%q) = %q, want empty", s, got)
		}
	}
}

// A run of combining marks keeps two; the rest is a flood and is dropped.
func TestCombiningFloodIsCut(t *testing.T) {
	in := "a" + strings.Repeat("́", 40)
	got := cleanText(in)
	if n := len([]rune(got)); n != 3 {
		t.Fatalf("cleanText kept %d runes (%q), want 3: the base letter and two marks", n, got)
	}
}
