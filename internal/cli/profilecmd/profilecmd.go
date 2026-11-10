// SPDX-License-Identifier: AGPL-3.0-or-later

// Package profilecmd is `selfcloud profile add|use|ls|rm`. These commands read and write the local profiles file and make
// no request to a server. A credential is read from --token-file or from standard input, never from argv.
package profilecmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/cli/profile"
)

// Stdin is what profile add reads a credential from. Tests replace it.
var Stdin io.Reader = os.Stdin

// IsTerminal reports whether r is a terminal or another character device, where what is typed is shown. Tests replace it.
var IsTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// store opens the profiles file at its default place.
func store() (*profile.Store, error) {
	dir, err := profile.DefaultDir()
	if err != nil {
		return nil, err
	}
	return profile.Open(dir), nil
}

// Add is `selfcloud profile add NAME --url ADDRESS [--token-file FILE | credential on standard input] [--ca-file FILE]`.
// The profile's address and its kind are printed; the credential is not.
func Add(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("profile add")
	var url, tokenFile, caFile string
	var insecureHTTP, asJSON bool
	set.StringVar(&url, "url", "", "the server's `address`, https://host")
	set.StringVar(&tokenFile, "token-file", "", "read the credential from `file`, which must be private (chmod 600)")
	set.StringVar(&caFile, "ca-file", "", "trust the roots in the PEM `file`; the path is saved with the profile")
	set.BoolVar(&insecureHTTP, "insecure-http", false, "allow plain http to a host that is not loopback (warns; not saved, so later uses need it too)")
	set.BoolVar(&asJSON, "json", false, "print JSON")
	pos, err := conn.Parse(set, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitcode.OK
		}
		return output.Fail(stderr, asJSON, err)
	}
	if len(pos) != 1 {
		return output.Fail(stderr, asJSON, exitcode.Usagef("profile add takes one name, then the flags"))
	}
	name := pos[0]
	if err := profile.ValidName(name); err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	if url == "" {
		return output.Fail(stderr, asJSON, exitcode.Usagef("--url is required"))
	}
	u, err := client.ParseBaseURL(url, insecureHTTP)
	if err != nil {
		return output.Fail(stderr, asJSON, exitcode.Usagef("%s", err))
	}
	if u.Scheme == "http" && !client.IsLoopback(u.Hostname()) {
		fmt.Fprintln(stderr, "selfcloud: warning: plain http is not encrypted; the credential and the data cross the "+
			"network in clear (--insecure-http)")
	}
	var tok string
	if tokenFile != "" {
		tok, err = profile.ReadCredentialFile(tokenFile)
	} else {
		if IsTerminal(Stdin) {
			return output.Fail(stderr, asJSON, exitcode.Usagef("standard input is a terminal: pipe the credential in, "+
				"or use --token-file, so that it is not shown as you type"))
		}
		tok, err = profile.ReadCredential(Stdin)
	}
	if err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	kind, err := profile.KindOf(tok)
	if err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	if caFile != "" {
		if _, err := client.LoadCAPool(caFile); err != nil {
			return output.Fail(stderr, asJSON, exitcode.Usagef("%s", err))
		}
	}
	s, err := store()
	if err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	f, err := s.Load()
	if err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	if _, ok := f.Profiles[name]; ok {
		return output.Fail(stderr, asJSON, exitcode.Usagef("profile %s exists; remove it with profile rm first", profile.Shown(name)))
	}
	f.Profiles[name] = profile.Profile{URL: u.String(), Kind: kind, Token: tok, CAFile: caFile}
	if f.Current == "" {
		f.Current = name
	}
	if err := s.Save(f); err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	current := f.Current == name
	if asJSON {
		if err := output.JSON(stdout, row{Name: name, URL: u.String(), Kind: kind, Current: current}); err != nil {
			return output.Fail(stderr, asJSON, err)
		}
		return exitcode.OK
	}
	if _, err := fmt.Fprintf(stdout, "added profile %s (kind %s)\n", name, kind); err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	return exitcode.OK
}

