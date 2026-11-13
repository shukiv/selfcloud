// SPDX-License-Identifier: AGPL-3.0-or-later

// The label field list (slice cli11f): `label field ls`. It names every field the caller's account lets it see, with the
// choices of a choice field and, with an account key, how many values the field has; a signed-in device's token does not
// get the count. An installation admin key has no account, so the API refuses it; the hint says what to use instead.

package manage

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/cli/profile"
)

// labelFieldListAdminHint is told to an installation admin key the label field route refused.
const labelFieldListAdminHint = "this needs an account key or a person's sign-in; an installation admin key has none"

// labelFieldListView is one item of GET /api/v1/label-fields, and the allow list --json prints. values is left out when
// the API leaves it out, so a device token's answer does not pretend the count is zero.
type labelFieldListView struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Restricted bool              `json:"restricted"`
	Choices    []labelChoiceView `json:"choices"`
	Values     *int              `json:"values,omitempty"`
}

// activeChoices counts the choices a person may pick.
func (v labelFieldListView) activeChoices() int {
	n := 0
	for _, c := range v.Choices {
		if !c.Archived {
			n++
		}
	}
	return n
}

// labelFieldListForbidden reports whether err is the API's 403 for the label field route.
func labelFieldListForbidden(err error) bool {
	var ae *client.APIError
	return errors.As(err, &ae) && ae.Status == http.StatusForbidden
}

// LabelFieldList is `selfcloud label field ls`: the account's label fields.
func LabelFieldList(args []string, stdout, stderr io.Writer) int {
	c := begin("label field ls", false, stdout, stderr)
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("label field ls", pos); err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	items, _, err := files.ListPages(cn, "/api/v1/label-fields", nil, false)
	if err != nil {
		code := c.fail(err)
		if !c.g.JSON && c.kind == profile.KindAdmin && labelFieldListForbidden(err) {
			_, _ = io.WriteString(c.stderr, "selfcloud: hint: "+labelFieldListAdminHint+"\n")
		}
		return code
	}
	views := make([]labelFieldListView, 0, len(items))
	for _, raw := range items {
		var v labelFieldListView
		if err := decodeAs(raw, &v, "a label field"); err != nil {
			return c.fail(err)
		}
		views = append(views, v)
	}
	if c.g.JSON {
		if err := output.JSON(c.stdout, views); err != nil {
			return c.fail(err)
		}
		return exitcode.OK
	}
	rows := make([][]string, 0, len(views))
	for _, v := range views {
		values := ""
		if v.Values != nil {
			values = strconv.Itoa(*v.Values)
		}
		restricted := ""
		if v.Restricted {
			restricted = "yes"
		}
		rows = append(rows, []string{v.ID, v.Name, v.Type, restricted,
			strconv.Itoa(v.activeChoices()), values})
	}
	if err := output.Table(c.stdout, []string{"ID", "NAME", "TYPE", "RESTRICTED", "CHOICES", "VALUES"}, rows); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
