// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWebhookURLTokensStayOutOfAuditAndOwnerViews: a URL may carry a token in its path or its query. Neither
// appears in any audit entry, nor in an owner's list or read. The account key's read still gives the full URL.
func TestWebhookURLTokensStayOutOfAuditAndOwnerViews(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	const pathToken, queryToken = "TPATHSECRET42", "QUERYSECRET42"
	full := "https://hooks.example.com/services/" + pathToken + "/B?token=" + queryToken
	hook, _ := fx.create(t, map[string]any{"url": full, "events": []string{"file.created"}})
	wantStatus(t, call(t, fx.s, "POST", fx.base()+"/"+hook+"/secret", fx.key, nil), 200)
	wantStatus(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"description": "d"}), 200)
	wantStatus(t, call(t, fx.s, "DELETE", fx.base()+"/"+hook, fx.key, nil), 204)

	// The audit entries of the endpoint's life: create, secret, update, delete.
	audit := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil)
	wantStatus(t, audit, 200)
	var log struct {
		Items []struct {
			Action  string         `json:"action"`
			Details map[string]any `json:"details"`
		} `json:"items"`
	}
	if err := json.Unmarshal(audit.Body.Bytes(), &log); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, it := range log.Items {
		if !strings.HasPrefix(it.Action, "webhook.") {
			continue
		}
		seen[it.Action] = true
		raw, _ := json.Marshal(it.Details)
		for _, tok := range []string{pathToken, queryToken} {
			if strings.Contains(string(raw), tok) {
				t.Errorf("audit %s carries %q: %s", it.Action, tok, raw)
			}
		}
	}
	for _, action := range []string{"webhook.create", "webhook.secret", "webhook.update", "webhook.delete"} {
		if !seen[action] {
			t.Errorf("no %s audit entry", action)
		}
	}

	// The owner's list and read: no token, and the URL is only the origin.
	// The owner has no endpoint to read after the delete, so the endpoint is made again first.
	hook, _ = fx.create(t, map[string]any{"url": full, "events": []string{"file.created"}})
	for name, rec := range map[string]string{
		"owner list": callSession(t, fx.s, fx.owner, "GET", fx.base(), nil).Body.String(),
		"owner get":  callSession(t, fx.s, fx.owner, "GET", fx.base()+"/"+hook, nil).Body.String(),
	} {
		if strings.Contains(rec, pathToken) || strings.Contains(rec, queryToken) {
			t.Errorf("%s carries a token: %s", name, rec)
		}
		if !strings.Contains(rec, `"url":"https://hooks.example.com"`) {
			t.Errorf("%s does not show the origin as url: %s", name, rec)
		}
	}

	// The account key's read keeps the full URL.
	got := decode[webhookJSON](t, call(t, fx.s, "GET", fx.base()+"/"+hook, fx.key, nil))
	if got.URL != full {
		t.Fatalf("account key read url = %q, want the full URL", got.URL)
	}
}
