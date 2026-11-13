// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

// The webhook commands. They run against /api/v1/accounts/{acc}/webhooks, which an account key reaches: a device token is
// answered "no such account" there, and every change (add, update, rm, rotate-secret, test, retry) is refused to anyone but
// the account key.
//
// A signing secret is shown once, by add and by rotate-secret, and nowhere else. Every other command decodes the answer into
// a view that has no field for a secret, a signature, a header or a body, so whatever the server sends beyond those fields
// never reaches the terminal, plain or --json. No error repeats an answer's text.

import (
	"flag"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/cli/cmdkit"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/webhook"
)

const (
	webhookURLMax         = 2048
	webhookDescriptionMax = 200
	secretNote            = "selfcloud: note: the secret is shown only now; keep it where the receiver can read it, " +
		"because it cannot be read again\n"
)

func hooksRoute(acc string) string { return "/api/v1/accounts/" + acc + "/webhooks" }

// webhookView is a webhook as the commands print it. It is the allow-list: a field the API adds is not shown until it is
// added here.
type webhookView struct {
	ID                  string   `json:"id"`
	URL                 string   `json:"url"`
	Description         *string  `json:"description"`
	Events              []string `json:"events"`
	State               string   `json:"state"`
	DisabledReason      *string  `json:"disabled_reason"`
	ConsecutiveFailures int      `json:"consecutive_failures"`
	LastSuccessAt       *string  `json:"last_success_at"`
	LastFailureAt       *string  `json:"last_failure_at"`
	PreviousSecretUntil *string  `json:"previous_secret_until"`
	CreatedAt           string   `json:"created_at"`
	UpdatedAt           string   `json:"updated_at"`
}

// webhookShown is a webhook that add or rotate-secret shows with its secret, once.
type webhookShown struct {
	webhookView
	Secret string `json:"secret"`
}

// webhookDetail is a webhook with its newest deliveries, as show prints it.
type webhookDetail struct {
	webhookView
	RecentDeliveries []deliveryView `json:"recent_deliveries"`
}

// deliveryView is a delivery as the commands print it: the log row, never the event's body or the request's headers.
type deliveryView struct {
	ID            string  `json:"id"`
	EventID       string  `json:"event_id"`
	Type          string  `json:"type"`
	State         string  `json:"state"`
	Attempts      int     `json:"attempts"`
	LastCode      *int    `json:"last_code"`
	LastError     *string `json:"last_error"`
	NextAttemptAt string  `json:"next_attempt_at"`
	CreatedAt     string  `json:"created_at"`
	FinishedAt    *string `json:"finished_at"`
}

func (v webhookView) pairs() []output.KV {
	return []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "url", Value: v.URL},
		{Key: "description", Value: cmdkit.Str(v.Description)},
		{Key: "events", Value: strings.Join(v.Events, ",")},
		{Key: "state", Value: v.State},
		{Key: "disabled_reason", Value: cmdkit.Str(v.DisabledReason)},
		{Key: "consecutive_failures", Value: cmdkit.Itoa(v.ConsecutiveFailures)},
		{Key: "last_success_at", Value: cmdkit.Str(v.LastSuccessAt)},
		{Key: "last_failure_at", Value: cmdkit.Str(v.LastFailureAt)},
		{Key: "previous_secret_until", Value: cmdkit.Str(v.PreviousSecretUntil)},
		{Key: "created_at", Value: v.CreatedAt},
		{Key: "updated_at", Value: v.UpdatedAt},
	}
}

func (d deliveryView) pairs() []output.KV {
	code := ""
	if d.LastCode != nil {
		code = cmdkit.Itoa(*d.LastCode)
	}
	return []output.KV{
		{Key: "id", Value: d.ID},
		{Key: "event_id", Value: d.EventID},
		{Key: "type", Value: d.Type},
		{Key: "state", Value: d.State},
		{Key: "attempts", Value: cmdkit.Itoa(d.Attempts)},
		{Key: "last_code", Value: code},
		{Key: "last_error", Value: cmdkit.Str(d.LastError)},
		{Key: "next_attempt_at", Value: d.NextAttemptAt},
		{Key: "created_at", Value: d.CreatedAt},
		{Key: "finished_at", Value: cmdkit.Str(d.FinishedAt)},
	}
}

