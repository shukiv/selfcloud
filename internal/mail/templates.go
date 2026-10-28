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

	"selfcloud/internal/locale"
)

//go:embed templates/*.txt
var builtin embed.FS

// templateName matches a template file: purpose, then locale, as in verify.en.txt. The locale must
// be one of locale.Supported, since a template for any other language would never be chosen.
var templateName = regexp.MustCompile(`^(verify|reset)\.([a-z0-9]+)\.txt$`)

// Templates holds the mail bodies, built-in or overridden from a directory. A file starts with
// its subject on a line of its own, "Subject: ...", then a blank line, then the body, which is a
// text/template.
type Templates struct {
	byKey map[string]tmpl // "verify.en"
}

type tmpl struct {
	subject string
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
	for _, purpose := range []string{PurposeVerify, PurposeReset} {
		if _, ok := t.byKey[purpose+".en"]; !ok {
			return nil, fmt.Errorf("mail: no English template for %s", purpose)
		}
	}
	return t, nil
}

func (t *Templates) add(name, text string) error {
	m := templateName.FindStringSubmatch(name)
	if m == nil || !slices.Contains(locale.Supported, m[2]) {
		return fmt.Errorf("mail: template %q is not named purpose.locale.txt, with purpose verify or reset and "+
			"locale one of %v", name, locale.Supported)
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	head, body, ok := strings.Cut(text, "\n\n")
	subject, found := strings.CutPrefix(head, "Subject: ")
	if !ok || !found || strings.TrimSpace(subject) == "" {
		return fmt.Errorf("mail: template %q must start with a Subject line and a blank line", name)
	}
	parsed, err := template.New(name).Option("missingkey=error").Parse(body)
	if err != nil {
		return fmt.Errorf("mail: template %q: %w", name, err)
	}
	t.byKey[m[1]+"."+m[2]] = tmpl{subject: strings.TrimSpace(subject), body: parsed}
	return nil
}

// Render returns the subject and body for purpose in the caller's locale. The locale is resolved as
// locale.Resolve does: "pt-BR" gets the Portuguese text, and an unknown language gets English. data
// fills the template.
func (t *Templates) Render(purpose, loc string, data any) (subject, body string, err error) {
	e, ok := t.byKey[purpose+"."+locale.Resolve(loc)]
	if !ok {
		e, ok = t.byKey[purpose+"."+locale.Default]
	}
	if !ok {
		return "", "", fmt.Errorf("mail: no template for %s", purpose)
	}
	var b strings.Builder
	if err := e.body.Execute(&b, data); err != nil {
		return "", "", fmt.Errorf("mail: rendering %s: %w", purpose, err)
	}
	return e.subject, b.String(), nil
}
