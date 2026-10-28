package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"selfcloud/internal/apierr"
	"selfcloud/internal/auth"
	"selfcloud/internal/id"
	"selfcloud/internal/store"
)

// adminKey stores a new installation admin key and returns its secret.
func adminKey(t *testing.T, s *Server) string {
	t.Helper()
	secret, prefix, hash := auth.NewKey(auth.AdminKeyPrefix)
	ctx := context.Background()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.InsertAPIKey(ctx, &store.APIKey{ID: id.New("key"), Name: "test", Prefix: prefix,
			Hash: hash, CreatedAt: store.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

// call sends a request with an optional bearer credential and JSON body.
func call(t *testing.T, s *Server, method, path, cred string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
	return v
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
}

func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, code apierr.Code) errorDetail {
	t.Helper()
	wantStatus(t, rec, status)
	e := decodeError(t, rec)
	if e.Code != code {
		t.Fatalf("code %s, want %s: %s", e.Code, code, e.Message)
	}
	return e
}

type createdKey struct {
	keyJSON
	Key string `json:"key"`
}

// newAccount creates an account and an account key through the API.
func newAccount(t *testing.T, s *Server, admin, name string) (accountJSON, string) {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts", admin, map[string]any{"name": name})
	wantStatus(t, rec, 201)
	acc := decode[accountJSON](t, rec)
	rec = call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/keys", admin, map[string]any{"name": "test"})
	wantStatus(t, rec, 201)
	return acc, decode[createdKey](t, rec).Key
}

func TestAuthentication(t *testing.T) {
	s := newTestServer(t)
	admin := adminKey(t, s)
	fakeAdmin, _, _ := auth.NewKey(auth.AdminKeyPrefix)

	for name, header := range map[string]string{
		"missing":       "",
		"basic":         "Basic dXNlcjpwYXNz",
		"empty bearer":  "Bearer ",
		"unknown kind":  "Bearer xyz_123",
		"malformed key": "Bearer " + auth.AdminKeyPrefix + "short",
		"unknown key":   "Bearer " + fakeAdmin,
	} {
		req := httptest.NewRequest("GET", "/api/v1/accounts", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 401 || decodeError(t, rec).Code != apierr.Unauthenticated {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	wantStatus(t, call(t, s, "GET", "/api/v1/accounts", admin, nil), 200)

	acc, key := newAccount(t, s, admin, "Acme")
	// An account key presented as an admin key is refused, and the reverse.
	wantError(t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID, auth.AdminKeyPrefix+strings.TrimPrefix(key, auth.AccountKeyPrefix), nil),
		401, apierr.Unauthenticated)

	expiring := call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/keys", admin,
		map[string]any{"name": "short-lived", "expires_at": time.Now().Add(time.Hour)})
	wantStatus(t, expiring, 201)
	ek := decode[createdKey](t, expiring)
	wantStatus(t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID, ek.Key, nil), 200)
	wantError(t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/keys", admin,
		map[string]any{"name": "past", "expires_at": time.Now().Add(-time.Hour)}), 400, apierr.InvalidRequest)
}

func TestAccountLifecycle(t *testing.T) {
	s := newTestServer(t)
	admin := adminKey(t, s)
	ctx := context.Background()

	body := map[string]any{"name": "Acme", "external_id": "tenant-1", "quota_bytes": 1000}
	rec := call(t, s, "POST", "/api/v1/accounts", admin, body)
	wantStatus(t, rec, 201)
	acc := decode[accountJSON](t, rec)
	if rec.Header().Get("Location") != "/api/v1/accounts/"+acc.ID || acc.Status != "active" ||
		acc.ExternalID == nil || *acc.ExternalID != "tenant-1" {
		t.Fatalf("created %+v, Location %q", acc, rec.Header().Get("Location"))
	}

	// Retrying the same create is safe; a different one with the same external_id is not.
	rec = call(t, s, "POST", "/api/v1/accounts", admin, body)
	wantStatus(t, rec, 200)
	if again := decode[accountJSON](t, rec); again.ID != acc.ID {
		t.Fatalf("retry made a second account %s", again.ID)
	}
	body["quota_bytes"] = 2000
	e := wantError(t, call(t, s, "POST", "/api/v1/accounts", admin, body), 409, apierr.Conflict)
	if e.Details["id"] != acc.ID {
		t.Errorf("conflict details = %v", e.Details)
	}

	rec = call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID, admin, map[string]any{"quota_bytes": 5, "status": "suspended"})
	wantStatus(t, rec, 200)
	if got := decode[accountJSON](t, rec); got.QuotaBytes != 5 || got.Status != "suspended" {
		t.Errorf("after PATCH: %+v", got)
	}
	wantError(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID, admin, map[string]any{"status": "deleting"}),
		400, apierr.InvalidRequest)

	rec = call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/keys", admin, map[string]any{"name": "backend"})
	wantStatus(t, rec, 201)
	ck := decode[createdKey](t, rec)
	if !auth.ValidKeyFormat(auth.AccountKeyPrefix, ck.Key) || !strings.HasPrefix(ck.Key, ck.Prefix) {
		t.Fatalf("created key %+v", ck)
	}
	rec = call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/keys", admin, nil)
	if strings.Contains(rec.Body.String(), ck.Key) || strings.Contains(rec.Body.String(), `"key"`) {
		t.Error("the key list shows the secret")
	}

	wantError(t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID, ck.Key, nil), 403, apierr.AccountSuspended)
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID, admin, map[string]any{"status": "active"}), 200)
	wantStatus(t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID, ck.Key, nil), 200)

	rec = call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/usage", ck.Key, nil)
	wantStatus(t, rec, 200)
	if u := decode[usageJSON](t, rec); u.AvailableBytes == nil || *u.AvailableBytes != 5 {
		t.Errorf("usage = %+v", u)
	}

	rec = call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/audit", ck.Key, nil)
	wantStatus(t, rec, 200)
	var actions []string
	for _, a := range decode[listResponse[auditJSON]](t, rec).Items {
		actions = append(actions, a.Action)
		if a.Actor.Kind != "admin_key" || a.IP == "" {
			t.Errorf("audit entry without actor or IP: %+v", a)
		}
	}
	if strings.Join(actions, ",") != "account.update,key.create,account.update,account.create" {
		t.Errorf("audit actions = %v", actions)
	}

	rec = call(t, s, "DELETE", "/api/v1/accounts/"+acc.ID, admin, nil)
	wantStatus(t, rec, 202)
	job := decode[jobJSON](t, rec)
	if job.Status != "queued" || rec.Header().Get("Location") != "/api/v1/jobs/"+job.ID {
		t.Fatalf("delete job %+v", job)
	}
	rec = call(t, s, "DELETE", "/api/v1/accounts/"+acc.ID, admin, nil)
	if again := decode[jobJSON](t, rec); again.ID != job.ID {
		t.Errorf("second DELETE queued another job %s", again.ID)
	}
	wantError(t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID, ck.Key, nil), 401, apierr.Unauthenticated)
	wantError(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID, admin, map[string]any{"name": "x"}), 409, apierr.Conflict)

	if err := s.jobs.RunPending(ctx); err != nil {
		t.Fatal(err)
	}
	rec = call(t, s, "GET", "/api/v1/jobs/"+job.ID, admin, nil)
	if got := decode[jobJSON](t, rec); got.Status != "succeeded" || got.FinishedAt == nil {
		t.Errorf("job after run = %+v", got)
	}
	wantError(t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID, admin, nil), 404, apierr.NotFound)

	// The external_id is free again.
	wantStatus(t, call(t, s, "POST", "/api/v1/accounts", admin, body), 201)
}