var deliveryHeaders = []string{"ID", "STATE", "TYPE", "ATTEMPTS", "CODE", "ERROR", "CREATED"}

func (d deliveryView) row() []string {
	code := ""
	if d.LastCode != nil {
		code = cmdkit.Itoa(*d.LastCode)
	}
	return []string{d.ID, d.State, d.Type, cmdkit.Itoa(d.Attempts), code, cmdkit.Str(d.LastError), d.CreatedAt}
}

// eventList is the repeatable --event flag. A value may hold several types, separated by commas.
type eventList struct{ values []string }

func (e *eventList) String() string { return strings.Join(e.values, ",") }

func (e *eventList) Set(v string) error {
	e.values = append(e.values, v)
	return nil
}

// events returns the types named, in the order given, each once. Each must be one the server knows, so a misspelling costs
// no request.
func (e *eventList) events() ([]string, error) {
	var out []string
	for _, v := range e.values {
		for _, name := range strings.Split(v, ",") {
			if !webhook.ValidEvent(name) {
				return nil, exitcode.Usagef("--event names a type that is not in the list; the types are %s",
					strings.Join(webhook.Events, ", "))
			}
			dup := false
			for _, have := range out {
				dup = dup || have == name
			}
			if !dup {
				out = append(out, name)
			}
		}
	}
	return out, nil
}

// cleanURL checks the form of an endpoint address: an absolute http or https address with a host and no user name or
// password. The network policy is the server's (no loopback or private target unless it is configured), so a refusal of
// it is shown as it comes. A refusal here never repeats the address, which may hold a password or a token.
func cleanURL(raw string) (string, error) {
	bad := func(why string) error {
		return exitcode.Usagef("--endpoint must be an absolute http or https address: %s", why)
	}
	if raw == "" {
		return "", bad("it is empty")
	}
	if len(raw) > webhookURLMax {
		return "", bad("it is longer than 2048 characters")
	}
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c <= 0x20 || c >= 0x7f {
			return "", bad("it must be ASCII, with no spaces or control characters")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", bad("it is not a valid address")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", bad("it must start with http:// or https://")
	}
	if u.User != nil {
		return "", bad("it must not carry a user name or password; have the receiver check the signature instead")
	}
	if u.Opaque != "" || u.Hostname() == "" {
		return "", bad("it must name a host")
	}
	return raw, nil
}

func cleanDescription(s string) error {
	if utf8.RuneCountInString(s) > webhookDescriptionMax {
		return exitcode.Usagef("--description is longer than 200 characters")
	}
	return nil
}

// hookArg checks a webhook id.
func hookArg(pos []string, name string) (string, error) {
	if len(pos) != 1 {
		return "", exitcode.Usagef("%s takes one webhook id", name)
	}
	return cmdkit.IDArg("whk", "a webhook", pos[0])
}

// decodeHook reads a webhook answer. An answer with no id is not one.
func decodeHook[T interface{ identity() string }](body []byte) (T, error) {
	var v T
	if err := cmdkit.DecodeAs(body, &v, "a webhook"); err != nil {
		return v, err
	}
	if v.identity() == "" {
		return v, cmdkit.ErrNotA("a webhook")
	}
	return v, nil
}

func (v webhookView) identity() string   { return v.ID }
func (v webhookShown) identity() string  { return v.ID }
func (v webhookDetail) identity() string { return v.ID }

func (c *command) printHook(pairs []output.KV, jsonDoc any) error {
	if c.G.JSON {
		return output.JSON(c.Stdout, jsonDoc)
	}
	return output.KeyValues(c.Stdout, pairs)
}

