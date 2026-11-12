// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"

	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
)

// envAccount names the account when --account is not given.
const envAccount = "SELFCLOUD_ACCOUNT"

// command is one command's flags and streams. begin makes it; the command adds its own flags before parse.
type command struct {
	g              conn.Global
	set            *flag.FlagSet
	account        string
	stdout, stderr io.Writer
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

// fail reports err and returns its exit code.
func (c *command) fail(err error) int { return output.Fail(c.stderr, c.g.JSON, err) }

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
	return v, nil
}

// idArg checks one positional id of the given prefix.
func idArg(prefix, what, arg string) (string, error) {
	if !id.Valid(prefix, arg) {
		return "", exitcode.Usagef("a %s is named by its %s_ id", what, prefix)
	}
	return arg, nil
}

// nameArg checks a name with the API's rules, so a name it would refuse costs no request. The name is sent as it was typed.
func nameArg(arg string) (string, error) {
	if _, _, err := names.Clean(arg); err != nil {
		return "", exitcode.Usagef("the name is not valid: %s", err)
	}
	return arg, nil
}

// printDocument writes one document: the API's own under --json, otherwise key=value lines from pairs.
func printDocument(c *command, body []byte, pairs func([]byte) ([]output.KV, error)) error {
	if c.g.JSON {
		return output.Indented(c.stdout, body)
	}
	kv, err := pairs(body)
	if err != nil {
		return err
	}
	return output.KeyValues(c.stdout, kv)
}

// printList writes a list: the items as the API wrote them under --json, otherwise a table. A note on stderr says when
// another page exists.
func printList(c *command, items []json.RawMessage, next string, headers []string,
	row func([]byte) ([]string, error)) error {
	if next != "" {
		_, _ = io.WriteString(c.stderr, "selfcloud: more items exist; --all prints every page\n")
	}
	if c.g.JSON {
		return output.JSON(c.stdout, items)
	}
	rows := make([][]string, 0, len(items))
	for _, raw := range items {
		r, err := row(raw)
		if err != nil {
			return err
		}
		rows = append(rows, r)
	}
	return output.Table(c.stdout, headers, rows)
}

// printDone writes the result of a change that answers with no body.
func printDone(c *command, target string) error {
	if c.g.JSON {
		return output.JSON(c.stdout, map[string]string{"target": target, "status": "done"})
	}
	return output.KeyValues(c.stdout, []output.KV{{Key: "target", Value: target}, {Key: "status", Value: "done"}})
}

// decodeAs reads a document into v.
func decodeAs(body []byte, v any, what string) error {
	if err := json.Unmarshal(body, v); err != nil {
		return errors.New("the server's answer is not " + what)
	}
	return nil
}

// noArgs refuses positional arguments.
func noArgs(name string, pos []string) error {
	if len(pos) != 0 {
		return exitcode.Usagef("%s takes no arguments", name)
	}
	return nil
}
