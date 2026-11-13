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

// linkSecret is the reader settings the share link caller uses.
func linkSecret(source string) SecretLine {
	return SecretLine{
		Source:     source,
		Limit:      64,
		TrimSpace:  true,
		Terminal:   "a terminal is refused",
		NotExist:   "the link file does not exist",
		OpenFailed: "the link file could not be opened",
		NotRegular: "the link file is not a regular file",
		ReadFailed: "the link could not be read",
		TooLong:    "the link is longer than %d bytes",
		Empty:      "the input holds no link on its first line",
	}
}

// passwordSecret is the reader settings a password caller uses. Spaces are part of the password, unlike a link.
func passwordSecret(source string) SecretLine {
	return SecretLine{
		Source:     source,
		Limit:      64,
		Terminal:   "a password is refused at a terminal",
		NotExist:   "the password file does not exist",
		OpenFailed: "the password file could not be opened",
		NotRegular: "the password file is not a regular file",
		ReadFailed: "the password could not be read",
		TooLong:    "the password is longer than %d bytes",
		Empty:      "the password is empty",
	}
}

// testStdin makes standard input text, not a terminal, and restores the globals after the test.
func testStdin(t *testing.T, text string) {
	t.Helper()
	oldStdin, oldTerminal := Stdin, IsTerminal
	t.Cleanup(func() { Stdin, IsTerminal = oldStdin, oldTerminal })
	Stdin, IsTerminal = strings.NewReader(text), func(io.Reader) bool { return false }
}

// testTerminal makes standard input a terminal.
func testTerminal(t *testing.T) {
	t.Helper()
	oldStdin, oldTerminal := Stdin, IsTerminal
	t.Cleanup(func() { Stdin, IsTerminal = oldStdin, oldTerminal })
	Stdin, IsTerminal = strings.NewReader("ignored"), func(io.Reader) bool { return true }
}

// secretFile writes content to a file in a temporary directory.
func secretFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestReadSecretLineLink: a link's first line is trimmed around it, whether it comes from standard input or a file.
func TestReadSecretLineLink(t *testing.T) {
	for _, in := range []string{"  https://x/s#secret  \r\nnext", "\thttps://x/s#secret\n"} {
		testStdin(t, in)
		got, err := ReadSecretLine(linkSecret("-"))
		if err != nil || got != "https://x/s#secret" {
			t.Fatalf("input %q: ReadSecretLine() = %q, %v", in, got, err)
		}
	}

	got, err := ReadSecretLine(linkSecret(secretFile(t, " \thttps://x/s#secret \r\nignored")))
	if err != nil || got != "https://x/s#secret" {
		t.Fatalf("file: ReadSecretLine() = %q, %v", got, err)
	}
}

// TestReadSecretLineLinkRefusals: each link refusal keeps its own message, and a padded line over the limit is not cut
// down to a shorter link.
func TestReadSecretLineLinkRefusals(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "a-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat(" ", 40) + strings.Repeat("A", 60)
	cases := []struct {
		what   string
		source string
		setup  func(t *testing.T)
		want   string
	}{
		{"a terminal", "-", testTerminal, "terminal"},
		{"an empty input", "-", func(t *testing.T) { testStdin(t, "") }, "no link"},
		{"a blank line", "-", func(t *testing.T) { testStdin(t, "\r\nlink\n") }, "no link"},
		{"a line over the limit", "-", func(t *testing.T) { testStdin(t, long) }, "longer than 64"},
		{"a missing file", filepath.Join(dir, "missing"), nil, "does not exist"},
		{"a directory", filepath.Join(dir, "a-directory"), nil, "regular file"},
		{"an empty file", secretFile(t, ""), nil, "no link"},
		{"a file over the limit", secretFile(t, long), nil, "longer than 64"},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}
			_, err := ReadSecretLine(linkSecret(tc.source))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ReadSecretLine() err = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "AAAA") {
				t.Errorf("the refusal repeats the secret: %v", err)
			}
		})
	}
}

// TestReadSecretLinePassword: a password keeps its spaces and loses only the line ending; standard input and a file are
// read the same way.
func TestReadSecretLinePassword(t *testing.T) {
	testStdin(t, " secret \r\nnext")
	got, err := ReadSecretLine(passwordSecret("-"))
	if err != nil || got != " secret " {
		t.Fatalf("stdin: ReadSecretLine() = %q, %v", got, err)
	}

	got, err = ReadSecretLine(passwordSecret(secretFile(t, "secret\r\nignored")))
	if err != nil || got != "secret" {
		t.Fatalf("file: ReadSecretLine() = %q, %v", got, err)
	}
}

// TestReadSecretLinePasswordRefusals: each password refusal keeps its own message.
func TestReadSecretLinePasswordRefusals(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "a-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("A", 65)
	cases := []struct {
		what   string
		source string
		setup  func(t *testing.T)
		want   string
	}{
		{"a terminal", "-", testTerminal, "terminal"},
		{"an empty input", "-", func(t *testing.T) { testStdin(t, "\r\n") }, "empty"},
		{"a line over the limit", "-", func(t *testing.T) { testStdin(t, long) }, "longer than 64"},
		{"a missing file", filepath.Join(dir, "missing"), nil, "does not exist"},
		{"a directory", filepath.Join(dir, "a-directory"), nil, "regular file"},
		{"an empty file", secretFile(t, ""), nil, "empty"},
		{"a file over the limit", secretFile(t, long), nil, "longer than 64"},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}
			_, err := ReadSecretLine(passwordSecret(tc.source))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ReadSecretLine() err = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "AAAA") {
				t.Errorf("the refusal repeats the password: %v", err)
			}
		})
	}
}

// TestReadSecretLineReadFailure: a read error that is not the end of input is refused without echoing it.
func TestReadSecretLineReadFailure(t *testing.T) {
	oldStdin, oldTerminal := Stdin, IsTerminal
	t.Cleanup(func() { Stdin, IsTerminal = oldStdin, oldTerminal })
	Stdin, IsTerminal = failingReader{}, func(io.Reader) bool { return false }
	_, err := ReadSecretLine(passwordSecret("-"))
	if err == nil || err.Error() != "the password could not be read" {
		t.Fatalf("ReadSecretLine() err = %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("a read failure") }
