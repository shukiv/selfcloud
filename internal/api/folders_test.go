package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"selfcloud/internal/apierr"
	"selfcloud/internal/mail"
)

// newFolder creates a folder through the API with the given credential.
func newFolder(t *testing.T, s *Server, cred, accID, name string) folderJSON {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts/"+accID+"/folders", cred, map[string]any{"name": name})
	wantStatus(t, rec, 201)
	return decode[folderJSON](t, rec)
}

func TestFolderCreateAndNames(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	folders := "/api/v1/accounts/" + acc.ID + "/folders"

	f := newFolder(t, s, key, acc.ID, "Reports")
	wantError(t, call(t, s, "POST", folders, key, map[string]any{"name": "REPORTS"}), 409, apierr.Conflict)
	wantError(t, call(t, s, "POST", folders, key, map[string]any{"name": "a/b"}), 400, apierr.InvalidName)
	wantError(t, call(t, s, "POST", folders, key, map[string]any{"name": " padded"}), 400, apierr.InvalidName)

	// With an external_id, the same name repeated returns the folder (200); another name conflicts.
	body := map[string]any{"name": "Hebrew דוח", "external_id": "ext-1"}
	first := call(t, s, "POST", folders, key, body)
	wantStatus(t, first, 201)
	wantStatus(t, call(t, s, "POST", folders, key, body), 200)
	wantError(t, call(t, s, "POST", folders, key, map[string]any{"name": "Other", "external_id": "ext-1"}),
		409, apierr.Conflict)

	// Listing and lookup by external ID; a folder of another account is not found.
	got := decode[listResponse[folderJSON]](t, call(t, s, "GET", folders+"?external_id=ext-1", key, nil))
	if len(got.Items) != 1 || got.Items[0].Name != "Hebrew דוח" {
		t.Errorf("by external_id = %+v", got.Items)
	}
	wantStatus(t, call(t, s, "GET", folders+"/"+f.ID, key, nil), 200)
	other, otherKey := newAccount(t, s, admin, "Other")
	wantError(t, call(t, s, "GET", "/api/v1/accounts/"+other.ID+"/folders/"+f.ID, otherKey, nil), 404, apierr.NotFound)

	// Rename: the name key is checked, and renaming to the same name is fine.
	wantStatus(t, call(t, s, "PATCH", folders+"/"+f.ID, key, map[string]any{"name": "reports"}), 200)
	wantError(t, call(t, s, "PATCH", folders+"/"+f.ID, key, map[string]any{"name": "Hebrew דוח"}), 409, apierr.Conflict)
	wantStatus(t, call(t, s, "PATCH", folders+"/"+f.ID, key, map[string]any{"name": "Reports"}), 200)
}

func TestFolderDeleteCascadesGrants(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	folders := "/api/v1/accounts/" + acc.ID + "/folders"
	f := newFolder(t, s, key, acc.ID, "Docs")
	_, member := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	var annID string
	for _, u := range decode[listResponse[userJSON]](t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/users", key, nil)).Items {
		if u.Email == "ann@example.com" {
			annID = u.ID
		}
	}
	wantStatus(t, call(t, s, "PUT", folders+"/"+f.ID+"/grants/"+annID, key, map[string]any{"level": "read"}), 200)

	wantStatus(t, call(t, s, "DELETE", folders+"/"+f.ID, key, nil), 204)
	wantError(t, call(t, s, "GET", folders+"/"+f.ID, key, nil), 404, apierr.NotFound)
	if got := decode[listResponse[folderAccessJSON]](t, callSession(t, s, member, "GET", "/api/v1/folders", nil)); len(got.Items) != 0 {
		t.Errorf("member still reaches %+v", got.Items)
	}
	if got := decode[listResponse[grantJSON]](t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/users/"+annID+"/grants", key, nil)); len(got.Items) != 0 {
		t.Errorf("grants left after folder delete: %+v", got.Items)
	}
}

