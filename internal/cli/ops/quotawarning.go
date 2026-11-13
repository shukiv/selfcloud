// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

// The quota-warning commands. An account sets the share of its quota, and of each user's limit, at which quota.warning
// is sent; quota.exceeded is always at 100 percent. The account key reads and sets the level, so these routes are not an
// installation admin's. The level is a whole number from 1 to 99.

import (
	"io"
	"net/http"
	"strconv"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

const quotaWarningWhat = "a quota warning level"

func quotaWarningRoute(acc string) string { return "/api/v1/accounts/" + acc + "/quota-warning" }

// quotaWarningView is the level an account now warns at.
type quotaWarningView struct {
	Percent int64 `json:"percent"`
}

// printQuotaWarning reads a quota-warning answer and prints it as percent=N, or as the server's number under --json.
func (c *command) printQuotaWarning(body []byte) error {
	var v quotaWarningView
	if err := decodeAs(body, &v, quotaWarningWhat); err != nil {
		return err
	}
	if c.g.JSON {
		return output.JSON(c.stdout, v)
	}
	return output.KeyValues(c.stdout, []output.KV{{Key: "percent", Value: strconv.FormatInt(v.Percent, 10)}})
}

// AccountQuotaWarningShow is `selfcloud account quota-warning show --account ACC`.
func AccountQuotaWarningShow(args []string, stdout, stderr io.Writer) int {
	c := begin("account quota-warning show", true, stdout, stderr)
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("account quota-warning show", pos); err != nil {
		return c.fail(err)
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodGet, quotaWarningRoute(acc), nil, nil)
	if err != nil {
		return c.fail(err)
	}
	if err := c.printQuotaWarning(resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// AccountQuotaWarningSet is `selfcloud account quota-warning set --account ACC PERCENT`.
func AccountQuotaWarningSet(args []string, stdout, stderr io.Writer) int {
	c := begin("account quota-warning set", true, stdout, stderr)
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("account quota-warning set takes one whole number from 1 to 99"))
	}
	percent, err := strconv.Atoi(pos[0])
	if err != nil || percent < 1 || percent > 99 {
		return c.fail(exitcode.Usagef("PERCENT is a whole number from 1 to 99; quota.exceeded is at 100"))
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPut, quotaWarningRoute(acc), nil, map[string]any{"percent": percent})
	if err != nil {
		return c.fail(err)
	}
	if err := c.printQuotaWarning(resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// AccountQuotaWarningClear is `selfcloud account quota-warning clear --account ACC`: it sets the level back to the
// default without asking, and prints the level now in force.
func AccountQuotaWarningClear(args []string, stdout, stderr io.Writer) int {
	c := begin("account quota-warning clear", true, stdout, stderr)
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("account quota-warning clear", pos); err != nil {
		return c.fail(err)
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodDelete, quotaWarningRoute(acc), nil, nil)
	if err != nil {
		return c.fail(err)
	}
	if err := c.printQuotaWarning(resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
