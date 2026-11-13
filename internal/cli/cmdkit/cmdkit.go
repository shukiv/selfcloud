// SPDX-License-Identifier: AGPL-3.0-or-later

// Package cmdkit holds the parts every remote command shares: its flags and streams, the account flag and the small
// helpers that read arguments, decode answers and print results.
package cmdkit

import (
	"flag"
	"io"
	"os"

	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/id"
)

// envAccount names the account when --account is not given.
const envAccount = "SELFCLOUD_ACCOUNT"

// Command is one command's flags and streams. New makes it; the command adds its own flags before Parse.
type Command struct {
	G       conn.Global
	Set     *flag.FlagSet
	Account string
	Stdout  io.Writer
	Stderr  io.Writer
	// Kind is the credential's kind once connected. AccountUsed says that the command named an account, UserRoutes that
	// its routes are a signed-in person's, and AdminRoutes that they need an installation admin key.
	Kind        string
	AccountUsed bool
	AdminRoutes bool
	UserRoutes  bool
}

// New starts a command with the global flags. withAccount adds --account, which the command then needs.
func New(name string, withAccount bool, stdout, stderr io.Writer) *Command {
	c := &Command{Set: conn.NewFlagSet(name), Stdout: stdout, Stderr: stderr}
	c.G.Register(c.Set)
	if withAccount {
		c.Set.StringVar(&c.Account, "account", "", "the account, `id` (acc_...); default: the "+envAccount+" variable")
	}
	return c
}

// Parse reads the arguments. ok is false when the command must return code at once (after -h, or a usage error that has
// been reported).
func (c *Command) Parse(args []string) (pos []string, code int, ok bool) {
	return files.Parse(c.Set, &c.G, args, c.Stdout, c.Stderr)
}

// Connect resolves the address and the credential, and remembers the credential's kind for the hints.
func (c *Command) Connect() (*conn.Conn, error) {
	cn, err := conn.Connect(c.G, c.Stderr, true)
	if err != nil {
		return nil, err
	}
	c.Kind = cn.Kind
	return cn, nil
}

// AccountID returns the account of an account-scoped command: --account, else the environment. It must have the shape
// of an account id, so nothing else reaches a request path.
func (c *Command) AccountID() (string, error) {
	v := c.Account
	if v == "" {
		v = os.Getenv(envAccount)
	}
	if !id.Valid("acc", v) {
		return "", exitcode.Usagef("an account is named by its acc_ id: pass --account acc_..., or set %s", envAccount)
	}
	c.AccountUsed = true
	return v, nil
}
