// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

// TestNormalizeOrigin pins the rules for one embed origin: https on any host, or http on localhost and
// 127.0.0.1 only; the origin alone, with no path, query, fragment, userinfo or wildcard; lower-cased
// scheme and host, the default port and a trailing slash dropped (decision 185).
func TestNormalizeOrigin(t *testing.T) {
	t.Parallel()
	accept := map[string]string{
		"https://app.example.com":      "https://app.example.com",
		"https://app.example.com/":     "https://app.example.com",
		"HTTPS://App.Example.COM:443/": "https://app.example.com",
		"https://app.example.com:8443": "https://app.example.com:8443",
		"http://localhost":             "http://localhost",
		"http://localhost:3000/":       "http://localhost:3000",
		"http://localhost:80":          "http://localhost",
		"http://127.0.0.1:8080":        "http://127.0.0.1:8080",
		"http://127.0.0.1":             "http://127.0.0.1",
		"https://127.0.0.1":            "https://127.0.0.1",
		"https://app.example.com:443":  "https://app.example.com",
		"  https://app.example.com  ":  "https://app.example.com",
		"https://xn--9ca.example":      "https://xn--9ca.example",
		"https://a.example:8443":       "https://a.example:8443",
	}
	for in, want := range accept {
		got, err := normalizeOrigin(in)
		if err != nil || got != want {
			t.Errorf("normalizeOrigin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	reject := []string{
		"",
		"http://app.example.com",
		"ftp://app.example.com",
		"https://",
		"https://app.example.com/path",
		"https://app.example.com/?q=1",
		"https://app.example.com/#frag",
		"https://app.example.com?",
		"https://app.example.com#",
		"https://user:pass@app.example.com",
		"https://*.example.com",
		"https://192.0.2.1",
		"http://192.0.2.1",
		"https://[::1]",
		"http://foo.localhost",
		"https://app.example.com x",
		"https://" + strings.Repeat("a", 250) + ".example.com",
		"https://a.example;sandbox",
		"https://a,b.example",
		"https://a'b.example",
		"https://a\"b.example",
		"https://a(b).example",
		"https://a_b.example",
		"https://-a.example",
		"https://a..example",
		"https://é.example",
		"https://a.example:0",
		"https://a.example:65536",
	}
	for _, in := range reject {
		if got, err := normalizeOrigin(in); err == nil {
			t.Errorf("normalizeOrigin(%q) = %q; want an error naming the origin", in, got)
		} else if !strings.Contains(err.Error(), "origin") {
			t.Errorf("normalizeOrigin(%q) error %q does not name an origin", in, err)
		}
	}
}

// TestEmbedOriginsValidation: the installation list is checked at start, naming the bad origin, and
// holds at most ten (decision 185).
func TestEmbedOriginsValidation(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Embed.Origins = []string{"https://app.example.com", "http://localhost:3000"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a valid list was refused: %v", err)
	}
	cfg = Defaults()
	cfg.Embed.Origins = []string{"http://evil.example.com"}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "embed.origins") || !strings.Contains(err.Error(), "http://evil.example.com") {
		t.Fatalf("a bad origin was not named: %v", err)
	}
	cfg = Defaults()
	for i := 0; i < 11; i++ {
		cfg.Embed.Origins = append(cfg.Embed.Origins, "https://app"+string(rune('a'+i))+".example.com")
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "embed.origins") {
		t.Fatalf("eleven origins were accepted: %v", err)
	}
}

// TestEmbedOriginsEnv: SELFCLOUD_EMBED_ORIGINS is a comma list, trimmed and normalised (decision 185).
func TestEmbedOriginsEnv(t *testing.T) {
	t.Parallel()
	cfg, err := Load("", env(map[string]string{
		"SELFCLOUD_EMBED_ORIGINS": " https://App.example.com/ , http://localhost:3000 ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Embed.Origins, " "); got != "https://app.example.com http://localhost:3000" {
		t.Errorf("embed.origins = %q; want them trimmed and normalised", got)
	}
}

// TestEmbedOriginsFact: the settings API shows the installation list as a read-only fact (decision 185).
func TestEmbedOriginsFact(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Embed.Origins = []string{"https://app.example.com"}
	for _, f := range cfg.Facts() {
		if f.Key == "embed.origins" {
			list, ok := f.Value.([]string)
			if !ok || len(list) != 1 || list[0] != "https://app.example.com" {
				t.Fatalf("fact value = %#v", f.Value)
			}
			return
		}
	}
	t.Fatal("embed.origins is not among the facts")
}