func TestGrantsAndMyFolders(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	folders := "/api/v1/accounts/" + acc.ID + "/folders"
	reports := newFolder(t, s, key, acc.ID, "Reports")
	newFolder(t, s, key, acc.ID, "Drafts")
	owner, ownerSess := verifiedUser(t, s, key, acc.ID, "owner@example.com")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID+"/users/"+owner.ID, key,
		map[string]any{"role": "owner"}), 200)
	ann, annSess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	// Only members hold grants: an owner is refused.
	wantError(t, call(t, s, "PUT", folders+"/"+reports.ID+"/grants/"+owner.ID, key, map[string]any{"level": "read"}),
		400, apierr.InvalidRequest)
	// A user of another account cannot be granted, and an unknown level is refused.
	other, otherKey := newAccount(t, s, admin, "Other")
	stranger, _ := verifiedUser(t, s, otherKey, other.ID, "stranger@example.com")
	wantError(t, call(t, s, "PUT", folders+"/"+reports.ID+"/grants/"+stranger.ID, key, map[string]any{"level": "read"}),
		404, apierr.NotFound)
	wantError(t, call(t, s, "PUT", folders+"/"+reports.ID+"/grants/"+ann.ID, key, map[string]any{"level": "admin"}),
		400, apierr.InvalidRequest)

	// Grant read on Reports, then change to write; the grant list shows it.
	wantStatus(t, call(t, s, "PUT", folders+"/"+reports.ID+"/grants/"+ann.ID, key, map[string]any{"level": "read"}), 200)
	wantStatus(t, call(t, s, "PUT", folders+"/"+reports.ID+"/grants/"+ann.ID, key, map[string]any{"level": "write"}), 200)
	list := decode[listResponse[grantJSON]](t, call(t, s, "GET", folders+"/"+reports.ID+"/grants", key, nil))
	if len(list.Items) != 1 || list.Items[0].Level != "write" || list.Items[0].UserID != ann.ID {
		t.Fatalf("grants = %+v", list.Items)
	}

	// The member sees only Reports, at write; Drafts is not found.
	mine := decode[listResponse[folderAccessJSON]](t, callSession(t, s, annSess, "GET", "/api/v1/folders", nil))
	if len(mine.Items) != 1 || mine.Items[0].ID != reports.ID || mine.Items[0].Access != "write" {
		t.Fatalf("member folders = %+v", mine.Items)
	}
	wantStatus(t, callSession(t, s, annSess, "GET", "/api/v1/folders", nil), 200)
	// Member sessions cannot manage folders at all: the account is not theirs to manage.
	wantError(t, callSession(t, s, annSess, "GET", folders, nil), 404, apierr.NotFound)

	// An owner session reaches every folder, with write, and no grant is needed.
	all := decode[listResponse[folderAccessJSON]](t, callSession(t, s, ownerSess, "GET", "/api/v1/folders", nil))
	if len(all.Items) != 2 {
		t.Fatalf("owner folders = %+v", all.Items)
	}
	for _, f := range all.Items {
		if f.Access != "write" {
			t.Errorf("owner access on %s = %s", f.Name, f.Access)
		}
	}

	// Removing the grant takes the folder away from the member.
	wantStatus(t, call(t, s, "DELETE", folders+"/"+reports.ID+"/grants/"+ann.ID, key, nil), 204)
	wantError(t, call(t, s, "DELETE", folders+"/"+reports.ID+"/grants/"+ann.ID, key, nil), 404, apierr.NotFound)
	if got := decode[listResponse[folderAccessJSON]](t, callSession(t, s, annSess, "GET", "/api/v1/folders", nil)); len(got.Items) != 0 {
		t.Errorf("member after revoke = %+v", got.Items)
	}
}

func TestAdminsNeverReadFolders(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	newFolder(t, s, key, acc.ID, "Reports")
	_, adminSess := newAdminUser(t, s, admin, "root@example.com")

	wantError(t, call(t, s, "GET", "/api/v1/folders", admin, nil), 403, apierr.Forbidden)
	wantError(t, callSession(t, s, adminSess, "GET", "/api/v1/folders", nil), 403, apierr.Forbidden)
	// Managing an account's folder metadata is not reading files; the admin may still list it.
	wantStatus(t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/folders", admin, nil), 200)
}

// probeHandler serves folderAccess for one folder. No route uses folderAccess yet, so the test
// wraps it in a route of its own, with the same authentication as a real one.
func probeHandler(s *Server, folderID string) http.Handler {
	return s.observe(s.wrap(route{method: http.MethodGet, path: "/probe", auth: true,
		handle: func(w http.ResponseWriter, r *http.Request) error {
			_, level, err := s.folderAccess(r, folderID)
			if err != nil {
				return err
			}
			w.Header().Set("X-Level", level)
			w.WriteHeader(http.StatusNoContent)
			return nil
		}}))
}

func probeWithKey(t *testing.T, s *Server, cred, folderID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+cred)
	rec := httptest.NewRecorder()
	probeHandler(s, folderID).ServeHTTP(rec, req)
	return rec
}

func probeWithSession(t *testing.T, s *Server, sess testSession, folderID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.AddCookie(sess.cookie)
	rec := httptest.NewRecorder()
	probeHandler(s, folderID).ServeHTTP(rec, req)
	return rec
}

// TestFolderAccessRules checks what folderAccess lets each kind of caller reach.
func TestFolderAccessRules(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	other, otherKey := newAccount(t, s, admin, "Other")
	f := newFolder(t, s, key, acc.ID, "Reports")
	foreign := newFolder(t, s, otherKey, other.ID, "Theirs")
	ann, annSess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/folders/"+f.ID+"/grants/"+ann.ID, key,
		map[string]any{"level": "read"}), 200)
	_, adminSess := newAdminUser(t, s, admin, "root@example.com")

	// The account key reaches its own folder with write, and not another account's.
	if rec := probeWithKey(t, s, key, f.ID); rec.Code != 204 || rec.Header().Get("X-Level") != "write" {
		t.Errorf("account key on own folder: %d %s", rec.Code, rec.Header().Get("X-Level"))
	}
	wantError(t, probeWithKey(t, s, key, foreign.ID), 404, apierr.NotFound)

	// The member reaches the granted folder at the granted level, and no other.
	if rec := probeWithSession(t, s, annSess, f.ID); rec.Code != 204 || rec.Header().Get("X-Level") != "read" {
		t.Errorf("member on granted folder: %d %s", rec.Code, rec.Header().Get("X-Level"))
	}
	wantError(t, probeWithSession(t, s, annSess, foreign.ID), 404, apierr.NotFound)

	// Admins are forbidden on any folder, with a key or a session.
	wantError(t, probeWithKey(t, s, admin, f.ID), 403, apierr.Forbidden)
	wantError(t, probeWithSession(t, s, adminSess, f.ID), 403, apierr.Forbidden)
}
