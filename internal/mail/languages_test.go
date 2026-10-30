// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/locale"
)

var fieldPattern = regexp.MustCompile(`\{\{\s*\.(\w+)\s*\}\}`)

// fieldsOf returns the sorted, de-duplicated field names a template file uses, in its subject and
// its body. Conditional blocks such as {{if .Message}} are not fields, so a field that appears only
// inside one is still counted where its {{ .Name }} appears.
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

// wantFields is the field set each purpose's templates must use, in every language. A change to the
// data a caller passes changes this table, and the English and translated templates together.
var wantFields = map[string][]string{
	PurposeVerify:         {"ExpiresAt", "URL"},
	PurposeReset:          {"ExpiresAt", "URL"},
	PurposeShare:          {"Actor", "Item", "Link", "Message", "Role"},
	PurposeAccessRequest:  {"Actor", "Item", "Link", "Message", "Role"},
	PurposeAccessDecision: {"Actor", "Item", "Link", "Role"},
	PurposeDrop:           {"Count", "Folder", "Link"},
}

// mailData carries every field any purpose uses, so one value can render each template.
type mailData struct {
	URL, ExpiresAt, Actor, Item, Role, Link, Message, Folder string
	Count                                                    int
	Approved                                                 bool
}

// TestEveryPurposeInEveryLanguage checks that each purpose has a template in all seven languages,
// and that each one uses exactly the fields the English template uses.
func TestEveryPurposeInEveryLanguage(t *testing.T) {
	tpl, err := NewTemplates("")
	if err != nil {
		t.Fatal(err)
	}
	for _, purpose := range Purposes {
		want, ok := wantFields[purpose]
		if !ok {
			t.Fatalf("no field set listed for purpose %s", purpose)
		}
		enBody, err := os.ReadFile(filepath.Join("templates", purpose+".en.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if got := fieldsOf(string(enBody)); !slices.Equal(got, want) {
			t.Fatalf("English %s uses fields %v, want %v", purpose, got, want)
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

// TestRenderEachLanguage renders every purpose in every language and checks the text that must be
// there: the link and expiry for the links, the actor, item and link for the others. A regional tag
// gets its base language.
func TestRenderEachLanguage(t *testing.T) {
	tpl, err := NewTemplates("")
	if err != nil {
		t.Fatal(err)
	}
	data := mailData{
		URL:       "https://drive.example.com/verify#token=abc",
		ExpiresAt: "2026-11-04 09:30 UTC",
		Actor:     "Dana Levi",
		Item:      "Q3 report.pdf",
		Role:      RoleLabel("en", RoleEditor),
		Link:      "https://drive.example.com/items/42",
		Approved:  true,
	}
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
	for _, purpose := range []string{PurposeShare, PurposeAccessRequest, PurposeAccessDecision} {
		for _, code := range locale.Supported {
			subject, body, err := tpl.Render(purpose, code, data)
			if err != nil {
				t.Fatalf("%s/%s: %v", purpose, code, err)
			}
			for _, must := range []string{data.Actor, data.Item, data.Link, data.Role} {
				if !strings.Contains(subject+body, must) {
					t.Errorf("%s/%s lacks %q: %q / %q", purpose, code, must, subject, body)
				}
			}
			if !strings.Contains(subject, data.Actor) && !strings.Contains(subject, data.Item) {
				t.Errorf("%s/%s subject names neither the actor nor the item: %q", purpose, code, subject)
			}
		}
	}
}

// TestNoteAndDecisionOutcomes checks the optional note appears only when set, and that an approval
// and a refusal read differently.
func TestNoteAndDecisionOutcomes(t *testing.T) {
	tpl, err := NewTemplates("")
	if err != nil {
		t.Fatal(err)
	}
	data := mailData{Actor: "Dana Levi", Item: "Q3 report.pdf", Role: "editor", Link: "https://drive.example.com/items/42"}
	const note = "Please check the totals before Friday."
	for _, purpose := range []string{PurposeShare, PurposeAccessRequest} {
		for _, code := range locale.Supported {
			_, without, err := tpl.Render(purpose, code, data)
			if err != nil {
				t.Fatalf("%s/%s: %v", purpose, code, err)
			}
			withNote := data
			withNote.Message = note
			_, with, err := tpl.Render(purpose, code, withNote)
			if err != nil {
				t.Fatalf("%s/%s with a note: %v", purpose, code, err)
			}
			if strings.Contains(without, note) || !strings.Contains(with, note) {
				t.Errorf("%s/%s: the note is shown only when set (%q / %q)", purpose, code, without, with)
			}
		}
	}
	for _, code := range locale.Supported {
		_, approved, err := tpl.Render(PurposeAccessDecision, code, mailData{Actor: "Dana Levi", Item: "Q3 report.pdf", Role: "editor", Link: "u", Approved: true})
		if err != nil {
			t.Fatal(err)
		}
		_, declined, err := tpl.Render(PurposeAccessDecision, code, mailData{Actor: "Dana Levi", Item: "Q3 report.pdf", Role: "editor", Link: "u"})
		if err != nil {
			t.Fatal(err)
		}
		if approved == declined {
			t.Errorf("access_decision/%s reads the same approved and declined", code)
		}
	}
}

// TestDecisionWithoutLinkRendersCleanly: a decision with no link (a denial through a shortcut, or a deleted shortcut)
// leaves no empty line where the link would be, in every language and for both outcomes.
func TestDecisionWithoutLinkRendersCleanly(t *testing.T) {
	tpl, err := NewTemplates("")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range locale.Supported {
		for _, approved := range []bool{true, false} {
			_, body, err := tpl.Render(PurposeAccessDecision, code, mailData{Actor: "Dana Levi", Item: GenericItemLabel(code), Role: "editor", Approved: approved})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(body, "\n\n\n") || strings.Contains(body, "  \n") {
				t.Errorf("access_decision/%s approved=%v leaves an empty line without a link: %q", code, approved, body)
			}
		}
	}
}

// TestSubjectHasNoLineBreak checks that a subject is one line even when the data holds line breaks,
// which is how a caller could otherwise add a mail header.
func TestSubjectHasNoLineBreak(t *testing.T) {
	tpl, err := NewTemplates("")
	if err != nil {
		t.Fatal(err)
	}
	hostile := mailData{
		Actor: "Dana\r\nBcc: someone@example.com", Item: "Report\nX-Injected: yes", Role: "editor",
		Link: "https://drive.example.com/items/42", Approved: true,
	}
	for _, purpose := range Purposes {
		for _, code := range locale.Supported {
			subject, _, err := tpl.Render(purpose, code, hostile)
			if err != nil {
				t.Fatalf("%s/%s: %v", purpose, code, err)
			}
			if strings.ContainsAny(subject, "\r\n") {
				t.Errorf("%s/%s subject has a line break: %q", purpose, code, subject)
			}
		}
	}
}

// TestSubjectLength checks that a subject stays under 120 characters with a long name, the longest
// role label, and the longest decision wording.
func TestSubjectLength(t *testing.T) {
	tpl, err := NewTemplates("")
	if err != nil {
		t.Fatal(err)
	}
	longest := ""
	for _, labels := range roleLabels {
		for _, l := range labels {
			if utf8.RuneCountInString(l) > utf8.RuneCountInString(longest) {
				longest = l
			}
		}
	}
	data := mailData{
		Actor: "Dana Levi-Abramovitch", Item: strings.Repeat("A very long folder name ", 3)[:60],
		Role: longest, Link: "https://drive.example.com/items/42",
	}
	for _, approved := range []bool{true, false} {
		data.Approved = approved
		for _, purpose := range Purposes {
			for _, code := range locale.Supported {
				subject, _, err := tpl.Render(purpose, code, data)
				if err != nil {
					t.Fatalf("%s/%s: %v", purpose, code, err)
				}
				if n := utf8.RuneCountInString(subject); n >= 120 {
					t.Errorf("%s/%s subject is %d characters: %q", purpose, code, n, subject)
				}
			}
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

// TestRejectsTemplateForUnknownPurpose checks that a template for a purpose no code uses is refused
// at load.
func TestRejectsTemplateForUnknownPurpose(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "invite.en.txt"), []byte("Subject: Hi\n\nHello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTemplates(dir); err == nil {
		t.Error("a template for an unknown purpose was accepted")
	}
}

// TestRoleLabelEveryLocale checks that every role has a label in every language, in the table itself
// and not only through the English fallback.
func TestRoleLabelEveryLocale(t *testing.T) {
	roles := []string{RoleViewer, RoleCommenter, RoleContributor, RoleEditor, RoleManager}
	for _, code := range locale.Supported {
		for _, role := range roles {
			if roleLabels[code][role] == "" {
				t.Errorf("no %s label for %s", role, code)
			}
			if got := RoleLabel(code, role); got != roleLabels[code][role] {
				t.Errorf("RoleLabel(%q, %q) = %q, want %q", code, role, got, roleLabels[code][role])
			}
		}
	}
	if n := len(roleLabels); n != len(locale.Supported) {
		t.Errorf("labels for %d languages, want %d", n, len(locale.Supported))
	}
}

// TestRoleLabelResolvesLocale checks that a regional tag gets its base language, and an unknown
// language gets English.
func TestRoleLabelResolvesLocale(t *testing.T) {
	if got, want := RoleLabel("pt-BR", RoleEditor), roleLabels["pt"][RoleEditor]; got != want {
		t.Errorf("pt-BR editor = %q, want %q", got, want)
	}
	if got, want := RoleLabel("xx", RoleViewer), roleLabels["en"][RoleViewer]; got != want {
		t.Errorf("unknown language viewer = %q, want the English %q", got, want)
	}
}

// TestRoleLabelUnknownRole checks that a role the package does not know has no label.
func TestRoleLabelUnknownRole(t *testing.T) {
	for _, code := range locale.Supported {
		if got := RoleLabel(code, "owner"); got != "" {
			t.Errorf("RoleLabel(%q, owner) = %q, want empty", code, got)
		}
	}
}
