// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linkFileWith writes content to a file in a temporary directory and returns its path.
func linkFileWith(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "link.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// fromStdin makes standard input a pipe or a file that holds text.
func fromStdin(text string) {
	Stdin, IsTerminal = strings.NewReader(text), func(io.Reader) bool { return false }
}

// TestGetShareLinkFromStandardInput: `get -` reads the link from standard input, so it is not in argv. The first line is
// the link; spaces around it and the line ending are trimmed; later lines are ignored.
func TestGetShareLinkFromStandardInput(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	data := randomBytes(t, 3000)
	link, _ := l.shareLink(l.upload("/", "in.bin", data, "fail"), "", nil)
	for _, input := range []string{link + "\n", link, "  " + link + " \r\n", link + "\nsecond line is ignored\n"} {
		fromStdin(input)
		dest := t.TempDir()
		res := getShare(t, "-", dest)
		l.wantCode(res, 0)
		if got, err := os.ReadFile(filepath.Join(dest, "in.bin")); err != nil || sha(got) != sha(data) {
			t.Fatalf("input %q: the file differs: %v", input, err)
		}
	}
}

// TestGetShareLinkFromAFile: --link-file reads the link from a regular file, with the same rules, and the one argument
// is the destination.
func TestGetShareLinkFromAFile(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	data := randomBytes(t, 3000)
	link, _ := l.shareLink(l.upload("/", "file.bin", data, "fail"), "", nil)
	for _, content := range []string{link + "\n", link, "\t" + link + "\r\nignored\n"} {
		dest := t.TempDir()
		res := getShare(t, "--link-file", linkFileWith(t, content), dest)
		l.wantCode(res, 0)
		if got, err := os.ReadFile(filepath.Join(dest, "file.bin")); err != nil || sha(got) != sha(data) {
			t.Fatalf("content %q: the file differs: %v", content, err)
		}
	}
}

// TestGetShareLinkInputRefusals: what is refused when the link is read from standard input or a file. Each refusal is
// exit 2, sends no request, and repeats neither the link nor the file's name.
func TestGetShareLinkInputRefusals(t *testing.T) {
	const secret = "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-AbCdE"
	badLink := "https://drive.example/x#" + secret // not a share link: its path does not end in /s
	dir := t.TempDir()
	notRegular := filepath.Join(dir, "a-directory")
	if err := os.Mkdir(notRegular, 0o700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "no-such-file-name")
	long := "https://drive.example/s#" + strings.Repeat("A", 5000)
	// A line over the limit that starts with spaces: the read stops at its cap inside the line, and cutting the spaces
	// and the rest off must not leave a shorter link that passes.
	padded := strings.Repeat(" ", 3000) + "https://drive.example/s#" + strings.Repeat("A", 2000)
	cases := []struct {
		what  string
		args  func() []string
		stdin string
		tty   bool
		say   string
	}{
		{"a terminal", func() []string { return []string{"-"} }, badLink + "\n", true, "terminal"},
		{"an empty input", func() []string { return []string{"-"} }, "", false, "no link"},
		{"a blank first line", func() []string { return []string{"-"} }, "\n" + badLink + "\n", false, "no link"},
		{"a line over the limit", func() []string { return []string{"-"} }, long + "\n", false, "longer than"},
		{"a padded line over the limit", func() []string { return []string{"-"} }, padded + "\n", false, "longer than"},
		{"a padded line over the limit with no line ending", func() []string { return []string{"-"} }, padded, false, "longer than"},
		{"a link that is not a share link", func() []string { return []string{"-"} }, badLink + "\n", false, "share link"},
		{"standard input given as the link file", func() []string { return []string{"--link-file", "-"} }, badLink + "\n", false, "takes a file"},
		{"standard input as the link file, with the password also on it", func() []string {
			return []string{"--link-file", "-", "--password-stdin"}
		}, badLink + "\nsecret-password\n", false, "takes a file"},
		{"the password also on standard input", func() []string { return []string{"-", "--password-stdin"} }, badLink + "\n", false, "standard input"},
		{"a file that is not there", func() []string { return []string{"--link-file", missing} }, "", false, "link file"},
		{"a directory", func() []string { return []string{"--link-file", notRegular} }, "", false, "regular file"},
		{"an empty file", func() []string { return []string{"--link-file", linkFileWith(t, "")} }, "", false, "no link"},
		{"a file over the limit", func() []string { return []string{"--link-file", linkFileWith(t, long)} }, "", false, "longer than"},
		{"a file with a padded line over the limit", func() []string { return []string{"--link-file", linkFileWith(t, padded)} }, "", false, "longer than"},
		{"a file with a link that is not a share link", func() []string { return []string{"--link-file", linkFileWith(t, badLink)} }, "", false, "share link"},
		{"a link on the command line besides the file", func() []string {
			return []string{"--link-file", linkFileWith(t, badLink), badLink, t.TempDir()}
		}, "", false, "destination"},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			withProfile(t, f.srv.URL, testAccountKey)
			Stdin, IsTerminal = strings.NewReader(tc.stdin), func(io.Reader) bool { return tc.tty }
			res := getShare(t, tc.args()...)
			wantCode(t, res, 2)
			if !strings.Contains(res.stderr, tc.say) {
				t.Errorf("stderr %q does not say %q", res.stderr, tc.say)
			}
			for _, s := range []string{secret, "drive.example", "no-such-file-name", "a-directory", "link.txt"} {
				if strings.Contains(res.stderr+res.stdout, s) {
					t.Errorf("the output repeats %q: %q", s, res.stderr)
				}
			}
			f.wantNoCalls()
		})
	}
}

