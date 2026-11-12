// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

// openRoot opens dir as a root for a test.
func openRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

// TestLocalNameRules: a name for the local disk passes the API's rules, stays one path element, is no device name on any
// platform, and leaves room for .part. The refusal is a usage error that does not repeat the name.
func TestLocalNameRules(t *testing.T) {
	for _, name := range []string{"../x", "a/b", `a\b`, "..", ".", "", "a\x00b", "a\nb", "CON", "con.txt", "Lpt1",
		"NUL.tar.gz", "COM9", "aux ", " lead", "conin$", strings.Repeat("n", 251)} {
		got, err := localName(name)
		if err == nil {
			t.Errorf("%q was taken as %q", name, got)
			continue
		}
		if code, _, msg := exitcode.Of(err); code != exitcode.Usage || (len(name) > 2 && strings.Contains(msg, name)) {
			t.Errorf("%q: exit %d, message %q", name, code, msg)
		}
	}
	for _, name := range []string{"report.pdf", "CONTRACT.txt", "console", "com10", "LPT", ".hidden", "a b",
		strings.Repeat("n", 250)} {
		if got, err := localName(name); err != nil || got != name {
			t.Errorf("%q: %q, %v", name, got, err)
		}
	}
	// The name is kept in the form the API stores, NFC.
	if got, err := localName("été.txt"); err != nil || got != "été.txt" {
		t.Errorf("decomposed name: %q, %v", got, err)
	}
}

// TestNewPartNeverFollowsALink: the part file is made with O_EXCL, so a link at its name, even one that points inside
// the directory, is refused and its target is left as it was.
func TestNewPartNeverFollowsALink(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "inside.txt")
	if err := os.WriteFile(inside, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside.txt", filepath.Join(dir, "f.bin.part")); err != nil {
		t.Fatal(err)
	}
	pf, err := newPart(openRoot(t, dir), "f.bin")
	if err == nil {
		pf.close()
		t.Fatal("a part file was made through a link")
	}
	if b, _ := os.ReadFile(inside); string(b) != "keep me" {
		t.Errorf("the link's target holds %q", b)
	}
}

// TestOldPartRefusesALinkPutInItsPlace: a part file that is replaced by a link between the look at it and its opening is
// not used: the file opened must be the one that was looked at.
func TestOldPartRefusesALinkPutInItsPlace(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "inside.txt")
	part := filepath.Join(dir, "f.bin.part")
	for path, data := range map[string]string{inside: "keep me", part: "held"} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := beforePartOpen
	t.Cleanup(func() { beforePartOpen = old })
	beforePartOpen = func(string) {
		if err := os.Remove(part); err != nil {
			t.Error(err)
		}
		if err := os.Symlink("inside.txt", part); err != nil {
			t.Error(err)
		}
	}
	pf, err := oldPart(openRoot(t, dir), "f.bin", true)
	if err == nil {
		pf.close()
		t.Fatal("the link put in the part file's place was opened")
	}
	if code, _, _ := exitcode.Of(err); code != exitcode.Usage {
		t.Errorf("exit %d, want %d", code, exitcode.Usage)
	}
	if b, _ := os.ReadFile(inside); string(b) != "keep me" {
		t.Errorf("the link's target holds %q", b)
	}
}

