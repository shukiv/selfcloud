// SPDX-License-Identifier: AGPL-3.0-or-later

package files

// get of a share link (decision 146): the link names its own server and carries its secret in the fragment, so it
// needs no profile and no sign-in. The link is opened with no Authorization header; the visitor credential the open
// gives is used for the file's reads alone, renewed before it ends, and opened again when it is refused. The profile's
// and the environment's credentials are never sent to the link's server. The secret, the password and the visitor
// credentials are kept in memory only: they are never printed, logged or written to a record.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/id"
)

const (
	shareOpenRoute  = "/api/v1/share/open"
	shareRenewRoute = "/api/v1/share/renew"
	// sharePage is the path of a share link's page, after the server's address.
	sharePage = "/s"
	// maxPassword is the longest password line read from standard input, in bytes.
	maxPassword = 4096
	// maxLink is the longest share link read from standard input or a file, in bytes.
	maxLink = 4096
	// maxVisitorToken bounds the credential a server may hand back, so it fits in a header.
	maxVisitorToken = 4096
)

// shareRenewBefore is how much of a visitor credential's life may be left before it is renewed. Tests change it.
var shareRenewBefore = 5 * time.Minute

// parseShareURL reads a share link: an https address (or http to loopback, or with --insecure-http), whose path ends
// in /s, with the secret in the fragment and no user name or query. It returns the server's address and the secret.
// A refusal never repeats the link, which holds the secret.
func parseShareURL(raw string, insecureHTTP bool) (base, secret string, err error) {
	addr, frag, found := strings.Cut(raw, "#")
	if !found || frag == "" {
		return "", "", exitcode.Usagef("the share link has no secret after its #; copy the whole link")
	}
	if !auth.ValidToken(frag) {
		return "", "", exitcode.Usagef("the share link's secret is not in the shape the server gives; copy the whole link")
	}
	u, err := url.Parse(addr)
	if err != nil {
		return "", "", exitcode.Usagef("the share link is not a valid address")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", "", exitcode.Usagef("a share link has no query (?...)")
	}
	p, ok := strings.CutSuffix(u.Path, sharePage)
	if !ok || u.RawPath != "" {
		return "", "", exitcode.Usagef("the address is not a share link: its path does not end in %s", sharePage)
	}
	u.Path = p
	b, err := client.ParseBaseURL(u.String(), insecureHTTP)
	if err != nil {
		return "", "", exitcode.Usagef("%s", err)
	}
	return b.String(), frag, nil
}

// getShared is get of a share link.
func getShared(g conn.Global, link, dest, folder string, passwordStdin bool, o dlOptions, stdout, stderr io.Writer) int {
	switch {
	case folder != "":
		return fail(stderr, g, exitcode.Usagef("--folder does not apply to a share link, which names its file"))
	case g.URL != "" || g.Profile != "" || g.TokenFile != "":
		return fail(stderr, g, exitcode.Usagef("a share link names its own server and needs no sign-in: --url, "+
			"--profile and --token-file do not apply to it"))
	case g.Timeout <= 0 || g.ConnectTimeout <= 0 || g.HeaderTimeout <= 0:
		return fail(stderr, g, exitcode.Usagef("the timeouts must be longer than zero"))
	}
	base, secret, err := parseShareURL(link, g.InsecureHTTP)
	if err != nil {
		return fail(stderr, g, err)
	}
	password := ""
	if passwordStdin {
		if password, err = readPasswordLine(); err != nil {
			return fail(stderr, g, err)
		}
	}
	dst, err := openDestination(dest, o.overwrite)
	if err != nil {
		return fail(stderr, g, err)
	}
	defer dst.close()
	if g.InsecureSkipVerify {
		fmt.Fprintln(stderr, "selfcloud: warning: TLS certificate checks are off (--insecure-skip-verify); "+
			"the server's identity is not checked, and this is not saved")
	}
	if u, err := url.Parse(base); err == nil && u.Scheme == "http" && !client.IsLoopback(u.Hostname()) {
		fmt.Fprintln(stderr, "selfcloud: warning: plain http is not encrypted; the link's secret and the data cross "+
			"the network in clear (--insecure-http)")
	}
	anon, err := client.New(client.Options{BaseURL: base, CAFile: g.CAFile, InsecureSkipVerify: g.InsecureSkipVerify,
		InsecureHTTP: g.InsecureHTTP, ConnectTimeout: g.ConnectTimeout, HeaderTimeout: g.HeaderTimeout,
		Timeout: g.Timeout})
	if err != nil {
		return fail(stderr, g, exitcode.Usagef("%s", err))
	}
	ctx := context.Background()
	v := &visitor{anon: anon, secret: secret, password: password, linkFromStdin: o.linkFromStdin}
	if err := v.open(ctx); err != nil {
		return fail(stderr, g, err)
	}
	if v.uploadOnly {
		return fail(stderr, g, exitcode.Usagef("the link takes uploads only; there is nothing to download from it"))
	}
	got, err := download(ctx, v, v.node, dst, o, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	return printGot(stdout, stderr, g, got)
}

// readPasswordLine reads the password from standard input: one line, without its line ending. A terminal is refused,
// since what is typed there is shown.
func readPasswordLine() (string, error) {
	if IsTerminal(Stdin) {
		return "", exitcode.Usagef("--password-stdin reads the password from a pipe or a file, not a terminal, where " +
			"it would be shown as it is typed")
	}
	line, err := bufio.NewReaderSize(io.LimitReader(Stdin, maxPassword+2), maxPassword+2).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", exitcode.Usagef("the password could not be read from standard input")
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	switch {
	case len(line) > maxPassword:
		return "", exitcode.Usagef("the password on standard input is longer than %d bytes", maxPassword)
	case line == "":
		return "", exitcode.Usagef("standard input holds no password")
	}
	return line, nil
}

// readLink reads a share link from standard input, when source is "-", or from a regular file, so the link's secret is not
// in argv, where `ps` and /proc show it to other users and a shell keeps it in its history. It is the first line, with
// the spaces and the line ending around it trimmed; the limit counts the spaces too. A terminal is refused for "-", since what is typed there is shown.
// An error never repeats the link or the file's name.
func readLink(source string) (string, error) {
	var r io.Reader
	if source == "-" {
		if IsTerminal(Stdin) {
			return "", exitcode.Usagef("get - reads the link from a pipe or a file, not a terminal, where it would be shown " +
				"as it is typed")
		}
		r = Stdin
	} else {
		f, err := os.Open(source)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", exitcode.Usagef("the link file does not exist")
			}
			return "", exitcode.Usagef("the link file could not be opened")
		}
		defer f.Close()
		if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
			return "", exitcode.Usagef("the link file is not a regular file")
		}
		r = f
	}
	line, err := bufio.NewReaderSize(io.LimitReader(r, maxLink+2), maxLink+2).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", exitcode.Usagef("the link could not be read")
	}
	// A read that ran to its cap without reaching a line end stopped inside the line, so the limit, which counts the
	// spaces around the link, was passed; trimming what was read would keep a part of the line as if it were all of it.
	capped := len(line) >= maxLink+2 && !strings.HasSuffix(line, "\n")
	line = strings.TrimSpace(line)
	switch {
	case capped, len(line) > maxLink:
		return "", exitcode.Usagef("the link is longer than %d bytes", maxLink)
	case line == "":
		return "", exitcode.Usagef("the input holds no link on its first line")
	}
	return line, nil
}

