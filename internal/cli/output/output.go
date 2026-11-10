// SPDX-License-Identifier: AGPL-3.0-or-later

// Package output writes what the remote commands print: tables, key=value lines, JSON, and the error line or envelope on
// stderr.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

// KV is one key=value line.
type KV struct {
	Key   string
	Value string
}

// clean makes a value one cell. Server text is untrusted, so every rune that a terminal could act on, or that could make
// the output show something other than the bytes, becomes a space: control characters (C0, DEL and C1, such as the
// one-byte CSI U+009B and OSC U+009D), format characters (bidi overrides such as U+202E, zero-width characters, the byte
// order mark), and the line and paragraph separators. A tab or a line break would break a table or a line, so it goes too.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unsafeRune(r) {
			return ' '
		}
		return r
	}, s)
}

// unsafeRune reports whether a terminal could act on r, or whether r could make the output show other than its bytes.
func unsafeRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' '
}

// escapeUnsafe rewrites each unsafe rune in a JSON document as its \uXXXX escape. Inside a JSON string the escape decodes
// to the same rune, so the values do not change; a terminal shows the escape, not the rune. Only runes from U+007F up are
// rewritten: the ASCII controls are JSON's own whitespace outside a string, and inside one the encoder already escapes them.
func escapeUnsafe(data []byte) []byte {
	var out bytes.Buffer
	for len(data) > 0 {
		r, n := utf8.DecodeRune(data)
		if r >= 0x7f && unsafeRune(r) {
			fmt.Fprintf(&out, `\u%04x`, r)
		} else {
			out.Write(data[:n])
		}
		data = data[n:]
	}
	return out.Bytes()
}

// Table writes a tab-aligned table with a header row. Each row has as many cells as there are headers.
func Table(w io.Writer, headers []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	cells := make([]string, len(headers))
	for i, h := range headers {
		cells[i] = clean(h)
	}
	if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		for i := range cells {
			v := ""
			if i < len(row) {
				v = clean(row[i])
			}
			cells[i] = v
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// KeyValues writes one key=value line per pair, in order.
func KeyValues(w io.Writer, pairs []KV) error {
	for _, p := range pairs {
		if _, err := fmt.Fprintf(w, "%s=%s\n", clean(p.Key), clean(p.Value)); err != nil {
			return err
		}
	}
	return nil
}

// JSON writes v as one indented JSON document and a newline. Unsafe runes in it are escaped.
func JSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(escapeUnsafe(data), '\n'))
	return err
}

// Indented writes a server's JSON document, indented, with a newline. The document's values are kept; unsafe runes are
// escaped. It returns an error, and writes nothing, when raw is not JSON.
func Indented(w io.Writer, raw []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return err
	}
	_, err := w.Write(append(escapeUnsafe(buf.Bytes()), '\n'))
	return err
}

// Fail reports err on stderr and returns its exit code. Under asJSON it writes the API's envelope,
// {"error":{"code":...,"message":...}}; otherwise one line. A nil error writes nothing and returns 0.
func Fail(stderr io.Writer, asJSON bool, err error) int {
	code, name, msg := exitcode.Of(err)
	if err == nil {
		return exitcode.OK
	}
	msg = clean(msg)
	if asJSON {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		env.Error.Code = name
		env.Error.Message = msg
		data, _ := json.Marshal(env)
		_, _ = fmt.Fprintln(stderr, string(data))
		return code
	}
	_, _ = fmt.Fprintf(stderr, "selfcloud: %s: %s\n", name, msg)
	return code
}