// Use is `selfcloud profile use NAME`: the profile the other commands use when no --profile is given.
func Use(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("profile use")
	var asJSON bool
	set.BoolVar(&asJSON, "json", false, "print JSON")
	pos, err := conn.Parse(set, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitcode.OK
		}
		return output.Fail(stderr, asJSON, err)
	}
	if len(pos) != 1 {
		return output.Fail(stderr, asJSON, exitcode.Usagef("profile use takes one name"))
	}
	s, err := store()
	if err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	f, err := s.Load()
	if err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	name := pos[0]
	if _, ok := f.Profiles[name]; !ok {
		return output.Fail(stderr, asJSON, exitcode.NotFoundf("no profile called %s", profile.Shown(name)))
	}
	f.Current = name
	if err := s.Save(f); err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	if asJSON {
		if err := output.JSON(stdout, map[string]string{"current": name}); err != nil {
			return output.Fail(stderr, asJSON, err)
		}
		return exitcode.OK
	}
	if _, err := fmt.Fprintf(stdout, "current profile is %s\n", name); err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	return exitcode.OK
}

// row is one profile as ls shows it. It has no field for the credential: ls never prints one.
type row struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Kind    string `json:"kind"`
	Current bool   `json:"current"`
}

// List is `selfcloud profile ls`: the profiles, by name, with their kind and which one is current.
func List(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("profile ls")
	var asJSON bool
	set.BoolVar(&asJSON, "json", false, "print JSON")
	pos, err := conn.Parse(set, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitcode.OK
		}
		return output.Fail(stderr, asJSON, err)
	}
	if len(pos) != 0 {
		return output.Fail(stderr, asJSON, exitcode.Usagef("profile ls takes no arguments"))
	}
	s, err := store()
	if err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	f, err := s.Load()
	if err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	names := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	rows := make([]row, 0, len(names))
	for _, n := range names {
		p := f.Profiles[n]
		rows = append(rows, row{Name: n, URL: p.URL, Kind: p.Kind, Current: n == f.Current})
	}
	if asJSON {
		if err := output.JSON(stdout, rows); err != nil {
			return output.Fail(stderr, asJSON, err)
		}
		return exitcode.OK
	}
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		cur := "no"
		if r.Current {
			cur = "yes"
		}
		cells = append(cells, []string{r.Name, r.URL, r.Kind, cur})
	}
	if err := output.Table(stdout, []string{"NAME", "URL", "KIND", "CURRENT"}, cells); err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	return exitcode.OK
}

// Remove is `selfcloud profile rm NAME`. It removes the local credential only; the server's key is not revoked. When the
// profile was the current one, no profile is current afterwards, and stderr says so.
func Remove(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("profile rm")
	var asJSON bool
	set.BoolVar(&asJSON, "json", false, "print JSON")
	pos, err := conn.Parse(set, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitcode.OK
		}
		return output.Fail(stderr, asJSON, err)
	}
	if len(pos) != 1 {
		return output.Fail(stderr, asJSON, exitcode.Usagef("profile rm takes one name"))
	}
	s, err := store()
	if err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	f, err := s.Load()
	if err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	name := pos[0]
	if _, ok := f.Profiles[name]; !ok {
		return output.Fail(stderr, asJSON, exitcode.NotFoundf("no profile called %s", profile.Shown(name)))
	}
	delete(f.Profiles, name)
	wasCurrent := f.Current == name
	if wasCurrent {
		f.Current = ""
	}
	if err := s.Save(f); err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	if wasCurrent {
		fmt.Fprintf(stderr, "selfcloud: profile %s was the current profile; no profile is current now "+
			"(profile use NAME selects one)\n", name)
	}
	if asJSON {
		if err := output.JSON(stdout, map[string]string{"removed": name}); err != nil {
			return output.Fail(stderr, asJSON, err)
		}
		return exitcode.OK
	}
	if _, err := fmt.Fprintf(stdout, "removed profile %s\n", name); err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	return exitcode.OK
}
