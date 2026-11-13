// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/cmdkit"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/cli/profile"
	"github.com/shukiv/selfcloud/internal/names"
)

// command is one command's flags and streams. begin makes it; the command adds its own flags before Parse.
type command struct{ *cmdkit.Command }

// begin starts a command with the global flags. withAccount adds --account, which the command then needs.
func begin(name string, withAccount bool, stdout, stderr io.Writer) *command {
	return &command{cmdkit.New(name, withAccount, stdout, stderr)}
}

// fail reports err and returns its exit code. A device token that gets "not found" from a route of an account is told why,
// in plain output only: the server never lets a token manage an account, and answers as if the account were not there.
// A command whose routes need an installation admin key tells a refused key or token the same way.
func (c *command) fail(err error) int {
	code := output.Fail(c.Stderr, c.G.JSON, err)
	var ae *client.APIError
	if !c.G.JSON && c.AccountUsed && c.Kind == profile.KindDevice && errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		_, _ = io.WriteString(c.Stderr, "selfcloud: hint: this needs an account key; a signed-in device cannot manage an account\n")
	}
	if !c.G.JSON && c.AdminRoutes && c.Kind != profile.KindAdmin && errors.As(err, &ae) && ae.Status == http.StatusForbidden {
		_, _ = io.WriteString(c.Stderr, "selfcloud: hint: this needs an installation admin key\n")
	}
	return code
}

// nameArg checks a name with the API's rules, so a name it would refuse costs no request. The name is sent as it was typed.
func nameArg(arg string) (string, error) {
	if _, _, err := names.Clean(arg); err != nil {
		return "", exitcode.Usagef("the name is not valid: %s", err)
	}
	return arg, nil
}

// confirmByName asks before a removal. Without a terminal and without --yes it refuses before anything is read or sent.
// At a terminal the person types the target's name, which is read from route first.
func confirmByName(cn *conn.Conn, route string, yes bool, what string, stderr io.Writer) error {
	phrase := ""
	if !yes && files.IsTerminal(files.Stdin) {
		resp, err := files.Send(cn, http.MethodGet, route, nil, nil)
		if err != nil {
			return err
		}
		var v struct {
			Name string `json:"name"`
		}
		if err := cmdkit.DecodeAs(resp.Body, &v, "a named thing"); err != nil {
			return err
		}
		if v.Name == "" {
			return errors.New("the server's answer has no name to confirm with; nothing was changed")
		}
		// A name with a control character would reach the terminal in the question and cannot be typed back.
		if _, _, err := names.Clean(v.Name); err != nil {
			return exitcode.Usagef("the name cannot be shown safely to confirm with; nothing was changed; " +
				"give --yes to remove it by its id")
		}
		phrase = v.Name
	}
	return files.Confirm(yes, phrase, what, stderr)
}

// printDocument writes one document: the API's own under --json, otherwise key=value lines from pairs.
func printDocument(c *command, body []byte, pairs func([]byte) ([]output.KV, error)) error {
	if c.G.JSON {
		return output.Indented(c.Stdout, body)
	}
	kv, err := pairs(body)
	if err != nil {
		return err
	}
	return output.KeyValues(c.Stdout, kv)
}

// printList writes a list: the items as the API wrote them under --json, otherwise a table. A note on stderr says when
// another page exists.
func printList(c *command, items []json.RawMessage, next string, headers []string,
	row func([]byte) ([]string, error)) error {
	if next != "" {
		_, _ = io.WriteString(c.Stderr, "selfcloud: more items exist; --all prints every page\n")
	}
	if c.G.JSON {
		return output.JSON(c.Stdout, items)
	}
	rows := make([][]string, 0, len(items))
	for _, raw := range items {
		r, err := row(raw)
		if err != nil {
			return err
		}
		rows = append(rows, r)
	}
	return output.Table(c.Stdout, headers, rows)
}
