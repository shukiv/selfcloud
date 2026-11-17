// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// TestSettingsScriptsPatchAppliesAndIsAudited: the admin sets and clears ui.scripts through the settings API; the change
// is audited as a list, and the next page served carries the scripts and their origins without a restart.
func TestSettingsScriptsPatchAppliesAndIsAudited(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)

	if v := getSettings(t, s, admin).get(t, "ui.scripts"); v.Type != "list" || v.Source != "default" || v.Locked || v.Restart {
		t.Fatalf("ui.scripts = %+v, want a list setting at its default", v)
	} else if list, ok := v.Value.([]any); !ok || len(list) != 0 {
		t.Fatalf("the default value = %#v, want an empty list", v.Value)
	}

	entries := []any{"https://cdn.example.com/menu.js", "/help.js"}
	rec := patchSettings(t, s, admin, map[string]any{"ui.scripts": entries})
	wantStatus(t, rec, 200)
	got := decode[testSettings](t, rec)
	v := got.get(t, "ui.scripts")
	if list, ok := v.Value.([]any); !ok || !slices.Equal(list, entries) || v.Source != "settings" || v.Locked {
		t.Fatalf("after the change ui.scripts = %+v", v)
	}

	// The change is audited with the list as the table's text.
	rows := settingsAudit(t, s)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	d, _ := rows[0].Details["ui.scripts"].(map[string]any)
	want := map[string]any{"old": "[]", "new": `["https://cdn.example.com/menu.js","/help.js"]`, "source": "settings"}
	for k, w := range want {
		if d[k] != w {
			t.Errorf("audit ui.scripts %s = %v, want %v", k, d[k], w)
		}
	}

	// The next page carries both tags in order before </body>, and the policy names the https origin once; the path
	// adds none. No restart: the same server answers.
	page := serveRaw(t, s, http.MethodGet, "/files")
	body := page.Body.String()
	tagA := `<script src="https://cdn.example.com/menu.js" defer referrerpolicy="no-referrer"></script>`
	tagB := `<script src="/help.js" defer referrerpolicy="no-referrer"></script>`
	if !strings.Contains(body, tagA+"\n"+tagB+"</body>") {
		t.Errorf("the page does not carry the two tags in order before </body>: %.400s", body)
	}
	csp := page.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self' https://cdn.example.com;") {
		t.Errorf("the policy's script-src = %q", csp)
	}
	if strings.Count(csp, "https://cdn.example.com") != 1 {
		t.Errorf("the https origin appears more than once: %q", csp)
	}

	// An empty list serves the page as before: no tag, and script-src 'self' alone.
	wantStatus(t, patchSettings(t, s, admin, map[string]any{"ui.scripts": []any{}}), 200)
	page = serveRaw(t, s, http.MethodGet, "/files")
	if strings.Contains(page.Body.String(), "referrerpolicy=\"no-referrer\"") {
		t.Errorf("the page still carries a script tag: %.400s", page.Body.String())
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self';") {
		t.Errorf("the policy after clearing = %q", csp)
	}

	// A reset removes the stored row, so the default applies again.
	wantStatus(t, patchSettings(t, s, admin, map[string]any{"ui.scripts": entries}), 200)
	wantStatus(t, patchSettings(t, s, admin, nil, "ui.scripts"), 200)
	if v := getSettings(t, s, admin).get(t, "ui.scripts"); v.Source != "default" {
		t.Errorf("after a reset ui.scripts = %+v", v)
	}
	if page := serveRaw(t, s, http.MethodGet, "/"); strings.Contains(page.Body.String(), "cdn.example.com") {
		t.Error("the page still carries the reset script")
	}
}

