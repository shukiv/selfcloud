// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/config"
)

// accountView is the part of an account document that the commands print. It is the allow-list, as webhookView is for a
// webhook: a field the API adds is not shown until it is added here.
type accountView struct {
	ID         string  `json:"id"`
	ExternalID *string `json:"external_id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	QuotaBytes int64   `json:"quota_bytes"`
	UsedBytes  int64   `json:"used_bytes"`
	UsedFiles  int64   `json:"used_files"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

// verificationView is the verification a new owner was given. It has no field for the token the API may send with a link,
// so that token never reaches the terminal.
type verificationView struct {
	Method    string  `json:"method"`
	URL       string  `json:"url,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	Sent      *bool   `json:"sent,omitempty"`
}

// ownerView is the first owner made with an account, without the fields the commands do not print.
type ownerView struct {
	ID           string            `json:"id"`
	Email        string            `json:"email"`
	DisplayName  string            `json:"display_name"`
	Status       string            `json:"status"`
	Verification *verificationView `json:"verification,omitempty"`
}

// accountCreateView is the answer of account create: an account and, when one was made with it, its owner.
type accountCreateView struct {
	accountView
	Owner *ownerView `json:"owner,omitempty"`
}

func accountsRoute() string          { return "/api/v1/accounts" }
func accountRoute(acc string) string { return accountsRoute() + "/" + acc }

func (v accountView) pairs() []output.KV {
	ext := ""
	if v.ExternalID != nil {
		ext = *v.ExternalID
	}
	return []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "external_id", Value: ext},
		{Key: "name", Value: v.Name},
		{Key: "status", Value: v.Status},
		{Key: "quota_bytes", Value: strconv.FormatInt(v.QuotaBytes, 10)},
		{Key: "used_bytes", Value: strconv.FormatInt(v.UsedBytes, 10)},
		{Key: "used_files", Value: strconv.FormatInt(v.UsedFiles, 10)},
		{Key: "created_at", Value: v.CreatedAt},
		{Key: "updated_at", Value: v.UpdatedAt},
	}
}

func printAccountDocument(c *command, body []byte) error {
	var v accountView
	if err := decodeAs(body, &v, "an account"); err != nil {
		return err
	}
	if c.g.JSON {
		return output.JSON(c.stdout, v)
	}
	return output.KeyValues(c.stdout, v.pairs())
}

// accountViews decodes a page of accounts into the allow-list views, so --json prints no field the view does not have.
func accountViews(items []json.RawMessage) ([]accountView, error) {
	views := make([]accountView, 0, len(items))
	for _, raw := range items {
		var v accountView
		if err := decodeAs(raw, &v, "an account"); err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}

func (v ownerView) pairs() []output.KV {
	method, sent := "", ""
	if v.Verification != nil {
		method = v.Verification.Method
		if v.Verification.Sent != nil {
			sent = strconv.FormatBool(*v.Verification.Sent)
		}
	}
	return []output.KV{
		{Key: "owner_id", Value: v.ID},
		{Key: "owner_email", Value: v.Email},
		{Key: "owner_status", Value: v.Status},
		{Key: "verification", Value: method},
		{Key: "sent", Value: sent},
	}
}

func accountCreatePairs(v accountCreateView) []output.KV {
	pairs := v.accountView.pairs()
	if v.Owner == nil {
		return pairs
	}
	pairs = append(pairs, v.Owner.pairs()...)
	if v.Owner.Verification != nil && v.Owner.Verification.Method == "link" {
		expires := ""
		if v.Owner.Verification.ExpiresAt != nil {
			expires = *v.Owner.Verification.ExpiresAt
		}
		pairs = append(pairs,
			output.KV{Key: "link", Value: v.Owner.Verification.URL},
			output.KV{Key: "link_expires_at", Value: expires},
		)
	}
	return pairs
}

// givenFlags returns the names of the given flags among the command's own, so an update sends only what it was given
// and a global flag such as --account is not mistaken for one.
func givenFlags(set *flag.FlagSet, only ...string) map[string]bool {
	allowed := make(map[string]bool, len(only))
	for _, name := range only {
		allowed[name] = true
	}
	given := map[string]bool{}
	set.Visit(func(f *flag.Flag) {
		if allowed[f.Name] {
			given[f.Name] = true
		}
	})
	return given
}

// AccountCreate is `selfcloud account create [--external-id ID] [--quota SIZE] [--owner-email EMAIL
// --owner-verification email|link|none [--owner-name NAME] [--owner-locale TAG]] NAME`.
func AccountCreate(args []string, stdout, stderr io.Writer) int {
	c := begin("account create", false, stdout, stderr)
	c.adminRoutes = true
	var ext, ownerEmail, ownerVerification, ownerName, ownerLocale string
	var quota config.ByteSize
	c.set.StringVar(&ext, "external-id", "", "the caller's own `id` for the account, kept with it")
	c.set.Func("quota", "the account's storage limit, as `size` (0: no limit)", func(v string) error {
		return quota.UnmarshalText([]byte(v))
	})
	c.set.StringVar(&ownerEmail, "owner-email", "", "the first owner's `email`")
	c.set.StringVar(&ownerVerification, "owner-verification", "", "how the owner verifies: email, link or none")
	c.set.StringVar(&ownerName, "owner-name", "", "the first owner's display `name`")
	c.set.StringVar(&ownerLocale, "owner-locale", "", "the first owner's locale `tag`, such as en or pt-BR")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("account create takes one name"))
	}
	name := strings.TrimSpace(pos[0])
	if name == "" {
		return c.fail(exitcode.Usagef("the name is required"))
	}
	given := givenFlags(c.set, "external-id", "quota", "owner-email", "owner-verification", "owner-name", "owner-locale")
	ownerGiven := given["owner-email"] || given["owner-verification"] || given["owner-name"] || given["owner-locale"]
	if ownerGiven {
		if ownerEmail == "" {
			return c.fail(exitcode.Usagef("an owner needs --owner-email"))
		}
		if ownerVerification != "email" && ownerVerification != "link" && ownerVerification != "none" {
			return c.fail(exitcode.Usagef("--owner-verification must be email, link or none"))
		}
	}
	body := map[string]any{"name": name}
	if given["external-id"] {
		body["external_id"] = ext
	}
	if given["quota"] {
		if quota < 0 {
			return c.fail(exitcode.Usagef("the quota may not be negative"))
		}
		body["quota_bytes"] = int64(quota)
	}
	if ownerGiven {
		owner := map[string]string{"email": ownerEmail, "verification": ownerVerification}
		if ownerName != "" {
			owner["display_name"] = ownerName
		}
		if ownerLocale != "" {
			owner["locale"] = ownerLocale
		}
		body["owner"] = owner
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPost, accountsRoute(), nil, body)
	if err != nil {
		return c.fail(err)
	}
	var v accountCreateView
	if err := decodeAs(resp.Body, &v, "an account"); err != nil {
		return c.fail(err)
	}
	if c.g.JSON {
		if err := output.JSON(c.stdout, v); err != nil {
			return c.fail(err)
		}
	} else {
		if err := output.KeyValues(c.stdout, accountCreatePairs(v)); err != nil {
			return c.fail(err)
		}
	}
	if resp.Status == http.StatusOK {
		_, _ = io.WriteString(stderr, "selfcloud: note: an account with this external id already exists; nothing was changed\n")
	}
	if !c.g.JSON && v.Owner != nil && v.Owner.Verification != nil && v.Owner.Verification.Method == "link" {
		_, _ = io.WriteString(stderr, "selfcloud: note: the sign-in link is shown only now; give it to the owner\n")
	}
	return exitcode.OK
}

// AccountLs is `selfcloud account ls [--all] [--status active|suspended] [--external-id ID] [--search TEXT]`.
func AccountLs(args []string, stdout, stderr io.Writer) int {
	c := begin("account ls", false, stdout, stderr)
	c.adminRoutes = true
	var all bool
	var status, ext, search string
	c.set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	c.set.StringVar(&status, "status", "", "only accounts with this `status`: active or suspended")
	c.set.StringVar(&ext, "external-id", "", "only the account with this external `id`")
	c.set.StringVar(&search, "search", "", "accounts whose name or external id contains this `text`")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("account ls", pos); err != nil {
		return c.fail(err)
	}
	if status != "" && status != "active" && status != "suspended" {
		return c.fail(exitcode.Usagef("--status must be active or suspended"))
	}
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if ext != "" {
		q.Set("external_id", ext)
	}
	if search != "" {
		q.Set("q", search)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	items, next, err := files.ListPages(cn, accountsRoute(), q, all)
	if err != nil {
		return c.fail(err)
	}
	if c.g.JSON {
		if next != "" {
			_, _ = io.WriteString(c.stderr, "selfcloud: more items exist; --all prints every page\n")
		}
		views, err := accountViews(items)
		if err != nil {
			return c.fail(err)
		}
		if err := output.JSON(c.stdout, views); err != nil {
			return c.fail(err)
		}
		return exitcode.OK
	}
	err = printList(c, items, next, []string{"ID", "NAME", "STATUS", "QUOTA", "USED", "FILES", "CREATED"},
		func(raw []byte) ([]string, error) {
			var v accountView
			if err := decodeAs(raw, &v, "an account"); err != nil {
				return nil, err
			}
			return []string{v.ID, v.Name, v.Status, strconv.FormatInt(v.QuotaBytes, 10),
				strconv.FormatInt(v.UsedBytes, 10), strconv.FormatInt(v.UsedFiles, 10), v.CreatedAt}, nil
		})
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// accountOne is the shared start of the commands that take one account: --account, and no positional arguments.
func accountOne(c *command, name string, args []string) (acc string, code int, ok bool) {
	pos, code, ok := c.parse(args)
	if !ok {
		return "", code, false
	}
	if err := noArgs(name, pos); err != nil {
		return "", c.fail(err), false
	}
	acc, err := c.accountID()
	if err != nil {
		return "", c.fail(err), false
	}
	return acc, 0, true
}

// AccountShow is `selfcloud account show [--account ACC]`.
func AccountShow(args []string, stdout, stderr io.Writer) int {
	c := begin("account show", true, stdout, stderr)
	acc, code, ok := accountOne(c, "account show", args)
	if !ok {
		return code
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodGet, accountRoute(acc), nil, nil)
	if err != nil {
		return c.fail(err)
	}
	if err := printAccountDocument(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// AccountUpdate is `selfcloud account update [--account ACC] [--name NAME] [--external-id ID] [--quota SIZE]
// [--status active|suspended] [--yes]`.
func AccountUpdate(args []string, stdout, stderr io.Writer) int {
	c := begin("account update", true, stdout, stderr)
	c.adminRoutes = true
	var name, ext, status string
	var quota config.ByteSize
	var yes bool
	c.set.StringVar(&name, "name", "", "the account's new `name`")
	c.set.StringVar(&ext, "external-id", "", "the caller's own `id` for the account; an empty value clears it")
	c.set.Func("quota", "the account's storage limit, as `size` (0: no limit)", func(v string) error {
		return quota.UnmarshalText([]byte(v))
	})
	c.set.StringVar(&status, "status", "", "the account's `status`: active or suspended")
	c.set.BoolVar(&yes, "yes", false, "do not ask first when suspending")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("account update", pos); err != nil {
		return c.fail(err)
	}
	given := givenFlags(c.set, "name", "external-id", "quota", "status")
	if len(given) == 0 {
		return c.fail(exitcode.Usagef("account update needs a change to make"))
	}
	if given["name"] {
		name = strings.TrimSpace(name)
		if name == "" {
			return c.fail(exitcode.Usagef("the name is required"))
		}
	}
	if given["quota"] && quota < 0 {
		return c.fail(exitcode.Usagef("the quota may not be negative"))
	}
	if given["status"] && status != "active" && status != "suspended" {
		return c.fail(exitcode.Usagef("--status must be active or suspended"))
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	if given["status"] && status == "suspended" {
		if err := confirmByName(cn, accountRoute(acc), yes,
			"suspend the account; none of its people can sign in until it is active again", stderr); err != nil {
			return c.fail(err)
		}
	}
	body := map[string]any{}
	if given["name"] {
		body["name"] = name
	}
	if given["external-id"] {
		body["external_id"] = ext
	}
	if given["quota"] {
		body["quota_bytes"] = int64(quota)
	}
	if given["status"] {
		body["status"] = status
	}
	resp, err := files.Send(cn, http.MethodPatch, accountRoute(acc), nil, body)
	if err != nil {
		return c.fail(err)
	}
	if err := printAccountDocument(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// AccountRm is `selfcloud account rm [--account ACC] [--yes]`: removes an account and everything in it, in the
// background. Asks first.
func AccountRm(args []string, stdout, stderr io.Writer) int {
	c := begin("account rm", true, stdout, stderr)
	c.adminRoutes = true
	var yes bool
	c.set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	acc, code, ok := accountOne(c, "account rm", args)
	if !ok {
		return code
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	route := accountRoute(acc)
	if err := confirmByName(cn, route, yes,
		"remove the account with every file, person and key in it; this cannot be undone", stderr); err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodDelete, route, nil, nil)
	if err != nil {
		return c.fail(err)
	}
	var job struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := decodeAs(resp.Body, &job, "a job"); err != nil {
		return c.fail(err)
	}
	view := struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}{job.ID, job.Status}
	if c.g.JSON {
		if err := output.JSON(c.stdout, view); err != nil {
			return c.fail(err)
		}
	} else {
		if err := output.KeyValues(c.stdout, []output.KV{
			{Key: "job_id", Value: view.JobID}, {Key: "status", Value: view.Status}}); err != nil {
			return c.fail(err)
		}
	}
	_, _ = io.WriteString(stderr, "selfcloud: note: the account is removed in the background; follow it with "+
		"`selfcloud job show "+job.ID+"`\n")
	return exitcode.OK
}

// usageView is usageJSON as the commands print it. The two nullable fields are pointers, and print empty when null.
type usageView struct {
	QuotaBytes        *int64 `json:"quota_bytes"`
	UsedBytes         int64  `json:"used_bytes"`
	ReservedBytes     int64  `json:"reserved_bytes"`
	AvailableBytes    *int64 `json:"available_bytes"`
	Files             int64  `json:"files"`
	Folders           int64  `json:"folders"`
	UsedFiles         int64  `json:"used_files"`
	UsersActive       int64  `json:"users_active"`
	UsersPending      int64  `json:"users_pending"`
	SearchTextBytes   int64  `json:"search_text_bytes"`
	SearchTextCap     int64  `json:"search_text_cap"`
	SearchTextSkipped int64  `json:"search_text_skipped"`
}

func (v usageView) pairs() []output.KV {
	quota, available := "", ""
	if v.QuotaBytes != nil {
		quota = strconv.FormatInt(*v.QuotaBytes, 10)
	}
	if v.AvailableBytes != nil {
		available = strconv.FormatInt(*v.AvailableBytes, 10)
	}
	itoa := strconv.FormatInt
	return []output.KV{
		{Key: "quota_bytes", Value: quota},
		{Key: "used_bytes", Value: itoa(v.UsedBytes, 10)},
		{Key: "reserved_bytes", Value: itoa(v.ReservedBytes, 10)},
		{Key: "available_bytes", Value: available},
		{Key: "files", Value: itoa(v.Files, 10)},
		{Key: "folders", Value: itoa(v.Folders, 10)},
		{Key: "used_files", Value: itoa(v.UsedFiles, 10)},
		{Key: "users_active", Value: itoa(v.UsersActive, 10)},
		{Key: "users_pending", Value: itoa(v.UsersPending, 10)},
		{Key: "search_text_bytes", Value: itoa(v.SearchTextBytes, 10)},
		{Key: "search_text_cap", Value: itoa(v.SearchTextCap, 10)},
		{Key: "search_text_skipped", Value: itoa(v.SearchTextSkipped, 10)},
	}
}

func printUsageDocument(c *command, body []byte) error {
	var v usageView
	if err := decodeAs(body, &v, "an account's usage"); err != nil {
		return err
	}
	if c.g.JSON {
		return output.JSON(c.stdout, v)
	}
	return output.KeyValues(c.stdout, v.pairs())
}

// AccountUsage is `selfcloud account usage [--account ACC]`.
func AccountUsage(args []string, stdout, stderr io.Writer) int {
	c := begin("account usage", true, stdout, stderr)
	acc, code, ok := accountOne(c, "account usage", args)
	if !ok {
		return code
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodGet, accountRoute(acc)+"/usage", nil, nil)
	if err != nil {
		return c.fail(err)
	}
	if err := printUsageDocument(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// auditView is the part of an audit entry that account audit prints.
type auditView struct {
	ID    string `json:"id"`
	Time  string `json:"time"`
	Actor struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"actor"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Outcome   string `json:"outcome"`
	IP        string `json:"ip"`
	UserAgent string `json:"user_agent"`
	// details is left out: it is a free-form map and cannot be allow-listed.
}

// auditViews decodes a page of audit entries into the allow-list views, so --json prints only the fields the view has.
func auditViews(items []json.RawMessage) ([]auditView, error) {
	views := make([]auditView, 0, len(items))
	for _, raw := range items {
		var v auditView
		if err := decodeAs(raw, &v, "an audit entry"); err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}

// AccountAudit is `selfcloud account audit [--account ACC] [--all] [--action ACTION] [--actor ID]`.
func AccountAudit(args []string, stdout, stderr io.Writer) int {
	c := begin("account audit", true, stdout, stderr)
	var all bool
	var action, actor string
	c.set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	c.set.StringVar(&action, "action", "", "only entries with this `action`")
	c.set.StringVar(&actor, "actor", "", "only entries by this actor `id`")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("account audit", pos); err != nil {
		return c.fail(err)
	}
	if utf8.RuneCountInString(action) > 100 || utf8.RuneCountInString(actor) > 100 {
		return c.fail(exitcode.Usagef("--action and --actor may be at most 100 characters"))
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	q := url.Values{}
	if action != "" {
		q.Set("action", action)
	}
	if actor != "" {
		q.Set("actor", actor)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	items, next, err := files.ListPages(cn, accountRoute(acc)+"/audit", q, all)
	if err != nil {
		return c.fail(err)
	}
	if c.g.JSON {
		if next != "" {
			_, _ = io.WriteString(c.stderr, "selfcloud: more items exist; --all prints every page\n")
		}
		views, err := auditViews(items)
		if err != nil {
			return c.fail(err)
		}
		if err := output.JSON(c.stdout, views); err != nil {
			return c.fail(err)
		}
		return exitcode.OK
	}
	err = printList(c, items, next, []string{"TIME", "ACTOR", "ACTION", "TARGET", "OUTCOME", "IP"},
		func(raw []byte) ([]string, error) {
			var v auditView
			if err := decodeAs(raw, &v, "an audit entry"); err != nil {
				return nil, err
			}
			return []string{v.Time, v.Actor.Kind + ":" + v.Actor.ID, v.Action, v.Target, v.Outcome, v.IP}, nil
		})
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
