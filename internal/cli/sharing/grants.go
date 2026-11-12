// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/names"
)

// maxMessageRunes is the longest note a grant may carry; the API refuses more.
const maxMessageRunes = 500

// permissionRoles are the roles the API gives a user or a group on a node, lowest first.
var permissionRoles = []string{"viewer", "commenter", "contributor", "editor", "manager"}

func validRole(role string, roles []string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// scope reads where a sharing command acts. With folder it is a team folder of an account (account is then needed, from
// the flag or the environment, and every positional argument is for the command); otherwise the first positional argument
// is a node. rest is what follows the node.
func scope(pos []string, folder, account string) (nodeID, folderID, accountID string, rest []string, err error) {
	if folder != "" {
		if folderID, err = idArg("fld", "a folder", folder); err != nil {
			return
		}
		if accountID, err = accountArg(account); err != nil {
			return
		}
		return "", folderID, accountID, pos, nil
	}
	if account != "" {
		err = exitcode.Usagef("--account goes with --folder; a node names its own account")
		return
	}
	if len(pos) == 0 {
		err = exitcode.Usagef("name a node (nod_...), or a team folder with --folder")
		return
	}
	if nodeID, err = idArg("nod", "a node", pos[0]); err != nil {
		return
	}
	return nodeID, "", "", pos[1:], nil
}

func nodePath(nodeID, suffix string) string { return "/api/v1/nodes/" + nodeID + suffix }

func grantsPath(accountID, folderID, suffix string) string {
	return "/api/v1/accounts/" + accountID + "/folders/" + folderID + "/grants" + suffix
}

// printDoc writes a document the server answered: indented under --json, otherwise key=value lines for the keys it holds.
func (r *run) printDoc(body []byte, keys ...string) error {
	if r.g.JSON {
		return output.Indented(r.out, body)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("the server's answer is not a document")
	}
	var kv []output.KV
	for _, k := range keys {
		if v := flagText(doc, k); v != "" {
			kv = append(kv, output.KV{Key: k, Value: v})
		}
	}
	return output.KeyValues(r.out, kv)
}

// printList writes a list: the items as the server wrote them under --json, otherwise a table of the columns.
func (r *run) printList(items []json.RawMessage, headers []string, keys []string) int {
	if r.g.JSON {
		if err := output.JSON(r.out, items); err != nil {
			return r.fail(err)
		}
		return exitcode.OK
	}
	rows := make([][]string, 0, len(items))
	for _, raw := range items {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			return r.fail(fmt.Errorf("the server's answer is not a list of documents"))
		}
		row := make([]string, len(keys))
		for i, k := range keys {
			row[i] = flagText(doc, k)
		}
		rows = append(rows, row)
	}
	if err := output.Table(r.out, headers, rows); err != nil {
		return r.fail(err)
	}
	return exitcode.OK
}

