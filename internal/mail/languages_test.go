// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/locale"
)

var fieldPattern = regexp.MustCompile(`\{\{\s*\.(\w+)\s*\}\}`)

// fieldsOf returns the sorted, de-duplicated field names a template body uses.
func fieldsOf(body string) []string {
	var out []string
	for _, m := range fieldPattern.FindAllStringSubmatch(body, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	slices.Sort(out)
	return out
}

// TestEveryPurposeInEveryLanguage checks that each purpose has a template in all seven languages,
// and that each one uses exactly the fields the English template uses.
func TestEveryPurposeInEveryLanguage(t *testing.T) {
	tpl, err := NewTemplates("")
	if err != nil {
		t.Fatal(err)
	}
	for _, purpose := range []string{PurposeVerify, PurposeReset} {
		_, ok := tpl.byKey[purpose+".en"]
		if !ok {
			t.Fatalf("no English %s template", purpose)
		}
		enBody, err := os.ReadFile(filepath.Join("templates", purpose+".en.txt"))
		if err != nil {
			t.Fatal(err)
		}
		want := fieldsOf(string(enBody))
		if !slices.Equal(want, []string{"ExpiresAt", "URL"}) {
			t.Fatalf("English %s uses fields %v", purpose, want)
		}

		for _, code := range locale.Supported {
			if _, ok := tpl.byKey[purpose+"."+code]; !ok {
				t.Errorf("no %s template for %s", purpose, code)
				continue
			}
			b, err := os.ReadFile(filepath.Join("templates", purpose+"."+code+".txt"))
			if err != nil {
				t.Errorf("%s.%s: %v", purpose, code, err)
				continue
			}
			if got := fieldsOf(string(b)); !slices.Equal(got, want) {
				t.Errorf("%s.%s uses fields %v, English uses %v", purpose, code, got, want)
			}
		}
	}
}

// TestRenderEachLanguage renders every purpose in every language with the same data, and checks
// the link and the expiry appear in each. A regional tag gets its base language.
func TestRenderEachLanguage(t *testing.T) {
	tpl, err := NewTemplates("")
	if err != nil {
		t.Fatal(err)
	}
	data := struct{ URL, ExpiresAt string }{"https://drive.example.com/verify#token=abc", "2026-11-04 09:30 UTC"}
	for _, purpose := range []string{PurposeVerify, PurposeReset} {
		for _, code := range locale.Supported {
			subject, body, err := tpl.Render(purpose, code, data)
			if err != nil {
				t.Fatalf("%s/%s: %v", purpose, code, err)
			}
			if strings.TrimSpace(subject) == "" || !strings.Contains(body, data.URL) || !strings.Contains(body, data.ExpiresAt) {
				t.Errorf("%s/%s lacks subject, link or expiry: %q / %q", purpose, code, subject, body)
			}
		}
		pt, _, _ := tpl.Render(purpose, "pt", data)
		ptBR, _, _ := tpl.Render(purpose, "pt-BR", data)
		if pt != ptBR {
			t.Errorf("%s: pt-BR did not get the pt text", purpose)
		}
	}
}

// TestRejectsTemplateForUnsupportedLanguage checks that a template named for a language outside the
// seven is refused at load, rather than being silently unused.
func TestRejectsTemplateForUnsupportedLanguage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "verify.de.txt"), []byte("Subject: Hallo\n\n{{.URL}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTemplates(dir); err == nil {
		t.Error("a German template was accepted")
	}
}
