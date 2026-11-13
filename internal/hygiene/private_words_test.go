// SPDX-License-Identifier: AGPL-3.0-or-later

package hygiene

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	maxScanSize  = 2 * 1024 * 1024
	nulProbeSize = 8000
	thisFile     = "internal/hygiene/private_words_test.go"
)

var privateWordPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bR[0-9]+-[0-9]+\b`),
	regexp.MustCompile(`\b(MED|LOW|NIT|HIGH)-[0-9]+\b`),
	regexp.MustCompile(`\bdesign-(cli|tl|ce|nt[0-9]*|j|r|w|q)\b`),
	regexp.MustCompile(`design-[a-z0-9]+-(proposal|phase[0-9])`),
	regexp.MustCompile(`pending\.md`),
	regexp.MustCompile(`(?i)\bplans (list|folder)\b`),
	regexp.MustCompile(`(?i)\breview (round|fix|item)\b`),
	regexp.MustCompile(`(?i)\bround [0-9]+:`),
	regexp.MustCompile(`selfcloud-plans`),
	regexp.MustCompile(`\.local/share/selfcloud`),
	regexp.MustCompile(`~/\.cache/selfcloud-`),
	regexp.MustCompile(`\((?:[HMLFS][0-9]{1,2})(?:(?:, ?| and )[HMLFS][0-9]{1,2})*\)`),
	regexp.MustCompile(`(?:^|[^A-Za-z0-9_])[HMLF][0-9]{1,2}:`),
}

// allowedPrivateWordHits maps a repository-relative path to exact line
// substrings that are not private.
var allowedPrivateWordHits = map[string][]string{
	"docs/design.md": {"Rename (F2)"}, // F2 is a keyboard key, not a review label.
}

func TestRepositoryFilesAreListed(t *testing.T) {
	root := moduleRoot(t)
	files := repositoryFiles(t, root)
	for _, want := range []string{"go.mod", "docs/design.md", "web/src/main.tsx"} {
		if _, ok := files[want]; !ok {
			t.Errorf("repository listing did not see %s", want)
		}
	}
}

func TestRepositoryFilesWalkExport(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create directory for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("go.mod", "module example.test\n")
	write("docs/a.md", "# a\n")
	write("node_modules/x/b.md", "# b\n")
	write("web/dist/c.js", "// c\n")

	files := repositoryFiles(t, root)
	for _, want := range []string{"go.mod", "docs/a.md"} {
		if _, ok := files[want]; !ok {
			t.Errorf("export listing did not see %s", want)
		}
	}
	for _, unwanted := range []string{"node_modules/x/b.md", "web/dist/c.js"} {
		if _, ok := files[unwanted]; ok {
			t.Errorf("export listing included %s", unwanted)
		}
	}
}

func TestRepositoryFilesExcludeGitIgnored(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")

	root := t.TempDir()
	writeFile := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create directory for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	writeFile("go.mod", "module example.test\n\ngo 1.22\n")
	writeFile("clean.txt", "clean\n")
	writeFile(".gitignore", "notes.local.md\n")
	writeFile("notes.local.md", "pending.md\n")

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{
			"-c", "user.name=t", "-c", "user.email=t@example.test",
		}, args...)...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init")
	git("add", "go.mod", "clean.txt", ".gitignore")
	git("commit", "-m", "initial")

	files := repositoryFiles(t, root)
	if _, ok := files["notes.local.md"]; ok {
		t.Error("repository listing included the git-ignored file")
	}
	if _, ok := files["clean.txt"]; !ok {
		t.Error("repository listing did not include the tracked file")
	}
}

func TestNoPrivateReviewWords(t *testing.T) {
	root := moduleRoot(t)
	files := repositoryFiles(t, root)

	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	var hits []string
	for _, rel := range rels {
		data, err := os.ReadFile(files[rel])
		if err != nil {
			t.Errorf("read %s: %v", rel, err)
			continue
		}
		probe := data
		if len(probe) > nulProbeSize {
			probe = probe[:nulProbeSize]
		}
		if bytes.IndexByte(probe, 0) >= 0 {
			continue
		}

		for i, line := range strings.Split(string(data), "\n") {
			for _, pattern := range privateWordPatterns {
				match := pattern.FindString(line)
				if match == "" {
					continue
				}
				if allowedPrivateHit(rel, line) {
					break
				}
				hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, i+1, match))
				break
			}
		}
	}

	if len(hits) > 0 {
		t.Fatalf("private review words found:\n%s", strings.Join(hits, "\n"))
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		info, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil && !info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the working directory")
		}
		dir = parent
	}
}

func repositoryFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		return trackedFiles(t, root)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat .git: %v", err)
	}
	return exportedFiles(t, root)
}

func trackedFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		t.Fatalf("git ls-files: %v\n%s", err, stderr)
	}

	files := make(map[string]string)
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "web/package-lock.json" || rel == thisFile {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			t.Fatalf("stat %s: %v", rel, err)
		}
		if !info.Mode().IsRegular() || info.Size() > maxScanSize {
			continue
		}
		files[rel] = path
	}
	return files
}

func exportedFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if skipExportDir(rel, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if rel == "web/package-lock.json" || rel == thisFile {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxScanSize {
			return nil
		}
		nul, err := fileHasNUL(path, nulProbeSize)
		if err != nil {
			return err
		}
		if nul {
			return nil
		}
		files[rel] = path
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

func skipExportDir(rel, name string) bool {
	switch name {
	case ".git", "node_modules", ".claude":
		return true
	}
	switch rel {
	case "bin", "dist", "data", "e2e-out", "web/dist":
		return true
	}
	return false
}

func fileHasNUL(path string, limit int) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	return bytes.IndexByte(buf[:n], 0) >= 0, nil
}

func allowedPrivateHit(rel, line string) bool {
	for _, substr := range allowedPrivateWordHits[rel] {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}
