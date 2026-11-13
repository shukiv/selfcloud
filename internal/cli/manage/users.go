// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/config"
)

const (
	linkShownNote = "selfcloud: note: the sign-in link is shown only now; give it to the person\n"
	existsNote    = "selfcloud: note: this person already exists; nothing was changed\n"
	rmHint        = "selfcloud: hint: give --transfer-to USER or --delete-files\n"
)

// userView is one person as the user commands allow it: every field of userJSON, and no secret.
type userView struct {
	ID           string  `json:"id"`
	ExternalID   *string `json:"external_id"`
	Email        string  `json:"email"`
	DisplayName  string  `json:"display_name"`
	Locale       string  `json:"locale"`
	Role         string  `json:"role"`
	Status       string  `json:"status"`
	VerifiedAt   *string `json:"verified_at"`
	QuotaBytes   *int64  `json:"quota_bytes"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	PendingEmail *string `json:"pending_email"`
	LastSignInAt *string `json:"last_sign_in_at"`
}

// userDetailView is a person as GET and PATCH on one answer it: the user, and the limit that applies.
type userDetailView struct {
	userView
	EffectiveQuotaBytes *int64 `json:"effective_quota_bytes"`
}

// userVerificationView is the verification of a create or an email change. The token is never part of it, so no command can
// print it.
type userVerificationView struct {
	Method    string  `json:"method"`
	URL       string  `json:"url,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	Sent      *bool   `json:"sent,omitempty"`
}

// userShown is a person just added: the user view, and the verification made for it.
type userShown struct {
	ID           string                `json:"id"`
	Email        string                `json:"email"`
	DisplayName  string                `json:"display_name"`
	Role         string                `json:"role"`
	Status       string                `json:"status"`
	Verification *userVerificationView `json:"verification,omitempty"`
}

func usersRoute(acc string) string { return "/api/v1/accounts/" + acc + "/users" }

func textOf(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func numberText(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}

func userDetailKV(v userDetailView) []output.KV {
	return []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "external_id", Value: textOf(v.ExternalID)},
		{Key: "email", Value: v.Email},
		{Key: "display_name", Value: v.DisplayName},
		{Key: "locale", Value: v.Locale},
		{Key: "role", Value: v.Role},
		{Key: "status", Value: v.Status},
		{Key: "verified_at", Value: textOf(v.VerifiedAt)},
		{Key: "quota_bytes", Value: numberText(v.QuotaBytes)},
		{Key: "created_at", Value: v.CreatedAt},
		{Key: "updated_at", Value: v.UpdatedAt},
		{Key: "pending_email", Value: textOf(v.PendingEmail)},
		{Key: "last_sign_in_at", Value: textOf(v.LastSignInAt)},
		{Key: "effective_quota_bytes", Value: numberText(v.EffectiveQuotaBytes)},
	}
}

// verificationKV writes the verification's method, and then what the person needs: whether the mail was sent, or the
// link and when it ends. The link is printed once, here.
func verificationKV(v *userVerificationView, method string, stderr io.Writer) []output.KV {
	kv := []output.KV{{Key: "verification", Value: method}}
	if v == nil {
		return kv
	}
	switch method {
	case "email":
		sent := false
		if v.Sent != nil {
			sent = *v.Sent
		}
		kv = append(kv, output.KV{Key: "sent", Value: strconv.FormatBool(sent)})
	case "link":
		kv = append(kv, output.KV{Key: "link", Value: v.URL},
			output.KV{Key: "link_expires_at", Value: textOf(v.ExpiresAt)})
		_, _ = io.WriteString(stderr, linkShownNote)
	}
	return kv
}

// UserAdd is `selfcloud user add --account ACC --verification email|link|none [--name NAME] [--locale TAG]
// [--role ROLE] [--external-id ID] EMAIL`: adds a person to the account. A person who already exists under the same
// external id and fields is answered (200), not refused.
func UserAdd(args []string, stdout, stderr io.Writer) int {
	c := begin("user add", true, stdout, stderr)
	var verification, name, locale, role, ext string
	c.set.StringVar(&verification, "verification", "", "how the person proves the address: `email`, `link` or `none`")
	c.set.StringVar(&name, "name", "", "the person's display `name`")
	c.set.StringVar(&locale, "locale", "", "the person's locale, a `tag` such as en or pt-BR")
	c.set.StringVar(&role, "role", "", "the person's `role`: member (the default) or owner")
	c.set.StringVar(&ext, "external-id", "", "the caller's own `id` for the person, kept with it")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("user add takes one email address"))
	}
	email := strings.TrimSpace(pos[0])
	if email == "" {
		return c.fail(exitcode.Usagef("user add takes one email address"))
	}
	switch verification {
	case "email", "link", "none":
	default:
		return c.fail(exitcode.Usagef("--verification must be email, link or none"))
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	body := map[string]any{"email": email, "verification": verification}
	c.set.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "name":
			body["display_name"] = name
		case "locale":
			body["locale"] = locale
		case "role":
			body["role"] = role
		case "external-id":
			body["external_id"] = ext
		}
	})
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPost, usersRoute(acc), nil, body)
	if err != nil {
		return c.fail(err)
	}
	var v userShown
	if err := decodeAs(resp.Body, &v, "a person"); err != nil {
		return c.fail(err)
	}
	created := resp.Status == http.StatusCreated
	if created && v.Verification == nil {
		v.Verification = &userVerificationView{Method: verification}
	}
	if err := printUserShown(c, v, created); err != nil {
		return c.fail(err)
	}
	if !created {
		_, _ = io.WriteString(stderr, existsNote)
	}
	return exitcode.OK
}