// TestSettingsScriptsLocked: a list the config file or the environment writes is locked, as the other settings are.
func TestSettingsScriptsLocked(t *testing.T) {
	t.Parallel()
	s := newSettingsServer(t, "ui:\n  scripts:\n    - /file.js\n", nil)
	admin := adminKey(t, s)
	if v := getSettings(t, s, admin).get(t, "ui.scripts"); !v.Locked || v.Source != "file" {
		t.Fatalf("the file's list = %+v, want locked and source file", v)
	}
	e := wantError(t, patchSettings(t, s, admin, map[string]any{"ui.scripts": []any{"/other.js"}}), 409, apierr.Conflict)
	if e.Details["reason"] != "setting_locked" {
		t.Errorf("reason = %v", e.Details["reason"])
	}
	if keys, _ := e.Details["keys"].([]any); !slices.Equal(keys, []any{"ui.scripts"}) {
		t.Errorf("keys = %v", e.Details["keys"])
	}

	s = newSettingsServer(t, "", map[string]string{"SELFCLOUD_UI_SCRIPTS": "/env.js"})
	admin = adminKey(t, s)
	if v := getSettings(t, s, admin).get(t, "ui.scripts"); !v.Locked || v.Source != "env" {
		t.Fatalf("the environment's list = %+v, want locked and source env", v)
	}
	wantError(t, patchSettings(t, s, admin, map[string]any{"ui.scripts": []any{}}), 409, apierr.Conflict)
}

// TestSettingsScriptsBadEntry: a bad entry is a 400 naming the key and the entry; a value that is not a JSON array of
// strings is refused too, and nothing is stored or audited.
func TestSettingsScriptsBadEntry(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	for _, entry := range []string{"http://cdn.example.com/menu.js", "javascript:alert(1)", "//cdn.example.com/menu.js", "/a/../b.js"} {
		e := wantError(t, patchSettings(t, s, admin, map[string]any{"ui.scripts": []any{entry}}), 400, apierr.InvalidRequest)
		if e.Details["reason"] != "invalid_setting" {
			t.Errorf("entry %q: reason = %v", entry, e.Details["reason"])
		}
		if keys, _ := e.Details["keys"].([]any); !slices.Contains(keys, any("ui.scripts")) {
			t.Errorf("entry %q: keys = %v", entry, e.Details["keys"])
		}
		if !strings.Contains(e.Message, entry) {
			t.Errorf("entry %q: the message does not name it: %q", entry, e.Message)
		}
	}
	for _, value := range []any{"https://cdn.example.com/menu.js", 42, map[string]any{"a": 1}, []any{1}} {
		wantError(t, patchSettings(t, s, admin, map[string]any{"ui.scripts": value}), 400, apierr.InvalidRequest)
	}
	if v := getSettings(t, s, admin).get(t, "ui.scripts"); v.Source != "default" {
		t.Errorf("after the refused changes ui.scripts = %+v", v)
	}
	if n := len(settingsAudit(t, s)); n != 0 {
		t.Errorf("a refused change wrote %d audit rows", n)
	}
}

// TestSettingsScriptsRefuseIPv6Zone: a host with an IPv6 zone is refused with a 400 naming the key and the entry, for
// the zone can carry a CSP separator into script-src.
func TestSettingsScriptsRefuseIPv6Zone(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	entry := "https://[fe80::1%25;x]/a.js"
	e := wantError(t, patchSettings(t, s, admin, map[string]any{"ui.scripts": []any{entry}}), 400, apierr.InvalidRequest)
	if e.Details["reason"] != "invalid_setting" {
		t.Errorf("reason = %v", e.Details["reason"])
	}
	if keys, _ := e.Details["keys"].([]any); !slices.Contains(keys, any("ui.scripts")) {
		t.Errorf("keys = %v", e.Details["keys"])
	}
	if !strings.Contains(e.Message, entry) {
		t.Errorf("the message does not name the entry: %q", e.Message)
	}
}

// TestSettingsScriptsWhoMayChangeThem: a non-admin is refused as for the other settings.
func TestSettingsScriptsWhoMayChangeThem(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	change := map[string]any{"set": map[string]any{"ui.scripts": []any{"/menu.js"}}}
	wantError(t, call(t, fx.s, "PATCH", "/api/v1/settings", fx.accKey, change), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.memSess, "PATCH", "/api/v1/settings", change), 403, apierr.Forbidden)
	wantError(t, call(t, fx.s, "PATCH", "/api/v1/settings", "", change), 401, apierr.Unauthenticated)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/settings", fx.admin, change), 200)
}
