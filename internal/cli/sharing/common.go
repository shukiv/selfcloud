// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sharing is the command-line client's sharing commands (design-cli.md section 8, slice cli7): share ls, grant,
// revoke and transfer-ownership; link ls, create, update, reset and rm; access ls, approve and deny; and shared.
//
// Secrets. A share link's secret is in the URL that the server returns once, when the link is made and when it is reset.
// Those two commands print that URL, and nothing else prints a secret: a listing never does, even when the server's
// answer holds one (linkFields keeps the known fields only). A link's password never comes from the command line: it is
// read from a file, or from standard input with "-", and it is never printed. Every stream a command writes goes through
// a scrubber that removes the password, and anything shaped like a link's secret, from whatever the server said back.
package sharing

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/id"
)

// maxPassword is the longest link password read, in bytes.
const maxPassword = 4096

// hidden replaces a secret in text the CLI prints.
const hidden = "[hidden]"

// accountEnv is the environment variable that names the account when --account is not given.
const accountEnv = "SELFCLOUD_ACCOUNT"

// accountArg returns the account the account-scoped routes name: the flag, or SELFCLOUD_ACCOUNT. It is checked as an
// acc_ id before any request.
func accountArg(flagValue string) (string, error) {
	v := flagValue
	if v == "" {
		v = os.Getenv(accountEnv)
	}
	if v == "" {
		return "", exitcode.Usagef("this needs the account: give --account acc_... or set %s", accountEnv)
	}
	if !id.Valid("acc", v) {
		return "", exitcode.Usagef("an account is named by its acc_ id")
	}
	return v, nil
}

// accountFromEnv reports whether SELFCLOUD_ACCOUNT is set.
func accountFromEnv() bool { return os.Getenv(accountEnv) != "" }

// idArg checks an id of the given prefix.
func idArg(prefix, what, v string) (string, error) {
	if !id.Valid(prefix, v) {
		return "", exitcode.Usagef("%s is named by its %s_ id", what, prefix)
	}
	return v, nil
}

// subjectArg checks a node permission's subject: a user (usr_) or a group (grp_).
func subjectArg(v string) (string, error) {
	if !id.Valid("usr", v) && !id.Valid("grp", v) {
		return "", exitcode.Usagef("a person or group is named by its usr_ or grp_ id")
	}
	return v, nil
}

// expiryArg turns --expires (a time) or --expires-in (a duration, with d for days) into the RFC 3339 time the API reads.
// It returns "" when neither is given. Giving both is a usage error.
func expiryArg(at, in string, now time.Time) (string, error) {
	switch {
	case at != "" && in != "":
		return "", exitcode.Usagef("give --expires or --expires-in, not both")
	case at != "":
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return "", exitcode.Usagef("--expires is a time such as 2026-12-31T00:00:00Z")
		}
		return t.UTC().Format(time.RFC3339), nil
	case in != "":
		d, err := durationArg(in)
		if err != nil {
			return "", exitcode.Usagef("--expires-in is a duration such as 36h or 30d")
		}
		return now.Add(d).UTC().Format(time.RFC3339), nil
	}
	return "", nil
}

// durationArg reads a positive duration: a Go duration such as 36h, or a whole number of days such as 30d.
func durationArg(in string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(in, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 || n > 36500 {
			return 0, errors.New("not a duration")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(in)
	if err != nil || d <= 0 {
		return 0, errors.New("not a duration")
	}
	return d, nil
}

// optBool is a boolean flag that tells "not given" from "false", so a change sends only what the person named.
type optBool struct{ set, val bool }

func (b *optBool) String() string {
	if b == nil || !b.set {
		return ""
	}
	return strconv.FormatBool(b.val)
}

func (b *optBool) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	b.set, b.val = true, v
	return nil
}

func (b *optBool) IsBoolFlag() bool { return true }

// optString is a string flag that tells "not given" from "given empty", so a person can clear a field.
type optString struct {
	set bool
	val string
}

func (s *optString) String() string {
	if s == nil {
		return ""
	}
	return s.val
}

func (s *optString) Set(v string) error {
	s.set, s.val = true, v
	return nil
}

// readPassword reads a link password from a file, or from standard input when source is "-". It is the first line, without
// its line ending. A terminal is refused for "-", since what is typed there is shown. An error never repeats the file's
// name or any of the text.
func readPassword(source string) (string, error) {
	var r io.Reader
	if source == "-" {
		if files.IsTerminal(files.Stdin) {
			return "", exitcode.Usagef("a password is read from a pipe or a file, not from a terminal, where it would be shown " +
				"as it is typed")
		}
		r = files.Stdin
	} else {
		f, err := os.Open(source)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", exitcode.Usagef("the password file does not exist")
			}
			return "", exitcode.Usagef("the password file could not be opened")
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			return "", exitcode.Usagef("the password file is not a regular file")
		}
		r = f
	}
	line, err := bufio.NewReaderSize(io.LimitReader(r, maxPassword+2), maxPassword+2).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", exitcode.Usagef("the password could not be read")
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	switch {
	case len(line) > maxPassword:
		return "", exitcode.Usagef("the password is longer than %d bytes", maxPassword)
	case line == "":
		return "", exitcode.Usagef("the password is empty")
	}
	// The server's own limits (auth.MinPasswordLen, auth.MaxPasswordLen), in characters; a refusal here costs no request.
	if n := utf8.RuneCountInString(line); n < auth.MinPasswordLen || n > auth.MaxPasswordLen {
		return "", exitcode.Usagef("the password must be %d to %d characters", auth.MinPasswordLen, auth.MaxPasswordLen)
	}
	return line, nil
}

