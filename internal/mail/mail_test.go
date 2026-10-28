package mail

import (
	"context"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildMessageHeadersAndBody(t *testing.T) {
	from, _ := mail.ParseAddress("Drive <drive@example.com>")
	to, _ := mail.ParseAddress("ann@example.com")
	msg, err := buildMessage(from, to, "Verify your account", "Open this link:\n\nhttps://drive.example.com/verify#token=abc\n", time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	s := string(msg)
	head, body, ok := strings.Cut(s, "\r\n\r\n")
	if !ok {
		t.Fatal("no header/body separator")
	}
	for _, want := range []string{`From: "Drive" <drive@example.com>`, "To: ann@example.com",
		"Subject: Verify your account", "Content-Transfer-Encoding: quoted-printable"} {
		if !strings.Contains(head, want) {
			t.Errorf("headers lack %q:\n%s", want, head)
		}
	}
	if strings.Contains(strings.ReplaceAll(s, "\r\n", ""), "\n") {
		t.Error("a bare LF made it onto the wire")
	}
	// Quoted-printable writes "=" as "=3D", so the link is checked in its encoded form.
	if !strings.Contains(body, "https://drive.example.com/verify#token=3Dabc") {
		t.Errorf("link missing from body: %q", body)
	}
	// A non-ASCII subject is Q-encoded.
	msg, _ = buildMessage(from, to, "Résumé", "x", time.Now())
	if !strings.Contains(string(msg), "=?utf-8?q?R=C3=A9sum=C3=A9?=") {
		t.Errorf("subject not Q-encoded: %s", msg)
	}
}

func TestHeaderInjectionRefused(t *testing.T) {
	s, err := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: 1, TLS: "none", From: "drive@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	// Send checks headers before it dials, so no connection is attempted.
	err = s.Send(context.Background(), Message{To: "ann@example.com", Subject: "Hi\r\nBcc: spy@example.com", Body: "x"})
	if err == nil || !strings.Contains(err.Error(), "line break") {
		t.Fatalf("subject with CRLF = %v", err)
	}
	if err := s.Send(context.Background(), Message{To: "ann@example.com\r\nBcc: spy@example.com", Subject: "Hi", Body: "x"}); err == nil {
		t.Fatal("recipient with CRLF was accepted")
	}
}

func TestNewSMTPValidates(t *testing.T) {
	good := SMTPConfig{Host: "smtp.example.com", Port: 587, TLS: "starttls", From: "drive@example.com"}
	if _, err := NewSMTP(good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*SMTPConfig){
		"no host":    func(c *SMTPConfig) { c.Host = "" },
		"bad port":   func(c *SMTPConfig) { c.Port = 0 },
		"bad tls":    func(c *SMTPConfig) { c.TLS = "ssl" },
		"bad from":   func(c *SMTPConfig) { c.From = "not an address" },
		"from break": func(c *SMTPConfig) { c.From = "a@example.com\r\nBcc: b@example.com" },
	} {
		c := good
		mutate(&c)
		if _, err := NewSMTP(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTemplatesRenderWithFallback(t *testing.T) {
	tpl, err := NewTemplates("")
	if err != nil {
		t.Fatal(err)
	}
	data := struct{ URL, ExpiresAt string }{"https://drive.example.com/verify#token=t", "2026-10-31 09:00 UTC"}
	subject, body, err := tpl.Render(PurposeVerify, "en", data)
	if err != nil || subject != "Verify your account" || !strings.Contains(body, data.URL) || !strings.Contains(body, data.ExpiresAt) {
		t.Fatalf("en = %q %q %v", subject, body, err)
	}
	// An unknown language, even with a region, falls back to English. A regional tag of a supported
	// language gets that language instead (see below).
	if s, _, err := tpl.Render(PurposeReset, "de-AT", data); err != nil || s != "Reset your password" {
		t.Errorf("fallback = %q, %v", s, err)
	}
	if _, _, err := tpl.Render("nonsense", "en", data); err == nil {
		t.Error("unknown purpose rendered")
	}
}

func TestTemplatesOverrideAndValidation(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "verify.en.txt"), []byte("Subject: Custom\n\nGo to {{.URL}}\n"), 0o600))
	must(os.WriteFile(filepath.Join(dir, "verify.pt.txt"), []byte("Subject: Verificar\n\nVá a {{.URL}}\n"), 0o600))
	tpl, err := NewTemplates(dir)
	must(err)
	if s, b, err := tpl.Render(PurposeVerify, "en", struct{ URL, ExpiresAt string }{"L", ""}); err != nil || s != "Custom" || b != "Go to L\n" {
		t.Errorf("override en = %q %q %v", s, b, err)
	}
	if s, _, _ := tpl.Render(PurposeVerify, "pt-BR", struct{ URL, ExpiresAt string }{"L", ""}); s != "Verificar" {
		t.Errorf("pt-BR should use pt: %q", s)
	}
	// Reset keeps its built-in text.
	if s, _, _ := tpl.Render(PurposeReset, "en", struct{ URL, ExpiresAt string }{"L", ""}); s != "Reset your password" {
		t.Errorf("reset = %q", s)
	}

	// Files that are wrong in form are refused when the templates load.
	for _, b := range []struct{ name, text string }{
		{"verify.en.txt", "no subject line here\n\nbody"},
		{"verify.en.txt", "Subject: x\n\n{{ broken"},
		{"sms.en.txt", "Subject: x\n\nbody"},
	} {
		d := t.TempDir()
		must(os.WriteFile(filepath.Join(d, b.name), []byte(b.text), 0o600))
		if _, err := NewTemplates(d); err == nil {
			t.Errorf("%s %q: loaded", b.name, b.text)
		}
	}
	// A field the data does not have fails when that template is rendered.
	d := t.TempDir()
	must(os.WriteFile(filepath.Join(d, "verify.en.txt"), []byte("Subject: x\n\n{{.Missing}}"), 0o600))
	tp, err := NewTemplates(d)
	must(err)
	if _, _, err := tp.Render(PurposeVerify, "en", struct{ URL, ExpiresAt string }{"L", ""}); err == nil {
		t.Error("a missing field rendered")
	}
}

func TestReadSecretFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(p, []byte("s3cret\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadSecretFile(p); err != nil || got != "s3cret" {
		t.Errorf("ReadSecretFile = %q, %v", got, err)
	}
	if got, err := ReadSecretFile(""); err != nil || got != "" {
		t.Errorf("empty path = %q, %v", got, err)
	}
}
