// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// decodeB64 and encodeB64 read and write the payload part of a token, so a test can change it.
func decodeB64(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return string(b), err
}

func encodeB64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// makeLink asks for a signed link to a node and returns the answer. body is the JSON request, or
// "" for none.
func makeLink(t *testing.T, s *Server, cred, nodeID, body string) linkJSON {
	t.Helper()
	rec := rawCall(t, s, "POST", "/api/v1/nodes/"+nodeID+"/links", cred, body,
		map[string]string{"Content-Type": "application/json"})
	wantStatus(t, rec, 201)
	return decode[linkJSON](t, rec)
}

// urlPath returns the path and query of an absolute URL, which is what a request names.
func urlPath(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.RequestURI()
}

// fromHost sends a request as if it arrived on host, with optional headers and cookie.
func fromHost(t *testing.T, s *Server, host, method, path string, hdr map[string]string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// tokenOf returns the token part of a link path.
func tokenOf(path string) string { return strings.TrimPrefix(path, "/api/v1/links/") }

// flipFirst changes the first character of the MAC part of a token, so the MAC is different.
func flipFirst(tok string) string {
	p, m, _ := strings.Cut(tok, ".")
	c := byte('A')
	if m[0] == 'A' {
		c = 'B'
	}
	return p + "." + string(c) + m[1:]
}

func TestSignedLinkLifecycle(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	doc := putNode(t, s, key, f.ID, "/doc.txt", alphabet)

	// The default lifetime is five minutes; the answer says when the link ends.
	start := time.Now()
	link := makeLink(t, s, key, doc.ID, "")
	if d := link.ExpiresAt.Sub(start); d < 4*time.Minute+50*time.Second || d > 5*time.Minute+5*time.Second {
		t.Errorf("default lifetime = %v", d)
	}
	if link.Disposition != "attachment" || !strings.HasPrefix(link.URL, "http://localhost:8080/api/v1/links/") {
		t.Fatalf("link = %+v", link)
	}
	path := urlPath(t, link.URL)

	// The link needs no credential. It serves the file with the same headers as a download.
	got := rawCall(t, s, "GET", path, "", "", nil)
	wantStatus(t, got, 200)
	if got.Body.String() != alphabet || got.Header().Get("ETag") != doc.ETag ||
		!strings.HasPrefix(got.Header().Get("Content-Disposition"), "attachment;") ||
		got.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Errorf("link GET: %q %v", got.Body, got.Header())
	}
	// Range works, as video seeking needs; HEAD works too.
	ranged := rawCall(t, s, "GET", path, "", "", map[string]string{"Range": "bytes=2-3"})
	if ranged.Code != 206 || ranged.Body.String() != "cd" || ranged.Header().Get("Content-Range") != "bytes 2-3/26" {
		t.Errorf("link range = %d %q %v", ranged.Code, ranged.Body, ranged.Header())
	}
	head := rawCall(t, s, "HEAD", path, "", "", nil)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "26" {
		t.Errorf("link HEAD = %d %v", head.Code, head.Header())
	}
	wantStatus(t, rawCall(t, s, "GET", path, "", "", map[string]string{"If-None-Match": doc.ETag}), 304)

	// A cookie is not read: a link works with, or without, a session of someone else.
	_, other := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	wantStatus(t, rawSession(t, s, other, "GET", path, "", nil), 200)

	// Creation is audited, with the disposition and the expiry.
	audit := decode[listResponse[auditJSON]](t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/audit", key, nil))
	found := false
	for _, e := range audit.Items {
		if e.Action == "node.link" && e.Target == doc.ID {
			found = true
			if e.Details["disposition"] != "attachment" || e.Details["expires_at"] == nil {
				t.Errorf("audit entry = %+v", e)
			}
		}
	}
	if !found {
		t.Error("link creation is not in the audit log")
	}
}

func TestSignedLinkRequests(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	doc := putNode(t, s, key, f.ID, "/doc.txt", alphabet)
	dir := mkdirs(t, s, key, f.ID, "/d")
	linkURL := "/api/v1/nodes/" + doc.ID + "/links"

	// ttl: positive, and at most links.max_ttl (24 hours by default).
	max := int64(24 * 60 * 60)
	for _, body := range []string{`{"ttl":0}`, `{"ttl":-5}`, `{"ttl":"5m"}`, `{"ttl":1.5}`} {
		wantError(t, rawCall(t, s, "POST", linkURL, key, body, map[string]string{"Content-Type": "application/json"}),
			400, apierr.InvalidRequest)
	}
	over := rawCall(t, s, "POST", linkURL, key, `{"ttl":86401}`, map[string]string{"Content-Type": "application/json"})
	if e := wantError(t, over, 400, apierr.InvalidRequest); e.Details["max_ttl_seconds"] != float64(max) {
		t.Errorf("ttl above the cap: details %v", e.Details)
	}
	wantStatus(t, rawCall(t, s, "POST", linkURL, key, `{"ttl":86400}`, map[string]string{"Content-Type": "application/json"}), 201)
	short := makeLink(t, s, key, doc.ID, `{"ttl":1}`)
	if d := time.Until(short.ExpiresAt); d > 2*time.Second {
		t.Errorf("ttl 1 ends in %v", d)
	}
	// Disposition: attachment or inline, else refused. An unknown field is refused too.
	wantError(t, rawCall(t, s, "POST", linkURL, key, `{"disposition":"download"}`,
		map[string]string{"Content-Type": "application/json"}), 400, apierr.InvalidRequest)
	wantError(t, rawCall(t, s, "POST", linkURL, key, `{"ttl":60,"bogus":1}`,
		map[string]string{"Content-Type": "application/json"}), 400, apierr.InvalidRequest)
	// A folder is not a file: it has no link.
	wantError(t, makeLinkStatus(t, s, key, dir.ID, ""), 400, apierr.InvalidRequest)
	// Nothing to link is not_found.
	wantError(t, makeLinkStatus(t, s, key, "nod_nothere", ""), 404, apierr.NotFound)
	// Without a credential, nothing is made.
	wantError(t, rawCall(t, s, "POST", linkURL, "", "", nil), 401, apierr.Unauthenticated)

	// Every token that is not a valid link is not_found.
	valid := makeLink(t, s, key, doc.ID, "")
	tok := tokenOf(urlPath(t, valid.URL))
	bad := []string{"x", "a.b", "....", "YQ.YQ", strings.Repeat("a", 600), flipFirst(tok),
		tok + "x", strings.Replace(tok, ".", "..", 1)}
	for _, b := range bad {
		wantError(t, rawCall(t, s, "GET", "/api/v1/links/"+b, "", "", nil), 404, apierr.NotFound)
	}
	// A valid token with its payload changed (inline for attachment) is not_found: the MAC covers it.
	p, m, _ := strings.Cut(tok, ".")
	payload, _ := decodeB64(p)
	swapped := strings.Replace(payload, "|attachment|", "|inline|", 1)
	wantError(t, rawCall(t, s, "GET", "/api/v1/links/"+encodeB64(swapped)+"."+m, "", "", nil), 404, apierr.NotFound)
}

// makeLinkStatus is makeLink's request without its status check, for error answers.
func makeLinkStatus(t *testing.T, s *Server, cred, nodeID, body string) *httptest.ResponseRecorder {
	t.Helper()
	return rawCall(t, s, "POST", "/api/v1/nodes/"+nodeID+"/links", cred, body,
		map[string]string{"Content-Type": "application/json"})
}

func TestSignedLinkExpiryAndChanges(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	doc := putNode(t, s, key, f.ID, "/doc.txt", "v1")
	other := putNode(t, s, key, f.ID, "/other.txt", "o")

	// A link past its time is link_expired. The link is signed with a time in the past, as a
	// genuine link would be once its time has run out.
	past := linkClaims{nod: doc.ID, acc: acc.ID, disp: dispAttachment, exp: time.Now().Add(-time.Minute).Unix()}
	expired := "/api/v1/links/" + s.links.sign(past, doc.ETag)
	wantError(t, rawCall(t, s, "GET", expired, "", "", nil), 410, apierr.LinkExpired)

	// Another node's ID under this node's MAC is not_found.
	live := makeLink(t, s, key, doc.ID, "")
	p, m, _ := strings.Cut(tokenOf(urlPath(t, live.URL)), ".")
	payload, _ := decodeB64(p)
	moved := strings.Replace(payload, doc.ID, other.ID, 1)
	wantError(t, rawCall(t, s, "GET", "/api/v1/links/"+encodeB64(moved)+"."+m, "", "", nil), 404, apierr.NotFound)

	// Changing the contents kills the link: its MAC names the old ETag.
	path := urlPath(t, live.URL)
	wantStatus(t, rawCall(t, s, "GET", path, "", "", nil), 200)
	wantStatus(t, rawCall(t, s, "PUT", "/api/v1/nodes/"+doc.ID+"/content", key, "v2",
		map[string]string{"If-Match": doc.ETag}), 200)
	wantError(t, rawCall(t, s, "GET", path, "", "", nil), 404, apierr.NotFound)
	// A new link to the new contents works.
	fresh := makeLink(t, s, key, doc.ID, "")
	wantStatus(t, rawCall(t, s, "GET", urlPath(t, fresh.URL), "", "", nil), 200)

	// A deleted file is not_found.
	gone := makeLink(t, s, key, other.ID, "")
	wantStatus(t, call(t, s, "DELETE", "/api/v1/nodes/"+other.ID, key, nil), 204)
	wantError(t, rawCall(t, s, "GET", urlPath(t, gone.URL), "", "", nil), 404, apierr.NotFound)
}

func TestSuspendedAccountRefusesContent(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	doc := putNode(t, s, key, f.ID, "/doc.txt", "x")
	link := makeLink(t, s, key, doc.ID, "")
	path := urlPath(t, link.URL)
	ann, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/folders/"+f.ID+"/grants/"+ann.ID, key,
		map[string]any{"level": "read"}), 200)
	wantStatus(t, rawSession(t, s, sess, "GET", "/api/v1/nodes/"+doc.ID+"/content", "", nil), 200)

	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID, admin, map[string]any{"status": "suspended"}), 200)
	// Everywhere: keys, sessions, and the link, which has no credential to refuse.
	wantError(t, rawCall(t, s, "GET", "/api/v1/nodes/"+doc.ID+"/content", key, "", nil), 403, apierr.AccountSuspended)
	wantError(t, rawCall(t, s, "GET", path, "", "", nil), 403, apierr.AccountSuspended)
	wantError(t, rawCall(t, s, "POST", "/api/v1/nodes/"+doc.ID+"/links", key, "", nil), 403, apierr.AccountSuspended)
	wantError(t, rawSession(t, s, sess, "GET", "/api/v1/nodes/"+doc.ID+"/content", "", nil), 403, apierr.AccountSuspended)

	// Reactivated, the same link works again: it was never revoked, only refused.
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID, admin, map[string]any{"status": "active"}), 200)
	wantStatus(t, rawCall(t, s, "GET", path, "", "", nil), 200)
}

