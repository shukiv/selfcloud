// SPDX-License-Identifier: AGPL-3.0-or-later

// Package signin is the command-line client's device sign-in (design-cli 3.3, decision 125): `login`, `logout` and
// `whoami`. The client never takes a password. `login` asks the server for a sign-in, prints the code and the approval
// page, polls until a person approves it in the browser, and saves the device token in a profile. The token never goes to
// standard output, to standard error, into an error, or into a --json document.
package signin

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/cli/profile"
)

const (
	// defaultProfile names the profile `login` saves when no --profile is given.
	defaultProfile = "default"
	// slowDownStep is how much a slow_down answer adds to the poll interval (RFC 8628, and the server's own step).
	slowDownStep = 5 * time.Second
	// The paths of the three routes, as the server mounts them.
	startPath  = "/api/v1/auth/cli/start"
	pollPath   = "/api/v1/auth/cli/poll"
	revokePath = "/api/v1/auth/cli/revoke"
	foldersURL = "/api/v1/folders"
)

// sleep waits between polls. It stops with the context. Tests replace it.
var sleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// startAnswer is what POST /api/v1/auth/cli/start answers. The device code is secret; the user code is for the person.
type startAnswer struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

// tokenAnswer is the poll's answer once the sign-in is approved. Its token is never printed.
type tokenAnswer struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Login is `selfcloud login --url ADDRESS [--profile NAME]`.
func Login(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("login")
	var g conn.Global
	g.Register(set)
	pos, err := conn.Parse(set, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitcode.OK
		}
		return output.Fail(stderr, g.JSON, err)
	}
	if len(pos) != 0 {
		return output.Fail(stderr, g.JSON, exitcode.Usagef("login takes no arguments"))
	}
	if g.URL == "" {
		return output.Fail(stderr, g.JSON, exitcode.Usagef("login needs --url, the server's address"))
	}
	name := g.Profile
	if name == "" {
		name = defaultProfile
	}
	if err := profile.ValidName(name); err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	store, err := openStore()
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	f, err := store.Load()
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	if _, ok := f.Profiles[name]; ok {
		return output.Fail(stderr, g.JSON, exitcode.Usagef("profile %s exists; remove it with profile rm first", profile.Shown(name)))
	}
	// The name is where the token is saved; the connection itself names no profile and sends no credential.
	g.Profile = ""
	c, err := conn.Connect(g, stderr, false)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	ctx := context.Background()
	body, err := json.Marshal(map[string]string{"client_name": "selfcloud command-line client on " + runtime.GOOS})
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	resp, err := c.Client.Do(ctx, client.Request{Method: http.MethodPost, Path: startPath, Accept: "application/json",
		ContentType: "application/json", Body: body})
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	var st startAnswer
	if err := json.Unmarshal(resp.Body, &st); err != nil || st.DeviceCode == "" || st.UserCode == "" {
		return output.Fail(stderr, g.JSON, errors.New("the server's answer is not a sign-in start"))
	}
	fmt.Fprintf(stderr, "To sign in, open this page in a browser: %s\nThen enter the code: %s\n", st.VerificationURL, st.UserCode)
	fmt.Fprintf(stderr, "Waiting for the approval; the code ends in %d minutes.\n", st.ExpiresIn/60)
	tok, err := poll(ctx, c.Client, st)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	kind, err := profile.KindOf(tok.Token)
	if err != nil || kind != profile.KindDevice {
		return output.Fail(stderr, g.JSON, errors.New("the server's token is not a device token"))
	}
	f.Profiles[name] = profile.Profile{URL: c.URL, Kind: kind, Token: tok.Token}
	if f.Current == "" {
		f.Current = name
	}
	if err := store.Save(f); err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	if g.JSON {
		if err := output.JSON(stdout, map[string]any{"profile": name, "url": c.URL, "kind": kind,
			"expires_at": tok.ExpiresAt.UTC().Format(time.RFC3339)}); err != nil {
			return output.Fail(stderr, g.JSON, err)
		}
		return exitcode.OK
	}
	if _, err := fmt.Fprintf(stdout, "signed in: profile %s (kind %s) until %s\n", name, kind,
		tok.ExpiresAt.UTC().Format(time.RFC3339)); err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	return exitcode.OK
}

// poll asks until the sign-in is approved. authorization_pending means keep waiting; slow_down adds the step to the
// interval; expired_token, or the request's own end, means the sign-in is over, and the person must start again.
func poll(ctx context.Context, cl *client.Client, st startAnswer) (tokenAnswer, error) {
	interval := time.Duration(st.Interval) * time.Second
	if interval <= 0 {
		interval = slowDownStep
	}
	deadline := time.Now().Add(time.Duration(st.ExpiresIn) * time.Second)
	body, err := json.Marshal(map[string]string{"device_code": st.DeviceCode})
	if err != nil {
		return tokenAnswer{}, err
	}
	for {
		if err := sleep(ctx, interval); err != nil {
			return tokenAnswer{}, err
		}
		if time.Now().After(deadline) {
			return tokenAnswer{}, ended()
		}
		resp, err := cl.Do(ctx, client.Request{Method: http.MethodPost, Path: pollPath, Accept: "application/json",
			ContentType: "application/json", Body: body})
		if err == nil {
			var t tokenAnswer
			if json.Unmarshal(resp.Body, &t) != nil || t.Token == "" {
				return tokenAnswer{}, errors.New("the server's answer is not a token")
			}
			return t, nil
		}
		var ae *client.APIError
		if !errors.As(err, &ae) {
			return tokenAnswer{}, err
		}
		switch ae.Code {
		case "authorization_pending":
		case "slow_down":
			interval += slowDownStep
		case "expired_token":
			return tokenAnswer{}, ended()
		default:
			return tokenAnswer{}, err
		}
	}
}