func TestAccountKeyScope(t *testing.T) {
	s := newTestServer(t)
	admin := adminKey(t, s)
	a, keyA := newAccount(t, s, admin, "A")
	b, _ := newAccount(t, s, admin, "B")

	wantStatus(t, call(t, s, "GET", "/api/v1/accounts/"+a.ID, keyA, nil), 200)
	wantError(t, call(t, s, "GET", "/api/v1/accounts/"+b.ID, keyA, nil), 404, apierr.NotFound)
	wantError(t, call(t, s, "GET", "/api/v1/accounts/"+b.ID+"/audit", keyA, nil), 404, apierr.NotFound)
	wantError(t, call(t, s, "GET", "/api/v1/accounts", keyA, nil), 403, apierr.Forbidden)
	wantError(t, call(t, s, "POST", "/api/v1/accounts", keyA, map[string]any{"name": "x"}), 403, apierr.Forbidden)
	wantError(t, call(t, s, "PATCH", "/api/v1/accounts/"+a.ID, keyA, map[string]any{"quota_bytes": 0}), 403, apierr.Forbidden)
	wantError(t, call(t, s, "POST", "/api/v1/accounts/"+a.ID+"/keys", keyA, map[string]any{"name": "x"}), 403, apierr.Forbidden)
	wantError(t, call(t, s, "DELETE", "/api/v1/accounts/"+a.ID, keyA, nil), 403, apierr.Forbidden)

	// Revoking a key ends it at once.
	rec := call(t, s, "GET", "/api/v1/accounts/"+a.ID+"/keys", admin, nil)
	keys := decode[listResponse[keyJSON]](t, rec).Items
	wantStatus(t, call(t, s, "DELETE", "/api/v1/accounts/"+a.ID+"/keys/"+keys[0].ID, admin, nil), 204)
	wantError(t, call(t, s, "GET", "/api/v1/accounts/"+a.ID, keyA, nil), 401, apierr.Unauthenticated)
	wantError(t, call(t, s, "DELETE", "/api/v1/accounts/"+b.ID+"/keys/"+keys[0].ID, admin, nil), 404, apierr.NotFound)
}

