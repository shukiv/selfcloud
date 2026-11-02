// SPDX-License-Identifier: AGPL-3.0-or-later

package audio

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture opens a file of testdata. The files are one second of silence with tags, made with ffmpeg (see docs/design.md
// section 15, decision 56); tag24.mp3 holds a Hebrew title and a PNG cover in ID3v2.4, tag23.mp3 the same in ID3v2.3.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustRead(t *testing.T, b []byte, picture bool) Result {
	t.Helper()
	res, err := read(bytes.NewReader(b), picture)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return res
}

func TestReadFixtures(t *testing.T) {
	cases := []struct {
		name   string
		format string
		title  string
		artist string
		album  string
		minMS  int64
		maxMS  int64
		cover  bool
	}{
		{"tag24.mp3", "mp3", "שלום עולם", "Artist One", "Album X", 1040, 1050, true},
		{"tag23.mp3", "mp3", "Title 23", "Artist 23", "", 1040, 1050, true},
		{"v1.mp3", "mp3", "Only V1", "V1 Artist", "", 1040, 1050, false},
		{"tag.flac", "flac", "Flac Title", "Flac Artist", "Flac Album", 1000, 1000, true},
		{"tag.m4a", "mp4", "M4A Title", "M4A Artist", "M4A Album", 1000, 1000, true},
		{"tag.ogg", "ogg", "Ogg Title", "Ogg Artist", "", 1000, 1000, false},
		{"tag.opus", "opus", "Opus Title", "Opus Artist", "", 990, 1010, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := fixture(t, c.name)
			if c.name == "v1.mp3" {
				// ffmpeg writes no ID3v1 tag, so the block is added here, to a file that has none.
				b = withID3v1(b, "Only V1", "V1 Artist", "")
			}
			res := mustRead(t, b, true)
			if res.Format != c.format {
				t.Errorf("format %q, want %q", res.Format, c.format)
			}
			if res.Tags.Title != c.title || res.Tags.Artist != c.artist || res.Tags.Album != c.album {
				t.Errorf("tags %q / %q / %q, want %q / %q / %q", res.Tags.Title, res.Tags.Artist, res.Tags.Album,
					c.title, c.artist, c.album)
			}
			if d := res.Tags.DurationMS; d < c.minMS || d > c.maxMS {
				t.Errorf("duration %d ms, want %d to %d", d, c.minMS, c.maxMS)
			}
			if got := res.Picture != nil; got != c.cover {
				t.Errorf("picture present %v, want %v", got, c.cover)
			}
			if c.cover && !bytes.HasPrefix(res.Picture, []byte("\x89PNG")) {
				t.Errorf("the picture is not the PNG cover that was embedded: % x", res.Picture[:min(8, len(res.Picture))])
			}
		})
	}
}

// A picture is read only when asked for: the tags path must not read or hold one.
func TestPictureOnlyWhenAsked(t *testing.T) {
	for _, name := range []string{"tag24.mp3", "tag.flac", "tag.m4a"} {
		res := mustRead(t, fixture(t, name), false)
		if res.Picture != nil {
			t.Errorf("%s: a picture was read with wantPicture false", name)
		}
		if res.Tags.Title == "" {
			t.Errorf("%s: the tags were not read", name)
		}
	}
}

// The same file read with its head cut off, or its tail cut off, must not panic and must not invent a value. Every cut
// length of every fixture is read: the reader's bounds are the test.
func TestEveryTruncationIsSafe(t *testing.T) {
	for _, name := range []string{"tag24.mp3", "v1.mp3", "tag.flac", "tag.m4a", "tag.ogg", "tag.opus"} {
		full := fixture(t, name)
		for n := 0; n < len(full); n++ {
			res, err := read(bytes.NewReader(full[:n]), true)
			if err == nil && res.Tags.DurationMS > 60_000 {
				t.Fatalf("%s cut to %d bytes: a duration of %d ms from a second of audio", name, n, res.Tags.DurationMS)
			}
		}
	}
}

