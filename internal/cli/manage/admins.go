// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"errors"
	"flag"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

const adminsPath = "/api/v1/admins"

// adminView is an installation admin as the list prints it. It is the allow-list: a field the API adds is not shown
// until it is added here.
type adminView struct {
	ID           string  `json:"id"`
	Email        string  `json:"email"`
	DisplayName  string  `json:"display_name"`
	Status       string  `json:"status"`
	VerifiedAt   *string `json:"verified_at"`
	LastSignInAt *string `json:"last_sign_in_at"`
	CreatedAt    string  `json:"created_at"`
}

func adminText(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func adminPairs(v adminView) []output.KV {
	return []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "email", Value: v.Email},
		{Key: "display_name", Value: v.DisplayName},
		{Key: "status", Value: v.Status},
		{Key: "verified_at", Value: adminText(v.VerifiedAt)},
		{Key: "last_sign_in_at", Value: adminText(v.LastSignInAt)},
		{Key: "created_at", Value: v.CreatedAt},
	}
}

func adminRow(v adminView) []string {
	return []string{v.ID, v.Email, v.DisplayName, v.Status, adminText(v.VerifiedAt),
		adminText(v.LastSignInAt), v.CreatedAt}
}

// printAdminList writes the admins as views, not as the API's raw items, so a field the server adds is not shown.
func printAdminList(c *command, items []adminView, next string) error {
	if next != "" {
		_, _ = io.WriteString(c.stderr, "selfcloud: more items exist; --all prints every page\n")
	}
	if c.g.JSON {
		if items == nil {
			items = []adminView{}
		}
		return output.JSON(c.stdout, items)
	}
	rows := make([][]string, 0, len(items))
	for _, v := range items {
		rows = append(rows, adminRow(v))
	}
	return output.Table(c.stdout, []string{"ID", "EMAIL", "NAME", "STATUS", "VERIFIED", "LAST SIGN-IN", "CREATED"}, rows)
}

// adminVerification is the verification an admin add shows. It has no field for the token, which is a secret.
type adminVerification struct {
	Method    string  `json:"method"`
	URL       string  `json:"url"`
	ExpiresAt *string `json:"expires_at"`
	Sent      *bool   `json:"sent"`
}

// adminShown is a new installation admin and the verification made for it, as add prints it.
type adminShown struct {
	ID           string            `json:"id"`
	Email        string            `json:"email"`
	DisplayName  string            `json:"display_name"`
	Status       string            `json:"status"`
	Verification adminVerification `json:"verification"`
}

func adminFlagGiven(set *flag.FlagSet, name string) bool {
	given := false
	set.Visit(func(f *flag.Flag) { given = given || f.Name == name })
	return given
}

// AdminLs is `selfcloud admin ls [--all]`: the installation admins.
func AdminLs(args []string, stdout, stderr io.Writer) int {
	c := begin("admin ls", false, stdout, stderr)
	c.adminRoutes = true
	var all bool
	c.set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("admin ls", pos); err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	items, next, err := files.ListPages(cn, adminsPath, nil, all)
	if err != nil {
		return c.fail(err)
	}
	views := make([]adminView, 0, len(items))
	for _, raw := range items {
		var v adminView
		if err := decodeAs(raw, &v, "an installation admin"); err != nil {
			return c.fail(err)
		}
		views = append(views, v)
	}
	if err := printAdminList(c, views, next); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// AdminAdd is `selfcloud admin add --verification email|link [--name NAME] EMAIL`: makes a pending installation admin
// and shows how it verifies. A link, which holds a secret, is shown once.
func AdminAdd(args []string, stdout, stderr io.Writer) int {
	c := begin("admin add", false, stdout, stderr)
	c.adminRoutes = true
	var method, name string
	c.set.StringVar(&method, "verification", "", "how the new admin verifies: `method`, email or link")
	c.set.StringVar(&name, "name", "", "the new admin's display `name`")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("admin add takes one email address"))
	}
	if strings.TrimSpace(pos[0]) == "" {
		return c.fail(exitcode.Usagef("the email address is empty"))
	}
	if method != "email" && method != "link" {
		return c.fail(exitcode.Usagef("--verification must be email or link"))
	}
	body := map[string]string{"email": pos[0], "verification": method}
	if adminFlagGiven(c.set, "name") {
		body["display_name"] = name
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPost, adminsPath, nil, body)
	if err != nil {
		return c.fail(err)
	}
	var v adminShown
	if err := decodeAs(resp.Body, &v, "an installation admin"); err != nil {
		return c.fail(err)
	}
	if v.Verification.Method == "" {
		return c.fail(errors.New("the server's answer has no verification for the new admin"))
	}
	if err := printAdminShown(c, v); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

func printAdminShown(c *command, v adminShown) error {
	if c.g.JSON {
		return output.JSON(c.stdout, v)
	}
	pairs := []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "email", Value: v.Email},
		{Key: "display_name", Value: v.DisplayName},
		{Key: "status", Value: v.Status},
		{Key: "verification", Value: v.Verification.Method},
	}
	if v.Verification.Method == "link" {
		pairs = append(pairs,
			output.KV{Key: "link", Value: v.Verification.URL},
			output.KV{Key: "link_expires_at", Value: adminText(v.Verification.ExpiresAt)})
	} else {
		pairs = append(pairs, output.KV{Key: "sent", Value: strconv.FormatBool(v.Verification.Sent != nil && *v.Verification.Sent)})
	}
	if err := output.KeyValues(c.stdout, pairs); err != nil {
		return err
	}
	if v.Verification.Method == "link" {
		_, _ = io.WriteString(c.stderr, "selfcloud: note: the sign-in link is shown only now; give it to the new admin\n")
	}
	return nil
}

// AdminRm is `selfcloud admin rm USER [--yes]`: removes an installation admin. It asks first.
func AdminRm(args []string, stdout, stderr io.Writer) int {
	c := begin("admin rm", false, stdout, stderr)
	c.adminRoutes = true
	var yes bool
	c.set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("admin rm takes one admin id"))
	}
	usr, err := idArg("usr", "admin", pos[0])
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	if err := files.Confirm(yes, usr, "remove the installation admin; they can no longer sign in", stderr); err != nil {
		return c.fail(err)
	}
	if _, err := files.Send(cn, http.MethodDelete, adminsPath+"/"+usr, nil, nil); err != nil {
		return c.fail(err)
	}
	if err := printDone(c, usr); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
