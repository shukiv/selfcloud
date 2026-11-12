// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// fetch reads the settings document once.
func fetch(c *conn.Conn) (*client.Response, error) {
	return c.Client.Do(context.Background(), client.Request{Method: "GET", Path: settingsPath, Accept: "application/json"})
}

// Get is `selfcloud settings get [KEY] [flags]`.
func Get(args []string, stdout, stderr io.Writer) int {
	fs := conn.NewFlagSet("settings get")
	var g conn.Global
	g.Register(fs)
	pos, err := conn.Parse(fs, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitcode.OK
		}
		return output.Fail(stderr, g.JSON, err)
	}
	if len(pos) > 1 {
		return output.Fail(stderr, g.JSON, usage("settings get takes at most one KEY"))
	}
	c, err := conn.Connect(g, stderr, true)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	resp, err := fetch(c)
	if err != nil {
		return fail(stderr, g.JSON, c.Kind, err)
	}
	a, err := parseAnswer(resp.Body)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	if len(pos) == 1 {
		return printOne(stdout, stderr, g.JSON, a, pos[0])
	}
	if g.JSON {
		return printJSON(stdout, stderr, resp.Body)
	}
	if err := printSettings(stdout, a.Settings); err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	fmt.Fprintln(stdout)
	if err := printPictures(stdout, a.Pictures); err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	return exitcode.OK
}

// printJSON writes the server's document, indented.
func printJSON(stdout, stderr io.Writer, body []byte) int {
	if err := output.Indented(stdout, body); err != nil {
		return output.Fail(stderr, true, errors.New("the server's answer is not JSON"))
	}
	return exitcode.OK
}

// printOne prints the setting or the picture named key.
func printOne(stdout, stderr io.Writer, asJSON bool, a *answer, key string) int {
	if s, i, ok := a.setting(key); ok {
		if asJSON {
			return printJSON(stdout, stderr, a.RawSettings[i])
		}
		err := output.KeyValues(stdout, []output.KV{
			{Key: "key", Value: s.Key},
			{Key: "type", Value: s.Type},
			{Key: "value", Value: valueText(s.Value)},
			{Key: "default", Value: valueText(s.Default)},
			{Key: "source", Value: s.Source},
			{Key: "locked", Value: fmt.Sprint(s.Locked)},
			{Key: "restart", Value: fmt.Sprint(s.Restart)},
		})
		if err != nil {
			return output.Fail(stderr, asJSON, err)
		}
		return exitcode.OK
	}
	if p, i, ok := a.picture(key); ok {
		if asJSON {
			return printJSON(stdout, stderr, a.RawPictures[i])
		}
		if err := pictureLines(stdout, p); err != nil {
			return output.Fail(stderr, asJSON, err)
		}
		return exitcode.OK
	}
	return output.Fail(stderr, asJSON, exitcode.NotFoundf("no setting named %q", key))
}