// The shapes of what the server makes. A share link's secret is 43 characters of unpadded base64url (32 random bytes). A
// signed address ends in a token, base64url(payload), a dot and a 43-character MAC. Only these are matched, so a path such
// as /repo/api/v1/links/handler.go is left alone.
var (
	fragmentSecret = regexp.MustCompile(`/s#[A-Za-z0-9_-]{43}([^A-Za-z0-9_-]|$)`)
	signedSecret   = regexp.MustCompile(`/api/v1/links/[A-Za-z0-9_-]+\.[A-Za-z0-9_-]{43}([^A-Za-z0-9_-]|$)`)
)

// scrubText removes the given secrets, and anything shaped like a share link's secret or a signed address, from s.
func scrubText(s string, secrets ...string) string {
	s = scrubSecrets(s, secrets)
	// A match keeps the character that ends it, which may be the slash that starts the next address, so go on until the
	// text stops changing.
	for range 8 {
		before := s
		s = fragmentSecret.ReplaceAllString(s, "/s#"+hidden+"${1}")
		s = signedSecret.ReplaceAllString(s, "/api/v1/links/"+hidden+"${1}")
		if s == before {
			break
		}
	}
	return s
}

// scrubSecrets removes the given secrets, and nothing else, from s.
func scrubSecrets(s string, secrets []string) string {
	for _, v := range secrets {
		if v != "" {
			s = strings.ReplaceAll(s, v, hidden)
		}
	}
	return s
}

// unsafeRune reports whether the output layer rewrites r: a control or format character, or a line or paragraph separator.
func unsafeRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' '
}

// secretForms returns the secret as typed and each form the output layer can give it: the JSON string forms with and
// without the HTML escapes, the form with its unsafe characters as \uXXXX escapes, and the form that a plain cell gives it,
// where each unsafe character is a space. Longest first, so a form that holds another is replaced whole.
func secretForms(s string) []string {
	var forms []string
	add := func(f string) {
		if strings.TrimSpace(f) != "" && !slices.Contains(forms, f) {
			forms = append(forms, f)
		}
	}
	cleaned := strings.Map(func(r rune) rune {
		if unsafeRune(r) {
			return ' '
		}
		return r
	}, s)
	for _, v := range []string{s, cleaned} {
		add(v)
		// the JSON string forms, with and without the HTML escapes, and each with its unsafe runes as \uXXXX
		if b, err := json.Marshal(v); err == nil {
			html := string(b[1 : len(b)-1])
			add(html)
			add(escapeUnsafeRunes(html))
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if enc.Encode(v) == nil {
			plain := strings.TrimSuffix(buf.String(), "\n")
			plain = plain[1 : len(plain)-1]
			add(plain)
			add(escapeUnsafeRunes(plain))
		}
	}
	slices.SortStableFunc(forms, func(a, b string) int { return len(b) - len(a) })
	return forms
}

// escapeUnsafeRunes writes each unsafe rune of s from U+007F up as \uXXXX, or as a surrogate pair above U+FFFF, as the output
// layer does to a JSON document.
func escapeUnsafeRunes(s string) string {
	var esc strings.Builder
	for _, r := range s {
		switch {
		case r >= 0x7f && unsafeRune(r) && r > 0xffff:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&esc, `\u%04x\u%04x`, hi, lo)
		case r >= 0x7f && unsafeRune(r):
			fmt.Fprintf(&esc, `\u%04x`, r)
		default:
			esc.WriteRune(r)
		}
	}
	return esc.String()
}