// TestFinishNeverReplacesANameThatAppeared: without --overwrite the finished file takes its name by a hard link, so a
// file that appeared at the name during the download is kept, and so is the part file.
func TestFinishNeverReplacesANameThatAppeared(t *testing.T) {
	dir := t.TempDir()
	root := openRoot(t, dir)
	pf, err := newPart(root, "f.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pf.copyRange(strings.NewReader("new bytes"), 9); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.bin"), []byte("someone else's"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = pf.finish(false)
	if code, _, _ := exitcode.Of(err); code != exitcode.Usage {
		t.Fatalf("finish over a name that appeared: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "f.bin")); string(b) != "someone else's" {
		t.Errorf("the file that appeared holds %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "f.bin.part")); string(b) != "new bytes" {
		t.Errorf("the part file holds %q", b)
	}
}

// TestFinishNeverRenamesAReplacedPartFile: on a file system without hard links, the part file is looked at again before
// it is given the name; one that was replaced since is not given the name, and the other file is kept.
func TestFinishNeverRenamesAReplacedPartFile(t *testing.T) {
	dir := t.TempDir()
	root := openRoot(t, dir)
	pf, err := newPart(root, "f.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pf.copyRange(strings.NewReader("new bytes"), 9); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte("someone else's"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := linkPart
	t.Cleanup(func() { linkPart = old })
	linkPart = func(*os.Root, string, string) error {
		if err := os.Rename(other, filepath.Join(dir, "f.bin.part")); err != nil {
			return err
		}
		return fs.ErrInvalid
	}
	err = pf.finish(false)
	if code, _, _ := exitcode.Of(err); code != exitcode.Failed {
		t.Fatalf("finish with a replaced part file: %v, want exit %d", err, exitcode.Failed)
	}
	if b, rerr := os.ReadFile(filepath.Join(dir, "f.bin")); rerr == nil {
		t.Errorf("the name holds %q, want nothing", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "f.bin.part")); string(b) != "someone else's" {
		t.Errorf("the file at the part file's name holds %q", b)
	}
}

// TestNewFileModeWithoutAUmask: when the umask cannot be read, the finished file is no more open than the part file
// was: it keeps the part file's mode, 0600, rather than a mode wider than the umask might allow.
func TestNewFileModeWithoutAUmask(t *testing.T) {
	old := umaskStatusPath
	t.Cleanup(func() { umaskStatusPath = old })
	for _, tc := range []struct {
		what    string
		content string
	}{
		{"a missing file", ""},
		{"a file with no Umask line", "Name:\tselfcloud\nState:\tR (running)\n"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "status")
			if tc.what == "a file with no Umask line" {
				if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			umaskStatusPath = path
			if got := newFileMode(); got != partMode {
				t.Errorf("the mode is %04o, want %04o", got, partMode)
			}
		})
	}
}

// TestOldPartRefusesALinkMadeInTheWindow: a hard link added to the part file between the look at it and its opening is
// refused, since the file opened then has another name. Only a check of the opened file reaches this, as the link is the
// file that was looked at.
func TestOldPartRefusesALinkMadeInTheWindow(t *testing.T) {
	dir := t.TempDir()
	part := filepath.Join(dir, "f.bin.part")
	if err := os.WriteFile(part, []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := beforePartOpen
	t.Cleanup(func() { beforePartOpen = old })
	beforePartOpen = func(string) {
		if err := os.Link(part, filepath.Join(dir, "other")); err != nil {
			t.Error(err)
		}
	}
	pf, err := oldPart(openRoot(t, dir), "f.bin", true)
	if err == nil {
		pf.close()
		t.Fatal("a part file with a link added after the look was reopened")
	}
	if code, _, msg := exitcode.Of(err); code != exitcode.Usage || !strings.Contains(msg, "has another name") {
		t.Errorf("exit %d, message %q, want a refusal naming another link", code, msg)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "other")); string(b) != "held" {
		t.Errorf("the other name holds %q", b)
	}
}

// TestReplacePartOfAnUnremovablePartFile: a part file --overwrite cannot remove leaves everything as it was, so the
// refusal is a usage failure (exit 2) that says nothing was changed, and the part file keeps its bytes.
func TestReplacePartOfAnUnremovablePartFile(t *testing.T) {
	dir := t.TempDir()
	part := filepath.Join(dir, "f.bin.part")
	if err := os.WriteFile(part, []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := removePart
	t.Cleanup(func() { removePart = old })
	removePart = func(*os.Root, string) error { return fs.ErrPermission }
	pf, err := replacePart(openRoot(t, dir), "f.bin")
	if err == nil {
		pf.close()
		t.Fatal("an unremovable part file was replaced")
	}
	if code, _, msg := exitcode.Of(err); code != exitcode.Usage || !strings.Contains(msg, "nothing was changed") {
		t.Errorf("exit %d, message %q, want a usage refusal saying nothing was changed", code, msg)
	}
	if b, _ := os.ReadFile(part); string(b) != "theirs" {
		t.Errorf("the part file holds %q", b)
	}
}