func printUserShown(c *command, v userShown, created bool) error {
	if c.g.JSON {
		return output.JSON(c.stdout, v)
	}
	kv := []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "email", Value: v.Email},
		{Key: "display_name", Value: v.DisplayName},
		{Key: "role", Value: v.Role},
		{Key: "status", Value: v.Status},
	}
	if created {
		kv = append(kv, verificationKV(v.Verification, v.Verification.Method, c.stderr)...)
	}
	return output.KeyValues(c.stdout, kv)
}

// UserLs is `selfcloud user ls --account ACC [--all] [--email EMAIL] [--external-id ID]`.
func UserLs(args []string, stdout, stderr io.Writer) int {
	c := begin("user ls", true, stdout, stderr)
	var all bool
	var email, ext string
	c.set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	c.set.StringVar(&email, "email", "", "only the person with this `email` address")
	c.set.StringVar(&ext, "external-id", "", "only the person with this caller's `id`")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("user ls", pos); err != nil {
		return c.fail(err)
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	q := url.Values{}
	if email != "" {
		q.Set("email", email)
	}
	if ext != "" {
		q.Set("external_id", ext)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	items, next, err := files.ListPages(cn, usersRoute(acc), q, all)
	if err != nil {
		return c.fail(err)
	}
	if next != "" {
		_, _ = io.WriteString(c.stderr, "selfcloud: more items exist; --all prints every page\n")
	}
	views := make([]userView, 0, len(items))
	for _, raw := range items {
		var v userView
		if err := decodeAs(raw, &v, "a person"); err != nil {
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
		rows = append(rows, []string{v.ID, v.Email, v.DisplayName, v.Role, v.Status, textOf(v.VerifiedAt)})
	}
	if err := output.Table(c.stdout, []string{"ID", "EMAIL", "NAME", "ROLE", "STATUS", "VERIFIED"}, rows); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// oneUser is the shared start of the commands that take one user id after the account.
func oneUser(c *command, name string, args []string) (acc, user string, code int, ok bool) {
	pos, code, ok := c.parse(args)
	if !ok {
		return "", "", code, false
	}
	if len(pos) != 1 {
		return "", "", c.fail(exitcode.Usagef("%s takes one user id", name)), false
	}
	var err error
	if user, err = idArg("usr", "user", pos[0]); err != nil {
		return "", "", c.fail(err), false
	}
	if acc, err = c.accountID(); err != nil {
		return "", "", c.fail(err), false
	}
	return acc, user, 0, true
}

// UserShow is `selfcloud user show --account ACC USER`.
func UserShow(args []string, stdout, stderr io.Writer) int {
	c := begin("user show", true, stdout, stderr)
	acc, user, code, ok := oneUser(c, "user show", args)
	if !ok {
		return code
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodGet, usersRoute(acc)+"/"+user, nil, nil)
	if err != nil {
		return c.fail(err)
	}
	var v userDetailView
	if err := decodeAs(resp.Body, &v, "a person"); err != nil {
		return c.fail(err)
	}
	if c.g.JSON {
		if err := output.JSON(c.stdout, v); err != nil {
			return c.fail(err)
		}
		return exitcode.OK
	}
	if err := output.KeyValues(c.stdout, userDetailKV(v)); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// UserUpdate is `selfcloud user update --account ACC USER [--name NAME] [--locale TAG] [--role ROLE]
// [--external-id ID] [--status active|disabled] [--quota SIZE|default] [--email EMAIL --verification email|link]
// [--yes]`: changes only what the flags give.
func UserUpdate(args []string, stdout, stderr io.Writer) int {
	c := begin("user update", true, stdout, stderr)
	var name, locale, role, ext, status, quota, email, verification string
	var yes bool
	c.set.StringVar(&name, "name", "", "the person's new display `name`")
	c.set.StringVar(&locale, "locale", "", "the person's new locale, a `tag` such as en or pt-BR")
	c.set.StringVar(&role, "role", "", "the person's new `role`: member or owner")
	c.set.StringVar(&ext, "external-id", "", "the caller's own `id` for the person; \"\" clears it")
	c.set.StringVar(&status, "status", "", "the person's `status`: active or disabled")
	c.set.StringVar(&quota, "quota", "", "the person's My files limit, a `size` such as 10GiB or 0, or default")
	c.set.StringVar(&email, "email", "", "the person's new `email` address, with --verification")
	c.set.StringVar(&verification, "verification", "", "how the new email is proved: `email` or `link`")
	c.set.BoolVar(&yes, "yes", false, "do not ask first when disabling (needed when standard input is not a terminal)")
	acc, user, code, ok := oneUser(c, "user update", args)
	if !ok {
		return code
	}
	body := map[string]any{}
	var emailSet, verificationSet, statusSet, quotaSet bool
	c.set.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "name":
			body["display_name"] = name
		case "locale":
			body["locale"] = locale
		case "role":
			body["role"] = role
		case "external-id":
			body["external_id"] = ext
		case "status":
			statusSet = true
			body["status"] = status
		case "email":
			emailSet = true
			body["email"] = email
		case "verification":
			verificationSet = true
			body["verification"] = verification
		case "quota":
			quotaSet = true
		}
	})
	if len(body) == 0 && !quotaSet {
		return c.fail(exitcode.Usagef("user update needs at least one change, such as --name or --quota"))
	}
	if statusSet && status != "active" && status != "disabled" {
		return c.fail(exitcode.Usagef("--status must be active or disabled"))
	}
	if emailSet != verificationSet {
		return c.fail(exitcode.Usagef("--email needs --verification, and --verification is only with --email"))
	}
	if verificationSet && verification != "email" && verification != "link" {
		return c.fail(exitcode.Usagef("--verification must be email or link"))
	}
	if quotaSet {
		if quota == "default" {
			body["quota_bytes"] = nil
		} else {
			var b config.ByteSize
			if err := b.UnmarshalText([]byte(quota)); err != nil {
				return c.fail(exitcode.Usagef("--quota is a size such as 10GiB or 0, or default: %v", err))
			}
			body["quota_bytes"] = int64(b)
		}
	}
	if statusSet && status == "disabled" {
		if err := files.Confirm(yes, user, "disable the person; they cannot sign in until they are active again", stderr); err != nil {
			return c.fail(err)
		}
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPatch, usersRoute(acc)+"/"+user, nil, body)
	if err != nil {
		return c.fail(err)
	}
	var v userDetailView
	if err := decodeAs(resp.Body, &v, "a person"); err != nil {
		return c.fail(err)
	}
	var doc struct {
		Verification *userVerificationView `json:"verification"`
	}
	if err := decodeAs(resp.Body, &doc, "a person"); err != nil {
		return c.fail(err)
	}
	if c.g.JSON {
		shown := struct {
			userDetailView
			Verification *userVerificationView `json:"verification,omitempty"`
		}{userDetailView: v, Verification: doc.Verification}
		if err := output.JSON(c.stdout, shown); err != nil {
			return c.fail(err)
		}
		return exitcode.OK
	}
	kv := userDetailKV(v)
	if doc.Verification != nil {
		kv = append(kv, verificationKV(doc.Verification, doc.Verification.Method, stderr)...)
	}
	if err := output.KeyValues(c.stdout, kv); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// UserRm is `selfcloud user rm --account ACC USER [--transfer-to USER2 | --delete-files] [--yes]`: removes a person.
// Their My files must be given to another person or deleted; a person alone cannot be removed while it holds files.
// Asks first, by the person's id.
func UserRm(args []string, stdout, stderr io.Writer) int {
	c := begin("user rm", true, stdout, stderr)
	var transferTo string
	var deleteFiles, yes bool
	c.set.StringVar(&transferTo, "transfer-to", "", "give the person's My files to this `user` (usr_...)")
	c.set.BoolVar(&deleteFiles, "delete-files", false, "delete the person's My files as well")
	c.set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	acc, user, code, ok := oneUser(c, "user rm", args)
	if !ok {
		return code
	}
	var other string
	if transferTo != "" && deleteFiles {
		return c.fail(exitcode.Usagef("give --transfer-to or --delete-files, not both"))
	}
	if transferTo != "" {
		var err error
		if other, err = idArg("usr", "user", transferTo); err != nil {
			return c.fail(err)
		}
		if other == user {
			return c.fail(exitcode.Usagef("a person cannot take over their own files; give another user"))
		}
	}
	what := "remove the person; their sign-ins end at once"
	if deleteFiles {
		what += " and their My files are deleted"
	}
	if err := files.Confirm(yes, user, what, stderr); err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	q := url.Values{}
	if other != "" {
		q.Set("transfer_to", other)
	} else if deleteFiles {
		q.Set("delete_files", "true")
	}
	if _, err := files.Send(cn, http.MethodDelete, usersRoute(acc)+"/"+user, q, nil); err != nil {
		code := c.fail(err)
		var ae *client.APIError
		if !c.g.JSON && errors.As(err, &ae) && ae.Status == http.StatusConflict {
			_, _ = io.WriteString(stderr, rmHint)
		}
		return code
	}
	if err := printDone(c, user); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