// scrubber is a writer that scrubs each write before it passes it on. It works on each write alone: a secret split across two
// writes is not found, so a caller must write each value in a single Write (a bufio.Writer or io.Copy in front of it breaks
// that). With shapes false it removes the registered secrets only, and leaves a link's address alone.
type scrubber struct {
	w       io.Writer
	secrets []string
	shapes  bool
}

func (s *scrubber) add(secret string) { s.secrets = append(s.secrets, secret) }

func (s *scrubber) Write(p []byte) (int, error) {
	text := string(p)
	if s.shapes {
		text = scrubText(text, s.secrets...)
	} else {
		text = scrubSecrets(text, s.secrets)
	}
	if _, err := io.WriteString(s.w, text); err != nil {
		return 0, err
	}
	return len(p), nil
}

// linkFieldNames are the fields of a share link that a listing shows. Anything else in the server's answer is dropped, so
// a secret, a URL or a hash in it can never reach the output.
var linkFieldNames = map[string]bool{
	"id": true, "node_id": true, "audience": true, "role": true, "expires_at": true, "created_at": true,
	"inherited_from": true, "created_by": true, "visitors": true, "paused": true, "password_set": true,
	"upload_only": true, "title": true, "message": true, "ask_name": true, "notify": true,
	"created_by_kind": true, "created_by_name": true, "folder_id": true, "folder_kind": true, "folder_name": true,
	"folder_owner_id": true, "folder_owner_name": true, "hidden": true, "item_name": true, "item_path": true,
}

// linkFields keeps the known fields of a link document.
func linkFields(raw []byte) map[string]json.RawMessage {
	var all map[string]json.RawMessage
	_ = json.Unmarshal(raw, &all)
	out := map[string]json.RawMessage{}
	for k, v := range all {
		if linkFieldNames[k] {
			out[k] = v
		}
	}
	return out
}

// valueFields keeps the known fields of a link document, every one a scalar, and keeps the password out of their values. A
// key is left as it is, so a password that is a word of the output cannot change it. A field that holds an array or an
// object is dropped. A number, a bool or a null is dropped when its text holds a registered secret in any form. A string
// has the secret removed; then, as the output layer will turn each unsafe rune into a space, the cleaned text is checked
// too, and a value that still holds a secret form is replaced whole by the marks.
func (r *run) valueFields(link []byte) map[string]json.RawMessage {
	fields := linkFields(link)
	secrets := r.outS.secrets
	holds := func(s string) bool {
		for _, v := range secrets {
			if v != "" && strings.Contains(s, v) {
				return true
			}
		}
		return false
	}
	for k, raw := range fields {
		if len(raw) == 0 || raw[0] == '[' || raw[0] == '{' {
			delete(fields, k)
			continue
		}
		if raw[0] != '"' {
			if holds(string(raw)) {
				delete(fields, k)
			}
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			delete(fields, k)
			continue
		}
		s = scrubSecrets(s, secrets)
		if holds(output.Clean(s)) || holds(s) {
			s = hidden
		}
		if b, err := json.Marshal(s); err == nil {
			fields[k] = b
		} else {
			delete(fields, k)
		}
	}
	return fields
}

// text reads a string field of a decoded document; a field that is absent or not a string is "".
func text(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}