func TestListAccounts(t *testing.T) {
	s := newTestServer(t)
	admin := adminKey(t, s)
	for _, n := range []string{"one", "two", "three"} {
		wantStatus(t, call(t, s, "POST", "/api/v1/accounts", admin, map[string]any{"name": n, "external_id": "ext-" + n}), 201)
	}
	rec := call(t, s, "GET", "/api/v1/accounts?limit=2", admin, nil)
	first := decode[listResponse[accountJSON]](t, rec)
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	rec = call(t, s, "GET", "/api/v1/accounts?limit=2&cursor="+first.NextCursor, admin, nil)
	second := decode[listResponse[accountJSON]](t, rec)
	if len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("second page = %+v", second)
	}
	seen := map[string]bool{}
	for _, a := range append(first.Items, second.Items...) {
		seen[a.Name] = true
	}
	if len(seen) != 3 {
		t.Errorf("pages covered %v, want all three accounts once", seen)
	}
	rec = call(t, s, "GET", "/api/v1/accounts?external_id=ext-two", admin, nil)
	if got := decode[listResponse[accountJSON]](t, rec); len(got.Items) != 1 || got.Items[0].Name != "two" {
		t.Errorf("filtered = %+v", got)
	}
	wantError(t, call(t, s, "GET", "/api/v1/accounts?cursor=%21%21", admin, nil), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "GET", "/api/v1/accounts?limit=0", admin, nil), 400, apierr.InvalidRequest)
}

func TestJSONBodies(t *testing.T) {
	s := newTestServer(t)
	admin := adminKey(t, s)
	post := func(contentType, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/accounts", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+admin)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"no content type": post("", `{"name":"x"}`),
		"unknown field":   post("application/json", `{"name":"x","quota":5}`),
		"trailing data":   post("application/json", `{"name":"x"} {}`),
		"empty name":      post("application/json", `{"name":"   "}`),
		"control char":    post("application/json", `{"name":"a\u0007b"}`),
		"negative quota":  post("application/json", `{"name":"x","quota_bytes":-1}`),
		"not json":        post("application/json", `name=x`),
	} {
		if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != apierr.InvalidRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	wantStatus(t, post("application/json; charset=utf-8", `{"name":"  Trimmed  "}`), 201)
}
