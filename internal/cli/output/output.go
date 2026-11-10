// SPDX-License-Identifier: AGPL-3.0-or-later

// Package output writes what the remote commands print: tables, key=value lines, JSON, and the error line or envelope on
// stderr.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

// KV is one key=value line.
type KV struct {
	Key   string
	Value string
}

// clean makes a value one cell: a tab, a line break or another control character would break a table or a line, so it
// becomes a space.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, s)
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

// JSON writes v as one indented JSON document and a newline.
func JSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
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
