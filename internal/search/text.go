// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"bytes"
	"errors"
	"io"
	"unicode/utf8"
)

// Content extraction (design section 14, slice q3, decision 46). A file's type is what its bytes say, never its name: the
// first bytes pick one of four families, and only the text family is read in this process. A document (PDF, a ZIP
// container of an office format, or an OLE file) goes to the converter, which reads it by its own content. Nothing in
// this package parses a document's structure.

// Family is the kind of a file, as its first bytes say.
type Family int

const (
	// FamilyText is any file whose bytes may be text. Whether it is text is decided by reading all of it.
	FamilyText Family = iota
	// FamilyPDF is a PDF: its first bytes are %PDF-.
	FamilyPDF
	// FamilyOffice is a ZIP container that holds an OOXML or ODF document (a [Content_Types].xml or a mimetype entry near
	// its start). A ZIP without one of those is not a document, and is binary.
	FamilyOffice
	// FamilyOLE is an OLE2 compound file, the old Word, Excel and PowerPoint formats.
	FamilyOLE
	// FamilyBinary is anything else that is not text: an image, an archive, an executable.
	FamilyBinary
)

// sniffLen is how many bytes the family is decided from. The entry names of a ZIP container are near its start.
const sniffLen = 8 << 10

var (
	pdfMagic  = []byte("%PDF-")
	zipMagics = [][]byte{[]byte("PK\x03\x04"), []byte("PK\x05\x06"), []byte("PK\x07\x08")}
	oleMagic  = []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1")
	ooxmlName = []byte("[Content_Types].xml")
	odfName   = []byte("mimetype")
)

// SniffFamily returns the family of a file from its first bytes. head is at most sniffLen bytes; a shorter head is a short
// file, and is decided the same way.
func SniffFamily(head []byte) Family {
	if bytes.HasPrefix(head, pdfMagic) {
		return FamilyPDF
	}
	if bytes.HasPrefix(head, oleMagic) {
		return FamilyOLE
	}
	for _, m := range zipMagics {
		if bytes.HasPrefix(head, m) {
			if bytes.Contains(head, ooxmlName) || bytes.Contains(head, odfName) {
				return FamilyOffice
			}
			return FamilyBinary
		}
	}
	return FamilyText
}

// ErrNotText is the answer for bytes that are not text: a NUL, or a sequence that is not valid UTF-8.
var ErrNotText = errors.New("search: the file is not text")

// errTextFull is what the output writer returns when the converter's output is over the text limit. The caller has the
// text it kept, and the output is marked truncated.
var errTextFull = errors.New("search: the text is over its limit")

// cleanRune reports whether a rune is kept in the text. Control characters are dropped, except tab, newline and carriage
// return, which separate words. NUL is not kept: a NUL makes the file not text (see readText).
func cleanRune(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' {
		return true
	}
	return !(r < 0x20 || (r >= 0x7f && r <= 0x9f))
}

// ValidText reads all of r and reports whether its bytes are text by the definition of ErrNotText: no NUL byte, and no
// byte sequence that is not valid UTF-8. A byte order mark is valid. The answer is ErrNotText, another read error, or nil.
// It reads in chunks and keeps one chunk in memory, whatever the size of r.
func ValidText(r io.Reader) error {
	_, _, err := ReadText(r, 1)
	return err
}

// ReadText reads all of r, which holds at most limit bytes of the file, and returns its text: the kept runes, cut to
// textMax bytes at a rune boundary. It returns ErrNotText when the bytes are not text: a NUL, or a byte sequence that is
// not valid UTF-8 anywhere in the file. truncated reports that the text was longer than textMax. The whole source is
// validated, so a file that is text for its first megabyte and binary after it is not text. A byte order mark at the
// start is dropped. The read is streamed: memory is bounded by textMax and a chunk, not by the file.
func ReadText(r io.Reader, textMax int64) (text []byte, truncated bool, err error) {
	if textMax <= 0 {
		return nil, false, errors.New("search: text limit must be positive")
	}
	const chunk = 64 << 10
	buf := make([]byte, chunk)
	out := make([]byte, 0, min(textMax, chunk))
	var carry []byte
	first := true
	for {
		n, rerr := r.Read(buf)
		data := append(carry, buf[:n]...)
		carry = nil
		i := 0
		for i < len(data) {
			if !utf8.FullRune(data[i:]) {
				if rerr == nil {
					break // a rune split by the chunk boundary: the next read completes it
				}
				return nil, false, ErrNotText
			}
			ch, size := utf8.DecodeRune(data[i:])
			if ch == utf8.RuneError && size == 1 {
				return nil, false, ErrNotText
			}
			if ch == 0 {
				return nil, false, ErrNotText
			}
			start := i
			i += size
			if first && ch == 0xfeff {
				first = false
				continue
			}
			first = false
			if !cleanRune(ch) {
				continue
			}
			if int64(len(out))+int64(size) <= textMax && !truncated {
				out = append(out, data[start:i]...)
			} else {
				truncated = true
			}
		}
		if i < len(data) {
			carry = append([]byte(nil), data[i:]...)
		}
		if rerr == io.EOF {
			if len(carry) > 0 {
				return nil, false, ErrNotText
			}
			return out, truncated, nil
		}
		if rerr != nil {
			return nil, false, rerr
		}
	}
}

// keepWhole drops the tail of b that a cut inside a rune left behind. A cut is at most utf8.UTFMax-1 bytes from a rune
// boundary, so at most that many bytes are dropped; a b that is not valid after that is left for ReadText to refuse.
func keepWhole(b []byte) []byte {
	for k := 0; k < utf8.UTFMax && !utf8.Valid(b) && len(b) > 0; k++ {
		b = b[:len(b)-1]
	}
	return b
}

// textWriter keeps the first max bytes of a converter's output and reports whether more followed. Once it is full it
// returns errTextFull, so the converter stops sending; the answer is then a success with truncated set.
type textWriter struct {
	max  int
	buf  []byte
	more bool
}

func (w *textWriter) Write(p []byte) (int, error) {
	room := w.max - len(w.buf)
	if len(p) <= room {
		w.buf = append(w.buf, p...)
		return len(p), nil
	}
	w.buf = append(w.buf, p[:room]...)
	w.more = true
	return room, errTextFull
}
