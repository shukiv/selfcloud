// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// The installation's page scripts (ui.scripts): every app page carries them before </body>, and the policy's script-src
// gains their origins. An empty list changes nothing; a change applies from the next page served.

// scriptFiles is a build whose index has a body the scripts can sit in.
func scriptFiles() fstest.MapFS {
	files := testFiles()
	files["index.html"] = &fstest.MapFile{Data: []byte(`<!doctype html><html><head><meta property="csp-nonce" nonce="` +
		cspNoncePlaceholder + `"><title>app</title></head><body><p>app</p></body></html>`)}
	return files
}

// answerOf is an answer's body and policy with the page's per-response nonce replaced, so two answers can be compared
// byte for byte. An answer with no nonce (an asset) is returned as it is.
func answerOf(t *testing.T, rec *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	body := rec.Body.String()
	policy := rec.Header().Get("Content-Security-Policy")
	const attr = `nonce="`
	if i := strings.Index(body, attr); i >= 0 {
		rest := body[i+len(attr):]
		j := strings.Index(rest, `"`)
		if j < 0 {
			t.Fatalf("unterminated nonce in the page: %q", body)
		}
		nonce := rest[:j]
		body = strings.ReplaceAll(body, nonce, "NONCE")
		policy = strings.ReplaceAll(policy, nonce, "NONCE")
	}
	return body, policy
}

// scriptTag is the tag one entry is served as.
func scriptTag(entry string) string {
	return `<script src="` + entry + `" defer referrerpolicy="no-referrer"></script>`
}

// TestNoScriptsPageAndPolicyUnchanged: an empty list serves the page and the policy byte for byte as today, on the app
// page and on an asset alike.
func TestNoScriptsPageAndPolicyUnchanged(t *testing.T) {
	t.Parallel()
	base := newTestHandler(t, scriptFiles(), Options{})
	empty := newTestHandler(t, scriptFiles(), Options{Scripts: []string{}})
	for _, target := range []string{"/", "/files/x", "/s/abc", "/assets/app-abc123.js"} {
		a, b := get(t, base, http.MethodGet, target), get(t, empty, http.MethodGet, target)
		if a.Code != b.Code {
			t.Fatalf("%s: codes %d and %d", target, a.Code, b.Code)
		}
		wantBody, wantPolicy := answerOf(t, a)
		gotBody, gotPolicy := answerOf(t, b)
		if wantBody != gotBody {
			t.Errorf("%s: the empty list changed the page:\n%s\n%s", target, wantBody, gotBody)
		}
		if wantPolicy != gotPolicy {
			t.Errorf("%s: the empty list changed the policy:\n%s\n%s", target, wantPolicy, gotPolicy)
		}
	}
}

// TestScriptsTagsInOrderBeforeBody: each entry is served as its own tag, in order, right before </body>, on every client
// route, share pages included.
func TestScriptsTagsInOrderBeforeBody(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, scriptFiles(), Options{Scripts: []string{"https://cdn.example.com/a.js", "/b.js"}})
	for _, target := range []string{"/", "/files/x", "/s/abc", "/index.html"} {
		body := get(t, h, http.MethodGet, target).Body.String()
		want := scriptTag("https://cdn.example.com/a.js") + "\n" + scriptTag("/b.js") + "</body>"
		if !strings.Contains(body, want) {
			t.Errorf("%s: the tags are not in order right before </body>: %s", target, body)
		}
		if strings.Count(body, "<script src=") != 2 {
			t.Errorf("%s: the page carries %d script tags, want 2", target, strings.Count(body, "<script src="))
		}
	}
	// An asset is not the app page: it carries no tag.
	if body := get(t, h, http.MethodGet, "/assets/app-abc123.js").Body.String(); strings.Contains(body, "<script") {
		t.Errorf("an asset carries a script tag: %s", body)
	}
}