// ended is the refusal of a sign-in that is over: it is an exit 3, and it names no code that could be a secret.
func ended() error {
	return &exitcode.Error{Exit: exitcode.Refused, Name: "expired_token",
		Message: "the sign-in has ended; run login again"}
}

// Logout is `selfcloud logout [--profile NAME]`. It ends the device token on the server and removes the profile. A token
// the server already refuses is removed too; a server that cannot be reached keeps the token, so logout can run again.
func Logout(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("logout")
	var g conn.Global
	g.Register(set)
	pos, err := conn.Parse(set, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitcode.OK
		}
		return output.Fail(stderr, g.JSON, err)
	}
	if len(pos) != 0 {
		return output.Fail(stderr, g.JSON, exitcode.Usagef("logout takes no arguments"))
	}
	c, err := conn.Connect(g, stderr, true)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	// Only a device token is signed out here. An account key or an admin key is not a sign-in, and is not revoked by logout.
	if c.Kind != profile.KindDevice {
		return output.Fail(stderr, g.JSON, exitcode.Usagef("logout signs out a terminal sign-in (a device token); "+
			"the credential in use is not one, so nothing was changed"))
	}
	_, err = c.Client.Do(context.Background(), client.Request{Method: http.MethodPost, Path: revokePath, Accept: "application/json"})
	revoked := err == nil
	if err != nil {
		var ae *client.APIError
		if !errors.As(err, &ae) || (ae.Status != 401 && ae.Status != 403) {
			return output.Fail(stderr, g.JSON, fmt.Errorf("the server could not be asked to end the token; the token is kept, "+
				"so logout can run again: %w", err))
		}
		// 401 and 403 are an answer that the token no longer works (ended by a password change, a revoke or a cut-off).
	}
	name := c.Profile
	if name != "" {
		if err := removeProfile(name); err != nil {
			return output.Fail(stderr, g.JSON, err)
		}
	}
	if g.JSON {
		if err := output.JSON(stdout, map[string]any{"profile": name, "revoked": revoked}); err != nil {
			return output.Fail(stderr, g.JSON, err)
		}
		return exitcode.OK
	}
	msg := "signed out"
	if !revoked {
		msg = "signed out; the server had already ended the token"
	}
	if name != "" {
		msg += fmt.Sprintf(": profile %s removed", profile.Shown(name))
	}
	if _, err := fmt.Fprintln(stdout, msg); err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	return exitcode.OK
}

// removeProfile deletes the profile name from the profiles file, with its token. A profile that is already gone is not an error.
func removeProfile(name string) error {
	store, err := openStore()
	if err != nil {
		return err
	}
	f, err := store.Load()
	if err != nil {
		return err
	}
	if _, ok := f.Profiles[name]; !ok {
		return nil
	}
	delete(f.Profiles, name)
	if f.Current == name {
		f.Current = ""
	}
	return store.Save(f)
}

// Whoami is `selfcloud whoami [--profile NAME]`. It reads GET /api/v1/folders, the folders the token reaches, and prints the
// profile, the address, the kind and the count. It never prints the token.
func Whoami(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("whoami")
	var g conn.Global
	g.Register(set)
	pos, err := conn.Parse(set, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitcode.OK
		}
		return output.Fail(stderr, g.JSON, err)
	}
	if len(pos) != 0 {
		return output.Fail(stderr, g.JSON, exitcode.Usagef("whoami takes no arguments"))
	}
	c, err := conn.Connect(g, stderr, true)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	resp, err := c.Client.Do(context.Background(), client.Request{Method: "GET", Path: foldersURL, Accept: "application/json"})
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(resp.Body, &list); err != nil {
		return output.Fail(stderr, g.JSON, errors.New("the server's answer is not a folder list"))
	}
	name := c.Profile
	if g.JSON {
		if err := output.JSON(stdout, map[string]any{"profile": name, "url": c.URL, "kind": c.Kind,
			"folders": len(list.Items)}); err != nil {
			return output.Fail(stderr, g.JSON, err)
		}
		return exitcode.OK
	}
	if err := output.KeyValues(stdout, []output.KV{
		{Key: "profile", Value: name},
		{Key: "url", Value: c.URL},
		{Key: "kind", Value: c.Kind},
		{Key: "folders", Value: fmt.Sprint(len(list.Items))},
	}); err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	return exitcode.OK
}

// openStore opens the profiles file in its directory.
func openStore() (*profile.Store, error) {
	dir, err := profile.DefaultDir()
	if err != nil {
		return nil, err
	}
	return profile.Open(dir), nil
}
