// SPDX-License-Identifier: AGPL-3.0-or-later

// Package conn holds what every remote command shares: the global flags, the order in which the server address and the
// credential are chosen (design-cli.md 3.11), the warnings that are printed on every use, and the client built from them.
package conn

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/profile"
)

// Environment variables the resolution reads. They are the client's names; the server's SELFCLOUD_CONFIG and SELFCLOUD_*
// settings are not used here.
const (
	envProfile = "SELFCLOUD_PROFILE"
	envURL     = "SELFCLOUD_URL"
	envToken   = "SELFCLOUD_TOKEN"
)

// Global is the set of flags every remote command takes.
type Global struct {
	Profile            string
	URL                string
	TokenFile          string
	CAFile             string
	InsecureHTTP       bool
	InsecureSkipVerify bool
	JSON               bool
	Timeout            time.Duration
	ConnectTimeout     time.Duration
	HeaderTimeout      time.Duration
}

// Register adds the global flags to fs, with their defaults.
func (g *Global) Register(fs *flag.FlagSet) {
	fs.StringVar(&g.Profile, "profile", "", "use the profile `name` (default: the current profile)")
	fs.StringVar(&g.URL, "url", "", "the server's `address`, https://host (overrides the profile's)")
	fs.StringVar(&g.TokenFile, "token-file", "", "read the credential from `file`, which must be private (chmod 600)")
	fs.StringVar(&g.CAFile, "ca-file", "", "trust the roots in the PEM `file` as well as the system's")
	fs.BoolVar(&g.InsecureHTTP, "insecure-http", false, "allow plain http to a host that is not loopback (warns)")
	fs.BoolVar(&g.InsecureSkipVerify, "insecure-skip-verify", false,
		"do not check the server's TLS certificate (warns on every use; never saved)")
	fs.BoolVar(&g.JSON, "json", false, "print JSON, and errors as JSON on stderr")
	fs.DurationVar(&g.Timeout, "timeout", 2*time.Minute, "the time limit for a whole request")
	fs.DurationVar(&g.ConnectTimeout, "connect-timeout", 10*time.Second, "the time limit for connecting and the TLS handshake")
	fs.DurationVar(&g.HeaderTimeout, "header-timeout", 30*time.Second, "the time limit for the response headers")
}

// NewFlagSet returns a flag set for a command. Its own errors are not printed; the command reports them through the
// output package, as a usage error.
func NewFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// Parse reads args with fs and returns the positional arguments. Flags may come before or after them, so
// "profile add NAME --url X" works as well as "profile add --url X NAME". A -h prints the flags to stdout and returns
// flag.ErrHelp, which the command turns into exit 0.
func Parse(fs *flag.FlagSet, args []string, stdout io.Writer) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprintf(stdout, "usage: selfcloud %s [flags]\n", fs.Name())
				fs.SetOutput(stdout)
				fs.PrintDefaults()
				return nil, flag.ErrHelp
			}
			return nil, exitcode.Usagef("%s: %s", fs.Name(), flagProblem(err))
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// flagProblem names the problem without repeating what the user typed. A credential typed as a flag name or a flag value
// must not come back in an error, so the flag package's text is not used.
func flagProblem(err error) string {
	if strings.HasPrefix(err.Error(), "flag provided but not defined") {
		return "an unknown flag; -h lists the flags"
	}
	return "a flag value is not valid; -h lists the flags"
}

// Conn is a resolved connection. It never holds the credential as a printable value; the client keeps it.
type Conn struct {
	Client   *client.Client
	URL      string // the address, which carries no credential
	URLFrom  string // "flag", "env" or "profile"
	Profile  string // the profile used, or ""
	CredFrom string // "flag", "env", "profile", or "" when no credential is sent
	Kind     string // the credential's kind, or ""
}