// ShareLs is `selfcloud share ls`: who holds a role on a node, or who holds a grant on a team folder.
func ShareLs(args []string, stdout, stderr io.Writer) int {
	var folder, account string
	r, code, ok := begin("share ls", args, stdout, stderr, func(set *flag.FlagSet) {
		set.StringVar(&folder, "folder", "", "list the grants of this team folder, `id` (fld_...), with --account")
		set.StringVar(&account, "account", "", "the account `id` (acc_...) of a team folder (default: $"+accountEnv+")")
	})
	if !ok {
		return code
	}
	nodeID, folderID, accountID, rest, err := scope(r.pos, folder, account)
	if err == nil && len(rest) != 0 {
		err = exitcode.Usagef("share ls takes one node, or --folder")
	}
	if err != nil {
		return r.fail(err)
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	if folderID != "" {
		items, _, err := files.ListPages(c, grantsPath(accountID, folderID, ""), nil, false)
		if err != nil {
			return r.fail(err)
		}
		return r.printList(items, []string{"USER", "LEVEL", "CREATED"}, []string{"user_id", "level", "created_at"})
	}
	items, _, err := files.ListPages(c, nodePath(nodeID, "/permissions"), nil, false)
	if err != nil {
		return r.fail(err)
	}
	return r.printList(items, []string{"SUBJECT", "KIND", "ROLE", "FROM", "EXPIRES", "NAME"},
		[]string{"subject_id", "subject_kind", "role", "inherited_from", "expires_at", "subject_name"})
}

// ShareGrant is `selfcloud share grant`: gives a user or a group a role on a node, or a user a level on a team folder.
func ShareGrant(args []string, stdout, stderr io.Writer) int {
	var folder, account, role, level, expires, expiresIn, message string
	var noNotify bool
	r, code, ok := begin("share grant", args, stdout, stderr, func(set *flag.FlagSet) {
		set.StringVar(&folder, "folder", "", "grant on this team folder, `id` (fld_...), with --account and --level")
		set.StringVar(&account, "account", "", "the account `id` (acc_...) of a team folder (default: $"+accountEnv+")")
		set.StringVar(&role, "role", "", "the role on a node: viewer, commenter, contributor, editor or manager")
		set.StringVar(&level, "level", "", "the level on a team folder: read or write")
		set.StringVar(&expires, "expires", "", "when the role ends, a `time` such as 2026-12-31T00:00:00Z")
		set.StringVar(&expiresIn, "expires-in", "", "when the role ends, as a `duration` from now such as 36h or 30d")
		set.StringVar(&message, "message", "", "a note for the mail the person gets")
		set.BoolVar(&noNotify, "no-notify", false, "do not mail the person")
	})
	if !ok {
		return code
	}
	nodeID, folderID, accountID, rest, err := scope(r.pos, folder, account)
	if err == nil && len(rest) != 1 {
		err = exitcode.Usagef("share grant takes a node (or --folder) and one person or group")
	}
	if err != nil {
		return r.fail(err)
	}
	if folderID != "" {
		return r.grantFolder(folderID, accountID, rest[0], role, level, expires, expiresIn, message, noNotify)
	}
	if level != "" {
		return r.fail(exitcode.Usagef("--level is for a team folder; a node takes --role"))
	}
	subject, err := subjectArg(rest[0])
	if err == nil && !validRole(role, permissionRoles) {
		err = exitcode.Usagef("--role is one of viewer, commenter, contributor, editor, manager")
	}
	if err == nil && utf8.RuneCountInString(message) > maxMessageRunes {
		err = exitcode.Usagef("the message may be at most %d characters", maxMessageRunes)
	}
	var end string
	if err == nil {
		end, err = expiryArg(expires, expiresIn, time.Now())
	}
	if err != nil {
		return r.fail(err)
	}
	body := map[string]any{"role": role}
	if end != "" {
		body["expires_at"] = end
	}
	if message != "" {
		body["message"] = message
	}
	if noNotify {
		body["notify"] = false
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	resp, err := files.Send(c, "PUT", nodePath(nodeID, "/permissions/"+subject), nil, body)
	if err != nil {
		return r.fail(err)
	}
	if err := r.printDoc(resp.Body, "subject_kind", "subject_id", "role", "expires_at", "notified", "reason"); err != nil {
		return r.fail(err)
	}
	return exitcode.OK
}

func (r *run) grantFolder(folderID, accountID, user, role, level, expires, expiresIn, message string, noNotify bool) int {
	if role != "" || expires != "" || expiresIn != "" || message != "" || noNotify {
		return r.fail(exitcode.Usagef("a team folder's grant takes --level only (read or write)"))
	}
	if level != "read" && level != "write" {
		return r.fail(exitcode.Usagef("--level is read or write"))
	}
	user, err := idArg("usr", "a person (a team folder is shared with users, not groups)", user)
	if err != nil {
		return r.fail(err)
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	resp, err := files.Send(c, "PUT", grantsPath(accountID, folderID, "/"+user), nil, map[string]any{"level": level})
	if err != nil {
		return r.fail(err)
	}
	if err := r.printDoc(resp.Body, "folder_id", "user_id", "level", "created_at"); err != nil {
		return r.fail(err)
	}
	return exitcode.OK
}

// nodeLabel reads a node's name for a question, as `name (nod_...)`.
func nodeLabel(c *conn.Conn, nodeID string) (name string, err error) {
	resp, err := files.Send(c, "GET", nodePath(nodeID, ""), url.Values{}, nil)
	if err != nil {
		return "", err
	}
	var n struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(resp.Body, &n) != nil || n.Name == "" {
		return "", fmt.Errorf("the server's answer is not a node")
	}
	// A name with a control or a format character (a bidi override, a zero-width space) would reach the terminal in the
	// question, and could reorder what the person must type.
	if _, _, err := names.Clean(n.Name); err != nil {
		return "", exitcode.Usagef("the node's name cannot be shown safely to confirm with; nothing was changed; " +
			"give --yes to go on")
	}
	return n.Name, nil
}

// ShareRevoke is `selfcloud share revoke`: takes a role or a grant away. It asks first.
func ShareRevoke(args []string, stdout, stderr io.Writer) int {
	var folder, account string
	var yes bool
	r, code, ok := begin("share revoke", args, stdout, stderr, func(set *flag.FlagSet) {
		set.StringVar(&folder, "folder", "", "revoke grants of this team folder, `id` (fld_...), with --account")
		set.StringVar(&account, "account", "", "the account `id` (acc_...) of a team folder (default: $"+accountEnv+")")
		set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	})
	if !ok {
		return code
	}
	nodeID, folderID, accountID, subjects, err := scope(r.pos, folder, account)
	if err == nil && len(subjects) == 0 {
		err = exitcode.Usagef("share revoke takes a node (or --folder) and one or more people or groups")
	}
	for i := 0; err == nil && i < len(subjects); i++ {
		if folderID != "" {
			_, err = idArg("usr", "a person (a team folder is shared with users, not groups)", subjects[i])
		} else {
			_, err = subjectArg(subjects[i])
		}
	}
	if err != nil {
		return r.fail(err)
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	fallback := "remove the access of the named people or groups"
	err = r.ask(yes, fallback, func() (string, string, error) {
		who := fmt.Sprint(subjects)
		if folderID != "" {
			return phraseFor(subjects), fmt.Sprintf("remove the grant of %s on the team folder %s", who, folderID), nil
		}
		name, err := nodeLabel(c, nodeID)
		if err != nil {
			return "", "", err
		}
		return phraseFor(subjects), fmt.Sprintf("remove the access of %s on %q (%s)", who, name, nodeID), nil
	})
	if err != nil {
		return r.fail(err)
	}
	outs := make([]outcome, 0, len(subjects))
	for _, s := range subjects {
		path := nodePath(nodeID, "/permissions/"+s)
		if folderID != "" {
			path = grantsPath(accountID, folderID, "/"+s)
		}
		if _, err := files.Send(c, "DELETE", path, nil, nil); err != nil {
			outs = append(outs, failedOutcome(s, err))
			continue
		}
		outs = append(outs, doneOutcome(s))
	}
	return r.report(outs)
}

// TransferOwnership is `selfcloud share transfer-ownership`: gives a file or a folder to another user. It asks first.
func TransferOwnership(args []string, stdout, stderr io.Writer) int {
	var to string
	var yes bool
	r, code, ok := begin("share transfer-ownership", args, stdout, stderr, func(set *flag.FlagSet) {
		set.StringVar(&to, "to", "", "the new owner, a user `id` (usr_...)")
		set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	})
	if !ok {
		return code
	}
	if len(r.pos) != 1 {
		return r.fail(exitcode.Usagef("share transfer-ownership takes one node id and --to"))
	}
	nodeID, err := idArg("nod", "a node", r.pos[0])
	if err == nil {
		_, err = idArg("usr", "the new owner", to)
	}
	if err != nil {
		return r.fail(err)
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	err = r.ask(yes, "give the item to another owner", func() (string, string, error) {
		name, err := nodeLabel(c, nodeID)
		if err != nil {
			return "", "", err
		}
		return name, fmt.Sprintf("give %q (%s) to %s, who becomes its owner", name, nodeID, to), nil
	})
	if err != nil {
		return r.fail(err)
	}
	resp, err := files.Send(c, "POST", nodePath(nodeID, "/transfer-ownership"), nil, map[string]any{"to": to})
	if err != nil {
		return r.fail(err)
	}
	if err := r.printDoc(resp.Body, "id", "name", "kind", "owner_id"); err != nil {
		return r.fail(err)
	}
	return exitcode.OK
}
