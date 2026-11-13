// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ops holds the remote commands that run an account's integrations: webhooks, imports and jobs. A command calls
// the API route the parity table names for it and prints what the route answers, through the output package.
package ops

import (
	"errors"
	"io"
	"net/http"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/cmdkit"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/cli/profile"
)

const (
	// accountHint is told to a device token that gets "not found" on a route of an account.
	accountHint = "this needs an account key; a signed-in device cannot manage an account"
	// userHint is told to an account key or an admin key that the import routes refuse.
	userHint = "imports belong to a signed-in person; an account key or an admin key cannot use them"
	// adminHint is told to a credential that is not an installation admin key when an admin route refuses it.
	adminHint = "this needs an installation admin key"
)

// command is one command's flags and streams. begin makes it; the command adds its own flags before Parse.
type command struct{ *cmdkit.Command }

// begin starts a command with the global flags. withAccount adds --account, which the command then needs.
func begin(name string, withAccount bool, stdout, stderr io.Writer) *command {
	return &command{cmdkit.New(name, withAccount, stdout, stderr)}
}

// fail reports err and returns its exit code. Two refusals are explained in plain output, and not under --json:
//   - a device token that gets "not found" from a route of an account, or 403 with the reason device_token from one that
//     changes a credential: the server never lets a token manage an account, and answers as if the account were not
//     there or as if the browser could do it (it cannot: the account key manages webhooks);
//   - an account key or an admin key that is refused by an import route: the routes are a signed-in person's.
func (c *command) fail(err error) int {
	code := output.Fail(c.Stderr, c.G.JSON, err)
	if c.G.JSON {
		return code
	}
	var ae *client.APIError
	if !errors.As(err, &ae) {
		return code
	}
	switch {
	case c.AccountUsed && c.Kind == profile.KindDevice && (ae.Status == http.StatusNotFound ||
		ae.Status == http.StatusForbidden && ae.Reason == "device_token"):
		_, _ = io.WriteString(c.Stderr, "selfcloud: hint: "+accountHint+"\n")
	case c.UserRoutes && ae.Status == http.StatusForbidden && (c.Kind == profile.KindAccount || c.Kind == profile.KindAdmin):
		_, _ = io.WriteString(c.Stderr, "selfcloud: hint: "+userHint+"\n")
	case c.AdminRoutes && c.Kind != profile.KindAdmin && ae.Status == http.StatusForbidden:
		_, _ = io.WriteString(c.Stderr, "selfcloud: hint: "+adminHint+"\n")
	}
	return code
}
