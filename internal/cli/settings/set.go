// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// parseFlags reads the flags and the positional arguments of a settings command, or reports the exit code to return.
func parseFlags(name string, args []string, stdout, stderr io.Writer) (conn.Global, []string, int, bool) {
	fs := conn.NewFlagSet(name)
	var g conn.Global
	g.Register(fs)
	pos, err := conn.Parse(fs, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return g, nil, exitcode.OK, false
		}
		return g, nil, output.Fail(stderr, g.JSON, err), false
	}
	return g, pos, 0, true
}

// pair is one KEY=VALUE argument.
type pair struct{ key, text string }

// parsePairs splits each argument at its first "=", so a value may hold one. It refuses an argument with no "=", an empty
// key, a key given twice, and no argument at all. Nothing is sent before every argument is judged.
func parsePairs(pos []string) ([]pair, error) {
	if len(pos) == 0 {
		return nil, usage("settings set needs at least one KEY=VALUE")
	}
	seen := map[string]bool{}
	out := make([]pair, 0, len(pos))
	for _, arg := range pos {
		key, text, ok := strings.Cut(arg, "=")
		switch {
		case !ok:
			return nil, usage("%q is not KEY=VALUE", arg)
		case key == "":
			return nil, usage("%q has no key before the =", arg)
		case seen[key]:
			return nil, usage("the key %q is given twice", key)
		}
		seen[key] = true
		out = append(out, pair{key, text})
	}
	return out, nil
}

// jsonValue turns the text typed for a setting into the JSON value its type takes. A duration, a size and a string go as
// text, and the server judges them.
func jsonValue(s setting, text string) (any, error) {
	switch s.Type {
	case "bool":
		switch text {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, usage("%s is a bool: the value must be true or false", s.Key)
	case "int":
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, usage("%s is an integer: the value must be a whole number", s.Key)
		}
		return n, nil
	}
	return text, nil
}

// Set is `selfcloud settings set KEY=VALUE... [flags]`. It reads the settings first, to learn each key's type.
func Set(args []string, stdout, stderr io.Writer) int {
	g, pos, code, ok := parseFlags("settings set", args, stdout, stderr)
	if !ok {
		return code
	}
	pairs, err := parsePairs(pos)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
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
	values := make(map[string]any, len(pairs))
	keys := make([]string, 0, len(pairs))
	for _, p := range pairs {
		s, _, known := a.setting(p.key)
		if !known {
			if _, _, isPicture := a.picture(p.key); isPicture {
				return output.Fail(stderr, g.JSON, usage("%s is a picture: use settings %s set", p.key, pictureWord(p.key)))
			}
			return output.Fail(stderr, g.JSON, usage("no setting named %q", p.key))
		}
		v, err := jsonValue(s, p.text)
		if err != nil {
			return output.Fail(stderr, g.JSON, err)
		}
		values[p.key] = v
		keys = append(keys, p.key)
	}
	return patch(c, g, stdout, stderr, map[string]any{"set": values}, keys)
}

// pictureWord names the command word of a picture key: ui.brand.logo is logo, ui.brand.icon is icon.
func pictureWord(key string) string { return key[strings.LastIndex(key, ".")+1:] }

// Reset is `selfcloud settings reset KEY... [flags]`. The server judges the keys, so nothing is read first.
func Reset(args []string, stdout, stderr io.Writer) int {
	g, pos, code, ok := parseFlags("settings reset", args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) == 0 {
		return output.Fail(stderr, g.JSON, usage("settings reset needs at least one KEY"))
	}
	seen := map[string]bool{}
	for _, k := range pos {
		if k == "" || seen[k] {
			return output.Fail(stderr, g.JSON, usage("a key must be named once, and not be empty"))
		}
		seen[k] = true
	}
	c, err := conn.Connect(g, stderr, true)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	return patch(c, g, stdout, stderr, map[string]any{"reset": pos}, pos)
}

// patch sends one change and prints the rows of the keys it named, or the server's document under --json.
func patch(c *conn.Conn, g conn.Global, stdout, stderr io.Writer, change map[string]any, keys []string) int {
	body, err := json.Marshal(change)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	resp, err := c.Client.Do(context.Background(), client.Request{
		Method: "PATCH", Path: settingsPath, Accept: "application/json", ContentType: "application/json", Body: body,
	})
	if err != nil {
		return fail(stderr, g.JSON, c.Kind, err)
	}
	if g.JSON {
		return printJSON(stdout, stderr, resp.Body)
	}
	a, err := parseAnswer(resp.Body)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	var rows []setting
	for _, s := range a.Settings {
		for _, k := range keys {
			if s.Key == k {
				rows = append(rows, s)
				break
			}
		}
	}
	if err := printSettings(stdout, rows); err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	return exitcode.OK
}
