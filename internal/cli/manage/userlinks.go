// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"io"
	"net/http"
	"strconv"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// linkNote says where a one-time link went. It holds no secret.
const linkNote = "selfcloud: note: the link is shown only now; give it to the person\n"

// linkVerificationView is an answer's delivery, without the token the API may send. The token is a secret and is never
// printed, plain or under --json.
type linkVerificationView struct {
	Method    string `json:"method"`
	URL       string `json:"url,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Sent      *bool  `json:"sent,omitempty"`
}

// userVerification is the answer to a verification: the user, with the delivery nested in it.
type userVerification struct {
	Verification *linkVerificationView `json:"verification"`
}

// methodArg checks the delivery method of a command before anything is sent.
func methodArg(command, method string, withNone bool) error {
	switch method {
	case "email", "link":
		return nil
	case "none":
		if withNone {
			return nil
		}
	}
	if withNone {
		return exitcode.Usagef("%s needs --method email, link or none", command)
	}
	return exitcode.Usagef("%s needs --method email or link", command)
}

// printVerification writes a delivery: the method, then whether mail went out or the link and its end. A link is shown
// once, on standard output, and the note on standard error holds no secret.
func printVerification(c *command, v linkVerificationView) error {
	if c.g.JSON {
		return output.JSON(c.stdout, v)
	}
	pairs := []output.KV{{Key: "verification", Value: v.Method}}
	switch {
	case v.Sent != nil:
		pairs = append(pairs, output.KV{Key: "sent", Value: strconv.FormatBool(*v.Sent)})
	case v.URL != "":
		pairs = append(pairs, output.KV{Key: "link", Value: v.URL})
		if v.ExpiresAt != "" {
			pairs = append(pairs, output.KV{Key: "link_expires_at", Value: v.ExpiresAt})
		}
	}
	if err := output.KeyValues(c.stdout, pairs); err != nil {
		return err
	}
	if v.URL != "" {
		_, _ = io.WriteString(c.stderr, linkNote)
	}
	return nil
}

// verify is `user verify` and `user reset-password`, whose delivery methods and output are the same.
func verify(name, route, command string, withNone bool, args []string, stdout, stderr io.Writer) int {
	var method string
	c := begin(name, true, stdout, stderr)
	c.set.StringVar(&method, "method", "", "how to deliver it: `email`, `link` or `none`")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("%s takes one user id", command))
	}
	user, err := idArg("usr", "user", pos[0])
	if err != nil {
		return c.fail(err)
	}
	if err := methodArg(command, method, withNone); err != nil {
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
	resp, err := files.Send(cn, http.MethodPost, "/api/v1/accounts/"+acc+"/users/"+user+route, nil,
		map[string]string{"method": method})
	if err != nil {
		return c.fail(err)
	}
	var v linkVerificationView
	if route == "/verification" {
		var answer userVerification
		if err := decodeAs(resp.Body, &answer, "a user and its verification"); err != nil {
			return c.fail(err)
		}
		if answer.Verification == nil {
			// "none" verifies the user outright and delivers nothing.
			v.Method = method
		} else {
			v = *answer.Verification
		}
	} else {
		if err := decodeAs(resp.Body, &v, "a verification"); err != nil {
			return c.fail(err)
		}
	}
	if err := printVerification(c, v); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// UserVerify is `selfcloud user verify [--account ACC] USER --method email|link|none`.
func UserVerify(args []string, stdout, stderr io.Writer) int {
	return verify("user verify", "/verification", "user verify", true, args, stdout, stderr)
}

// UserResetPassword is `selfcloud user reset-password [--account ACC] USER --method email|link`. It never takes or prints
// a password.
func UserResetPassword(args []string, stdout, stderr io.Writer) int {
	return verify("user reset-password", "/password-reset", "user reset-password", false, args, stdout, stderr)
}

// UserSignInLink is `selfcloud user sign-in-link [--account ACC] USER [--next PATH]`.
func UserSignInLink(args []string, stdout, stderr io.Writer) int {
	var next string
	c := begin("user sign-in-link", true, stdout, stderr)
	c.set.StringVar(&next, "next", "", "the path to open after sign-in, such as /files/x")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("user sign-in-link takes one user id"))
	}
	user, err := idArg("usr", "user", pos[0])
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
	body := map[string]string{}
	if next != "" {
		body["next"] = next
	}
	resp, err := files.Send(cn, http.MethodPost, "/api/v1/accounts/"+acc+"/users/"+user+"/sign-in-links", nil, body)
	if err != nil {
		return c.fail(err)
	}
	var v struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := decodeAs(resp.Body, &v, "a sign-in link"); err != nil {
		return c.fail(err)
	}
	if c.g.JSON {
		if err := output.JSON(c.stdout, v); err != nil {
			return c.fail(err)
		}
	} else {
		pairs := []output.KV{{Key: "link", Value: v.URL}, {Key: "expires_at", Value: v.ExpiresAt}}
		if err := output.KeyValues(c.stdout, pairs); err != nil {
			return c.fail(err)
		}
		_, _ = io.WriteString(c.stderr, linkNote)
	}
	return exitcode.OK
}

// UserRevokeTokens is `selfcloud user revoke-tokens [--account ACC] USER [--yes]`.
func UserRevokeTokens(args []string, stdout, stderr io.Writer) int {
	var yes bool
	c := begin("user revoke-tokens", true, stdout, stderr)
	c.set.BoolVar(&yes, "yes", false, "do not ask first")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("user revoke-tokens takes one user id"))
	}
	user, err := idArg("usr", "user", pos[0])
	if err != nil {
		return c.fail(err)
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	if err := files.Confirm(yes, user, "end every sign-in of the person on every device", stderr); err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPost, "/api/v1/accounts/"+acc+"/users/"+user+"/tokens/revoke", nil, nil)
	if err != nil {
		return c.fail(err)
	}
	var v struct {
		TokensNotBefore string `json:"tokens_not_before"`
	}
	if err := decodeAs(resp.Body, &v, "a token cut-off"); err != nil {
		return c.fail(err)
	}
	if c.g.JSON {
		if err := output.JSON(c.stdout, v); err != nil {
			return c.fail(err)
		}
	} else if err := output.KeyValues(c.stdout, []output.KV{{Key: "tokens_not_before", Value: v.TokensNotBefore}}); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
