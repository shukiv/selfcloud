// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"fmt"
	"io"
	"net/http"

	"github.com/shukiv/selfcloud/internal/cli/cmdkit"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// sessionRefArg checks the application's own name for the sessions to end, before anything is sent. Empty means all;
// otherwise it is 1 to 128 letters, digits, - or _, as the API holds it (decision 211).
func sessionRefArg(ref string) error {
	if ref == "" {
		return nil
	}
	bad := exitcode.Usagef("user sessions end: --ref must be 1 to 128 letters, digits, - or _")
	if len(ref) > 128 {
		return bad
	}
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return bad
		}
	}
	return nil
}

// UserSessionsEnd is `selfcloud user sessions end [--account ACC] USER [--ref REF]`. It ends the web sessions the
// person holds: those a sign-in link named REF started, or all of them when no ref is given. It touches no token and
// asks nothing, for it signs the person out of the web interface but deletes nothing (decision 211).
func UserSessionsEnd(args []string, stdout, stderr io.Writer) int {
	var ref string
	c := begin("user sessions end", true, stdout, stderr)
	c.Set.StringVar(&ref, "ref", "", "end only the sessions a sign-in link named this; empty ends all")
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("user sessions end takes one user id"))
	}
	user, err := cmdkit.IDArg("usr", "a user", pos[0])
	if err != nil {
		return c.fail(err)
	}
	if err := sessionRefArg(ref); err != nil {
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
	body := map[string]string{}
	if ref != "" {
		body["session_ref"] = ref
	}
	resp, err := files.Send(cn, http.MethodPost, "/api/v1/accounts/"+acc+"/users/"+user+"/sessions/end", nil, body)
	if err != nil {
		return c.fail(err)
	}
	var v struct {
		Ended int64 `json:"ended"`
	}
	if err := cmdkit.DecodeAs(resp.Body, &v, "the number of sessions ended"); err != nil {
		return c.fail(err)
	}
	if c.G.JSON {
		if err := output.JSON(c.Stdout, v); err != nil {
			return c.fail(err)
		}
	} else if _, err := fmt.Fprintf(c.Stdout, "ended %d session(s)\n", v.Ended); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