// Random damage to the head and the tail of each fixture, from a fixed seed: a flipped byte must never panic the reader.
func TestRandomDamageIsSafe(t *testing.T) {
	rng := rand.New(rand.NewSource(56))
	for _, name := range []string{"tag24.mp3", "tag.flac", "tag.m4a", "tag.ogg", "tag.opus"} {
		orig := fixture(t, name)
		for i := 0; i < 400; i++ {
			b := append([]byte(nil), orig...)
			for k := 0; k < 1+rng.Intn(8); k++ {
				span := min(len(b), 600)
				p := rng.Intn(span)
				if rng.Intn(2) == 0 {
					p = len(b) - 1 - rng.Intn(span)
				}
				b[p] = byte(rng.Intn(256))
			}
			// Read is the only call: a panic would fail the test here.
			_, _ = read(bytes.NewReader(b), true)
		}
	}
}

// zeros is a file of head followed by zero bytes up to size, so a test can name a size no memory holds.
type zeros struct {
	head []byte
	size int64
	pos  int64
}

func (z *zeros) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case 0:
		z.pos = off
	case 1:
		z.pos += off
	case 2:
		z.pos = z.size + off
	}
	return z.pos, nil
}

func (z *zeros) Read(p []byte) (int, error) {
	if z.pos >= z.size {
		return 0, os.ErrClosed
	}
	n := 0
	for n < len(p) && z.pos < z.size {
		if z.pos < int64(len(z.head)) {
			p[n] = z.head[z.pos]
		} else {
			p[n] = 0
		}
		n++
		z.pos++
	}
	return n, nil
}

// id3v24 builds an ID3v2.4 tag with the given frames. Each frame's size field is written as the caller gives it, so a test
// can declare a size the file does not hold.
func id3v24(frames ...[]byte) []byte {
	var body bytes.Buffer
	for _, f := range frames {
		body.Write(f)
	}
	b := []byte("ID3\x04\x00\x00")
	n := body.Len()
	b = append(b, byte(n>>21), byte(n>>14), byte(n>>7), byte(n))
	return append(b, body.Bytes()...)
}

// id3Frame builds one v2.4 frame with a declared size and the given data.
func id3Frame(id string, declared uint32, data []byte) []byte {
	f := []byte(id)
	f = append(f, byte(declared>>21), byte(declared>>14), byte(declared>>7), byte(declared))
	f = append(f, 0, 0)
	return append(f, data...)
}

func TestID3FrameDeclaredPastTheFileIsRefused(t *testing.T) {
	// A title frame that claims 0x0FFFFFFF bytes, with three bytes of text: the frame is cut short, so no title is read, and
	// nothing is allocated for the claimed size.
	tag := id3v24(id3Frame("TIT2", 0x0FFFFFFF, []byte{3, 'A', 'b', 'c'}))
	b := append(tag, bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0x64}, 300)...)
	res, err := read(bytes.NewReader(b), true)
	if err != nil {
		t.Fatalf("a damaged tag must not refuse the file: %v", err)
	}
	if res.Tags.Title != "" {
		t.Fatalf("a frame cut short gave the title %q", res.Tags.Title)
	}
}

func TestID3TagDeclaredOverMaxReadIsIgnored(t *testing.T) {
	// The tag's own size is 256 MiB: it is not read at all, and the file is read from its first frame on.
	b := []byte("ID3\x04\x00\x00\x10\x00\x00\x00")
	b = append(b, bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0x64}, 300)...)
	z := &zeros{head: b, size: 1 << 20}
	res, err := read(z, true)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Format != "mp3" || res.Tags.Title != "" {
		t.Fatalf("got %+v", res)
	}
}

