// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// linkRoles are the roles a share link may give. A link never gives manager.
var linkRoles = []string{"viewer", "commenter", "contributor", "editor"}

// The longest title and message of a drop link, in characters; the API refuses more.
const (
	dropTitleMax   = 100
	dropMessageMax = 1000
)

func linkPath(linkID, suffix string) string { return "/api/v1/share-links/" + linkID + suffix }

func accountLinksPath(accountID, suffix string) string {
	return "/api/v1/accounts/" + accountID + "/share-links" + suffix
}

// linkKeys are the fields a link's key=value output shows, in order. password_set and upload_only show only when true.
var linkKeys = []string{"id", "node_id", "audience", "role", "expires_at", "password_set", "upload_only"}

// printLink writes a link's fields from the server's link document, known fields only. When secretURL is not empty it is
// the one place a secret is written: the last line, on the unscrubbed stream, under the name url. Under --json the document
// is {link, url} with the known fields of the link, or the known fields alone when there is no address.
func (r *run) printLink(link []byte, secretURL string) error {
	if r.g.JSON {
		if secretURL != "" {
			return output.JSON(r.raw, map[string]any{"link": linkFields(link), "url": secretURL})
		}
		return output.JSON(r.out, linkFields(link))
	}
	fields := linkFields(link)
	var kv []output.KV
	for _, k := range linkKeys {
		if v := flagText(fields, k); v != "" && v != "false" {
			kv = append(kv, output.KV{Key: k, Value: v})
		}
	}
	if err := output.KeyValues(r.out, kv); err != nil {
		return err
	}
	if secretURL != "" {
		return output.KeyValues(r.raw, []output.KV{{Key: "url", Value: secretURL}})
	}
	return nil
}

const secretNotice = "selfcloud: this is the only time the link's secret is shown; it cannot be read again " +
	"(link reset makes a new one)"