// TestScriptsEscaped: an entry with a quote and a < in its query stays inside the attribute; nothing breaks out of the
// attribute or the tag.
func TestScriptsEscaped(t *testing.T) {
	t.Parallel()
	entry := `https://cdn.example.com/a.js?q="x<y>&z=1`
	h := newTestHandler(t, scriptFiles(), Options{Scripts: []string{entry}})
	body := get(t, h, http.MethodGet, "/").Body.String()
	escaped := `https://cdn.example.com/a.js?q=&#34;x&lt;y&gt;&amp;z=1`
	if !strings.Contains(body, scriptTag(escaped)) {
		t.Errorf("the entry is not attribute-escaped: %s", body)
	}
	if strings.Contains(body, `q="x<y`) {
		t.Errorf("the raw entry broke into the page: %s", body)
	}
	if n := strings.Count(body, "</script>"); n != 1 {
		t.Errorf("the page holds %d script end tags, want 1: %s", n, body)
	}
}

// TestScriptsPolicyOrigins: script-src gains the origin of each URL entry, each once, on every response the handler sets
// its policy on; a path adds nothing, and the rest of the policy is unchanged. A brand SVG keeps its own sandbox policy.
func TestScriptsPolicyOrigins(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, scriptFiles(), Options{Scripts: []string{
		"https://cdn.example.com/a.js", "https://cdn.example.com/b.js", "/c.js", "http://127.0.0.1:8080/d.js",
	}})
	for _, target := range []string{"/", "/files/x", "/assets/app-abc123.js", "/assets/missing.js"} {
		rec := get(t, h, http.MethodGet, target)
		csp := rec.Header().Get("Content-Security-Policy")
		if csp == "" {
			t.Fatalf("%s: no policy", target)
		}
		if !strings.Contains(csp, "script-src 'self' https://cdn.example.com http://127.0.0.1:8080;") {
			t.Errorf("%s: script-src = %q", target, csp)
		}
		if n := strings.Count(csp, "https://cdn.example.com"); n != 1 {
			t.Errorf("%s: the https origin appears %d times, want once: %q", target, n, csp)
		}
	}
	// A brand SVG runs nothing and loads nothing; the list does not loosen its policy.
	if csp := get(t, h, http.MethodGet, "/brand/mark.svg").Header().Get("Content-Security-Policy"); csp != svgPolicy {
		t.Errorf("the brand SVG policy = %q, want %q", csp, svgPolicy)
	}
	// The whole policy is today's with the two origins in script-src and nothing else changed.
	rec := get(t, h, http.MethodGet, "/")
	nonce := nonceIn(t, rec.Body.String())
	want := "default-src 'self'; script-src 'self' https://cdn.example.com http://127.0.0.1:8080; style-src 'self' 'nonce-" + nonce + "'; " +
		"img-src 'self' data: blob:; media-src 'self'; font-src 'self'; connect-src 'self'; manifest-src 'self'; worker-src 'self'; " +
		"frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
	if got := rec.Header().Get("Content-Security-Policy"); got != want {
		t.Errorf("policy = %q, want %q", got, want)
	}
}

// TestScriptsLiveSwap: a change of the list applies from the next page served, without a restart, and an empty list
// serves the page and the policy as before.
func TestScriptsLiveSwap(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, scriptFiles(), Options{})
	beforeBody, beforePolicy := answerOf(t, get(t, h, http.MethodGet, "/"))

	h.UseScripts([]string{"https://cdn.example.com/a.js"})
	after := get(t, h, http.MethodGet, "/")
	if !strings.Contains(after.Body.String(), scriptTag("https://cdn.example.com/a.js")+"</body>") {
		t.Errorf("the next page does not carry the script: %s", after.Body.String())
	}
	if csp := after.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self' https://cdn.example.com;") {
		t.Errorf("the next policy = %q", csp)
	}

	h.UseScripts(nil)
	backBody, backPolicy := answerOf(t, get(t, h, http.MethodGet, "/"))
	if backBody != beforeBody {
		t.Errorf("the page after clearing is not the page before:\n%s\n%s", beforeBody, backBody)
	}
	if backPolicy != beforePolicy {
		t.Errorf("the policy after clearing is not the policy before:\n%s\n%s", beforePolicy, backPolicy)
	}
}
