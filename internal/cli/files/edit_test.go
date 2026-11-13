// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const editETag = `"v1"`

// editSetup isolates a test, gives the editor a temporary directory of its own, and starts a fake server.
func editSetup(t *testing.T) *fake {
	t.Helper()
	isolate(t, testAccountKey)
	typeAtTerminal("")
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("VISUAL", "")
	return newFake(t)
}

// editorScript writes an executable shell script that runs body with the edited file at $1, and makes it EDITOR.
func editorScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "editor.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", p)
	return p
}

// editFile is a node document for a small text file.
func editFile(name, kind string, size int, etag, mime string) map[string]any {
	n := node(testNode, name, kind)
	n["size"] = size
	n["etag"] = etag
	n["mime_type"] = mime
	return n
}

// tempEntries lists what is left under TMPDIR.
func tempEntries(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(os.Getenv("TMPDIR"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// wantNoTemp checks that the command removed its temporary directory.
func wantNoTemp(t *testing.T) {
	t.Helper()
	if entries := tempEntries(t); len(entries) != 0 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

// keptText returns the one file the command kept under TMPDIR, and its text.
func keptText(t *testing.T) (string, []byte) {
	t.Helper()
	var files []string
	err := filepath.WalkDir(os.Getenv("TMPDIR"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("kept files %v; want exactly one", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	return files[0], data
}

func TestEditSavesTheEditedTextWithIfMatch(t *testing.T) {
	f := editSetup(t)
	f.on(http.MethodGet, nodePath(testNode, ""), ok(editFile("note.txt", "file", 4, editETag, "text/plain")))
	f.on(http.MethodGet, nodePath(testNode, "/content"), reply{status: http.StatusOK, body: "old\n"})
	f.on(http.MethodPut, nodePath(testNode, "/content"), ok(editFile("note.txt", "file", 4, `"v2"`, "text/plain")))
	editorScript(t, `printf 'new\n' > "$1"`)

	res := f.run(Edit, testNode)
	wantCode(t, res, 0)
	f.wantCalls("GET "+nodePath(testNode, ""), "GET "+nodePath(testNode, "/content"),
		"PUT "+nodePath(testNode, "/content"))
	if got := f.header(2, "If-Match"); got != editETag {
		t.Errorf("If-Match = %q, want %q", got, editETag)
	}
	if got := f.header(2, "Content-Type"); got != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", got)
	}
	if got := f.bodyAt(2); got != "new\n" {
		t.Errorf("body = %q, want %q", got, "new\n")
	}
	if !strings.Contains(res.stdout, "size=4\n") || !strings.Contains(res.stdout, "etag=\"v2\"\n") {
		t.Errorf("stdout = %q, want the saved size and etag", res.stdout)
	}
	wantNoTemp(t)
}

func TestEditUnchangedSendsNothing(t *testing.T) {
	f := editSetup(t)
	f.on(http.MethodGet, nodePath(testNode, ""), ok(editFile("note.txt", "file", 4, editETag, "text/plain")))
	f.on(http.MethodGet, nodePath(testNode, "/content"), reply{status: http.StatusOK, body: "old\n"})
	editorScript(t, ":")

	res := f.run(Edit, testNode)
	wantCode(t, res, 0)
	f.wantCalls("GET "+nodePath(testNode, ""), "GET "+nodePath(testNode, "/content"))
	if !strings.Contains(res.stdout, "no change; nothing was saved") {
		t.Errorf("stdout = %q, want the no-change message", res.stdout)
	}
	wantNoTemp(t)
}

func TestEditRefusesBeforeReadingWhatItCannotEdit(t *testing.T) {
	cases := []struct {
		name string
		node map[string]any
		want string
	}{
		{"a directory", editFile("notes", "dir", 0, "", ""), "directory"},
		{"a file over the limit", editFile("big.txt", "file", editLimit+1, editETag, "text/plain"), "limit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := editSetup(t)
			f.on(http.MethodGet, nodePath(testNode, ""), ok(c.node))
			editorScript(t, `printf 'x' > "$1"`)

			res := f.run(Edit, testNode)
			wantCode(t, res, 2)
			f.wantCalls("GET " + nodePath(testNode, ""))
			if !strings.Contains(res.stderr, c.want) {
				t.Errorf("stderr = %q, want %q", res.stderr, c.want)
			}
		})
	}
}

func TestEditNeedsATerminal(t *testing.T) {
	cases := []struct {
		name   string
		stdin  bool
		stdout bool
	}{
		{"no standard input", false, true},
		{"no standard output", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := editSetup(t)
			editorScript(t, `printf 'x' > "$1"`)
			IsTerminal = func(r io.Reader) bool {
				if c.stdin && r == Stdin {
					return true
				}
				return c.stdout && r != Stdin
			}
			res := f.run(Edit, testNode)
			wantCode(t, res, 2)
			f.wantNoCalls()
			if !strings.Contains(res.stderr, "needs a terminal") {
				t.Errorf("stderr = %q, want the terminal refusal", res.stderr)
			}
		})
	}
}

func TestEditFindsAPathAndKeepsTheNameQuotedForTheEditor(t *testing.T) {
	f := editSetup(t)
	const name = `plain "name" $x.txt`
	f.on(http.MethodGet, folderPath(), page("", node(testNode, name, "file")))
	f.on(http.MethodGet, nodePath(testNode, ""), ok(editFile(name, "file", 4, editETag, "text/plain")))
	f.on(http.MethodGet, nodePath(testNode, "/content"), reply{status: http.StatusOK, body: "old\n"})
	f.on(http.MethodPut, nodePath(testNode, "/content"), ok(editFile(name, "file", 4, `"v2"`, "text/plain")))
	record := filepath.Join(t.TempDir(), "path")
	t.Setenv("RECORD", record)
	editorScript(t, `printf '%s' "$1" > "$RECORD"`+"\n"+`printf 'new\n' > "$1"`)

	res := f.run(Edit, "--folder", testFolder, "/"+name)
	wantCode(t, res, 0)
	f.wantCalls("GET "+folderPath()+"?path=%2F", "GET "+nodePath(testNode, ""),
		"GET "+nodePath(testNode, "/content"), "PUT "+nodePath(testNode, "/content"))
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(string(got)) != name {
		t.Errorf("the editor's file = %q, want a file called %q", got, name)
	}
	wantNoTemp(t)
}

func TestEditRefusesBadTextAndKeepsIt(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"invalid UTF-8", `printf '\377' > "$1"`, "UTF-8"},
		{"a NUL byte", `printf 'a\000b' > "$1"`, "NUL"},
		{"an oversized file", `dd if=/dev/zero bs=1024 count=2049 2>/dev/null | tr '\000' x > "$1"`, "limit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := editSetup(t)
			f.on(http.MethodGet, nodePath(testNode, ""), ok(editFile("note.txt", "file", 4, editETag, "text/plain")))
			f.on(http.MethodGet, nodePath(testNode, "/content"), reply{status: http.StatusOK, body: "old\n"})
			editorScript(t, c.body)

			res := f.run(Edit, testNode)
			wantCode(t, res, 2)
			f.wantCalls("GET "+nodePath(testNode, ""), "GET "+nodePath(testNode, "/content"))
			if !strings.Contains(res.stderr, c.want) {
				t.Errorf("stderr = %q, want %q", res.stderr, c.want)
			}
			path, _ := keptText(t)
			if !strings.Contains(res.stderr, path) {
				t.Errorf("stderr %q does not name the kept text %s", res.stderr, path)
			}
		})
	}
}

func TestEditKeepsTheTextWhenTheServerRefusesTheChange(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		want   int
		text   string
	}{
		{"the file changed", http.StatusPreconditionFailed, "precondition_failed", 8, "changed on the server after it was opened"},
		{"the file is locked", http.StatusLocked, "locked", 3, "your text is kept in"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := editSetup(t)
			f.on(http.MethodGet, nodePath(testNode, ""), ok(editFile("note.txt", "file", 4, editETag, "text/plain")))
			f.on(http.MethodGet, nodePath(testNode, "/content"), reply{status: http.StatusOK, body: "old\n"})
			f.on(http.MethodPut, nodePath(testNode, "/content"), apiErr(c.status, c.code, "no"))
			editorScript(t, `printf 'new text\n' > "$1"`)

			res := f.run(Edit, testNode)
			wantCode(t, res, c.want)
			if !strings.Contains(res.stderr, c.text) {
				t.Errorf("stderr = %q, want %q", res.stderr, c.text)
			}
			path, data := keptText(t)
			if string(data) != "new text\n" {
				t.Errorf("kept text = %q, want the edited text", data)
			}
			if !strings.Contains(res.stderr, path) {
				t.Errorf("stderr %q does not name the kept text %s", res.stderr, path)
			}
			dirInfo, err := os.Stat(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			if got := dirInfo.Mode().Perm(); got != 0o700 {
				t.Errorf("temporary directory mode = %o, want 700", got)
			}
			fileInfo, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := fileInfo.Mode().Perm(); got != 0o600 {
				t.Errorf("temporary file mode = %o, want 600", got)
			}
		})
	}
}