func TestID3PictureWithoutItsDescriptionEndIsNoCover(t *testing.T) {
	// An APIC frame whose description has no NUL end: the picture's start cannot be found, so no picture is read.
	data := []byte{0, 'i', 'm', 'a', 'g', 'e', '/', 'p', 'n', 'g', 0, 3, 'x', 'y', 'z'}
	tag := id3v24(id3Frame("APIC", uint32(len(data)), data))
	b := append(tag, bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0x64}, 300)...)
	res := mustRead(t, b, true)
	if res.Picture != nil {
		t.Fatalf("a picture without a description end was read: % x", res.Picture)
	}
}

func TestID3PictureOverMaxPictureIsNoCover(t *testing.T) {
	big := make([]byte, MaxPicture+1)
	big[0] = 0x89
	data := append([]byte{0, 'i', 'm', 'a', 'g', 'e', '/', 'p', 'n', 'g', 0, 3, 0}, big...)
	tag := id3v24(id3Frame("APIC", uint32(len(data)), data))
	b := append(tag, bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0x64}, 300)...)
	if res := mustRead(t, b, true); res.Picture != nil {
		t.Fatalf("a picture of %d bytes was read; the cap is %d", len(big), MaxPicture)
	}
}

func TestFLACBlockPastTheEndStopsTheWalk(t *testing.T) {
	// STREAMINFO is valid (44.1 kHz, 88200 samples: two seconds); a comment block claims 16 MiB and the file ends.
	info := make([]byte, 34)
	rate := uint32(44100)
	info[10] = byte(rate >> 12)
	info[11] = byte(rate >> 4)
	info[12] = byte(rate<<4) & 0xF0
	binary.BigEndian.PutUint32(info[14:18], 88200)
	var b bytes.Buffer
	b.WriteString("fLaC")
	b.Write([]byte{0x00, 0x00, 0x00, 34})
	b.Write(info)
	b.Write([]byte{0x84, 0xFF, 0xFF, 0xFF})
	res, err := read(bytes.NewReader(b.Bytes()), true)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Tags.DurationMS != 2000 {
		t.Fatalf("duration %d ms, want 2000", res.Tags.DurationMS)
	}
}

func TestFLACShortStreamInfoIsCorrupt(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("fLaC")
	b.Write([]byte{0x80, 0x00, 0x00, 0x10})
	b.Write(make([]byte, 16))
	if _, err := read(bytes.NewReader(b.Bytes()), true); err != ErrCorrupt {
		t.Fatalf("err %v, want ErrCorrupt", err)
	}
}

func TestFLACPictureDeclaredOverMaxPictureIsNoCover(t *testing.T) {
	be := binary.BigEndian
	pic := []byte{}
	pic = be.AppendUint32(pic, 3)
	pic = be.AppendUint32(pic, 9)
	pic = append(pic, "image/png"...)
	pic = be.AppendUint32(pic, 0)
	pic = append(pic, make([]byte, 16)...)
	pic = be.AppendUint32(pic, uint32(MaxPicture+10))
	pic = append(pic, 0x89, 'P', 'N', 'G')
	if _, _, ok := parsePictureBlock(pic); ok {
		t.Fatal("a picture declared over MaxPicture was accepted")
	}
}

func TestMP4BoxSizesAreChecked(t *testing.T) {
	// A top-level box whose size is below its own header is corrupt, not a loop.
	bad := []byte{0, 0, 0, 4, 'f', 't', 'y', 'p', 0, 0, 0, 0}
	if _, err := read(bytes.NewReader(bad), true); err == nil {
		t.Fatal("a box of size 4 was read")
	}
	// A box that runs past the end of the file is corrupt.
	past := []byte{0, 0, 0, 0x40, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}
	if _, err := read(bytes.NewReader(past), true); err == nil {
		t.Fatal("a box past the end was read")
	}
	// A file with a box but no moov has no tags: it is corrupt.
	noMoov := []byte{0, 0, 0, 12, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}
	if _, err := read(bytes.NewReader(noMoov), true); err != ErrCorrupt {
		t.Fatalf("no moov: err %v, want ErrCorrupt", err)
	}
}

