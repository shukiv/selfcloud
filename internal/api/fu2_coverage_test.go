// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// sqlCount runs a count query straight on the database, for the rows a route does not show (a trashed node's labels).
func sqlCount(t *testing.T, s *Server, query string, args ...any) int {
	t.Helper()
	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// fu2: the audit entries that name a change. Each names the value it set, so the owner can read what changed.
func TestAuditEntriesNameQuotaPolicyAndAppearance(t *testing.T) {
	t.Parallel()
	fx := newP1Fixture(t)
	base := "/api/v1/accounts/" + fx.acc.ID

	wantStatus(t, call(t, fx.s, "PATCH", base+"/users/"+fx.bob.ID, fx.key, map[string]any{"quota_bytes": 10}), 200)
	var userQuota *store.AuditEntry
	for _, e := range auditActionsOf(t, fx.s, fx.acc.ID, "user.update") {
		if e.Target == fx.bob.ID {
			if _, ok := e.Details["quota_bytes"]; ok {
				userQuota = e
			}
		}
	}
	if userQuota == nil || userQuota.Details["quota_bytes"] != float64(10) {
		t.Errorf("user.update for a quota change = %v, want an entry on bob with quota_bytes 10", userQuota)
	}

	wantStatus(t, call(t, fx.s, "PATCH", base+"/storage-policy", fx.key, map[string]any{"user_quota_bytes": 30}), 200)
	pol := auditActionsOf(t, fx.s, fx.acc.ID, "account.storage_policy")
	if len(pol) != 1 || pol[0].Details["user_quota_bytes"] != float64(30) {
		t.Errorf("account.storage_policy entries = %v, want one with user_quota_bytes 30", pol)
	}

	dir := mkdirs(t, fx.s, fx.key, fx.annPF, "/tagged")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+dir.ID, fx.key, map[string]any{"color": "red"}), 200)
	app := auditActionsOf(t, fx.s, fx.acc.ID, "node.appearance")
	if len(app) != 1 || app[0].Target != dir.ID || app[0].Details["color"] != "red" {
		t.Errorf("node.appearance entries = %v, want one on the folder with color red", app)
	}
}

// fu2: a label value is kept while its node is in the trash, comes back with a restore, and is removed by a purge.
func TestLabelValuesSurviveTrashAndGoOnPurge(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	setLabel(t, lf.keyDo, lf.deep.ID, lf.cost, 2, 204)
	const selectValue = "SELECT COUNT(*) FROM node_labels WHERE node_id = ? AND field_id = ?"

	wantStatus(t, lf.keyDo("DELETE", "/api/v1/nodes/"+lf.deep.ID, nil), 204)
	if n := sqlCount(t, lf.s, selectValue, lf.deep.ID, lf.cost); n != 1 {
		t.Fatalf("label values of a trashed node = %d rows, want 1 (kept)", n)
	}
	wantStatus(t, lf.keyDo("POST", "/api/v1/nodes/"+lf.deep.ID+"/restore", nil), 200)
	if v := nodeLabels(t, lf.keyDo, lf.deep.ID)[lf.cost].Value; v != float64(2) {
		t.Errorf("value after a restore = %v, want 2", v)
	}

	wantStatus(t, lf.keyDo("DELETE", "/api/v1/nodes/"+lf.deep.ID, nil), 204)
	wantStatus(t, lf.keyDo("DELETE", "/api/v1/trash/"+lf.deep.ID, nil), 204)
	if n := sqlCount(t, lf.s, selectValue, lf.deep.ID, lf.cost); n != 0 {
		t.Errorf("label values after a purge = %d rows, want 0", n)
	}
}

// fu2: a field's choices are limited to MaxLabelChoices on PATCH too, not only on POST.
func TestLabelChoicesLimitHoldsOnPatch(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	path := "/api/v1/accounts/" + lf.acc.ID + "/label-fields"
	full := make([]map[string]any, store.MaxLabelChoices)
	for i := range full {
		full[i] = map[string]any{"name": fmt.Sprintf("c%03d", i)}
	}
	created := wantStatus201(t, lf.keyDo("POST", path, map[string]any{"name": "Full", "type": "choice", "choices": full}))
	e := wantError(t, lf.keyDo("PATCH", path+"/"+created.ID, map[string]any{"choices": []map[string]any{{"name": "one more"}}}),
		409, apierr.Conflict)
	if e.Details["reason"] != "limit" {
		t.Errorf("a choice past %d on PATCH: reason %v, want limit", store.MaxLabelChoices, e.Details["reason"])
	}
	if n := len(listFieldChoices(t, lf, created.ID)); n != store.MaxLabelChoices {
		t.Errorf("choices after the refused PATCH = %d, want %d", n, store.MaxLabelChoices)
	}
}

// wantStatus201 checks a 201 and decodes a label field from it.
func wantStatus201(t *testing.T, rec *httptest.ResponseRecorder) labelFieldJSON {
	t.Helper()
	wantStatus(t, rec, http.StatusCreated)
	return decode[labelFieldJSON](t, rec)
}

// listFieldChoices returns the choices of one field, archived ones included.
func listFieldChoices(t *testing.T, lf *labelFixture, fieldID string) []labelChoiceJSON {
	t.Helper()
	for _, f := range listFields(t, lf.keyDo) {
		if f.ID == fieldID {
			return f.Choices
		}
	}
	t.Fatalf("field %s not listed", fieldID)
	return nil
}
