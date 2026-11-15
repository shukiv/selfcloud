// SPDX-License-Identifier: AGPL-3.0-or-later

// The embed site commands: embed show|set. They read and replace the sites that may show the account's pages in a
// frame. Only the account's owner or key may change the list; the server checks and normalises the origins.

package manage

import (
	"fmt"
	"io"
	"net/http"
	"unicode"

	"github.com/shukiv/selfcloud/internal/cli/cmdkit"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/config"
)

// embedOriginsView is the document the embed routes answer: the account's allowed host origins.
type embedOriginsView struct {
	Origins []string `json:"origins"`
}

// printEmbedOrigins prints the list: one origin per line, or the line for an empty list. Under --json it writes the
// server's document as it came.
func printEmbedOrigins(c *command, body []byte) error {
	var v embedOriginsView
	if err := cmdkit.DecodeAs(body, &v, "an embed list"); err != nil {
		return err
	}
	if c.G.JSON {
		return output.Indented(c.Stdout, body)
	}
	if len(v.Origins) == 0 {
		_, err := io.WriteString(c.Stdout, "no sites may embed this account\n")
		return err
	}
	for _, o := range v.Origins {
		if _, err := fmt.Fprintln(c.Stdout, output.Clean(o)); err != nil {
			return err
		}
	}
	return nil
}

func embedRoute(acc string) string { return "/api/v1/accounts/" + acc + "/embed" }

// embedOriginArg checks what the command can check before a request: an origin is not empty, at most 2048 bytes, and
// holds no whitespace or control character. The rest — the scheme, the host, the server's own length cap — is the
// server's to check.
func embedOriginArg(o string) error {
	if o == "" {
		return exitcode.Usagef("an embed origin is empty")
	}
	if len(o) > 2048 {
		return exitcode.Usagef("an embed origin is longer than 2048 bytes")
	}
	for _, r := range o {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return exitcode.Usagef("an embed origin holds whitespace or a control character")
		}
	}
	return nil
}

// EmbedShow is `selfcloud embed show [--account ACC]`: the sites that may show the account's pages in a frame.
func EmbedShow(args []string, stdout, stderr io.Writer) int {
	c := begin("embed show", true, stdout, stderr)
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	if err := cmdkit.NoArgs("embed show", pos); err != nil {
		return c.fail(err)
	}
	acc, err := c.AccountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodGet, embedRoute(acc), nil, nil)
	if err != nil {
		return c.fail(err)
	}
	if err := printEmbedOrigins(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// EmbedSet is `selfcloud embed set [--account ACC] ORIGIN... | --clear [--yes]`: it replaces the account's embed sites
// with the origins given, in the order given, duplicates included; the server normalises and deduplicates them.
// Clearing asks first, because every frame of the account stops working.
func EmbedSet(args []string, stdout, stderr io.Writer) int {
	c := begin("embed set", true, stdout, stderr)
	var yes, clear bool
	c.Set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	c.Set.BoolVar(&clear, "clear", false, "remove every origin; every frame of the account stops working (asks first)")
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	switch {
	case clear && len(pos) > 0:
		return c.fail(exitcode.Usagef("embed set takes no origins with --clear"))
	case !clear && len(pos) == 0:
		return c.fail(exitcode.Usagef("embed set needs origins, or --clear"))
	case len(pos) > config.MaxEmbedOrigins:
		return c.fail(exitcode.Usagef("an embed list holds at most %d origins", config.MaxEmbedOrigins))
	}
	for _, o := range pos {
		if err := embedOriginArg(o); err != nil {
			return c.fail(err)
		}
	}
	acc, err := c.AccountID()
	if err != nil {
		return c.fail(err)
	}
	if clear {
		if err := files.Confirm(yes, acc, "clear the embed sites; every frame of the account stops working", stderr); err != nil {
			return c.fail(err)
		}
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	origins := pos
	if origins == nil {
		origins = []string{}
	}
	resp, err := files.Send(cn, http.MethodPut, embedRoute(acc), nil, map[string][]string{"origins": origins})
	if err != nil {
		return c.fail(err)
	}
	if err := printEmbedOrigins(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
