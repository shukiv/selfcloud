// SPDX-License-Identifier: AGPL-3.0-or-later

// Package audio reads the title, artist, album, duration and embedded cover picture of audio files (design section 14,
// slice x3, decision 56). It reads files that any user may have uploaded, so every read is bounded: each read has a cap,
// each container has a cap on how many boxes, blocks, frames or pages it may hold, and a file is read at the places a
// tag can be (its head and its tail) rather than whole. The result is text, never markup, and a picture is returned as
// the bytes of the file, which the caller must decode before it is shown (thumb.Make does that).
//
// The formats are MP3 (ID3v2.2 to v2.4 and ID3v1; duration from a Xing, Info or VBRI header, or estimated from the
// bitrate for a constant-rate file), FLAC (Vorbis comments and a picture block; duration from STREAMINFO), Ogg Vorbis
// and Ogg Opus (comments; duration from the last page's granule position) and MP4 or M4A (iTunes-style tags in
// moov/udta/meta/ilst, and a cover in covr; duration from mvhd).
//
// A header that is malformed refuses the whole file with ErrCorrupt. A tag block that is malformed or cut short keeps what
// was read before it and skips the rest, so a file with a damaged tag still gives its duration. Panics are recovered and
// become ErrCorrupt, so a bug here is a refusal of one file, never a crash of the server.
package audio

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Errors returned by Read. The child process maps each to an exit code.
var (
	// ErrUnsupported is a file that is none of the formats above, by its first bytes.
	ErrUnsupported = errors.New("audio: not a format this reader knows")
	// ErrCorrupt is a file whose container header cannot be read.
	ErrCorrupt = errors.New("audio: the file cannot be read")
	// ErrTooLarge is a header that declares more than a cap allows.
	ErrTooLarge = errors.New("audio: a declared size is over a limit")
)

// The caps. Each is a bound on memory or work for one file, whatever the file says.
const (
	// MaxText is how many runes one tag value keeps. A longer value is cut.
	MaxText = 256
	// MaxPicture is the largest picture that is read. A larger one is no cover.
	MaxPicture = 8 << 20
	// maxRead is the most bytes one read takes. Every block read is checked against it.
	maxRead = 16 << 20
	// maxTotalRead is the most bytes one file may make the reader read, over all its reads. A cover of 8 MiB, a tag and a
	// moov box fit; a file that asks for more is refused as too large.
	maxTotalRead = 64 << 20
	// maxFrames bounds the ID3v2 frames of one tag; maxBlocks the FLAC metadata blocks; maxAtoms the MP4 boxes of one
	// level; maxPages the Ogg pages read at the head; maxComments the Vorbis comments of one packet.
	maxFrames   = 512
	maxBlocks   = 256
	maxAtoms    = 4096
	maxPages    = 512 // a comment packet with an 8 MiB picture spans about 170 pages of 64 KiB; the total read cap bounds the rest
	maxComments = 1024
	// tailWindow is how far from the end the Ogg granule and the ID3v1 tag are looked for.
	tailWindow = 64 << 10
	// syncWindow is how far from the audio start an MP3 frame is looked for.
	syncWindow = 64 << 10
)

// Tags are the values read from a file. DurationMS is 0 when it is not known.
type Tags struct {
	Title      string
	Artist     string
	Album      string
	DurationMS int64
}

// Result is what Read found. Picture is the bytes of the front cover, or of the first picture when there is no
// front cover, and nil when there is none, or when the caller did not ask for pictures.
type Result struct {
	Format  string // "mp3", "flac", "ogg", "opus" or "mp4"
	Tags    Tags
	Picture []byte
}

// Sniff reports whether head, the first bytes of a file, begins like one of the formats. It is the cheap test a caller
// runs before it asks for a full read.
func Sniff(head []byte) bool {
	switch {
	case bytes.HasPrefix(head, []byte("ID3")):
		return true
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return true
	case bytes.HasPrefix(head, []byte("fLaC")), bytes.HasPrefix(head, []byte("OggS")):
		return true
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		return true
	}
	return false
}

// maxDuration is the longest duration a file may report: a week. A duration over it is a bad header, not a song, so the
// reader gives 0 for it and keeps the tags it read (the title and the artist are not lost to a bad length).
const maxDuration = 7 * 24 * 60 * 60 * 1000