// printNewLink writes the answer of a create or a reset: the link, and its URL once.
func (r *run) printNewLink(body []byte) int {
	var doc struct {
		Link json.RawMessage `json:"link"`
		URL  string          `json:"url"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.URL == "" {
		return r.fail(fmt.Errorf("the server's answer holds no link"))
	}
	if err := r.printLink(doc.Link, doc.URL); err != nil {
		return r.fail(err)
	}
	_, _ = fmt.Fprintln(r.err, secretNotice)
	return exitcode.OK
}

// textLimit checks a drop link's text against the API's limits.
func textLimit(name, v string, max int) error {
	if utf8.RuneCountInString(v) > max {
		return exitcode.Usagef("the %s may be at most %d characters", name, max)
	}
	return nil
}

// LinkCreate is `selfcloud link create`: makes a node's share link, or with --signed a short-lived download address. The
// address, which holds the secret, is written once, here, and cannot be read again.
func LinkCreate(args []string, stdout, stderr io.Writer) int {
	var audience, role, expires, expiresIn, passwordFile, title, message, ttl string
	var drop, askName, notify, signed, inline bool
	r, code, ok := begin("link create", args, stdout, stderr, func(set *flag.FlagSet) {
		set.StringVar(&audience, "audience", "", "who can open it: organisation (the default) or anyone")
		set.StringVar(&role, "role", "", "the role it gives: viewer (the default), commenter, contributor or editor")
		set.StringVar(&expires, "expires", "", "when it ends, a `time` such as 2026-12-31T00:00:00Z")
		set.StringVar(&expiresIn, "expires-in", "", "when it ends, as a `duration` from now such as 36h or 30d")
		set.StringVar(&passwordFile, "password-file", "", "read the password of a link that anyone can open from `file`, "+
			"or from standard input with -; never from the command line")
		set.BoolVar(&drop, "drop", false, "a drop link: anyone can send files into a directory, and reads nothing")
		set.StringVar(&title, "title", "", "a drop link's title")
		set.StringVar(&message, "message", "", "a drop link's message")
		set.BoolVar(&askName, "ask-name", false, "a drop link asks the sender's name")
		set.BoolVar(&notify, "notify", false, "a drop link mails its owner on an upload")
		set.BoolVar(&signed, "signed", false, "make a short-lived download address of a file instead")
		set.StringVar(&ttl, "ttl", "", "with --signed, how long it lasts, a `duration` such as 1h (default: the server's)")
		set.BoolVar(&inline, "inline", false, "with --signed, an address to view the file, not to save it")
	})
	if !ok {
		return code
	}
	if len(r.pos) != 1 {
		return r.fail(exitcode.Usagef("link create takes one node id"))
	}
	nodeID, err := idArg("nod", "a node", r.pos[0])
	if err != nil {
		return r.fail(err)
	}
	if signed {
		return r.createSigned(nodeID, ttl, inline, audience != "" || role != "" || expires != "" || expiresIn != "" ||
			passwordFile != "" || drop || title != "" || message != "" || askName || notify)
	}
	if ttl != "" || inline {
		return r.fail(exitcode.Usagef("--ttl and --inline go with --signed"))
	}
	if !drop && (title != "" || message != "" || askName || notify) {
		return r.fail(exitcode.Usagef("--title, --message, --ask-name and --notify belong to a drop link (--drop)"))
	}
	body := map[string]any{}
	switch {
	case drop && role != "":
		err = exitcode.Usagef("a drop link gives no role; it takes uploads only")
	case drop && audience != "" && audience != "anyone":
		err = exitcode.Usagef("a drop link is a link that anyone can open")
	case drop:
		audience = "anyone"
		body["upload_only"] = true
	case audience == "":
		audience = "organisation"
	}
	if err == nil && audience != "organisation" && audience != "anyone" {
		err = exitcode.Usagef("--audience is organisation or anyone")
	}
	if err == nil && role != "" && !validRole(role, linkRoles) {
		err = exitcode.Usagef("--role is one of viewer, commenter, contributor, editor (a link never gives manager)")
	}
	if err == nil {
		err = textLimit("title", title, dropTitleMax)
	}
	if err == nil {
		err = textLimit("message", message, dropMessageMax)
	}
	var end string
	if err == nil {
		end, err = expiryArg(expires, expiresIn, time.Now())
	}
	if err == nil && passwordFile != "" {
		var pw string
		if pw, err = readPassword(passwordFile); err == nil {
			r.hide(pw)
			body["password"] = pw
		}
	}
	if err != nil {
		return r.fail(err)
	}
	body["audience"] = audience
	if role != "" {
		body["role"] = role
	}
	if end != "" {
		body["expires_at"] = end
	}
	if title != "" {
		body["title"] = title
	}
	if message != "" {
		body["message"] = message
	}
	if askName {
		body["ask_name"] = true
	}
	if notify {
		body["notify"] = true
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	resp, err := files.Send(c, "POST", nodePath(nodeID, "/share-links"), nil, body)
	if err != nil {
		return r.fail(err)
	}
	return r.printNewLink(resp.Body)
}

// createSigned makes the signed download address of a file (POST /nodes/{nod}/links).
func (r *run) createSigned(nodeID, ttl string, inline, others bool) int {
	if others {
		return r.fail(exitcode.Usagef("--signed takes --ttl and --inline only"))
	}
	body := map[string]any{"disposition": "attachment"}
	if inline {
		body["disposition"] = "inline"
	}
	if ttl != "" {
		d, err := durationArg(ttl)
		if err != nil || d < time.Second {
			return r.fail(exitcode.Usagef("--ttl is a duration of at least a second, such as 1h"))
		}
		body["ttl"] = int64(d / time.Second)
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	resp, err := files.Send(c, "POST", nodePath(nodeID, "/links"), nil, body)
	if err != nil {
		return r.fail(err)
	}
	var doc struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(resp.Body, &doc); err != nil || doc.URL == "" {
		return r.fail(fmt.Errorf("the server's answer holds no address"))
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(resp.Body, &fields)
	known := map[string]string{"url": doc.URL}
	var kv []output.KV
	for _, k := range []string{"disposition", "expires_at"} {
		if v := flagText(fields, k); v != "" {
			known[k] = v
			kv = append(kv, output.KV{Key: k, Value: v})
		}
	}
	kv = append(kv, output.KV{Key: "url", Value: doc.URL})
	if r.g.JSON {
		err = output.JSON(r.raw, known)
	} else {
		err = output.KeyValues(r.raw, kv)
	}
	if err != nil {
		return r.fail(err)
	}
	_, _ = fmt.Fprintln(r.err, "selfcloud: this address works for anyone who has it until it ends; it cannot be read again")
	return exitcode.OK
}

// LinkLs is `selfcloud link ls`: the share links on a node and above it, or every live link of an account. It never prints
// a secret or an address: only the fields it knows.
func LinkLs(args []string, stdout, stderr io.Writer) int {
	var account, audience, kind string
	var all bool
	r, code, ok := begin("link ls", args, stdout, stderr, func(set *flag.FlagSet) {
		set.StringVar(&account, "account", "", "list the links of this account, `id` (acc_..., default: $"+accountEnv+" when no node is named)")
		set.StringVar(&audience, "audience", "", "with the account: only organisation or anyone links")
		set.StringVar(&kind, "kind", "", "with the account: only access or drop links")
		set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	})
	if !ok {
		return code
	}
	var path string
	q := url.Values{}
	switch {
	case len(r.pos) == 1 && account == "":
		nodeID, err := idArg("nod", "a node", r.pos[0])
		if err == nil && (audience != "" || kind != "") {
			err = exitcode.Usagef("--audience and --kind go with the account's list")
		}
		if err != nil {
			return r.fail(err)
		}
		path = nodePath(nodeID, "/share-links")
	case len(r.pos) == 0 && (account != "" || accountFromEnv()):
		accountID, err := accountArg(account)
		if err == nil && audience != "" && audience != "organisation" && audience != "anyone" {
			err = exitcode.Usagef("--audience is organisation or anyone")
		}
		if err == nil && kind != "" && kind != "access" && kind != "drop" {
			err = exitcode.Usagef("--kind is access or drop")
		}
		if err != nil {
			return r.fail(err)
		}
		path = accountLinksPath(accountID, "")
		if audience != "" {
			q.Set("audience", audience)
		}
		if kind != "" {
			q.Set("kind", kind)
		}
	default:
		return r.fail(exitcode.Usagef("link ls takes one node id, or --account"))
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	items, next, err := files.ListPages(c, path, q, all)
	if err != nil {
		return r.fail(err)
	}
	r.listNotice(next)
	kept := make([]map[string]json.RawMessage, 0, len(items))
	for _, raw := range items {
		kept = append(kept, linkFields(raw))
	}
	if r.g.JSON {
		if err := output.JSON(r.out, kept); err != nil {
			return r.fail(err)
		}
		return exitcode.OK
	}
	rows := make([][]string, 0, len(kept))
	for _, m := range kept {
		kind := "access"
		if flagText(m, "upload_only") == "true" {
			kind = "drop"
		}
		rows = append(rows, []string{flagText(m, "id"), flagText(m, "node_id"), flagText(m, "audience"), flagText(m, "role"),
			kind, flagText(m, "expires_at"), flagText(m, "password_set"), flagText(m, "paused"), flagText(m, "item_path")})
	}
	if err := output.Table(r.out, []string{"ID", "NODE", "AUDIENCE", "ROLE", "KIND", "EXPIRES", "PASSWORD", "PAUSED", "ITEM"}, rows); err != nil {
		return r.fail(err)
	}
	return exitcode.OK
}

// LinkUpdate is `selfcloud link update`: changes a link. A field not given keeps its value.
func LinkUpdate(args []string, stdout, stderr io.Writer) int {
	var audience, role, expires, expiresIn, passwordFile string
	var noExpiry, noPassword bool
	var askName, notify optBool
	var title, message optString
	r, code, ok := begin("link update", args, stdout, stderr, func(set *flag.FlagSet) {
		set.StringVar(&audience, "audience", "", "who can open it: organisation or anyone")
		set.StringVar(&role, "role", "", "the role it gives: viewer, commenter, contributor or editor")
		set.StringVar(&expires, "expires", "", "when it ends, a `time` such as 2026-12-31T00:00:00Z")
		set.StringVar(&expiresIn, "expires-in", "", "when it ends, as a `duration` from now such as 36h or 30d")
		set.BoolVar(&noExpiry, "no-expiry", false, "let it run with no end (an anyone link refuses this)")
		set.StringVar(&passwordFile, "password-file", "", "set the password from `file`, or from standard input with -; "+
			"never from the command line")
		set.BoolVar(&noPassword, "no-password", false, "remove the password")
		set.Var(&title, "title", "a drop link's title (--title= clears it)")
		set.Var(&message, "message", "a drop link's message (--message= clears it)")
		set.Var(&askName, "ask-name", "a drop link asks the sender's name (--ask-name=false to stop)")
		set.Var(&notify, "notify", "a drop link mails its owner on an upload (--notify=false to stop)")
	})
	if !ok {
		return code
	}
	if len(r.pos) != 1 {
		return r.fail(exitcode.Usagef("link update takes one link id"))
	}
	linkID, err := idArg("shl", "a link", r.pos[0])
	body := map[string]any{}
	if err == nil && audience != "" {
		if audience != "organisation" && audience != "anyone" {
			err = exitcode.Usagef("--audience is organisation or anyone")
		}
		body["audience"] = audience
	}
	if err == nil && role != "" {
		if !validRole(role, linkRoles) {
			err = exitcode.Usagef("--role is one of viewer, commenter, contributor, editor (a link never gives manager)")
		}
		body["role"] = role
	}
	if err == nil && noExpiry && (expires != "" || expiresIn != "") {
		err = exitcode.Usagef("give --no-expiry, or an end, not both")
	}
	if err == nil && noPassword && passwordFile != "" {
		err = exitcode.Usagef("give --no-password, or --password-file, not both")
	}
	if err == nil {
		var end string
		if end, err = expiryArg(expires, expiresIn, time.Now()); end != "" {
			body["expires_at"] = end
		}
	}
	if err == nil && noExpiry {
		body["expires_at"] = nil
	}
	if err == nil && noPassword {
		body["password"] = ""
	}
	if err == nil && passwordFile != "" {
		var pw string
		if pw, err = readPassword(passwordFile); err == nil {
			r.hide(pw)
			body["password"] = pw
		}
	}
	if err == nil && title.set {
		err = textLimit("title", title.val, dropTitleMax)
		body["title"] = title.val
	}
	if err == nil && message.set {
		err = textLimit("message", message.val, dropMessageMax)
		body["message"] = message.val
	}
	if askName.set {
		body["ask_name"] = askName.val
	}
	if notify.set {
		body["notify"] = notify.val
	}
	if err == nil && len(body) == 0 {
		err = exitcode.Usagef("nothing to change: give a flag such as --role or --expires-in")
	}
	if err != nil {
		return r.fail(err)
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	resp, err := files.Send(c, "PATCH", linkPath(linkID, ""), nil, body)
	if err != nil {
		return r.fail(err)
	}
	if err := r.printLink(resp.Body, ""); err != nil {
		return r.fail(err)
	}
	return exitcode.OK
}

// LinkReset is `selfcloud link reset`: gives a link a new secret. The old address stops working; the new one is written
// once, here.
func LinkReset(args []string, stdout, stderr io.Writer) int {
	r, code, ok := begin("link reset", args, stdout, stderr, nil)
	if !ok {
		return code
	}
	if len(r.pos) != 1 {
		return r.fail(exitcode.Usagef("link reset takes one link id"))
	}
	linkID, err := idArg("shl", "a link", r.pos[0])
	if err != nil {
		return r.fail(err)
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	resp, err := files.Send(c, "POST", linkPath(linkID, "/reset"), nil, nil)
	if err != nil {
		return r.fail(err)
	}
	return r.printNewLink(resp.Body)
}

// LinkRm is `selfcloud link rm`: removes share links, or with --public every link of an account that anyone can open. It
// asks first.
func LinkRm(args []string, stdout, stderr io.Writer) int {
	var account string
	var public, yes bool
	r, code, ok := begin("link rm", args, stdout, stderr, func(set *flag.FlagSet) {
		set.BoolVar(&public, "public", false, "remove every link of the account that anyone can open")
		set.StringVar(&account, "account", "", "with --public, the account `id` (acc_..., default: $"+accountEnv+")")
		set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	})
	if !ok {
		return code
	}
	if public {
		return r.removePublic(account, yes)
	}
	var err error
	if account != "" {
		err = exitcode.Usagef("--account goes with --public")
	}
	if err == nil && len(r.pos) == 0 {
		err = exitcode.Usagef("link rm takes one or more link ids, or --public")
	}
	for i := 0; err == nil && i < len(r.pos); i++ {
		_, err = idArg("shl", "a link", r.pos[i])
	}
	if err != nil {
		return r.fail(err)
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	err = r.ask(yes, "remove the link or links, whose addresses stop working", func() (string, string, error) {
		stops := "whose address stops working"
		if len(r.pos) > 1 {
			stops = "whose addresses stop working"
		}
		return phraseFor(r.pos), fmt.Sprintf("remove %s, %s", strings.Join(r.pos, ", "), stops), nil
	})
	if err != nil {
		return r.fail(err)
	}
	outs := make([]outcome, 0, len(r.pos))
	for _, l := range r.pos {
		if _, err := files.Send(c, "DELETE", linkPath(l, ""), nil, nil); err != nil {
			outs = append(outs, failedOutcome(l, err))
			continue
		}
		outs = append(outs, doneOutcome(l))
	}
	return r.report(outs)
}

func (r *run) removePublic(account string, yes bool) int {
	if len(r.pos) != 0 {
		return r.fail(exitcode.Usagef("--public takes no link ids"))
	}
	accountID, err := accountArg(account)
	if err != nil {
		return r.fail(err)
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	err = r.ask(yes, "remove every link of the account that anyone can open", func() (string, string, error) {
		items, _, err := files.ListPages(c, accountLinksPath(accountID, ""), url.Values{"audience": {"anyone"}}, true)
		if err != nil {
			return "", "", err
		}
		return "yes", fmt.Sprintf("remove all %d live links that anyone can open in the account %s, and any expired ones; their addresses stop working",
			len(items), accountID), nil
	})
	if err != nil {
		return r.fail(err)
	}
	resp, err := files.Send(c, "POST", accountLinksPath(accountID, "/delete-public"), nil, nil)
	if err != nil {
		return r.fail(err)
	}
	if err := r.printDoc(resp.Body, "deleted"); err != nil {
		return r.fail(err)
	}
	return exitcode.OK
}