func TestEditEditorFailureSendsNothing(t *testing.T) {
	f := editSetup(t)
	f.on(http.MethodGet, nodePath(testNode, ""), ok(editFile("note.txt", "file", 4, editETag, "text/plain")))
	f.on(http.MethodGet, nodePath(testNode, "/content"), reply{status: http.StatusOK, body: "old\n"})
	editorScript(t, "exit 3")

	res := f.run(Edit, testNode)
	wantCode(t, res, 1)
	f.wantCalls("GET "+nodePath(testNode, ""), "GET "+nodePath(testNode, "/content"))
	if !strings.Contains(res.stderr, "the editor exited with status 3; nothing was saved") {
		t.Errorf("stderr = %q, want the editor's status", res.stderr)
	}
	wantNoTemp(t)
}

func TestEditKeepsAnOddNameInsideItsTemporaryDirectory(t *testing.T) {
	for _, name := range []string{"../x.md", "-rf.txt", "/etc/passwd"} {
		t.Run(name, func(t *testing.T) {
			f := editSetup(t)
			f.on(http.MethodGet, nodePath(testNode, ""), ok(editFile(name, "file", 4, editETag, "text/plain")))
			f.on(http.MethodGet, nodePath(testNode, "/content"), reply{status: http.StatusOK, body: "old\n"})
			f.on(http.MethodPut, nodePath(testNode, "/content"), ok(editFile(name, "file", 4, `"v2"`, "text/plain")))
			record := filepath.Join(t.TempDir(), "path")
			t.Setenv("RECORD", record)
			editorScript(t, `printf '%s' "$1" > "$RECORD"`+"\n"+`printf 'new\n' > "$1"`)

			res := f.run(Edit, testNode)
			wantCode(t, res, 0)
			got, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			base := filepath.Base(string(got))
			if strings.ContainsAny(base, `/\`) || strings.HasPrefix(base, "-") {
				t.Errorf("the editor's file name = %q, want no separator and no leading dash", base)
			}
			if dir := filepath.Dir(string(got)); filepath.Dir(dir) != filepath.Clean(os.Getenv("TMPDIR")) ||
				!strings.HasPrefix(filepath.Base(dir), "selfcloud-edit-") {
				t.Errorf("the editor's file = %q, want a file directly in a selfcloud-edit temporary directory", got)
			}
		})
	}
}

func TestEditJSONPrintsTheSavedSizeAndETag(t *testing.T) {
	f := editSetup(t)
	f.on(http.MethodGet, nodePath(testNode, ""), ok(editFile("note.txt", "file", 4, editETag, "text/plain")))
	f.on(http.MethodGet, nodePath(testNode, "/content"), reply{status: http.StatusOK, body: "old\n"})
	f.on(http.MethodPut, nodePath(testNode, "/content"), ok(editFile("note.txt", "file", 7, `"v2"`, "text/plain")))
	editorScript(t, `printf 'new doc' > "$1"`)

	res := f.run(Edit, testNode, "--json")
	wantCode(t, res, 0)
	var got struct {
		Size int64  `json:"size"`
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("stdout %q is not one JSON object: %v", res.stdout, err)
	}
	if got.Size != 7 || got.ETag != `"v2"` {
		t.Errorf("JSON = %+v, want size 7 and etag \"v2\"", got)
	}
}

func TestEditTakesOneFileAndItsFolderOnlyWithAPath(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no target", nil},
		{"two targets", []string{testNode, testOther}},
		{"a folder with a node id", []string{testNode, "--folder", testFolder}},
		{"a path with no folder", []string{"/a.txt"}},
		{"the folder's root", []string{"--folder", testFolder, "/"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := editSetup(t)
			editorScript(t, `printf 'x' > "$1"`)
			res := f.run(Edit, c.args...)
			wantCode(t, res, 2)
			f.wantNoCalls()
		})
	}
}

func TestEditNameIsSafe(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"note.txt", "note.txt"},
		{"../x.md", "x.md"},
		{"/etc/passwd", "etcpasswd"},
		{"-rf.txt", "rf.txt"},
		{"... ", "file"},
		{"", "file"},
		{"a\x00b\x1b.txt", "ab.txt"},
	}
	for _, c := range cases {
		if got := editName(c.in); got != c.want {
			t.Errorf("editName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("a", 200) + ".txt"
	if got := editName(long); len(got) > 100 || !strings.HasSuffix(got, ".txt") {
		t.Errorf("editName(a long name) = %q (len %d), want at most 100 bytes and the extension", got, len(got))
	}
	ext := "." + strings.Repeat("x", 60)
	if got := editName("a" + ext); len(got) > 100 {
		t.Errorf("editName(a long extension) = %q (len %d), want at most 100 bytes", got, len(got))
	}
	// A long extension is not kept as an extension when the whole name has to be cut.
	if got := editName(strings.Repeat("b", 90) + ext); len(got) > 100 || strings.HasSuffix(got, ext) {
		t.Errorf("editName(a long stem and extension) = %q (len %d), want at most 100 bytes without the extension", got, len(got))
	}
	// A cut in the middle of a rune does not leave half a rune.
	multi := strings.Repeat("é", 80)
	got := editName(multi)
	if len(got) > 100 || strings.ToValidUTF8(got, "") != got {
		t.Errorf("editName(multibyte) = %q (len %d), want whole runes", got, len(got))
	}
}
