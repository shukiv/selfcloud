// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/shukiv/selfcloud/internal/locale"
)

//go:embed templates/*.txt
var builtin embed.FS

// templateName matches a template file: purpose, then locale, as in verify.en.txt. The purposes are
// taken from Purposes, so the pattern cannot drift from them. The locale must be one of
// locale.Supported, since a template for any other language would never be chosen.
var templateName = func() *regexp.Regexp {
	alts := make([]string, len(Purposes))
	for i, p := range Purposes {
		alts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile(`^(` + strings.Join(alts, "|") + `)\.([a-z0-9]+)\.txt$`)
}()

// subjectReplacer turns line breaks in a rendered subject into spaces. A subject is a mail header,
// and a line break in one would let user data start a new header. It applies even when a template
// or a caller puts a break in the subject.
var subjectReplacer = strings.NewReplacer("\r", " ", "\n", " ")

// Templates holds the mail bodies, built-in or overridden from a directory. A file starts with
// its subject on a line of its own, "Subject: ...", then a blank line, then the body. Both the
// subject and the body are text/templates, filled with the same data.
type Templates struct {
	byKey map[string]tmpl // "verify.en"
}

type tmpl struct {
	subject *template.Template
	body    *template.Template
}

// NewTemplates loads the built-in templates, then any file in dir (when dir is not "") that has
// the same name, which replaces the built-in one. Every purpose must have an English template.
// A broken template is an error here, not when a mail is sent.
func NewTemplates(dir string) (*Templates, error) {
	t := &Templates{byKey: map[string]tmpl{}}
	names, err := fs.Glob(builtin, "templates/*.txt")
	if err != nil {
		return nil, err
	}
	for _, p := range names {
		b, err := builtin.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if err := t.add(filepath.Base(p), string(b)); err != nil {
			return nil, err
		}
	}
	if dir != "" {
		files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
		if err != nil {
			return nil, err
		}
		for _, p := range files {
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			if err := t.add(filepath.Base(p), string(b)); err != nil {
				return nil, err
			}
		}
	}
	for _, purpose := range Purposes {
		if _, ok := t.byKey[purpose+".en"]; !ok {
			return nil, fmt.Errorf("mail: no English template for %s", purpose)
		}
	}
	return t, nil
}

func (t *Templates) add(name, text string) error {
	m := templateName.FindStringSubmatch(name)
	if m == nil || !slices.Contains(locale.Supported, m[2]) {
		return fmt.Errorf("mail: template %q is not named purpose.locale.txt, with purpose one of %v and "+
			"locale one of %v", name, Purposes, locale.Supported)
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	head, body, ok := strings.Cut(text, "\n\n")
	subject, found := strings.CutPrefix(head, "Subject: ")
	if !ok || !found || strings.TrimSpace(subject) == "" {
		return fmt.Errorf("mail: template %q must start with a Subject line and a blank line", name)
	}
	subjectT, err := template.New(name + " subject").Option("missingkey=error").Parse(strings.TrimSpace(subject))
	if err != nil {
		return fmt.Errorf("mail: template %q subject: %w", name, err)
	}
	parsed, err := template.New(name).Option("missingkey=error").Parse(body)
	if err != nil {
		return fmt.Errorf("mail: template %q: %w", name, err)
	}
	t.byKey[m[1]+"."+m[2]] = tmpl{subject: subjectT, body: parsed}
	return nil
}

// Render returns the subject and body for purpose in the caller's locale. The locale is resolved as
// locale.Resolve does: "pt-BR" gets the Portuguese text, and an unknown language gets English. data
// fills both the subject and the body. Line breaks in the rendered subject become spaces, so no
// value can add a mail header.
func (t *Templates) Render(purpose, loc string, data any) (subject, body string, err error) {
	e, ok := t.byKey[purpose+"."+locale.Resolve(loc)]
	if !ok {
		e, ok = t.byKey[purpose+"."+locale.Default]
	}
	if !ok {
		return "", "", fmt.Errorf("mail: no template for %s", purpose)
	}
	var sb strings.Builder
	if err := e.subject.Execute(&sb, data); err != nil {
		return "", "", fmt.Errorf("mail: rendering the subject of %s: %w", purpose, err)
	}
	var b strings.Builder
	if err := e.body.Execute(&b, data); err != nil {
		return "", "", fmt.Errorf("mail: rendering %s: %w", purpose, err)
	}
	return subjectReplacer.Replace(sb.String()), b.String(), nil
}
