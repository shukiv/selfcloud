package hygiene

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
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
}

// allowedPrivateWordHits maps a repository-relative path to exact line
// substrings that are not private. It is empty because every hit is either
// private or has been removed.
var allowedPrivateWordHits = map[string][]string{}

func TestWalkSeesRepositoryFiles(t *testing.T) {
	root := moduleRoot(t)
	files := walkFiles(t, root)
	for _, want := range []string{"go.mod", "docs/design.md", "web/src/main.tsx"} {
		if _, ok := files[want]; !ok {
			t.Errorf("walk did not see %s", want)
		}
	}
}

func TestNoPrivateReviewWords(t *testing.T) {
	root := moduleRoot(t)
	files := walkFiles(t, root)

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

func walkFiles(t *testing.T, root string) map[string]string {
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
			if path == root {
				return nil
			}
			if skipDir(rel, entry.Name()) {
				return fs.SkipDir
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
		files[rel] = path
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

func skipDir(rel, name string) bool {
	switch name {
	case ".git", "node_modules", "bin", ".claude":
		return true
	}
	return rel == "web/dist"
}

func allowedPrivateHit(rel, line string) bool {
	for _, substr := range allowedPrivateWordHits[rel] {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}
