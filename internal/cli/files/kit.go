// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"encoding/json"
	"flag"
	"io"
	"net/url"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
)

// The commands of other packages (folders, groups, sharing) use these, so their arguments, confirmations, requests and
// lists behave as the file commands' do.

// Parse reads a command's arguments, as parse does.
func Parse(set *flag.FlagSet, g *conn.Global, args []string, stdout, stderr io.Writer) (pos []string, code int, ok bool) {
	return parse(set, g, args, stdout, stderr)
}

// Confirm asks before a destructive change, as confirm does: nothing with yes, the phrase typed at a terminal otherwise,
// and a refusal (exit 2) when standard input is not a terminal.
func Confirm(yes bool, phrase, what string, stderr io.Writer) error {
	return confirm(yes, phrase, what, stderr)
}

// Send makes one request and reads JSON back. A doc that is not nil is sent as the JSON body.
func Send(c *conn.Conn, method, path string, query url.Values, doc any) (*client.Response, error) {
	r := request{method: method, path: path}
	if doc != nil {
		var err error
		if r, err = jsonRequest(method, path, doc); err != nil {
			return nil, err
		}
	}
	r.query = query
	return send(c, r)
}

// ListPages reads the list at path, as listPages does: the first page, or with all every page by the API's cursor.
func ListPages(c *conn.Conn, path string, query url.Values, all bool) (items []json.RawMessage, next string, err error) {
	return listPages(c, request{method: "GET", path: path, query: query}, all)
}