func TestMP4MoovOverMaxReadIsTooLarge(t *testing.T) {
	// A moov box of 20 MiB, in a file that holds it: the reader refuses it before reading it.
	head := []byte{0, 0, 0, 12, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}
	moov := []byte{0x01, 0x40, 0, 0, 'm', 'o', 'o', 'v'}
	z := &zeros{head: append(head, moov...), size: 20<<20 + 20}
	if _, err := read(z, true); err != ErrTooLarge {
		t.Fatalf("err %v, want ErrTooLarge", err)
	}
}

func TestMP4LargeSizeBelowHeaderIsCorrupt(t *testing.T) {
	// The 64-bit size of a box is 4: below its 16-byte header.
	b := []byte{0, 0, 0, 1, 'f', 't', 'y', 'p', 0, 0, 0, 0, 0, 0, 0, 4}
	if _, err := read(bytes.NewReader(b), true); err != ErrCorrupt {
		t.Fatalf("err %v, want ErrCorrupt", err)
	}
}

func TestOggGranuleOverflowIsIgnored(t *testing.T) {
	ident := oggIdent44100()
	// The first page carries the identification packet alone.
	page := func(granule uint64, body []byte) []byte {
		h := []byte("OggS\x00\x02")
		h = binary.LittleEndian.AppendUint64(h, granule)
		h = append(h, make([]byte, 8)...) // serial and sequence
		h = append(h, 0, 0, 0, 0)         // crc, not checked here
		h = append(h, 1, byte(len(body)))
		return append(h, body...)
	}
	var b []byte
	b = append(b, page(0, ident)...)
	b = append(b, page(1<<62, nil)...)
	res := mustRead(t, b, false)
	if res.Tags.DurationMS != 0 {
		t.Fatalf("a granule of 2^62 gave %d ms", res.Tags.DurationMS)
	}
}

// withID3v1 returns b with a 128-byte ID3v1 tag at its end.
func withID3v1(b []byte, title, artist, album string) []byte {
	tag := make([]byte, 128)
	copy(tag, "TAG")
	copy(tag[3:33], title)
	copy(tag[33:63], artist)
	copy(tag[63:93], album)
	return append(append([]byte(nil), b...), tag...)
}

// oggIdent44100 is a Vorbis identification packet at 44100 Hz, so a duration is found if the granule is used.
func oggIdent44100() []byte {
	ident := []byte("\x01vorbis\x00\x00\x00\x00\x01")
	ident = binary.LittleEndian.AppendUint32(ident, 44100)
	return append(ident, make([]byte, 13)...)
}

func TestCleanText(t *testing.T) {
	cases := map[string]string{
		"  two   words \t":          "two words",
		"tab\x00and\x07bell":        "tab and bell",
		"rlo‮override":              "rlo override",
		"zwj‍kept":                  "zwj‍kept",
		"é":                        "é", // NFC: e with a combining acute is one letter
		"שלום עולם":                 "שלום עולם",
		strings.Repeat("ש", 300):    strings.Repeat("ש", MaxText),
		"<script>alert(1)</script>": "<script>alert(1)</script>",
	}
	for in, want := range cases {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnsynchronise(t *testing.T) {
	if got := unsynchronise([]byte{0xFF, 0x00, 0xE0, 0xFF, 0x00}); !bytes.Equal(got, []byte{0xFF, 0xE0, 0xFF}) {
		t.Fatalf("got % x", got)
	}
}

func TestSniff(t *testing.T) {
	yes := []string{"ID3\x04", "\xFF\xFB\x90", "fLaC", "OggS", "\x00\x00\x00\x18ftypM4A "}
	no := []string{"\x89PNG\r\n\x1a\n", "GIF89a", "hello", ""}
	for _, s := range yes {
		if !Sniff([]byte(s)) {
			t.Errorf("Sniff(%q) = false", s)
		}
	}
	for _, s := range no {
		if Sniff([]byte(s)) {
			t.Errorf("Sniff(%q) = true", s)
		}
	}
}