// visitor is a share link opened for a download: the credential the open gave, when it ends, and the link's node.
type visitor struct {
	anon       *client.Client
	secret     string
	password   string
	cl         *client.Client
	expires    time.Time
	node       string
	uploadOnly bool
	// linkFromStdin is set when the link came from standard input, which cannot also carry the password.
	linkFromStdin bool
}

// visitorAnswer is the answer to an open or a renewal.
type visitorAnswer struct {
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	Node       string    `json:"node"`
	UploadOnly bool      `json:"upload_only"`
}

// open opens the link with its secret, and its password when it has one. It sends no Authorization header.
func (v *visitor) open(ctx context.Context) error {
	doc := map[string]string{"secret": v.secret}
	if v.password != "" {
		doc["password"] = v.password
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	resp, err := v.anon.Do(ctx, client.Request{Method: http.MethodPost, Path: shareOpenRoute, Accept: "application/json",
		ContentType: "application/json", Body: b})
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) && ae.Reason == "password_required" {
			if v.linkFromStdin {
				return withHint(err, "give the link with --link-file and the password on standard input with --password-stdin")
			}
			return withHint(err, "give it on standard input with --password-stdin")
		}
		return err
	}
	var a visitorAnswer
	if json.Unmarshal(resp.Body, &a) != nil {
		return errors.New("the server's answer to the open is not valid")
	}
	if a.UploadOnly {
		v.uploadOnly = true
		return nil
	}
	if !id.Valid("nod", a.Node) {
		return errors.New("the server's answer to the open names no file")
	}
	if v.node != "" && a.Node != v.node {
		return errors.New("the link names another file than when it was first opened")
	}
	if err := v.take(a); err != nil {
		return err
	}
	v.node = a.Node
	return nil
}

// take keeps a credential from an open or a renewal, once it is checked: a visitor credential's shape, short enough for
// a header.
func (v *visitor) take(a visitorAnswer) error {
	if !strings.HasPrefix(a.Token, "scl_") || len(a.Token) > maxVisitorToken || strings.IndexFunc(a.Token, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
	}) >= 0 {
		return errors.New("the server's answer holds no valid visitor credential")
	}
	v.cl, v.expires = v.anon.WithCredential(a.Token), a.ExpiresAt
	return nil
}

// client is the visitor's client, renewed first when little of the credential's life is left. A renewal that is
// refused opens the link again.
func (v *visitor) client(ctx context.Context) (*client.Client, error) {
	if time.Until(v.expires) > shareRenewBefore {
		return v.cl, nil
	}
	resp, err := v.cl.Do(ctx, client.Request{Method: http.MethodPost, Path: shareRenewRoute, Accept: "application/json"})
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
			if err := v.open(ctx); err != nil {
				return nil, err
			}
			return v.cl, nil
		}
		return nil, err
	}
	var a visitorAnswer
	if json.Unmarshal(resp.Body, &a) != nil {
		return nil, errors.New("the server's answer to the renewal is not valid")
	}
	if err := v.take(a); err != nil {
		return nil, err
	}
	return v.cl, nil
}

// reopen opens the link again after the credential was refused.
func (v *visitor) reopen(ctx context.Context) bool { return v.open(ctx) == nil }

// server is the link's server.
func (v *visitor) server() string { return v.anon.BaseURL() }