func TestMemberNeedsReadToMakeLink(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	doc := putNode(t, s, key, f.ID, "/doc.txt", "x")
	ann, annSess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	linkURL := "/api/v1/nodes/" + doc.ID + "/links"

	// No grant: the folder is not reachable, so the file is not_found, and no link is made.
	wantError(t, callSession(t, s, annSess, "POST", linkURL, map[string]any{}), 404, apierr.NotFound)
	// Read is enough to make a link.
	wantStatus(t, call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/folders/"+f.ID+"/grants/"+ann.ID, key,
		map[string]any{"level": "read"}), 200)
	wantStatus(t, callSession(t, s, annSess, "POST", linkURL, map[string]any{}), 201)
	// Admins are refused, as they are for every file.
	wantError(t, call(t, s, "POST", linkURL, admin, map[string]any{}), 403, apierr.Forbidden)
}

func TestContentOriginRefusesOtherRoutes(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.ContentURL = "https://files.example.test" })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	doc := putNode(t, s, key, f.ID, "/doc.txt", alphabet)
	link := makeLink(t, s, key, doc.ID, "")
	if !strings.HasPrefix(link.URL, "https://files.example.test/api/v1/links/") {
		t.Fatalf("link URL = %q", link.URL)
	}
	// The link is served on the content origin, and needs no cookie there.
	got := fromHost(t, s, "files.example.test", "GET", urlPath(t, link.URL), nil, nil)
	wantStatus(t, got, 200)
	// A HEAD of the link works there too.
	wantStatus(t, fromHost(t, s, "files.example.test", "HEAD", urlPath(t, link.URL), nil, nil), 200)
	// A dot segment that would leave the link path is refused, not cleaned up into the API.
	rec := fromHost(t, s, "files.example.test", "GET", "/api/v1/links/x/../../../me", nil, nil)
	wantError(t, rec, 404, apierr.NotFound)
	// A cookie is not read there, even one that is not a session: a link works with any cookie.
	garbage := &http.Cookie{Name: "selfcloud_session", Value: "not-a-session"}
	wantStatus(t, fromHost(t, s, "files.example.test", "GET", urlPath(t, link.URL), nil, garbage), 200)
	// A default port on the Host header names the same origin.
	wantStatus(t, fromHost(t, s, "files.example.test:443", "GET", urlPath(t, link.URL), nil, nil), 200)
	// Creating a link is not possible on the content origin.
	wantError(t, fromHost(t, s, "files.example.test", "POST", "/api/v1/nodes/"+doc.ID+"/links", map[string]string{
		"Authorization": "Bearer " + key}, nil), 404, apierr.NotFound)
}

