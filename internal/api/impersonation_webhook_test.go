// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
)

// TestImpersonatedWritesAreMarkedAndNamed: a write made while an installation admin is signed in as a user says so on
// its webhook event, without the admin's ID, and the audit row names the admin for the owner's and the admin's viewers
// (decision 41).
func TestImpersonatedWritesAreMarkedAndNamed(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t, func(c *config.Config) { c.Admin.TenantAccess = true })
	root, rootSess := newAdminUser(t, fx.s, adminKey(t, fx.s), "root@example.com")
	user, _ := ownerUser(t, fx.s, fx.key, fx.acc.ID, "signed-in@example.com")
	fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.updated"}})
	one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
	a := putNode(t, fx.s, fx.key, one.ID, "/a.txt", "aaa")

	rec := callSession(t, fx.s, rootSess, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+user.ID+"/sign-in",
		map[string]any{"admin_password": testPassword})
	wantStatus(t, rec, 200)
	imp := sessionFrom(t, fx.s, rec)
	wantStatus(t, rawSession(t, fx.s, imp, "PUT", "/api/v1/nodes/"+a.ID+"/content", "bbbb",
		map[string]string{"If-Match": a.ETag}), 200)
	b := putNode(t, fx.s, fx.key, one.ID, "/b.txt", "bbb")
	wantStatus(t, rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+b.ID+"/content", fx.key, "bbbb",
		map[string]string{"If-Match": b.ETag}), 200)

	var marked, plain []string
	for _, r := range outboxOf(t, fx.s, fx.acc.ID) {
		if r.Type != "file.updated" {
			continue
		}
		if strings.Contains(r.Payload, `"impersonated":true`) {
			marked = append(marked, r.Payload)
		} else {
			plain = append(plain, r.Payload)
		}
	}
	if len(marked) != 1 || len(plain) != 1 {
		t.Fatalf("file.updated events: %d marked, %d plain; want one of each", len(marked), len(plain))
	}
	if strings.Contains(marked[0], root.ID) {
		t.Errorf("the marked event names the admin: %s", marked[0])
	}

	items := decode[listResponse[auditJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit",
		fx.key, nil)).Items
	var named bool
	for _, e := range items {
		if e.Details["impersonated_by"] == root.ID && e.Action == "node.write" {
			named = e.Details["impersonated_by_name"] == "root@example.com"
		}
	}
	if !named {
		t.Errorf("the audit row of the sign-in-as write does not name the admin")
	}
}