// Read reads the file r holds. r must be seekable; its length is taken from it. With wantPicture false, no picture is
// read, which is the tags path. Read never panics: a panic inside the parsers is recovered as ErrCorrupt. The parsers
// are tested through read, which does not recover, so a bounds bug shows as a failing test and not as a refusal.
func Read(r io.ReadSeeker, wantPicture bool) (res Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			res, err = Result{}, ErrCorrupt
		}
	}()
	return read(r, wantPicture)
}

// read is Read without the recover. See Read.
func read(r io.ReadSeeker, wantPicture bool) (Result, error) {
	res, err := readFormat(r, wantPicture)
	if res.Tags.DurationMS < 0 || res.Tags.DurationMS > maxDuration {
		res.Tags.DurationMS = 0
	}
	return res, err
}

// readFormat dispatches on the first bytes of the file.
func readFormat(r io.ReadSeeker, wantPicture bool) (Result, error) {
	d, err := newReader(r)
	if err != nil {
		return Result{}, err
	}
	head, err := d.at(0, int(min(d.size, 12)))
	if err != nil {
		return Result{}, ErrUnsupported
	}
	switch {
	case bytes.HasPrefix(head, []byte("ID3")) || (len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0):
		return readMP3(d, wantPicture)
	case bytes.HasPrefix(head, []byte("fLaC")):
		return readFLAC(d, wantPicture)
	case bytes.HasPrefix(head, []byte("OggS")):
		return readOgg(d, wantPicture)
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		return readMP4(d, wantPicture)
	}
	return Result{}, ErrUnsupported
}

// reader reads bounded slices of a seekable file.
type reader struct {
	r    io.ReadSeeker
	size int64
	used int64 // the bytes read so far, against maxTotalRead
}

func newReader(r io.ReadSeeker) (*reader, error) {
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, ErrUnsupported
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, ErrUnsupported
	}
	return &reader{r: r, size: size}, nil
}

// at returns n bytes at off. A read past the end, or one over maxRead, is an error; the caller decides what that means.
func (d *reader) at(off int64, n int) ([]byte, error) {
	if off < 0 || n < 0 || off > d.size || int64(n) > d.size-off {
		return nil, ErrCorrupt
	}
	if n > maxRead {
		return nil, ErrTooLarge
	}
	// The whole file's reads are capped, not only each one: a file of many blocks, each under the per-read cap, must not
	// make the child read gigabytes.
	if d.used+int64(n) > maxTotalRead {
		return nil, ErrTooLarge
	}
	d.used += int64(n)
	if _, err := d.r.Seek(off, io.SeekStart); err != nil {
		return nil, ErrCorrupt
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(d.r, b); err != nil {
		return nil, ErrCorrupt
	}
	return b, nil
}

// setIfEmpty sets a tag value when it is not set yet and the new one is not empty.
func setIfEmpty(dst *string, v string) {
	if *dst == "" && v != "" {
		*dst = v
	}
}

// blankRune reports whether r draws nothing although it is a letter: the Hangul fillers (U+115F, U+1160, U+3164, U+FFA0)
// and the Braille blank (U+2800). A title of only these reads as blank, so it is treated as no title.
func blankRune(r rune) bool {
	switch r {
	case 0x115F, 0x1160, 0x3164, 0xFFA0, 0x2800:
		return true
	}
	return false
}

// cleanText makes a tag value plain text: Unicode-normalised (NFC), with control characters, bidirectional controls and
// other format characters turned to spaces (zero-width joiners kept, as Arabic and Persian need them), runs of white space
// made one space, and the value cut to MaxText runes. The value is then shown with its text only, never as markup.
func cleanText(s string) string {
	s = norm.NFC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	marks := 0 // combining marks in a row: a run of more than two is a flood, not a letter
	for _, r := range s {
		switch {
		case r == 0x200C || r == 0x200D:
			b.WriteRune(r)
			marks = 0
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), blankRune(r):
			// Fillers that draw nothing become spaces, so a title made only of them is empty and the listing falls back
			// to the file name.
			b.WriteByte(' ')
			marks = 0
		case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r):
			marks++
			if marks <= 2 {
				b.WriteRune(r)
			}
		default:
			b.WriteRune(r)
			marks = 0
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if utf8.RuneCountInString(out) > MaxText {
		out = string([]rune(out)[:MaxText])
	}
	return out
}