func TestLinkKeys(t *testing.T) {
	dir := t.TempDir()
	s := newTestServer(t, func(c *config.Config) { c.DataDir = dir })
	keyDir := filepath.Join(dir, "keys")
	info, err := os.Stat(keyDir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("keys directory: %v %v", info, err)
	}
	cur := filepath.Join(keyDir, "signing.key")
	fi, err := os.Stat(cur)
	if err != nil || fi.Mode().Perm() != 0o600 || fi.Size() != 32 {
		t.Fatalf("signing key: %v %v", fi, err)
	}
	// A second start keeps the same key.
	before, _ := os.ReadFile(cur)
	s2 := newTestServerOn(t, s)
	after, _ := os.ReadFile(cur)
	if string(before) != string(after) || string(s2.links.current) != string(before) {
		t.Error("a restart changed the signing key")
	}
	// A key readable by others is refused, and so is one of the wrong size.
	if err := os.Chmod(cur, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLinkKeys(dir, config.LinksConfig{}); err == nil {
		t.Error("a group-readable key was accepted")
	}
	if err := os.Chmod(cur, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cur, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLinkKeys(dir, config.LinksConfig{}); err == nil {
		t.Error("a short key was accepted")
	}
	// A key named by configuration is used, and must exist.
	given := filepath.Join(dir, "given.key")
	if err := os.WriteFile(given, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := loadLinkKeys(dir, config.LinksConfig{SigningKeyFile: given})
	if err != nil || string(k.current) != string(make([]byte, 32)) {
		t.Errorf("configured key: %v", err)
	}
	if _, err := loadLinkKeys(dir, config.LinksConfig{SigningKeyFile: filepath.Join(dir, "missing.key")}); err == nil {
		t.Error("a missing configured key was accepted")
	}
}

func TestLinkKeyRotation(t *testing.T) {
	s1 := newAuthServer(t, &mail.Recorder{})
	dir := s1.cfg.DataDir
	admin := adminKey(t, s1)
	acc, key := newAccount(t, s1, admin, "Acme")
	f := newFolder(t, s1, key, acc.ID, "Docs")
	doc := putNode(t, s1, key, f.ID, "/doc.txt", alphabet)
	old := makeLink(t, s1, key, doc.ID, "")
	keyDir := filepath.Join(dir, "keys")
	cur := filepath.Join(keyDir, "signing.key")
	prev := filepath.Join(keyDir, "signing.prev.key")

	// Rotate: the current key becomes the previous one, and a new current key is written.
	if err := os.Rename(cur, prev); err != nil {
		t.Fatal(err)
	}
	fresh := make([]byte, 32)
	fresh[0] = 1
	if err := os.WriteFile(cur, fresh, 0o600); err != nil {
		t.Fatal(err)
	}
	s2 := newTestServerOn(t, s1)
	wantStatus(t, rawCall(t, s2, "GET", urlPath(t, old.URL), "", "", nil), 200)
	newer := makeLink(t, s2, key, doc.ID, "")
	wantStatus(t, rawCall(t, s2, "GET", urlPath(t, newer.URL), "", "", nil), 200)
	// A server that has only the new key does not know links signed with the old one.
	if err := os.Remove(prev); err != nil {
		t.Fatal(err)
	}
	s3 := newTestServerOn(t, s1)
	wantError(t, rawCall(t, s3, "GET", urlPath(t, old.URL), "", "", nil), 404, apierr.NotFound)
	wantStatus(t, rawCall(t, s3, "GET", urlPath(t, newer.URL), "", "", nil), 200)
	// A link signed with a key the server does not hold is not_found.
	stranger := linkClaims{nod: doc.ID, acc: acc.ID, disp: dispAttachment, exp: time.Now().Add(time.Hour).Unix()}
	forged := &linkKeys{current: make([]byte, 32)}
	forged.current[0] = 9
	wantError(t, rawCall(t, s3, "GET", "/api/v1/links/"+forged.sign(stranger, doc.ETag), "", "", nil), 404, apierr.NotFound)
}

// newTestServerOn starts a second server on the same store and configuration, as a restart does.
func newTestServerOn(t *testing.T, first *Server) *Server {
	t.Helper()
	s, err := New(Options{Config: first.cfg, Store: first.store, Mailer: &mail.Recorder{}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// errorHeaders checks the safety headers every JSON error carries.
func errorHeaders(t *testing.T, name string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		rec.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Errorf("%s: error headers = %v", name, rec.Header())
	}
}

func TestErrorsCarrySafetyHeaders(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	doc := putNode(t, s, key, f.ID, "/doc.txt", alphabet)
	link := makeLink(t, s, key, doc.ID, "")
	path := urlPath(t, link.URL)

	// A malformed link.
	bad := rawCall(t, s, "GET", "/api/v1/links/x.y", "", "", nil)
	wantError(t, bad, 404, apierr.NotFound)
	errorHeaders(t, "malformed link", bad)

	// An expired link.
	past := linkClaims{nod: doc.ID, acc: acc.ID, disp: dispAttachment, exp: time.Now().Add(-time.Minute).Unix()}
	expired := rawCall(t, s, "GET", "/api/v1/links/"+s.links.sign(past, doc.ETag), "", "", nil)
	wantError(t, expired, 410, apierr.LinkExpired)
	errorHeaders(t, "expired link", expired)

	// A 416 carries the headers and no validators.
	rng := rawCall(t, s, "GET", path, "", "", map[string]string{"Range": "bytes=900-"})
	wantError(t, rng, 416, apierr.RangeNotSatisfiable)
	errorHeaders(t, "416", rng)
	if rng.Header().Get("ETag") != "" || rng.Header().Get("Last-Modified") != "" {
		t.Errorf("416 carries validators: %v", rng.Header())
	}

	// A 412 carries the headers and no validators.
	pre := rawCall(t, s, "GET", path, "", "", map[string]string{"If-Match": `"stale"`})
	wantError(t, pre, 412, apierr.PreconditionFailed)
	errorHeaders(t, "412", pre)
	if pre.Header().Get("ETag") != "" || pre.Header().Get("Last-Modified") != "" {
		t.Errorf("412 carries validators: %v", pre.Header())
	}

	// A 500, from contents missing on disk, carries no validators either.
	gone := putNode(t, s, key, f.ID, "/gone.txt", "g")
	nodeRow, err := s.store.Node(context.Background(), acc.ID, gone.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.blobs.Remove(acc.ID, nodeRow.BlobID); err != nil {
		t.Fatal(err)
	}
	internal := rawCall(t, s, "GET", "/api/v1/nodes/"+gone.ID+"/content", key, "", nil)
	wantError(t, internal, 500, apierr.Internal)
	errorHeaders(t, "500", internal)
	if internal.Header().Get("ETag") != "" || internal.Header().Get("Last-Modified") != "" {
		t.Errorf("500 carries validators: %v", internal.Header())
	}

	// Success still carries them.
	ok := rawCall(t, s, "GET", path, "", "", nil)
	if ok.Header().Get("ETag") != doc.ETag || ok.Header().Get("Last-Modified") == "" {
		t.Errorf("200 lacks validators: %v", ok.Header())
	}
}

func TestSignedLinkRateLimits(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	doc := putNode(t, s, key, f.ID, "/doc.txt", alphabet)

	if got := s.limits["links.create"].perMinute; got != 120 {
		t.Errorf("links.create default = %d a minute", got)
	}
	if got := s.limits["links.serve"].perMinute; got != 600 {
		t.Errorf("links.serve default = %d a minute", got)
	}

	// Creation is limited per principal: a fourth link in a burst of three is rate_limited, and
	// another principal is not affected.
	s.limits["links.create"] = newLimiter(3)
	for i := 0; i < 3; i++ {
		makeLink(t, s, key, doc.ID, "")
	}
	wantError(t, makeLinkStatus(t, s, key, doc.ID, ""), 429, apierr.RateLimited)
	otherAcc, otherKey := newAccount(t, s, admin, "Other")
	otherFld := newFolder(t, s, otherKey, otherAcc.ID, "Docs")
	otherFile := putNode(t, s, otherKey, otherFld.ID, "/o.txt", "o")
	makeLink(t, s, otherKey, otherFile.ID, "")

	// Serving is limited per client address, and the charge comes before the token is checked, so
	// a flood of guesses is refused without a database read.
	s.limits["links.serve"] = newLimiter(3)
	for i := 0; i < 3; i++ {
		wantError(t, rawCall(t, s, "GET", "/api/v1/links/guess"+string(rune('a'+i)), "", "", nil), 404, apierr.NotFound)
	}
	wantError(t, rawCall(t, s, "GET", "/api/v1/links/guessd", "", "", nil), 429, apierr.RateLimited)
}

func TestKeysDirectoryIsTightened(t *testing.T) {
	dir := t.TempDir()
	keyDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLinkKeys(dir, config.LinksConfig{}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(keyDir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("keys directory mode = %v, %v", fi.Mode().Perm(), err)
	}
}
