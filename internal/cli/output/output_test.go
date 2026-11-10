// SPDX-License-Identifier: AGPL-3.0-or-later

package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

func TestFailHumanLine(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	code := Fail(&errOut, false, &client.APIError{Status: 401, Code: "unauthenticated", Message: "the key is not valid"})
	if code != exitcode.Refused {
		t.Errorf("exit = %d, want %d", code, exitcode.Refused)
	}
	if got, want := errOut.String(), "selfcloud: unauthenticated: the key is not valid (HTTP 401)\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestFailJSONEnvelope: under --json an error is the API's envelope, on stderr, and nothing goes to stdout.
func TestFailJSONEnvelope(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	code := Fail(&errOut, true, exitcode.Usagef("the name is not valid"))
	if code != exitcode.Usage {
		t.Errorf("exit = %d, want %d", code, exitcode.Usage)
	}
	want := `{"error":{"code":"usage","message":"the name is not valid"}}` + "\n"
	if errOut.String() != want {
		t.Errorf("stderr = %q, want %q", errOut.String(), want)
	}
}

func TestFailNilIsDone(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	if code := Fail(&errOut, false, nil); code != 0 || errOut.Len() != 0 {
		t.Errorf("Fail(nil) = %d, stderr %q", code, errOut.String())
	}
}

func TestFailMessageWithControlCharactersIsOneLine(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	Fail(&errOut, false, errors.New("first\nsecond\tthird"))
	if strings.Count(errOut.String(), "\n") != 1 {
		t.Errorf("stderr = %q, want one line", errOut.String())
	}
}

func TestTableWithHeaderAndSanitizedCells(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := Table(&b, []string{"NAME", "URL"}, [][]string{{"home", "https://a.example"}, {"x\ty", "two\nlines"}}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	if !strings.HasPrefix(lines[0], "NAME") || !strings.Contains(lines[0], "URL") {
		t.Errorf("header = %q", lines[0])
	}
	if strings.Count(b.String(), "\t") != 0 {
		t.Errorf("a tab survived into the table: %q", b.String())
	}
}

func TestKeyValues(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := KeyValues(&b, []KV{{"name", "SelfCloud"}, {"features", ""}}); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "name=SelfCloud\nfeatures=\n"; got != want {
		t.Errorf("KeyValues = %q, want %q", got, want)
	}
}

// ch returns the rune with the given code point. The source holds only ASCII, so no format character sits raw in it.
func ch(n rune) string { return string(n) }

// hostile are server strings a terminal would act on, or that could make the output lie: a C1 CSI (U+009B), a C1 OSC
// (U+009D), a bidi override (U+202E), the line and paragraph separators (U+2028, U+2029), a zero-width space (U+200B)
// and a byte order mark (U+FEFF). Each must leave nothing behind but a space.
var hostile = []string{
	"\u009b31m",
	"\u009d0;title\u0007",
	"a" + ch(0x202e) + "b",
	"a" + ch(0x2028) + "b",
	"a" + ch(0x2029) + "b",
	ch(0x200b) + "z",
	ch(0xfeff) + "z",
}

// unchanged are plain strings that must come through as they are.
var unchanged = []string{"x y", "naïve 日本", "ünïcode ✓"}

// badRunes are the runes that must not reach a terminal from any path.
var badRunes = []rune{0x009b, 0x009d, 0x202e, 0x2028, 0x2029, 0x0007, 0x200b, 0xfeff}

func TestCleanReplacesControlAndFormatRunes(t *testing.T) {
	t.Parallel()
	for _, in := range hostile {
		got := clean(in)
		for _, r := range badRunes {
			if strings.ContainsRune(got, r) {
				t.Errorf("clean(%q) = %q, still holds %U", in, got, r)
			}
		}
	}
	if got := clean("a\u009b31mb"); got != "a 31mb" {
		t.Errorf("clean of a C1 CSI = %q, want %q", got, "a 31mb")
	}
	for _, in := range unchanged {
		if got := clean(in); got != in {
			t.Errorf("clean(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestTableAndKeyValuesCleanHostileCells(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := Table(&b, []string{"NAME" + ch(0x202e), "URL"}, [][]string{{"\u009b31mhome", "https://a.example" + ch(0x2028)}}); err != nil {
		t.Fatal(err)
	}
	if err := KeyValues(&b, []KV{{"name\u009d0;title\u0007", ch(0x202e) + "evil"}}); err != nil {
		t.Fatal(err)
	}
	for _, r := range badRunes {
		if strings.ContainsRune(b.String(), r) {
			t.Errorf("output holds %U: %q", r, b.String())
		}
	}
	if !strings.Contains(b.String(), "name 0;title") {
		t.Errorf("output = %q, want the key cleaned in place", b.String())
	}
}

// TestFailCleansTheMessageInBothModes: a server message printed in text mode or in the JSON envelope is cleaned first.
func TestFailCleansTheMessageInBothModes(t *testing.T) {
	t.Parallel()
	hostileErr := errors.New("bad\u009b31m name " + ch(0x202e) + "evil" + ch(0x2028))
	for _, asJSON := range []bool{false, true} {
		var errOut bytes.Buffer
		Fail(&errOut, asJSON, hostileErr)
		for _, r := range badRunes {
			if strings.ContainsRune(errOut.String(), r) {
				t.Errorf("json %v: stderr holds %U: %q", asJSON, r, errOut.String())
			}
		}
	}
}

// TestJSONEscapesFormatRunes: a JSON document keeps its values, and a terminal sees no raw control or format rune. The
// escape is valid JSON that decodes to the same string.
func TestJSONEscapesFormatRunes(t *testing.T) {
	t.Parallel()
	v := map[string]string{"a": "x" + ch(0x009b) + "31m" + ch(0x202e) + "y" + ch(0x2028), "b": "plain 日"}
	var b bytes.Buffer
	if err := JSON(&b, v); err != nil {
		t.Fatal(err)
	}
	for _, r := range badRunes {
		if strings.ContainsRune(b.String(), r) {
			t.Errorf("JSON output holds %U raw: %q", r, b.String())
		}
	}
	var back map[string]string
	if err := json.Unmarshal(b.Bytes(), &back); err != nil {
		t.Fatalf("the output is not JSON: %v", err)
	}
	if back["a"] != v["a"] || back["b"] != v["b"] {
		t.Errorf("the values changed: %q", back)
	}
}

// TestIndentedPassesTheDocumentThrough: a server's JSON document is indented and keeps its values, with no raw format rune.
func TestIndentedPassesTheDocumentThrough(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"name":"` + ch(0x009b) + `31m","n":1}`)
	var b bytes.Buffer
	if err := Indented(&b, raw); err != nil {
		t.Fatal(err)
	}
	for _, r := range badRunes {
		if strings.ContainsRune(b.String(), r) {
			t.Errorf("indented output holds %U raw: %q", r, b.String())
		}
	}
	var back map[string]any
	if err := json.Unmarshal(b.Bytes(), &back); err != nil || back["name"] != ch(0x009b)+"31m" {
		t.Errorf("decoded = %v, %v; want the same name", back, err)
	}
	if err := Indented(&b, []byte("<html>")); err == nil {
		t.Error("a non-JSON answer was indented")
	}
}

// TestInvalidUTF8NeverReachesTheTerminal: a lone byte 0x9b (a CSI on 8-bit terminals) or any broken sequence is not text.
// Indented must write U+FFFD for it, and JSON must not write it either.
func TestInvalidUTF8NeverReachesTheTerminal(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"{\"a\":\"x\x9by\"}", "{\"a\":\"\xff\"}", "{\"a\":\"\xe2\x80\"}"} {
		var b bytes.Buffer
		if err := Indented(&b, []byte(raw)); err != nil {
			t.Fatalf("Indented(%q): %v", raw, err)
		}
		if !utf8.Valid(b.Bytes()) || bytes.IndexByte(b.Bytes(), 0x9b) >= 0 {
			t.Errorf("Indented(%q) wrote bytes that are not valid UTF-8: % x", raw, b.Bytes())
		}
		var back map[string]string
		if err := json.Unmarshal(b.Bytes(), &back); err != nil {
			t.Errorf("Indented(%q): the output is not JSON: %v", raw, err)
		}
	}
	var b bytes.Buffer
	if err := JSON(&b, map[string]string{"a": "x\x9by\xff"}); err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(b.Bytes()) || bytes.IndexByte(b.Bytes(), 0x9b) >= 0 {
		t.Errorf("JSON wrote bytes that are not valid UTF-8: % x", b.Bytes())
	}
}

// TestAstralFormatRunesRoundTrip: a rune above U+FFFF is escaped as a surrogate pair, so the value decodes to the same
// rune. The tag characters (U+E0001, U+E0020 to U+E007F) and the musical format controls (U+1D173 to U+1D17A) are format
// characters, so they are escaped.
func TestAstralFormatRunesRoundTrip(t *testing.T) {
	t.Parallel()
	for _, r := range []rune{0xE0001, 0xE0020, 0xE007F, 0x1D173, 0x1D17A} {
		raw := `{"a":"x` + string(r) + `y"}`
		var b bytes.Buffer
		if err := Indented(&b, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsRune(b.String(), r) {
			t.Errorf("U+%X was written raw: %q", r, b.String())
		}
		var back map[string]string
		if err := json.Unmarshal(b.Bytes(), &back); err != nil || back["a"] != "x"+string(r)+"y" {
			t.Errorf("U+%X: decoded %q, %v; want the same rune back", r, back["a"], err)
		}
	}
}

func TestJSONIsIndentedWithANewline(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := JSON(&b, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "{\n  \"a\": 1\n}\n"; got != want {
		t.Errorf("JSON = %q, want %q", got, want)
	}
}
