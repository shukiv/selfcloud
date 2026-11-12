// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ops holds the remote commands that run an account's integrations: webhooks, imports and jobs. A command calls
// the API route the parity table names for it and prints what the route answers, through the output package.
package ops

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/cli/profile"
	"github.com/shukiv/selfcloud/internal/id"
)

// envAccount names the account when --account is not given.
const envAccount = "SELFCLOUD_ACCOUNT"

const (
	// accountHint is told to a device token that gets "not found" on a route of an account.
	accountHint = "this needs an account key; a signed-in device cannot manage an account"
	// userHint is told to an account key or an admin key that the import routes refuse.
	userHint = "imports belong to a signed-in person; an account key or an admin key cannot use them"
)

// command is one command's flags and streams. begin makes it; the command adds its own flags before parse.
type command struct {
	g              conn.Global
	set            *flag.FlagSet
	account        string
	stdout, stderr io.Writer
	// kind is the credential's kind once connected. accountUsed says that the command named an account, and userRoutes that
	// its routes are a signed-in person's.
	kind        string
	accountUsed bool
	userRoutes  bool
}

// begin starts a command with the global flags. withAccount adds --account, which the command then needs.
func begin(name string, withAccount bool, stdout, stderr io.Writer) *command {
	c := &command{set: conn.NewFlagSet(name), stdout: stdout, stderr: stderr}
	c.g.Register(c.set)
	if withAccount {
		c.set.StringVar(&c.account, "account", "", "the account, `id` (acc_...); default: the "+envAccount+" variable")
	}
	return c
}

// parse reads the arguments. ok is false when the command must return code at once (after -h, or a usage error that has
// been reported).
func (c *command) parse(args []string) (pos []string, code int, ok bool) {
	return files.Parse(c.set, &c.g, args, c.stdout, c.stderr)
}

// connect resolves the address and the credential, and remembers the credential's kind for the hints.
func (c *command) connect() (*conn.Conn, error) {
	cn, err := conn.Connect(c.g, c.stderr, true)
	if err != nil {
		return nil, err
	}
	c.kind = cn.Kind
	return cn, nil
}

// fail reports err and returns its exit code. Two refusals are explained in plain output, and not under --json:
//   - a device token that gets "not found" from a route of an account, or 403 with the reason device_token from one that
//     changes a credential: the server never lets a token manage an account, and answers as if the account were not
//     there or as if the browser could do it (it cannot: the account key manages webhooks);
//   - an account key or an admin key that is refused by an import route: the routes are a signed-in person's.
func (c *command) fail(err error) int {
	code := output.Fail(c.stderr, c.g.JSON, err)
	if c.g.JSON {
		return code
	}
	var ae *client.APIError
	if !errors.As(err, &ae) {
		return code
	}
	switch {
	case c.accountUsed && c.kind == profile.KindDevice && (ae.Status == http.StatusNotFound ||
		ae.Status == http.StatusForbidden && ae.Reason == "device_token"):
		_, _ = io.WriteString(c.stderr, "selfcloud: hint: "+accountHint+"\n")
	case c.userRoutes && ae.Status == http.StatusForbidden && (c.kind == profile.KindAccount || c.kind == profile.KindAdmin):
		_, _ = io.WriteString(c.stderr, "selfcloud: hint: "+userHint+"\n")
	}
	return code
}

// accountID returns the account of an account-scoped command: --account, else the environment. It must have the shape
// of an account id, so nothing else reaches a request path.
func (c *command) accountID() (string, error) {
	v := c.account
	if v == "" {
		v = os.Getenv(envAccount)
	}
	if !id.Valid("acc", v) {
		return "", exitcode.Usagef("an account is named by its acc_ id: pass --account acc_..., or set %s", envAccount)
	}
	c.accountUsed = true
	return v, nil
}

// idArg checks one positional id of the given prefix.
func idArg(prefix, what, arg string) (string, error) {
	if !id.Valid(prefix, arg) {
		return "", exitcode.Usagef("a %s is named by its %s_ id", what, prefix)
	}
	return arg, nil
}

// noArgs refuses positional arguments.
func noArgs(name string, pos []string) error {
	if len(pos) != 0 {
		return exitcode.Usagef("%s takes no arguments", name)
	}
	return nil
}

// flagGiven reports whether the command line set the flag name, even to its default.
func flagGiven(set *flag.FlagSet, name string) bool {
	given := false
	set.Visit(func(f *flag.Flag) { given = given || f.Name == name })
	return given
}

// errNotA is the error of an answer that is not the document the command expects. It never repeats the answer, which may
// hold a secret.
func errNotA(what string) error { return errors.New("the server's answer is not " + what) }

// decodeAs reads a document into v.
func decodeAs(body []byte, v any, what string) error {
	if err := json.Unmarshal(body, v); err != nil {
		return errNotA(what)
	}
	return nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// printDone writes the result of a change that answers with no body.
func printDone(c *command, target string) error {
	if c.g.JSON {
		return output.JSON(c.stdout, map[string]string{"target": target, "status": "done"})
	}
	return output.KeyValues(c.stdout, []output.KV{{Key: "target", Value: target}, {Key: "status", Value: "done"}})
}

// printList writes a list: the items as given under --json, otherwise a table. A note on stderr says when another page
// exists.
func printList[T any](c *command, items []T, more bool, headers []string, row func(T) []string) error {
	if more {
		_, _ = io.WriteString(c.stderr, "selfcloud: more items exist; --all prints every page\n")
	}
	if c.g.JSON {
		if items == nil {
			items = []T{}
		}
		return output.JSON(c.stdout, items)
	}
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		rows = append(rows, row(it))
	}
	return output.Table(c.stdout, headers, rows)
}

// decodeItems reads each raw item of a list into a T.
func decodeItems[T any](items []json.RawMessage, what string) ([]T, error) {
	out := make([]T, 0, len(items))
	for _, raw := range items {
		var v T
		if err := decodeAs(raw, &v, what); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// confirmByID asks before a change that cannot be undone. These targets have no name to type, so the person types the
// target's id. Without a terminal and without --yes the command refuses before anything is sent.
func confirmByID(yes bool, target, what string, stderr io.Writer) error {
	return files.Confirm(yes, target, what, stderr)
}

// str returns the value a nullable string points to, or "".
func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
