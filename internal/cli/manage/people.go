// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/cli/profile"
)

// peopleView is the part of a directory entry that the plain output prints.
type peopleView struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// peoplePage is a directory answer, decoded into the fields that the command prints.
type peoplePage struct {
	Items []peopleView `json:"items"`
	More  bool         `json:"more"`
}

// PeopleFind is `selfcloud people find [QUERY] [--kind user|group|all]`: the people and groups of the account.
func PeopleFind(args []string, stdout, stderr io.Writer) int {
	c := begin("people find", false, stdout, stderr)
	var kind string
	c.set.StringVar(&kind, "kind", "all", "only a `kind`: user, group or all")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) > 1 {
		return c.fail(exitcode.Usagef("people find takes at most one query"))
	}
	q := ""
	if len(pos) == 1 {
		q = pos[0]
	}
	switch kind {
	case "user", "group", "all":
	default:
		return c.fail(exitcode.Usagef("--kind is user, group or all"))
	}
	if utf8.RuneCountInString(strings.TrimSpace(q)) > 100 {
		return c.fail(exitcode.Usagef("the query is at most 100 characters"))
	}
	query := url.Values{"q": {q}, "kind": {kind}}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodGet, "/api/v1/directory", query, nil)
	if err != nil {
		code := c.fail(err)
		var ae *client.APIError
		if !c.g.JSON && cn.Kind == profile.KindDevice && errors.As(err, &ae) && ae.Status == http.StatusForbidden {
			_, _ = io.WriteString(stderr, "selfcloud: hint: this needs an account key; a signed-in device cannot use it\n")
		}
		return code
	}
	var page peoplePage
	if err := decodeAs(resp.Body, &page, "a directory answer"); err != nil {
		return c.fail(err)
	}
	if page.More {
		_, _ = io.WriteString(stderr, "selfcloud: more matched; narrow the query (at most 20 are shown)\n")
	}
	if c.g.JSON {
		if err := output.JSON(c.stdout, page); err != nil {
			return c.fail(err)
		}
		return exitcode.OK
	}
	rows := make([][]string, 0, len(page.Items))
	for _, v := range page.Items {
		rows = append(rows, []string{v.ID, v.Kind, v.Name, v.Email})
	}
	if err := output.Table(c.stdout, []string{"ID", "KIND", "NAME", "EMAIL"}, rows); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
