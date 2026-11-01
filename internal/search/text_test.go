// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// Content extraction (slice q3, decision 46): the rules that decide what a file's text is, with no store.

func TestSniffFamilyDecidesFromBytesNotName(t *testing.T) {
	ooxml := append([]byte("PK\x03\x04"), []byte("....[Content_Types].xml....")...)
	odf := append([]byte("PK\x03\x04"), []byte("....mimetype....")...)
	plainZip := append([]byte("PK\x03\x04"), []byte("....notes.txt....")...)
	cases := []struct {
		name string
		head []byte
		want Family
	}{
		{"pdf", []byte("%PDF-1.7\n"), FamilyPDF},
		{"ooxml container", ooxml, FamilyOffice},
		{"odf container", odf, FamilyOffice},
		{"zip without a document", plainZip, FamilyBinary},
		{"ole2 compound file", append([]byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"), 0, 0), FamilyOLE},
		{"utf-8 text", []byte("שלום לכולם"), FamilyText},
		{"png bytes are a family text candidate, refused later by ReadText", []byte("\x89PNG\r\n\x1a\n"), FamilyText},
	}
	for _, c := range cases {
		if got := SniffFamily(c.head); got != c.want {
			t.Errorf("%s: family %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReadTextRefusesWhatIsNotText(t *testing.T) {
	cases := map[string][]byte{
		"NUL byte":               []byte("abc\x00def"),
		"invalid UTF-8":          []byte("abc\xffdef"),
		"rune cut at end":        []byte("abc\xd7"),
		"UTF-16 with BOM (NUL)":  []byte("\xff\xfeh\x00i\x00"),
		"lone continuation byte": []byte("\x80"),
	}
	for name, in := range cases {
		if _, _, err := ReadText(bytes.NewReader(in), 1<<20); !errors.Is(err, ErrNotText) {
			t.Errorf("%s: err %v, want ErrNotText", name, err)
		}
	}
}

func TestReadTextValidatesTheWholeSource(t *testing.T) {
	// A file that is text for its first megabyte and binary after it is not text: the whole source is checked, even
	// though only the first textMax bytes are kept.
	in := append(bytes.Repeat([]byte("a"), 2<<20), 0xff)
	if _, _, err := ReadText(bytes.NewReader(in), 1<<20); !errors.Is(err, ErrNotText) {
		t.Fatalf("err %v, want ErrNotText for binary at the end", err)
	}
}

func TestReadTextKeepsHebrewAndArabicAndMarksTheCut(t *testing.T) {
	in := []byte("שָׁלוֹם לכולם. التقرير السنوي")
	text, truncated, err := ReadText(bytes.NewReader(in), 1<<20)
	if err != nil || truncated {
		t.Fatalf("err %v truncated %v", err, truncated)
	}
	if string(text) != string(in) {
		t.Errorf("text %q, want %q", text, in)
	}
	// A cut inside a two-byte Hebrew letter ends on a rune boundary and says so.
	text, truncated, err = ReadText(bytes.NewReader(in), 5)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || !utf8.Valid(text) || len(text) > 5 {
		t.Errorf("cut text %q (%d bytes) truncated %v", text, len(text), truncated)
	}
}

func TestReadTextDropsControlsAndTheByteOrderMark(t *testing.T) {
	in := []byte("\xef\xbb\xbfone\x01\x07two\tthree\r\nfour\x7f\u0085five")
	text, _, err := ReadText(bytes.NewReader(in), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if want := "onetwo\tthree\r\nfourfive"; string(text) != want {
		t.Errorf("text %q, want %q", text, want)
	}
}

func TestReadTextAcrossAChunkBoundary(t *testing.T) {
	// A two-byte letter straddles the 64 KiB read boundary: it must not be refused or split.
	in := append(bytes.Repeat([]byte("a"), (64<<10)-1), []byte("ש")...)
	in = append(in, bytes.Repeat([]byte("b"), 10)...)
	text, _, err := ReadText(bytes.NewReader(in), 1<<20)
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if string(text) != string(in) {
		t.Errorf("chunk boundary changed the text (%d bytes, want %d)", len(text), len(in))
	}
}

func TestReadTextHugeLineIsCutNotRead(t *testing.T) {
	in := bytes.Repeat([]byte("x"), 5<<20)
	text, truncated, err := ReadText(bytes.NewReader(in), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(text) != 1<<20 {
		t.Errorf("huge line: %d bytes kept, truncated %v", len(text), truncated)
	}
}

func TestTextWriterStopsAtTheLimit(t *testing.T) {
	w := &textWriter{max: 4}
	n, err := w.Write([]byte("abcdef"))
	if n != 4 || !errors.Is(err, errTextFull) || !w.more || string(w.buf) != "abcd" {
		t.Errorf("write: n %d err %v more %v buf %q", n, err, w.more, w.buf)
	}
}

func TestKeepWholeDropsAPartialRune(t *testing.T) {
	// "ש" is two bytes; a cut after its first byte leaves an incomplete rune, which keepWhole drops.
	b := []byte("ab" + "ש")[:3]
	if got := keepWhole(b); string(got) != "ab" {
		t.Errorf("keepWhole %q, want %q", got, "ab")
	}
}

func TestParseSnippetReadsMarksAsRanges(t *testing.T) {
	raw := "the \x01report\x02 of \x01reports\x02 here"
	sn := parseSnippet(raw)
	if sn.Text != "the report of reports here" {
		t.Fatalf("text %q", sn.Text)
	}
	if len(sn.Ranges) != 2 || sn.Ranges[0] != [2]int{4, 10} || sn.Ranges[1] != [2]int{14, 21} {
		t.Errorf("ranges %v", sn.Ranges)
	}
	for _, r := range sn.Ranges {
		if got := sn.Text[r[0]:r[1]]; !strings.HasPrefix(got, "report") {
			t.Errorf("range reads %q", got)
		}
	}
}

func TestClipSnippetKeepsTheMatchInAShortWindow(t *testing.T) {
	text := strings.Repeat("a", 1000) + "שלום" + strings.Repeat("b", 1000)
	start := strings.Index(text, "שלום")
	sn := clipSnippet(text, [][2]int{{start, start + len("שלום")}})
	if len(sn.Text) > snippetMax || !utf8.ValidString(sn.Text) {
		t.Fatalf("snippet %d bytes, valid %v", len(sn.Text), utf8.ValidString(sn.Text))
	}
	if len(sn.Ranges) != 1 || sn.Text[sn.Ranges[0][0]:sn.Ranges[0][1]] != "שלום" {
		t.Errorf("range after clipping: %v in %d bytes", sn.Ranges, len(sn.Text))
	}
}

func TestParseSnippetOfAHugeTokenIsBounded(t *testing.T) {
	// FTS returns a whole token; one huge word must not make a huge snippet.
	raw := strings.Repeat("a", 1<<20) + "\x01find\x02" + strings.Repeat("b", 1<<20)
	sn := parseSnippet(raw)
	if len(sn.Text) > snippetMax {
		t.Fatalf("snippet of a huge token is %d bytes", len(sn.Text))
	}
	if len(sn.Ranges) != 1 || sn.Text[sn.Ranges[0][0]:sn.Ranges[0][1]] != "find" {
		t.Errorf("ranges %v", sn.Ranges)
	}
}

// FuzzReadText: whatever the bytes, a kept text is valid UTF-8, has no NUL, and is at most the limit.
func FuzzReadText(f *testing.F) {
	f.Add([]byte("שלום"), int64(3))
	f.Add([]byte("a\x00b"), int64(10))
	f.Add([]byte("\xff\xfe"), int64(1))
	f.Fuzz(func(t *testing.T, in []byte, limit int64) {
		if limit < 1 || limit > 1<<16 {
			t.Skip()
		}
		text, _, err := ReadText(bytes.NewReader(in), limit)
		if err != nil {
			return
		}
		if !utf8.Valid(text) || bytes.IndexByte(text, 0) >= 0 || int64(len(text)) > limit {
			t.Fatalf("kept %q (limit %d)", text, limit)
		}
	})
}
