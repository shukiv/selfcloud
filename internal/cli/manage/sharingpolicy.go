// SPDX-License-Identifier: AGPL-3.0-or-later

// The sharing policy commands (design-cli.md section 8, slice cli9b4): sharing policy show|set. They read and change the
// account's switches for links that anyone can open, for comments, and for who viewed. Only the account's owner or key
// may change them; the show command also answers managers.

package manage

import (
	"flag"
	"io"
	"net/http"
	"strconv"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// sharingPolicyView is the part of the policy document that the command prints, plain or under --json. AnyoneLinks
// counts the account's anyone links; the others are switches. A field the API adds is not shown until it is added here.
type sharingPolicyView struct {
	PublicLinks bool `json:"public_links"`
	AnyoneLinks int  `json:"anyone_links"`
	Comments    bool `json:"comments"`
	WhoViewed   bool `json:"who_viewed"`
}

func (v sharingPolicyView) pairs() []output.KV {
	return []output.KV{
		{Key: "public_links", Value: strconv.FormatBool(v.PublicLinks)},
		{Key: "anyone_links", Value: strconv.Itoa(v.AnyoneLinks)},
		{Key: "comments", Value: strconv.FormatBool(v.Comments)},
		{Key: "who_viewed", Value: strconv.FormatBool(v.WhoViewed)},
	}
}

// printSharingPolicy prints the policy, from the allow-list view: a field the API adds is not shown until
// sharingPolicyView has it.
func printSharingPolicy(c *command, body []byte) error {
	var v sharingPolicyView
	if err := decodeAs(body, &v, "a sharing policy"); err != nil {
		return err
	}
	if c.g.JSON {
		return output.JSON(c.stdout, v)
	}
	return output.KeyValues(c.stdout, v.pairs())
}

func sharingPolicyRoute(acc string) string { return "/api/v1/accounts/" + acc + "/sharing-policy" }

// sharingSwitchArg reads a true or false that must be given with the flag, as --public-links false.
func sharingSwitchArg(flagName, value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, exitcode.Usagef("--%s takes true or false", flagName)
}

// SharingPolicyShow is `selfcloud sharing policy show [--account ACC]`.
func SharingPolicyShow(args []string, stdout, stderr io.Writer) int {
	c := begin("sharing policy show", true, stdout, stderr)
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("sharing policy show", pos); err != nil {
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
	resp, err := files.Send(cn, http.MethodGet, sharingPolicyRoute(acc), nil, nil)
	if err != nil {
		return c.fail(err)
	}
	if err := printSharingPolicy(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// SharingPolicySet is `selfcloud sharing policy set [--account ACC] [--public-links true|false]
// [--comments true|false] [--who-viewed true|false]`: it changes only the switches that are given. Turning public links
// off asks first, because every anyone link of the account stops working.
func SharingPolicySet(args []string, stdout, stderr io.Writer) int {
	c := begin("sharing policy set", true, stdout, stderr)
	var yes bool
	var publicLinks, comments, whoViewed string
	c.set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	c.set.StringVar(&publicLinks, "public-links", "", "links that anyone can open: true or false")
	c.set.StringVar(&comments, "comments", "", "comments on files: true or false")
	c.set.StringVar(&whoViewed, "who-viewed", "", "the who-viewed switch: true or false")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	seen := map[string]bool{}
	c.set.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if err := noArgs("sharing policy set", pos); err != nil {
		return c.fail(err)
	}
	if !seen["public-links"] && !seen["comments"] && !seen["who-viewed"] {
		return c.fail(exitcode.Usagef("sharing policy set needs --public-links, --comments or --who-viewed"))
	}
	body := map[string]bool{}
	if seen["public-links"] {
		value, err := sharingSwitchArg("public-links", publicLinks)
		if err != nil {
			return c.fail(err)
		}
		body["public_links"] = value
	}
	if seen["comments"] {
		value, err := sharingSwitchArg("comments", comments)
		if err != nil {
			return c.fail(err)
		}
		body["comments"] = value
	}
	if seen["who-viewed"] {
		value, err := sharingSwitchArg("who-viewed", whoViewed)
		if err != nil {
			return c.fail(err)
		}
		body["who_viewed"] = value
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	if value, ok := body["public_links"]; ok && !value {
		if err := files.Confirm(yes, acc, "turn off public links; every public link of the account stops working", stderr); err != nil {
			return c.fail(err)
		}
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPatch, sharingPolicyRoute(acc), nil, body)
	if err != nil {
		return c.fail(err)
	}
	if err := printSharingPolicy(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
