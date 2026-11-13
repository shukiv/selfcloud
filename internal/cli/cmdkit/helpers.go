// SPDX-License-Identifier: AGPL-3.0-or-later

package cmdkit

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strconv"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/id"
)

// IDArg checks one positional id of the given prefix. what names it in the error, with its article.
func IDArg(prefix, what, arg string) (string, error) {
	if !id.Valid(prefix, arg) {
		return "", exitcode.Usagef("%s is named by its %s_ id", what, prefix)
	}
	return arg, nil
}

// NoArgs refuses positional arguments.
func NoArgs(name string, pos []string) error {
	if len(pos) != 0 {
		return exitcode.Usagef("%s takes no arguments", name)
	}
	return nil
}

// FlagGiven reports whether the command line set the flag name, even to its default.
func FlagGiven(set *flag.FlagSet, name string) bool {
	given := false
	set.Visit(func(f *flag.Flag) { given = given || f.Name == name })
	return given
}

// ErrNotA is the error of an answer that is not the document the command expects. It never repeats the answer, which may
// hold a secret.
func ErrNotA(what string) error { return errors.New("the server's answer is not " + what) }

// DecodeAs reads a document into v.
func DecodeAs(body []byte, v any, what string) error {
	if err := json.Unmarshal(body, v); err != nil {
		return ErrNotA(what)
	}
	return nil
}

// Itoa writes n as a decimal integer.
func Itoa(n int) string { return strconv.Itoa(n) }

// PrintDone writes the result of a change that answers with no body.
func PrintDone(c *Command, target string) error {
	if c.G.JSON {
		return output.JSON(c.Stdout, map[string]string{"target": target, "status": "done"})
	}
	return output.KeyValues(c.Stdout, []output.KV{{Key: "target", Value: target}, {Key: "status", Value: "done"}})
}

// DecodeItems reads each raw item of a list into a T.
func DecodeItems[T any](items []json.RawMessage, what string) ([]T, error) {
	out := make([]T, 0, len(items))
	for _, raw := range items {
		var v T
		if err := DecodeAs(raw, &v, what); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// PrintList writes a list: the items as given under --json, otherwise a table. A note on stderr says when another page
// exists.
func PrintList[T any](c *Command, items []T, more bool, headers []string, row func(T) []string) error {
	if more {
		_, _ = io.WriteString(c.Stderr, "selfcloud: more items exist; --all prints every page\n")
	}
	if c.G.JSON {
		if items == nil {
			items = []T{}
		}
		return output.JSON(c.Stdout, items)
	}
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		rows = append(rows, row(it))
	}
	return output.Table(c.Stdout, headers, rows)
}

// ConfirmByID asks before a change that cannot be undone. These targets have no name to type, so the person types the
// target's id. Without a terminal and without --yes the command refuses before anything is sent.
func ConfirmByID(yes bool, target, what string, stderr io.Writer) error {
	return files.Confirm(yes, target, what, stderr)
}

// Str returns the value a nullable string points to, or "".
func Str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