// printShown prints a webhook with its secret, once, and a note that it is not shown again.
func (c *command) printShown(v webhookShown) error {
	if v.Secret == "" {
		return cmdkit.ErrNotA("a webhook with its secret (webhook " + v.ID + "); the secret is not shown again, so rotate it")
	}
	if c.G.JSON {
		return output.JSON(c.Stdout, v)
	}
	if err := output.KeyValues(c.Stdout, append(v.pairs(), output.KV{Key: "secret", Value: v.Secret})); err != nil {
		return err
	}
	_, _ = io.WriteString(c.Stderr, secretNote)
	return nil
}

// WebhookAdd is `selfcloud webhook add --account ACC --endpoint ADDRESS --event TYPE... [--description TEXT]`. It prints the
// signing secret, once.
func WebhookAdd(args []string, stdout, stderr io.Writer) int {
	c := begin("webhook add", true, stdout, stderr)
	var raw, desc string
	var evs eventList
	c.Set.StringVar(&raw, "endpoint", "", "the endpoint's `address`: an absolute https (or http) address; the server decides which hosts it may reach")
	c.Set.Var(&evs, "event", "an event `type` to send; repeat it, or separate types with commas")
	c.Set.StringVar(&desc, "description", "", "a `note` of up to 200 characters")
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	if err := cmdkit.NoArgs("webhook add", pos); err != nil {
		return c.fail(err)
	}
	addr, err := cleanURL(raw)
	if err != nil {
		return c.fail(err)
	}
	events, err := evs.events()
	if err != nil {
		return c.fail(err)
	}
	if len(events) == 0 {
		return c.fail(exitcode.Usagef("webhook add needs at least one --event"))
	}
	if err := cleanDescription(desc); err != nil {
		return c.fail(err)
	}
	acc, err := c.AccountID()
	if err != nil {
		return c.fail(err)
	}
	body := map[string]any{"url": addr, "events": events}
	if desc != "" {
		body["description"] = desc
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPost, hooksRoute(acc), nil, body)
	if err != nil {
		return c.fail(err)
	}
	v, err := decodeHook[webhookShown](resp.Body)
	if err == nil {
		err = c.printShown(v)
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// WebhookLs is `selfcloud webhook ls --account ACC [--all]`.
func WebhookLs(args []string, stdout, stderr io.Writer) int {
	c := begin("webhook ls", true, stdout, stderr)
	var all bool
	c.Set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	if err := cmdkit.NoArgs("webhook ls", pos); err != nil {
		return c.fail(err)
	}
	acc, err := c.AccountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	items, next, err := files.ListPages(cn, hooksRoute(acc), nil, all)
	var hooks []webhookView
	if err == nil {
		hooks, err = cmdkit.DecodeItems[webhookView](items, "a webhook")
	}
	if err == nil {
		err = cmdkit.PrintList(c.Command, hooks, next != "", []string{"ID", "STATE", "EVENTS", "URL"}, func(v webhookView) []string {
			return []string{v.ID, v.State, strings.Join(v.Events, ","), v.URL}
		})
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// WebhookShow is `selfcloud webhook show --account ACC HOOK`: the endpoint and its newest deliveries. The secret is not
// shown.
func WebhookShow(args []string, stdout, stderr io.Writer) int {
	c := begin("webhook show", true, stdout, stderr)
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	hook, err := hookArg(pos, "webhook show")
	if err != nil {
		return c.fail(err)
	}
	acc, err := c.AccountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodGet, hooksRoute(acc)+"/"+hook, nil, nil)
	if err != nil {
		return c.fail(err)
	}
	d, err := decodeHook[webhookDetail](resp.Body)
	if err == nil {
		if d.RecentDeliveries == nil {
			d.RecentDeliveries = []deliveryView{}
		}
		err = c.printHook(d.pairs(), d)
	}
	if err == nil && !c.G.JSON && len(d.RecentDeliveries) > 0 {
		rows := make([][]string, 0, len(d.RecentDeliveries))
		for _, dv := range d.RecentDeliveries {
			rows = append(rows, dv.row())
		}
		_, _ = io.WriteString(c.Stdout, "\n")
		err = output.Table(c.Stdout, deliveryHeaders, rows)
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// WebhookUpdate is `selfcloud webhook update --account ACC HOOK [--endpoint ADDRESS] [--event TYPE...] [--description TEXT]
// [--state active|paused]`. Only what is given is sent.
func WebhookUpdate(args []string, stdout, stderr io.Writer) int {
	c := begin("webhook update", true, stdout, stderr)
	var raw, desc, state string
	var evs eventList
	c.Set.StringVar(&raw, "endpoint", "", "a new `address` for the endpoint")
	c.Set.Var(&evs, "event", "replace the events with these `types`; repeat it, or separate types with commas")
	c.Set.StringVar(&desc, "description", "", "a new `note`; an empty one clears it")
	c.Set.StringVar(&state, "state", "", "`active` or `paused`")
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	hook, err := hookArg(pos, "webhook update")
	if err != nil {
		return c.fail(err)
	}
	body := map[string]any{}
	if cmdkit.FlagGiven(c.Set, "endpoint") {
		if body["url"], err = cleanURL(raw); err != nil {
			return c.fail(err)
		}
	}
	if cmdkit.FlagGiven(c.Set, "event") {
		events, err := evs.events()
		if err != nil {
			return c.fail(err)
		}
		if len(events) == 0 {
			return c.fail(exitcode.Usagef("--event names no type"))
		}
		body["events"] = events
	}
	if cmdkit.FlagGiven(c.Set, "description") {
		if err := cleanDescription(desc); err != nil {
			return c.fail(err)
		}
		body["description"] = desc
	}
	if cmdkit.FlagGiven(c.Set, "state") {
		if state != "active" && state != "paused" {
			return c.fail(exitcode.Usagef("--state is active or paused"))
		}
		body["state"] = state
	}
	if len(body) == 0 {
		return c.fail(exitcode.Usagef("webhook update changes nothing: give --endpoint, --event, --description or --state"))
	}
	acc, err := c.AccountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPatch, hooksRoute(acc)+"/"+hook, nil, body)
	if err != nil {
		return c.fail(err)
	}
	v, err := decodeHook[webhookView](resp.Body)
	if err == nil {
		err = c.printHook(v.pairs(), v)
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// WebhookRm is `selfcloud webhook rm --account ACC HOOK [--yes]`. Asks first, by the webhook's id.
func WebhookRm(args []string, stdout, stderr io.Writer) int {
	c := begin("webhook rm", true, stdout, stderr)
	var yes bool
	c.Set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	hook, err := hookArg(pos, "webhook rm")
	if err != nil {
		return c.fail(err)
	}
	acc, err := c.AccountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	if err := cmdkit.ConfirmByID(yes, hook, "remove the webhook; its delivery log goes with it", stderr); err != nil {
		return c.fail(err)
	}
	if _, err := files.Send(cn, http.MethodDelete, hooksRoute(acc)+"/"+hook, nil, nil); err != nil {
		return c.fail(err)
	}
	if err := cmdkit.PrintDone(c.Command, hook); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// WebhookRotateSecret is `selfcloud webhook rotate-secret --account ACC HOOK [--yes]`. It prints the new secret, once.
// Asks first, by the webhook's id.
func WebhookRotateSecret(args []string, stdout, stderr io.Writer) int {
	c := begin("webhook rotate-secret", true, stdout, stderr)
	var yes bool
	c.Set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	hook, err := hookArg(pos, "webhook rotate-secret")
	if err != nil {
		return c.fail(err)
	}
	acc, err := c.AccountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	if err := cmdkit.ConfirmByID(yes, hook, "replace the webhook's signing secret; the receiver must be given the new one", stderr); err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPost, hooksRoute(acc)+"/"+hook+"/secret", nil, nil)
	if err != nil {
		return c.fail(err)
	}
	v, err := decodeHook[webhookShown](resp.Body)
	if err == nil {
		err = c.printShown(v)
	}
	if err != nil {
		return c.fail(err)
	}
	// The secret it replaces goes on signing for a while, so a receiver can switch without a gap. The server keeps only
	// one old secret, so a second rotation ends that period at once.
	// The end is the server's text on a terminal, so it is printed only as the time it parses to.
	if !c.G.JSON && v.PreviousSecretUntil != nil {
		if until, err := time.Parse(time.RFC3339, *v.PreviousSecretUntil); err == nil {
			_, _ = io.WriteString(c.Stderr, "selfcloud: note: the old secret keeps signing until "+until.Format(time.RFC3339)+
				"; a second rotation ends that at once\n")
		}
	}
	return exitcode.OK
}

// WebhookTest is `selfcloud webhook test --account ACC HOOK`: queues a test event for the endpoint.
func WebhookTest(args []string, stdout, stderr io.Writer) int {
	c := begin("webhook test", true, stdout, stderr)
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	hook, err := hookArg(pos, "webhook test")
	if err != nil {
		return c.fail(err)
	}
	acc, err := c.AccountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPost, hooksRoute(acc)+"/"+hook+"/test", nil, nil)
	if err != nil {
		return c.fail(err)
	}
	var v struct {
		EventID string `json:"event_id"`
	}
	if err = cmdkit.DecodeAs(resp.Body, &v, "a test event"); err == nil && v.EventID == "" {
		err = cmdkit.ErrNotA("a test event")
	}
	if err == nil {
		if c.G.JSON {
			err = output.JSON(c.Stdout, v)
		} else {
			err = output.KeyValues(c.Stdout, []output.KV{{Key: "event_id", Value: v.EventID}})
		}
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// WebhookDeliveries is `selfcloud webhook deliveries --account ACC HOOK [--all]`: the delivery log, newest first.
func WebhookDeliveries(args []string, stdout, stderr io.Writer) int {
	c := begin("webhook deliveries", true, stdout, stderr)
	var all bool
	c.Set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	hook, err := hookArg(pos, "webhook deliveries")
	if err != nil {
		return c.fail(err)
	}
	acc, err := c.AccountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	items, next, err := files.ListPages(cn, hooksRoute(acc)+"/"+hook+"/deliveries", nil, all)
	var list []deliveryView
	if err == nil {
		list, err = cmdkit.DecodeItems[deliveryView](items, "a delivery")
	}
	if err == nil {
		err = cmdkit.PrintList(c.Command, list, next != "", deliveryHeaders, deliveryView.row)
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// WebhookRetry is `selfcloud webhook retry --account ACC HOOK DELIVERY`: queues a failed delivery again.
func WebhookRetry(args []string, stdout, stderr io.Writer) int {
	c := begin("webhook retry", true, stdout, stderr)
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	if len(pos) != 2 {
		return c.fail(exitcode.Usagef("webhook retry takes a webhook id and a delivery id"))
	}
	hook, err := cmdkit.IDArg("whk", "a webhook", pos[0])
	if err != nil {
		return c.fail(err)
	}
	dlv, err := cmdkit.IDArg("dlv", "a delivery", pos[1])
	if err != nil {
		return c.fail(err)
	}
	acc, err := c.AccountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPost, hooksRoute(acc)+"/"+hook+"/deliveries/"+dlv+"/retry", nil, nil)
	if err != nil {
		return c.fail(err)
	}
	var v deliveryView
	if err = cmdkit.DecodeAs(resp.Body, &v, "a delivery"); err == nil && v.ID == "" {
		err = cmdkit.ErrNotA("a delivery")
	}
	if err == nil {
		if c.G.JSON {
			err = output.JSON(c.Stdout, v)
		} else {
			err = output.KeyValues(c.Stdout, v.pairs())
		}
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

var _ flag.Value = (*eventList)(nil)