// flagText reads a field as it is printed: a string as it is, null and absent as "", anything else as its JSON.
func flagText(m map[string]json.RawMessage, key string) string {
	raw, found := m[key]
	if !found || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// run is a command in progress: its flags, its positional arguments and its scrubbed streams.
type run struct {
	g    conn.Global
	pos  []string
	out  io.Writer // standard output, scrubbed
	err  io.Writer // standard error, scrubbed
	raw  io.Writer // standard output as it is: only the one line of a new secret goes here
	outS *scrubber
	errS *scrubber
	val  io.Writer // standard output for a link's lines and documents (link shapes hidden, the password not)
}

// begin makes the flag set of a command, lets setup add its flags, and parses args. ok is false when the command must
// return code at once: after -h, or after a usage error that is already reported.
func begin(name string, args []string, stdout, stderr io.Writer, setup func(*flag.FlagSet)) (r *run, code int, ok bool) {
	r = &run{}
	r.outS = &scrubber{w: stdout, shapes: true}
	r.errS = &scrubber{w: stderr, shapes: true}
	// The stream of a new secret hides the password and leaves the address alone.
	// The link's own lines and documents go to val, which hides link-shaped text but never the password: the password is
	// hidden in the link's values (valueFields), not in the address or in a key. raw is standard output as it is.
	r.val = &scrubber{w: stdout, shapes: true}
	r.out, r.err, r.raw = r.outS, r.errS, stdout
	set := conn.NewFlagSet(name)
	r.g.Register(set)
	if setup != nil {
		setup(set)
	}
	pos, code, ok := files.Parse(set, &r.g, args, r.out, r.err)
	if !ok {
		return nil, code, false
	}
	r.pos = pos
	return r, exitcode.OK, true
}

// hide makes both streams remove secret, and each form the output layer can give it, from now on.
// A secret shorter than the server's shortest password is not registered: such a text would be replaced inside the CLI's own
// words, and the marks would show what it is.
func (r *run) hide(secret string) {
	if utf8.RuneCountInString(secret) < auth.MinPasswordLen {
		return
	}
	for _, f := range secretForms(secret) {
		r.outS.add(f)
		r.errS.add(f)
	}
}

// fail reports err and returns its exit code. The message is scrubbed first, while it is still the text the server wrote:
// the output layer cleans and encodes it, and would change a secret's characters before the scrubber of the stream saw them.
func (r *run) fail(err error) int {
	if err != nil {
		code, name, msg := exitcode.Of(err)
		err = &exitcode.Error{Exit: code, Name: scrubText(name, r.outS.secrets...), Message: scrubText(msg, r.outS.secrets...)}
	}
	return output.Fail(r.err, r.g.JSON, err)
}

func (r *run) connect() (*conn.Conn, error) { return conn.Connect(r.g, r.err, true) }

// ask asks before a destructive change. yes asks nothing. At a terminal the person types phrase, and details gives it and
// the question, which may need a read; without a terminal the command refuses with fallback, and reads nothing.
func (r *run) ask(yes bool, fallback string, details func() (phrase, what string, err error)) error {
	if yes {
		return nil
	}
	phrase, what := "", fallback
	if files.IsTerminal(files.Stdin) {
		var err error
		if phrase, what, err = details(); err != nil {
			return err
		}
	}
	return files.Confirm(false, phrase, what, r.err)
}

// outcome is one target's result in a command that acts on several targets.
type outcome struct {
	Target string     `json:"target"`
	Status string     `json:"status"`
	Error  *errorBody `json:"error,omitempty"`
	err    error
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func doneOutcome(target string) outcome { return outcome{Target: target, Status: "done"} }

func failedOutcome(target string, err error) outcome {
	_, name, msg := exitcode.Of(err)
	return outcome{Target: target, Status: "failed", Error: &errorBody{Code: name, Message: msg}, err: err}
}

// report writes the results of a command that acts on several targets and returns its exit code: the exit code of the
// error for one target; otherwise 0 when all are done, 7 when some are, and 1 when none is (design-cli 3.11).
func (r *run) report(outs []outcome) int {
	if len(outs) == 1 && outs[0].err != nil {
		return r.fail(outs[0].err)
	}
	var err error
	if r.g.JSON {
		err = output.JSON(r.out, outs)
	} else {
		rows := make([][]string, 0, len(outs))
		for _, o := range outs {
			detail := ""
			if o.Error != nil {
				detail = o.Error.Message
			}
			rows = append(rows, []string{o.Target, o.Status, detail})
		}
		err = output.Table(r.out, []string{"TARGET", "STATUS", "DETAIL"}, rows)
	}
	if err != nil {
		return r.fail(err)
	}
	failed := 0
	for _, o := range outs {
		if o.err != nil {
			failed++
		}
	}
	switch {
	case failed == 0:
		return exitcode.OK
	case failed == len(outs):
		return exitcode.Failed
	default:
		return exitcode.Partial
	}
}

// phraseFor is what a person types to confirm: the one target's id, or yes for several.
func phraseFor(targets []string) string {
	if len(targets) == 1 {
		return targets[0]
	}
	return "yes"
}

// listNotice tells the person that a listing stopped at its first page.
func (r *run) listNotice(next string) {
	if next != "" {
		_, _ = fmt.Fprintln(r.err, "selfcloud: more items exist; --all prints every page")
	}
}