// endless yields the byte 'A' forever, never a line end, and fails the read once more than limit bytes have been taken.
type endless struct {
	n, limit int
}

func (e *endless) Read(p []byte) (int, error) {
	if e.n > e.limit {
		return 0, errors.New("read past the limit")
	}
	for i := range p {
		p[i] = 'A'
	}
	e.n += len(p)
	return len(p), nil
}

// TestReadLinkIsBoundedBeforeItIsBuffered: an input with no line end is refused as too long after a bounded read, and not
// read to its end.
func TestReadLinkIsBoundedBeforeItIsBuffered(t *testing.T) {
	e := &endless{limit: 1 << 20}
	Stdin, IsTerminal = e, func(io.Reader) bool { return false }
	_, err := readLink("-")
	if err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Fatalf("an endless input: err = %v, want a refusal that it is longer than the limit", err)
	}
	if e.n > 2*maxLink+8192 {
		t.Errorf("read %d bytes of an endless input, want a few more than %d", e.n, maxLink)
	}
}

// TestGetShareLinkFileThatCannotBeOpened: a link file that exists but cannot be opened is refused, and the refusal does
// not repeat its name.
func TestGetShareLinkFileThatCannotBeOpened(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a file of mode 000 can be opened by root")
	}
	p := filepath.Join(t.TempDir(), "unreadable-link-name")
	if err := os.WriteFile(p, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	isolate(t, testAccountKey)
	f := newFake(t)
	withProfile(t, f.srv.URL, testAccountKey)
	res := getShare(t, "--link-file", p)
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, "could not be opened") {
		t.Errorf("stderr %q does not say the file could not be opened", res.stderr)
	}
	if strings.Contains(res.stderr+res.stdout, "unreadable-link-name") {
		t.Errorf("the output repeats the file's name: %q", res.stderr)
	}
	f.wantNoCalls()
}

// TestGetShareLinkWithAPasswordFromStandardInput: `get -` cannot take the password from standard input, so the hint for
// a link that needs one names --link-file, and not the flag that `get -` refuses.
func TestGetShareLinkWithAPasswordFromStandardInput(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	link, _ := l.shareLink(l.upload("/", "locked.bin", randomBytes(t, 1000), "fail"), "correct horse battery", nil)

	fromStdin(link + "\n")
	res := getShare(t, "-", t.TempDir())
	l.wantCode(res, 3)
	if !strings.Contains(res.stderr, "--link-file") {
		t.Errorf("the hint for a password after `get -` does not name --link-file: %q", res.stderr)
	}

	// With the link in a file, the hint names the password's flag.
	res = getShare(t, "--link-file", linkFileWith(t, link+"\n"), t.TempDir())
	l.wantCode(res, 3)
	if !strings.Contains(res.stderr, "--password-stdin") {
		t.Errorf("the hint for a password does not name --password-stdin: %q", res.stderr)
	}
}
