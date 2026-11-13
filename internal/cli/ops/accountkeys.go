// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

// The account key commands. An installation admin makes, lists and revokes the keys of an account: every key route
// answers an account key or a device token with 403, so plain output says which key these commands need. The secret is
// shown once, by create, and nowhere else: ls decodes the answer into a view that has no field for it.

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

const keyNote = "selfcloud: note: the key is shown only now; keep it where the program that uses it can read it\n"

func keysRoute(acc string) string { return "/api/v1/accounts/" + acc + "/keys" }

// keyView is a key as ls prints it. It is the allow-list: a field the API adds is not shown until it is added here, and
// there is no field for the secret.
type keyView struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	CreatedAt  string  `json:"created_at"`
	ExpiresAt  *string `json:"expires_at"`
	RevokedAt  *string `json:"revoked_at"`
	LastUsedAt *string `json:"last_used_at"`
}

// keyShown is a key that create shows with its secret, once.
type keyShown struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Prefix    string  `json:"prefix"`
	CreatedAt string  `json:"created_at"`
	ExpiresAt *string `json:"expires_at"`
	Key       string  `json:"key"`
}

var keyHeaders = []string{"ID", "NAME", "PREFIX", "CREATED", "EXPIRES", "REVOKED", "LAST USED"}

func (v keyView) row() []string {
	return []string{v.ID, v.Name, v.Prefix, v.CreatedAt, str(v.ExpiresAt), str(v.RevokedAt), str(v.LastUsedAt)}
}

func (v keyShown) pairs() []output.KV {
	return []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "name", Value: v.Name},
		{Key: "prefix", Value: v.Prefix},
		{Key: "created_at", Value: v.CreatedAt},
		{Key: "expires_at", Value: str(v.ExpiresAt)},
		{Key: "key", Value: v.Key},
	}
}

// AccountKeyCreate is `selfcloud account key create --account ACC [--expires TIME] NAME`. It prints the secret, once.
func AccountKeyCreate(args []string, stdout, stderr io.Writer) int {
	c := begin("account key create", true, stdout, stderr)
	c.adminRoutes = true
	var expires string
	c.set.StringVar(&expires, "expires", "", "when the key stops working, as an RFC 3339 `time` in the future")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("account key create takes one name"))
	}
	name := pos[0]
	if strings.TrimSpace(name) == "" {
		return c.fail(exitcode.Usagef("account key create needs a name that is not blank"))
	}
	body := map[string]any{"name": name}
	if expires != "" {
		t, err := time.Parse(time.RFC3339, expires)
		if err != nil {
			return c.fail(exitcode.Usagef("--expires is a time as RFC 3339, such as 2030-01-01T00:00:00Z"))
		}
		if !t.After(time.Now()) {
			return c.fail(exitcode.Usagef("--expires must be in the future"))
		}
		body["expires_at"] = t.UTC().Format(time.RFC3339)
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPost, keysRoute(acc), nil, body)
	if err != nil {
		return c.fail(err)
	}
	var v keyShown
	err = decodeAs(resp.Body, &v, "a key")
	if err == nil && (v.ID == "" || v.Key == "") {
		err = errNotA("a key with its secret; the key is not shown again, so make another")
	}
	if err == nil {
		if c.g.JSON {
			err = output.JSON(c.stdout, v)
		} else {
			err = output.KeyValues(c.stdout, v.pairs())
		}
	}
	if err != nil {
		return c.fail(err)
	}
	if !c.g.JSON {
		_, _ = io.WriteString(c.stderr, keyNote)
	}
	return exitcode.OK
}

// AccountKeyLs is `selfcloud account key ls --account ACC`: the account's keys, newest first as the API orders them.
func AccountKeyLs(args []string, stdout, stderr io.Writer) int {
	c := begin("account key ls", true, stdout, stderr)
	c.adminRoutes = true
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("account key ls", pos); err != nil {
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
	items, _, err := files.ListPages(cn, keysRoute(acc), nil, false)
	var keys []keyView
	if err == nil {
		keys, err = decodeItems[keyView](items, "a key")
	}
	if err == nil {
		err = printList(c, keys, false, keyHeaders, keyView.row)
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// AccountKeyRevoke is `selfcloud account key revoke --account ACC KEY [--yes]`. Asks first, by the key's id.
func AccountKeyRevoke(args []string, stdout, stderr io.Writer) int {
	c := begin("account key revoke", true, stdout, stderr)
	c.adminRoutes = true
	var yes bool
	c.set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("account key revoke takes one key id"))
	}
	key, err := idArg("key", "key", pos[0])
	if err != nil {
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
	if err := confirmByID(yes, key, "revoke the key; every program that uses it stops working at once", stderr); err != nil {
		return c.fail(err)
	}
	if _, err := files.Send(cn, http.MethodDelete, keysRoute(acc)+"/"+key, nil, nil); err != nil {
		return c.fail(err)
	}
	if err := printDone(c, key); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