// Connect resolves the address and the credential, in the order of design-cli.md 3.11: flags, then environment, then the
// profile, then the defaults. A profile's credential is sent only to that profile's own address; when the address is
// given another way, the credential is sent only with needCred, where it is a refusal, and a public command runs without
// it. Warnings go to stderr on every call.
func Connect(g Global, stderr io.Writer, needCred bool) (*Conn, error) {
	if g.Timeout <= 0 || g.ConnectTimeout <= 0 || g.HeaderTimeout <= 0 {
		return nil, exitcode.Usagef("the timeouts must be longer than zero")
	}
	dir, err := profile.DefaultDir()
	if err != nil {
		return nil, err
	}
	f, err := profile.Open(dir).Load()
	if err != nil {
		return nil, err
	}

	name := g.Profile
	if name == "" {
		name = os.Getenv(envProfile)
	}
	if name == "" {
		name = f.Current
	}
	var p profile.Profile
	haveProfile := false
	if name != "" {
		pr, ok := f.Profiles[name]
		if !ok {
			return nil, exitcode.NotFoundf("no profile called %s; see selfcloud profile ls", profile.Shown(name))
		}
		p, haveProfile = pr, true
	}

	c := &Conn{}
	if haveProfile {
		c.Profile = name
	}
	switch {
	case g.URL != "":
		c.URL, c.URLFrom = g.URL, "flag"
	case os.Getenv(envURL) != "":
		c.URL, c.URLFrom = os.Getenv(envURL), "env"
	case haveProfile:
		c.URL, c.URLFrom = p.URL, "profile"
	default:
		return nil, exitcode.Usagef("no server address: pass --url, set %s, or choose a profile", envURL)
	}

	tok, credFrom, err := chooseCredential(g, p, haveProfile)
	if err != nil {
		return nil, err
	}
	if credFrom == "profile" && c.URLFrom != "profile" {
		if needCred {
			return nil, exitcode.Usagef("the profile's credential is sent only to the profile's own address; "+
				"for this address pass --token-file, or set %s", envToken)
		}
		tok, credFrom = "", ""
	}
	if tok == "" && needCred {
		return nil, exitcode.Usagef("no credential: pass --profile, --token-file, or set %s", envToken)
	}
	if tok != "" {
		if c.Kind, err = profile.KindOf(tok); err != nil {
			return nil, err
		}
	}
	c.CredFrom = credFrom

	ca := g.CAFile
	if ca == "" && c.URLFrom == "profile" {
		ca = p.CAFile
	}
	if g.InsecureSkipVerify {
		fmt.Fprintln(stderr, "selfcloud: warning: TLS certificate checks are off (--insecure-skip-verify); "+
			"the server's identity is not checked, and this is not saved")
	}
	if u, err := url.Parse(c.URL); err == nil && u.Scheme == "http" && !client.IsLoopback(u.Hostname()) {
		fmt.Fprintln(stderr, "selfcloud: warning: plain http is not encrypted; the credential and the data cross the "+
			"network in clear (--insecure-http)")
	}
	cl, err := client.New(client.Options{
		BaseURL:            c.URL,
		Credential:         tok,
		CAFile:             ca,
		InsecureSkipVerify: g.InsecureSkipVerify,
		InsecureHTTP:       g.InsecureHTTP,
		ConnectTimeout:     g.ConnectTimeout,
		HeaderTimeout:      g.HeaderTimeout,
		Timeout:            g.Timeout,
	})
	if err != nil {
		return nil, exitcode.Usagef("%s", err)
	}
	c.Client = cl
	return c, nil
}

// chooseCredential returns the credential and where it came from: the token file, the environment, or the profile.
func chooseCredential(g Global, p profile.Profile, haveProfile bool) (string, string, error) {
	switch {
	case g.TokenFile != "":
		tok, err := profile.ReadCredentialFile(g.TokenFile)
		return tok, "flag", err
	case strings.TrimSpace(os.Getenv(envToken)) != "":
		return strings.TrimSpace(os.Getenv(envToken)), "env", nil
	case haveProfile && p.Token != "":
		return p.Token, "profile", nil
	}
	return "", "", nil
}
