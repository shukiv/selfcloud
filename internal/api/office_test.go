// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/sweep"
	"github.com/shukiv/selfcloud/internal/wopi"
	"github.com/shukiv/selfcloud/internal/wopi/wopitest"
)

// officeFixture is an account with a team folder, a Word file in it, two verified users, and a fake Collabora. The
// office is on, with the fake's discovery; offset moves the discovery cache's clock, so a test can make a refetch due.
type officeFixture struct {
	s       *Server
	col     *wopitest.Collabora
	admin   string
	acc     accountJSON
	key     string
	fld     folderJSON
	doc     nodeJSON // letter.docx in the team folder, holding "hello"
	ann     userResponse
	annSess testSession // ann may be given a role on doc
	bob     userResponse
	bobSess testSession // bob never gets a role on doc
	offset  time.Duration
}

func newOfficeFixture(t *testing.T, mutate ...func(*config.Config)) *officeFixture {
	t.Helper()
	col := wopitest.New(t)
	on := func(c *config.Config) {
		c.Office.Enabled = true
		c.Office.DiscoveryURL = col.DiscoveryURL()
	}
	s := newAuthServer(t, &mail.Recorder{}, append([]func(*config.Config){on}, mutate...)...)
	fx := &officeFixture{s: s, col: col}
	fx.admin = adminKey(t, s)
	fx.acc, fx.key = newAccount(t, s, fx.admin, "Acme")
	fx.fld = newFolder(t, s, fx.key, fx.acc.ID, "Team")
	fx.setDiscovery(col.Discovery())
	fx.doc = putNode(t, s, fx.key, fx.fld.ID, "/letter.docx", "hello")
	fx.ann, fx.annSess = verifiedUser(t, s, fx.key, fx.acc.ID, "ann@example.com")
	fx.bob, fx.bobSess = verifiedUser(t, s, fx.key, fx.acc.ID, "bob@example.com")
	return fx
}

// setDiscovery gives the server a discovery client whose cache clock is the test's offset clock.
func (fx *officeFixture) setDiscovery(d *wopi.Discovery) {
	fx.s.office = d
}

// discovery returns a fresh discovery for the fake, with its cache clock moved by the fixture's offset.
func (fx *officeFixture) discovery() *wopi.Discovery {
	return wopi.NewDiscovery(fx.col.DiscoveryURL(), fx.col.Client(), func() time.Time {
		return time.Now().Add(fx.offset)
	})
}

// grant gives a user a role on a node, as the account key.
func (fx *officeFixture) grant(t *testing.T, nodeID, usrID, role string) {
	t.Helper()
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+nodeID+"/permissions/"+usrID, fx.key,
		map[string]any{"role": role}), 200)
}

// open asks for an editor on a node as a session, and returns the answer.
func (fx *officeFixture) open(t *testing.T, sess testSession, nodeID, mode string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+nodeID+"/office", map[string]any{"mode": mode})
}

// openToken opens the file for ann in mode and returns the token, failing the test if the open is refused.
func (fx *officeFixture) openToken(t *testing.T, mode string) string {
	t.Helper()
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	rec := fx.open(t, fx.annSess, fx.doc.ID, mode)
	wantStatus(t, rec, 200)
	return decode[officeJSON](t, rec).Token
}

// wopiCall sends one WOPI request as Collabora sends it: the token in the query, and the proof headers signed with the
// fake's current key over the URL Selfcloud will rebuild (the configured WOPI base, the path, the query).
func (fx *officeFixture) wopiCall(t *testing.T, method, path, token string, body io.Reader, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, fx.wopiReq(method, path, token, body, hdr))
	return rec
}

// wopiReq builds one signed WOPI request, without sending it.
func (fx *officeFixture) wopiReq(method, path, token string, body io.Reader, hdr map[string]string) *http.Request {
	q := url.Values{"access_token": {token}}.Encode()
	ts := wopitest.Ticks(fx.s.store.Clock())
	req := httptest.NewRequest(method, path+"?"+q, body)
	req.Header.Set(wopi.HeaderTimeStamp, strconv.FormatInt(ts, 10))
	req.Header.Set(wopi.HeaderProof, fx.col.Sign(token, fx.s.wopiBase()+path+"?"+q, ts))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req
}

// nodeRoute is the WOPI path of a node.
func nodeRoute(nodeID string) string { return "/wopi/files/" + nodeID }

// lockFor takes the file's lock with lockID for the token's editor, as Collabora does before a save, and asks that
// it is granted.
func (fx *officeFixture) lockFor(t *testing.T, token, lockID string) {
	t.Helper()
	fx.lockNodeFor(t, fx.doc.ID, token, lockID)
}

// lockNodeFor is lockFor for the file nodeID.
func (fx *officeFixture) lockNodeFor(t *testing.T, nodeID, token, lockID string) {
	t.Helper()
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(nodeID), token, nil,
		map[string]string{"X-WOPI-Override": "LOCK", "X-WOPI-Lock": lockID}), 200)
}

// brace matches a path parameter in a route pattern, such as {acc}.
var brace = regexp.MustCompile(`\{[^}]+\}`)

func TestOfficeOpenAnswersAnEditorURLAndAToken(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	rec := fx.open(t, fx.annSess, fx.doc.ID, "edit")
	wantStatus(t, rec, 200)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	out := decode[officeJSON](t, rec)
	if !strings.HasPrefix(out.Token, "sct_") || strings.Contains(out.URL, out.Token) {
		t.Errorf("the token must be returned once, apart from the editor URL")
	}
	wantSrc := "WOPISrc=" + url.QueryEscape(fx.s.wopiFileURL(fx.doc.ID))
	if !strings.Contains(out.URL, wantSrc) || strings.ContainsAny(out.URL, "<>") {
		t.Errorf("editor URL = %q, want WOPISrc set to the file's WOPI address", out.URL)
	}
	if !strings.HasPrefix(out.URL, fx.col.Origin()) {
		t.Errorf("editor URL %q is not on the discovery host", out.URL)
	}
	if !out.ExpiresAt.After(time.Now()) {
		t.Errorf("expires_at %v is not in the future", out.ExpiresAt)
	}
}

func TestOfficeOpenRules(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tool := putNode(t, fx.s, fx.key, fx.fld.ID, "/tool.exe", "x")
	foreign := putNode(t, fx.s, fx.key, fx.fld.ID, "/foreign.odt", "x")
	dir := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/folder")
	fx.grant(t, fx.doc.ID, fx.ann.ID, "viewer")
	for _, n := range []nodeJSON{tool, foreign, dir} {
		fx.grant(t, n.ID, fx.ann.ID, "viewer")
	}

	cases := []struct {
		name   string
		status int
		code   apierr.Code // "" when the status alone is checked
		do     func() *httptest.ResponseRecorder
	}{
		{"a viewer may not edit", 403, apierr.Forbidden, func() *httptest.ResponseRecorder {
			return fx.open(t, fx.annSess, fx.doc.ID, "edit")
		}},
		{"a viewer may view", 200, "", func() *httptest.ResponseRecorder {
			return fx.open(t, fx.annSess, fx.doc.ID, "view")
		}},
		{"a person with no role is not told the file exists", 404, apierr.NotFound, func() *httptest.ResponseRecorder {
			return fx.open(t, fx.bobSess, fx.doc.ID, "view")
		}},
		{"an account key is not a person", 403, apierr.Forbidden, func() *httptest.ResponseRecorder {
			return call(t, fx.s, "POST", "/api/v1/nodes/"+fx.doc.ID+"/office", fx.key, map[string]any{"mode": "view"})
		}},
		{"a folder is not a document", 400, apierr.InvalidRequest, func() *httptest.ResponseRecorder {
			return fx.open(t, fx.annSess, dir.ID, "view")
		}},
		{"a type Collabora does not open is refused", 400, apierr.InvalidRequest, func() *httptest.ResponseRecorder {
			return fx.open(t, fx.annSess, tool.ID, "view")
		}},
		{"a type whose action is on another host is refused", 400, apierr.InvalidRequest, func() *httptest.ResponseRecorder {
			return fx.open(t, fx.annSess, foreign.ID, "view")
		}},
		{"the mode must be edit or view", 400, apierr.InvalidRequest, func() *httptest.ResponseRecorder {
			return fx.open(t, fx.annSess, fx.doc.ID, "print")
		}},
	}
	for _, tc := range cases {
		rec := tc.do()
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d: %s", tc.name, rec.Code, tc.status, rec.Body.String())
			continue
		}
		if tc.code != "" {
			wantError(t, rec, tc.status, tc.code)
		}
	}
}

// TestOfficeNoDownloadBindsViewersOnly holds the no-download rule to the one the API applies to downloads (restrict.go):
// it binds a viewer, and not an editor. A viewer opens the file read-only with print, export and copy disabled; an
// editor is unaffected and may save.
func TestOfficeNoDownloadBindsViewersOnly(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	fx.grant(t, fx.doc.ID, fx.bob.ID, "viewer")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.doc.ID, fx.key, map[string]any{"no_download": true}), 200)

	// The viewer.
	wantError(t, fx.open(t, fx.bobSess, fx.doc.ID, "edit"), 403, "forbidden")
	viewRec := fx.open(t, fx.bobSess, fx.doc.ID, "view")
	wantStatus(t, viewRec, 200)
	viewTok := decode[officeJSON](t, viewRec).Token
	info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), viewTok, nil, nil))
	if info["UserCanWrite"] != false || info["ReadOnly"] != true {
		t.Errorf("viewer of a no-download file: UserCanWrite = %v, ReadOnly = %v; want false, true",
			info["UserCanWrite"], info["ReadOnly"])
	}
	for _, k := range []string{"DisablePrint", "DisableExport", "DisableCopy"} {
		if info[k] != true {
			t.Errorf("viewer of a no-download file: %s = %v, want true", k, info[k])
		}
	}
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID)+"/contents", viewTok, nil, nil), 200)
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID)+"/contents", viewTok, strings.NewReader("x"),
		map[string]string{"X-WOPI-Override": "PUT"}), 404)

	// The editor is unaffected: edit opens, no Disable property is set, and a save goes through.
	editRec := fx.open(t, fx.annSess, fx.doc.ID, "edit")
	wantStatus(t, editRec, 200)
	editTok := decode[officeJSON](t, editRec).Token
	info = decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), editTok, nil, nil))
	if info["UserCanWrite"] != true || info["DisablePrint"] != false || info["DisableCopy"] != false {
		t.Errorf("editor of a no-download file: UserCanWrite = %v, DisablePrint = %v; want true, false",
			info["UserCanWrite"], info["DisablePrint"])
	}
	fx.lockFor(t, editTok, "lock-nodl")
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID)+"/contents", editTok, strings.NewReader("saved"),
		map[string]string{"X-WOPI-Override": "PUT", "X-WOPI-Lock": "lock-nodl"}), 200)
}

func TestOfficeOpenRefusesALargeFile(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t, func(c *config.Config) { c.Office.MaxFileBytes = 4 })
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	wantError(t, fx.open(t, fx.annSess, fx.doc.ID, "edit"), 413, "too_large")
}

func TestOfficeIsNotFoundWhileOff(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Team")
	doc := putNode(t, s, key, fld.ID, "/letter.docx", "hello")
	ann, annSess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/nodes/"+doc.ID+"/permissions/"+ann.ID, key,
		map[string]any{"role": "editor"}), 200)
	wantError(t, callSession(t, s, annSess, "POST", "/api/v1/nodes/"+doc.ID+"/office",
		map[string]any{"mode": "edit"}), 404, "not_found")
	for _, p := range []string{nodeRoute(doc.ID), nodeRoute(doc.ID) + "/contents"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", p+"?access_token=sct_x.y", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s while off = %d, want 404", p, rec.Code)
		}
	}
}

func TestOfficeDiscoveryDownAnswersUnavailable(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	fx.col.FailDiscovery(http.StatusInternalServerError)
	rec := fx.open(t, fx.annSess, fx.doc.ID, "edit")
	wantError(t, rec, 503, "unavailable")
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want 60", got)
	}
}

func TestOfficeTokenNeverOutlivesTheDelegatedToken(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID},
		"ttl": 60, "permissions": "write"})
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.doc.ID+"/office", tok, map[string]any{"mode": "edit"})
	wantStatus(t, rec, 200)
	out := decode[officeJSON](t, rec)
	if limit := time.Now().Add(61 * time.Second); out.ExpiresAt.After(limit) {
		t.Errorf("office token ends at %v, after the delegated token's 60 seconds", out.ExpiresAt)
	}
}

func TestWOPIProofRefusals(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	var logs bytes.Buffer
	fx.s.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	path := nodeRoute(fx.doc.ID)
	q := url.Values{"access_token": {tok}}.Encode()
	now := time.Now()
	stale := wopitest.Ticks(now.Add(-21 * time.Minute))
	fresh := wopitest.Ticks(now)
	other := wopitest.New(t)

	cases := []struct {
		name string
		hdr  map[string]string
	}{
		{"no proof and no timestamp", map[string]string{}},
		{"a proof signed by another key", map[string]string{
			wopi.HeaderTimeStamp: strconv.FormatInt(fresh, 10),
			wopi.HeaderProof:     wopitest.SignWith(other.Current(), tok, fx.s.wopiBase()+path+"?"+q, fresh)}},
		{"a timestamp older than 20 minutes, correctly signed", map[string]string{
			wopi.HeaderTimeStamp: strconv.FormatInt(stale, 10),
			wopi.HeaderProof:     fx.col.Sign(tok, fx.s.wopiBase()+path+"?"+q, stale)}},
		{"a proof signed over another token", map[string]string{
			wopi.HeaderTimeStamp: strconv.FormatInt(fresh, 10),
			wopi.HeaderProof:     fx.col.Sign("sct_other", fx.s.wopiBase()+path+"?"+q, fresh)}},
		{"a proof signed over a different query", map[string]string{
			wopi.HeaderTimeStamp: strconv.FormatInt(fresh, 10),
			wopi.HeaderProof:     fx.col.Sign(tok, fx.s.wopiBase()+path+"?"+q+"&x=1", fresh)}},
	}
	for _, tc := range cases {
		// The request carries only the headers of the case: wopiCall would sign it, these cases must not be signed.
		req := httptest.NewRequest("GET", path+"?"+q, nil)
		for k, v := range tc.hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		fx.s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status %d, want 500", tc.name, rec.Code)
		}
	}
	if strings.Contains(logs.String(), tok) {
		t.Error("a refused request logged its token")
	}
	if !strings.Contains(logs.String(), "wopi request refused") {
		t.Error("a refused proof is not logged as a warning")
	}
	if strings.Contains(logs.String(), `level=ERROR`) {
		t.Errorf("a forged request was logged as an error:\n%s", logs.String())
	}
}

func TestWOPIKeyRotationIsPickedUpOnceAndForgeriesDoNotRefetch(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.setDiscovery(fx.discovery())
	tok := fx.openToken(t, "edit") // the cache now holds the current key
	fx.col.Rotate(t)
	fx.offset += wopi.RetryGap // so a refetch is due
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil), 200)

	before := fx.col.Hits()
	for range 5 {
		req := httptest.NewRequest("GET", nodeRoute(fx.doc.ID)+"?access_token="+url.QueryEscape(tok), nil)
		req.Header.Set(wopi.HeaderTimeStamp, strconv.FormatInt(wopitest.Ticks(time.Now()), 10))
		req.Header.Set(wopi.HeaderProof, "AAAA")
		rec := httptest.NewRecorder()
		fx.s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("a forged request answered %d, want 500", rec.Code)
		}
	}
	// The legitimate refetch above already started the retry gap, so none of the forged requests may fetch.
	if extra := fx.col.Hits() - before; extra != 0 {
		t.Errorf("five forged requests fetched discovery %d times, want 0 inside RetryGap", extra)
	}
}

func TestWOPINoDiscoveryAnswersServerError(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	fx.setDiscovery(wopi.NewDiscovery(fx.col.DiscoveryURL(), fx.col.Client(), nil))
	fx.col.FailDiscovery(http.StatusServiceUnavailable)
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil), 500)
}

func TestWOPICheckFileInfoAndGetFile(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil))
	checks := map[string]any{
		"BaseFileName": "letter.docx", "UserCanWrite": true, "ReadOnly": false,
		"SupportsLocks": true, "SupportsGetLock": true, "SupportsUpdate": true, "DisablePrint": false,
		"Size": float64(5), "UserId": fx.ann.ID,
	}
	for k, want := range checks {
		if info[k] != want {
			t.Errorf("CheckFileInfo %s = %v, want %v", k, info[k], want)
		}
	}
	if info["Version"] != strings.Trim(fx.doc.ETag, `"`) {
		t.Errorf("Version = %v, want the ETag without quotes", info["Version"])
	}

	rec := fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID)+"/contents", tok, nil, nil)
	wantStatus(t, rec, 200)
	if got := rec.Body.String(); got != "hello" {
		t.Errorf("GetFile body = %q, want hello", got)
	}
	if cl := rec.Header().Get("Content-Length"); cl != "5" {
		t.Errorf("Content-Length = %q, want 5", cl)
	}
	if v := rec.Header().Get("X-WOPI-ItemVersion"); v != strings.Trim(fx.doc.ETag, `"`) {
		t.Errorf("X-WOPI-ItemVersion = %q", v)
	}
}

// zeroReader yields n zero bytes without holding them, so a test can send a body far larger than the memory the test
// allows itself.
type zeroReader struct{ n int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.n <= 0 {
		return 0, io.EOF
	}
	k := min(int64(len(p)), z.n)
	clear(p[:k])
	z.n -= k
	return int(k), nil
}

// countingWriter discards a body and counts it, so a GetFile of a large file does not fill the test's own buffers.
type countingWriter struct {
	h http.Header
	n int64
}

func (c *countingWriter) Header() http.Header {
	if c.h == nil {
		c.h = http.Header{}
	}
	return c.h
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

func (c *countingWriter) WriteHeader(int) {}

// TestWOPIPutAndGetStreamALargeFile sends a 64 MiB file in through PutFile and reads it back through GetFile, and asserts
// that neither side grows the heap by anything near the file's size. The allocation counts are logged. The test is not
// parallel, so no other test's allocations are counted.
func TestWOPIPutAndGetStreamALargeFile(t *testing.T) {
	fx := newOfficeFixture(t, func(c *config.Config) { c.Office.MaxFileBytes = 128 << 20 })
	tok := fx.openToken(t, "edit")
	const size = 64 << 20
	const budget = 16 << 20
	fx.lockFor(t, tok, "lock-large")

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	put := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(put, fx.wopiReq("POST", nodeRoute(fx.doc.ID)+"/contents", tok,
		&zeroReader{n: size}, map[string]string{"X-WOPI-Override": "PUT", "X-WOPI-Lock": "lock-large"}))
	runtime.ReadMemStats(&after)
	putAlloc := after.TotalAlloc - before.TotalAlloc
	t.Logf("PutFile of %d MiB: status %d, TotalAlloc grew by %d KiB", size>>20, put.Code, putAlloc>>10)
	wantStatus(t, put, 200)
	if putAlloc > budget {
		t.Errorf("PutFile allocated %d KiB for a %d MiB body; the budget is %d KiB", putAlloc>>10, size>>20, budget>>10)
	}

	runtime.GC()
	runtime.ReadMemStats(&before)
	get := &countingWriter{}
	fx.s.Handler().ServeHTTP(get, fx.wopiReq("GET", nodeRoute(fx.doc.ID)+"/contents", tok, nil, nil))
	runtime.ReadMemStats(&after)
	getAlloc := after.TotalAlloc - before.TotalAlloc
	t.Logf("GetFile of %d MiB: sent %d bytes, TotalAlloc grew by %d KiB", size>>20, get.n, getAlloc>>10)
	if get.n != size {
		t.Errorf("GetFile sent %d bytes, want %d", get.n, size)
	}
	if getAlloc > budget {
		t.Errorf("GetFile allocated %d KiB for a %d MiB file; the budget is %d KiB", getAlloc>>10, size>>20, budget>>10)
	}
}

func TestWOPIGetFileStreamsALargeFile(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	const size = 3 << 20
	body := strings.Repeat("0123456789abcdef", size/16)
	big := putNode(t, fx.s, fx.key, fx.fld.ID, "/big.docx", body)
	fx.grant(t, big.ID, fx.ann.ID, "viewer")
	rec := fx.open(t, fx.annSess, big.ID, "view")
	wantStatus(t, rec, 200)
	tok := decode[officeJSON](t, rec).Token
	got := fx.wopiCall(t, "GET", nodeRoute(big.ID)+"/contents", tok, nil, nil)
	wantStatus(t, got, 200)
	if !bytes.Equal(got.Body.Bytes(), []byte(body)) {
		t.Errorf("GetFile of a %d-byte file returned %d bytes that differ", size, got.Body.Len())
	}
}

func TestWOPITokenRefusedOnOtherRoutesAndOtherTokensOnWOPI(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")

	// Every route that authenticates a bearer credential refuses the WOPI token.
	for _, rt := range fx.s.routes() {
		if !rt.auth || rt.session {
			continue
		}
		req := httptest.NewRequest(rt.method, brace.ReplaceAllString(rt.path, "x"), nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		fx.s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with a WOPI token: status %d, want 401", rt.method+" "+rt.path, rec.Code)
		}
	}

	// An API token is refused on /wopi.
	api := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}})
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), api, nil, nil), 401)

	// A WOPI token re-signed with the API key is refused by both kinds of route: the MAC key and the audience differ.
	pl := payloadOf(t, tok)
	pl.KeyID = fx.s.tokens.current.kid
	pl.Audience = tokenAudience
	forgedAPI, err := fx.s.tokens.sign(pl)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/nodes/"+fx.doc.ID, nil)
	req.Header.Set("Authorization", "Bearer "+forgedAPI)
	rec := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, 401)
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), forgedAPI, nil, nil), 401)

	// An API token re-signed with the WOPI key is refused on /api by the audience and on /wopi by the key's rules.
	apiPl := payloadOf(t, api)
	apiPl.KeyID = fx.s.wopiTokens.current.kid
	signedWOPI, err := fx.s.wopiTokens.sign(apiPl)
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("GET", "/api/v1/nodes/"+fx.doc.ID, nil)
	req.Header.Set("Authorization", "Bearer "+signedWOPI)
	rec = httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, 401)
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), signedWOPI, nil, nil), 401)
}

func TestWOPIRefusesWhatTheTokenNoLongerReaches(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	other := putNode(t, fx.s, fx.key, fx.fld.ID, "/other.docx", "other")

	// A token for one file, used on another file's path.
	tok := fx.openToken(t, "edit")
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(other.ID), tok, nil, nil), 404)

	// A user's tokens revoked: the session ends at once.
	tok = fx.openToken(t, "edit")
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil), 200)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID+"/tokens/revoke", fx.key, nil), 200)
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil), 401)

	// The account's tokens revoked: every session ends, and each row says so.
	tok = fx.openToken(t, "edit")
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/tokens/revoke", fx.key, nil), 200)
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil), 401)
	sid := payloadOf(t, tok).Session
	sess, err := fx.s.store.WOPISession(t.Context(), fx.acc.ID, sid)
	if err != nil {
		t.Fatal(err)
	}
	if sess.RevokedAt == nil {
		t.Error("the editing session row was not marked revoked by the account's revoke")
	}

	// The person's access removed: the next call finds no role, and the file is not theirs to reach.
	fx2 := newOfficeFixture(t)
	tok = fx2.openToken(t, "edit")
	wantStatus(t, fx2.wopiCall(t, "GET", nodeRoute(fx2.doc.ID), tok, nil, nil), 200)
	wantStatus(t, call(t, fx2.s, "DELETE", "/api/v1/nodes/"+fx2.doc.ID+"/permissions/"+fx2.ann.ID, fx2.key, nil), 204)
	wantStatus(t, fx2.wopiCall(t, "GET", nodeRoute(fx2.doc.ID), tok, nil, nil), 404)

	// The file trashed: it is refused on the next call.
	fx3 := newOfficeFixture(t)
	tok = fx3.openToken(t, "edit")
	wantStatus(t, call(t, fx3.s, "DELETE", "/api/v1/nodes/"+fx3.doc.ID, fx3.key, nil), 204)
	wantStatus(t, fx3.wopiCall(t, "GET", nodeRoute(fx3.doc.ID), tok, nil, nil), 404)
}

func TestWOPIIsolatesAccounts(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	accB, keyB := newAccount(t, fx.s, fx.admin, "Other")
	bUser, bSess := verifiedUser(t, fx.s, keyB, accB.ID, "x@other.example")
	// Account B's person cannot open account A's file, whatever role they hold in their own account.
	wantError(t, fx.open(t, bSess, fx.doc.ID, "view"), 404, "not_found")

	// Account B's own legitimate token, for B's own file, presented on A's file on every WOPI route: each is refused.
	fldB := newFolder(t, fx.s, keyB, accB.ID, "Team")
	bDoc := putNode(t, fx.s, keyB, fldB.ID, "/b.docx", "bee")
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+bDoc.ID+"/permissions/"+bUser.ID, keyB,
		map[string]any{"role": "editor"}), 200)
	bRec := callSession(t, fx.s, bSess, "POST", "/api/v1/nodes/"+bDoc.ID+"/office", map[string]any{"mode": "edit"})
	wantStatus(t, bRec, 200)
	bTok := decode[officeJSON](t, bRec).Token
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(bDoc.ID), bTok, nil, nil), 200)
	aPath := nodeRoute(fx.doc.ID)
	wantStatus(t, fx.wopiCall(t, "GET", aPath, bTok, nil, nil), 404)
	wantStatus(t, fx.wopiCall(t, "GET", aPath+"/contents", bTok, nil, nil), 404)
	wantStatus(t, fx.wopiCall(t, "POST", aPath+"/contents", bTok, strings.NewReader("x"),
		map[string]string{"X-WOPI-Override": "PUT"}), 404)
	wantStatus(t, fx.wopiCall(t, "POST", aPath, bTok, nil, map[string]string{"X-WOPI-Override": "LOCK"}), 404)
	got := fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID)+"/contents", fx.openToken(t, "view"), nil, nil)
	if got.Body.String() != "hello" {
		t.Errorf("account A's file changed through account B's token: %q", got.Body.String())
	}
	// Nor can account B's key reach it through the WOPI host: a token is always for one account and one file.
	tok := fx.openToken(t, "edit")
	pl := payloadOf(t, tok)
	pl.AccountID = accB.ID
	pl.KeyID = fx.s.wopiTokens.current.kid
	forged, err := fx.s.wopiTokens.sign(pl)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), forged, nil, nil), 401)
}

func TestWOPIPutFileReplacesAndVersions(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	fx.lockFor(t, tok, "lock-replace")
	put := func(body string) *httptest.ResponseRecorder {
		return fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID)+"/contents", tok, strings.NewReader(body),
			map[string]string{"X-WOPI-Override": "PUT", "X-WOPI-Lock": "lock-replace"})
	}
	rec := put("first save")
	wantStatus(t, rec, 200)
	if v := rec.Header().Get("X-WOPI-ItemVersion"); v == "" || v == strings.Trim(fx.doc.ETag, `"`) {
		t.Errorf("X-WOPI-ItemVersion = %q; the save must give a new version", v)
	}
	wantStatus(t, put("second save"), 200)

	got := fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID)+"/contents", tok, nil, nil)
	if got.Body.String() != "second save" {
		t.Errorf("content after two saves = %q", got.Body.String())
	}
	versions, err := fx.s.store.Versions(t.Context(), fx.acc.ID, fx.doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Both saves are one editing session under one lock, so the session keeps one version: the content before its
	// first save (decision 32). A PUT still keeps one version per replacement.
	if len(versions) != 1 {
		t.Errorf("two saves under one lock kept %d versions, want 1 (the session's version)", len(versions))
	}
	entries := decode[listResponse[auditJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil))
	writes := 0
	for _, a := range entries.Items {
		if a.Action == "node.write" && a.Target == fx.doc.ID {
			writes++
		}
	}
	if writes != 2 {
		t.Errorf("node.write audit entries for the file = %d, want 2", writes)
	}
}

func TestWOPIPutFileRefusals(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	editTok := fx.openToken(t, "edit")
	put := func(tok, body string, hdr map[string]string) *httptest.ResponseRecorder {
		return fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID)+"/contents", tok, strings.NewReader(body), hdr)
	}

	wantStatus(t, put(editTok, "x", nil), 501) // no X-WOPI-Override: PUT

	// A read-only token is refused as the specification gives for an unauthorized user.
	fx.grant(t, fx.doc.ID, fx.bob.ID, "viewer")
	viewRec := fx.open(t, fx.bobSess, fx.doc.ID, "view")
	wantStatus(t, viewRec, 200)
	viewTok := decode[officeJSON](t, viewRec).Token
	wantStatus(t, put(viewTok, "nope", map[string]string{"X-WOPI-Override": "PUT"}), 404)

	got := fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID)+"/contents", editTok, nil, nil)
	if got.Body.String() != "hello" {
		t.Errorf("a refused save changed the file to %q", got.Body.String())
	}
}

func TestWOPIPutFileRefusesAnOverLimitBody(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t, func(c *config.Config) { c.Office.MaxFileBytes = 4 })
	small := putNode(t, fx.s, fx.key, fx.fld.ID, "/small.docx", "abc")
	fx.grant(t, small.ID, fx.ann.ID, "editor")
	rec := fx.open(t, fx.annSess, small.ID, "edit")
	wantStatus(t, rec, 200)
	tok := decode[officeJSON](t, rec).Token
	fx.lockNodeFor(t, small.ID, tok, "lock-limit")
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(small.ID)+"/contents", tok,
		strings.NewReader("too long for four bytes"), map[string]string{"X-WOPI-Override": "PUT", "X-WOPI-Lock": "lock-limit"}), 413)
	got := fx.wopiCall(t, "GET", nodeRoute(small.ID)+"/contents", tok, nil, nil)
	if got.Body.String() != "abc" {
		t.Errorf("an over-limit save changed the file to %q", got.Body.String())
	}
}

func TestWOPIPutFileRefusesAQuotaTheSaveWouldPass(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	// The account holds the 5 bytes of letter.docx; a 6-byte save would make 11 of the 10 allowed.
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin,
		map[string]any{"quota_bytes": 10}), 200)
	tok := fx.openToken(t, "edit")
	fx.lockFor(t, tok, "lock-quota")
	rec := fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID)+"/contents", tok, strings.NewReader("six by"),
		map[string]string{"X-WOPI-Override": "PUT", "X-WOPI-Lock": "lock-quota"})
	wantStatus(t, rec, 413)
	if g := fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID)+"/contents", tok, nil, nil); g.Body.String() != "hello" {
		t.Errorf("a quota refusal changed the file to %q", g.Body.String())
	}
}

// readFlag is a request body that records whether anything read it.
type readFlag struct {
	r    io.Reader
	read bool
}

func (f *readFlag) Read(p []byte) (int, error) {
	f.read = true
	return f.r.Read(p)
}

// TestWOPIPutRefusesOverQuotaBeforeTheBodyIsRead covers a save whose Content-Length already breaks the account quota:
// it is refused with 413 before the body is read.
func TestWOPIPutRefusesOverQuotaBeforeTheBodyIsRead(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin,
		map[string]any{"quota_bytes": 10}), 200)
	tok := fx.openToken(t, "edit")
	body := &readFlag{r: strings.NewReader("six by")}
	fx.lockFor(t, tok, "lock-quota-pre")
	req := fx.wopiReq("POST", nodeRoute(fx.doc.ID)+"/contents", tok, body,
		map[string]string{"X-WOPI-Override": "PUT", "X-WOPI-Lock": "lock-quota-pre"})
	req.ContentLength = 6
	rec := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, 413)
	if body.read {
		t.Error("the body was read although its length already breaks the quota")
	}
}

// TestWOPIRevocationDuringTheUploadRefusesTheSave covers a token revoked while the body is still arriving. The check
// made again inside the write refuses the save with 401, and the file keeps its content.
func TestWOPIRevocationDuringTheUploadRefusesTheSave(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	revoke := &revokeOnRead{fx: fx, r: strings.NewReader("six by")}
	fx.lockFor(t, tok, "lock-revoke")
	req := fx.wopiReq("POST", nodeRoute(fx.doc.ID)+"/contents", tok, revoke,
		map[string]string{"X-WOPI-Override": "PUT", "X-WOPI-Lock": "lock-revoke"})
	rec := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, 401)
	if !revoke.done {
		t.Fatal("the test did not revoke during the upload")
	}
	if got := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/content", fx.key, nil).Body.String(); got != "hello" {
		t.Errorf("a save refused after revocation changed the file to %q", got)
	}
}

// revokeOnRead ends every editor of the account on its first read, as a sign-out or a revocation made during an
// upload would.
type revokeOnRead struct {
	fx   *officeFixture
	r    io.Reader
	done bool
}

func (v *revokeOnRead) Read(p []byte) (int, error) {
	if !v.done {
		v.done = true
		err := v.fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
			_, err := tx.RevokeWOPISessions(context.Background(), v.fx.acc.ID, "", time.Now())
			return err
		})
		if err != nil {
			return 0, err
		}
	}
	return v.r.Read(p)
}

// TestWOPIUnimplementedOverridesAnswer501: the lock operations are implemented (office_locks_test.go). RenameFile is
// slice l4, and the others are not in the design, so they are checked and answered 501.
func TestWOPIUnimplementedOverridesAnswer501(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	var logs bytes.Buffer
	fx.s.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for _, o := range []string{"RENAME_FILE", "PUT_RELATIVE"} {
		wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, nil,
			map[string]string{"X-WOPI-Override": o}), 501)
	}
	// A 501 is an answer the client caused, not a server failure: the access log must not call it an error.
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("a 501 for a lock request was logged as an error:\n%s", logs.String())
	}
}

func TestWOPISessionEndsAndItsRowIsSwept(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	sid := payloadOf(t, tok).Session
	ends := time.Now().Add(time.Duration(fx.s.cfg.Office.TokenMinutes)*time.Minute + time.Minute)
	if _, err := fx.s.store.WOPISession(t.Context(), fx.acc.ID, sid); err != nil {
		t.Fatalf("the session row is missing right after the open: %v", err)
	}
	sw := sweep.New(sweep.Options{Store: fx.s.store, Blobs: fx.s.blobs, DeleteGrace: time.Hour})
	res, err := sw.Once(t.Context(), ends)
	if err != nil {
		t.Fatal(err)
	}
	if res.WOPISessions != 1 {
		t.Errorf("sweep removed %d editing sessions, want 1", res.WOPISessions)
	}
	if _, err := fx.s.store.WOPISession(t.Context(), fx.acc.ID, sid); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the ended session's row is still there: %v", err)
	}
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil), 401)
}

func TestWOPIAccessLogNeverHasTheToken(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	var logs bytes.Buffer
	fx.s.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil), 200)
	if strings.Contains(logs.String(), tok) {
		t.Errorf("the access log holds the token:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "GET /wopi/files/{nod}") {
		t.Errorf("the access log should name the route pattern, not the path:\n%s", logs.String())
	}
}

// TestOfficeViewFallsBackToTheEditActionWhenThereIsNoView covers a discovery document that lists only an edit action
// for the type. A view is then opened through the edit action, and the token and CheckFileInfo keep it read-only.
func TestOfficeViewFallsBackToTheEditActionWhenThereIsNoView(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.col.OmitViewActions()
	fx.grant(t, fx.doc.ID, fx.ann.ID, "viewer")
	rec := fx.open(t, fx.annSess, fx.doc.ID, "view")
	wantStatus(t, rec, 200)
	out := decode[officeJSON](t, rec)
	if !strings.HasPrefix(out.URL, fx.col.Origin()) {
		t.Errorf("editor URL %q is not on the discovery host", out.URL)
	}
	info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), out.Token, nil, nil))
	if info["UserCanWrite"] != false || info["ReadOnly"] != true {
		t.Errorf("view through the edit action: UserCanWrite = %v, ReadOnly = %v; want false, true",
			info["UserCanWrite"], info["ReadOnly"])
	}
	put := fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID)+"/contents", out.Token, strings.NewReader("changed"),
		map[string]string{"X-WOPI-Override": "PUT"})
	wantStatus(t, put, 404)
	got := fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID)+"/contents", out.Token, nil, nil)
	if got.Body.String() != "hello" {
		t.Errorf("a refused PutFile changed the file to %q", got.Body.String())
	}
}

// TestWOPITokenAlphabetIsURLSafe pins the token form the editor URL relies on: the token travels in the query of
// every WOPI request, so it must survive a query escape unchanged. Payload bytes are base64url-encoded, so reserved
// characters in the payload must not reach the token.
func TestWOPITokenAlphabetIsURLSafe(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	weird := []string{"a+b/c=d?e&f", "~!@#$%^*()", "spaces and %41 escapes", "é漢字"}
	for _, w := range weird {
		pl := tokenPayload{Version: tokenVersion, KeyID: fx.s.wopiTokens.current.kid, AccountID: w, Folders: []string{w},
			Node: w, Permission: permRead, Session: w}
		tok, err := fx.s.wopiTokens.sign(pl)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(tok, auth.TokenPrefix) {
			t.Errorf("token %q lacks the %s prefix", tok, auth.TokenPrefix)
		}
		if esc := url.QueryEscape(tok); esc != tok {
			t.Errorf("token changes under a query escape: %q -> %q", tok, esc)
		}
		for _, c := range tok {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
				t.Fatalf("token has the character %q, outside the URL-safe alphabet", c)
			}
		}
	}
}

// TestWOPICheckFileInfoNamesThePersonNotTheirID covers UserFriendlyName, which Collabora shows to other editors. It is
// the person's display name, or the neutral word User when there is none, and never the user's ID.
func TestWOPICheckFileInfoNamesThePersonNotTheirID(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": "cy@example.com", "display_name": "Cy Rivera", "verification": "link"})
	wantStatus(t, rec, 201)
	cy := decode[userResponse](t, rec)
	rec = call(t, fx.s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": cy.Verification.Token, "password": testPassword})
	wantStatus(t, rec, 200)
	fx.grant(t, fx.doc.ID, cy.ID, "editor")

	named := fx.open(t, sessionFrom(t, fx.s, rec), fx.doc.ID, "edit")
	wantStatus(t, named, 200)
	info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), decode[officeJSON](t, named).Token, nil, nil))
	if info["UserFriendlyName"] != "Cy Rivera" {
		t.Errorf("UserFriendlyName = %v, want the display name Cy Rivera", info["UserFriendlyName"])
	}

	unnamed := fx.openToken(t, "edit")
	info = decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), unnamed, nil, nil))
	if info["UserFriendlyName"] != "User" {
		t.Errorf("UserFriendlyName without a display name = %v, want the neutral word User", info["UserFriendlyName"])
	}
	for _, v := range []any{info["UserFriendlyName"]} {
		if s, _ := v.(string); s == fx.ann.ID || strings.Contains(s, "@") {
			t.Errorf("UserFriendlyName %q is an ID or an email", s)
		}
	}
}

// TestWOPIGetFileHeadersAndMaxExpectedSize covers the GetFile details: nosniff, and X-WOPI-MaxExpectedSize, which
// answers 412 when the file is larger than the value the caller sends.
func TestWOPIGetFileHeadersAndMaxExpectedSize(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	get := func(max string) *httptest.ResponseRecorder {
		hdr := map[string]string{}
		if max != "" {
			hdr["X-WOPI-MaxExpectedSize"] = max
		}
		return fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID)+"/contents", tok, nil, hdr)
	}
	rec := get("")
	wantStatus(t, rec, 200)
	if v := rec.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", v)
	}
	wantStatus(t, get("4"), 412)
	wantStatus(t, get("5"), 200)
	wantStatus(t, get("6"), 200)
	wantStatus(t, get("five"), 400)
}

// TestWOPIEditorEndsWithTheBrowserSessionThatOpenedIt covers each way a browser session ends: sign-out, a password
// change, a reset and a disabled user. The next CheckFileInfo, GetFile and PutFile are each refused.
func TestWOPIEditorEndsWithTheBrowserSessionThatOpenedIt(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T, fx *officeFixture){
		"sign out": func(t *testing.T, fx *officeFixture) {
			wantStatus2xx(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/auth/logout", nil))
		},
		"password change": func(t *testing.T, fx *officeFixture) {
			wantStatus2xx(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/me/password",
				map[string]any{"current_password": testPassword, "new_password": "a new passphrase"}))
		},
		"password reset": func(t *testing.T, fx *officeFixture) {
			reset := decode[verificationJSON](t, call(t, fx.s, "POST",
				"/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID+"/password-reset", fx.key,
				map[string]any{"method": "link"}))
			wantStatus(t, call(t, fx.s, "POST", "/api/v1/auth/password/reset", "",
				map[string]any{"token": reset.Token, "password": "a new passphrase"}), 200)
		},
		"disabled user": func(t *testing.T, fx *officeFixture) {
			wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key,
				map[string]any{"status": "disabled"}), 200)
		},
	}
	for name, end := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fx := newOfficeFixture(t)
			fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
			rec := fx.open(t, fx.annSess, fx.doc.ID, "edit")
			wantStatus(t, rec, 200)
			tok := decode[officeJSON](t, rec).Token
			wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil), 200)
			end(t, fx)
			wantWOPIRefused(t, fx, tok)
		})
	}
}

// TestWOPIEditorIsNotEndedByAnotherBrowsersSignOut is the reading of single sign-out: signing out one browser ends only
// the editor that browser opened. An editor opened from another browser of the same person keeps working.
func TestWOPIEditorIsNotEndedByAnotherBrowsersSignOut(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	tok := fx.openToken(t, "edit")
	other := sessionFrom(t, fx.s, login(t, fx.s, "ann@example.com", testPassword, ""))
	wantStatus2xx(t, callSession(t, fx.s, other, "POST", "/api/v1/auth/logout", nil))
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil), 200)
}

// wantStatus2xx fails the test unless the answer is a success.
func wantStatus2xx(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code < 200 || rec.Code > 299 {
		t.Fatalf("status %d, want a 2xx: %s", rec.Code, rec.Body.String())
	}
}

// wantWOPIRefused checks that CheckFileInfo, GetFile and PutFile each refuse tok with 401, and the file is unchanged.
func wantWOPIRefused(t *testing.T, fx *officeFixture, tok string) {
	t.Helper()
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil), 401)
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID)+"/contents", tok, nil, nil), 401)
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID)+"/contents", tok, strings.NewReader("changed"),
		map[string]string{"X-WOPI-Override": "PUT"}), 401)
	if got := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/content", fx.key, nil).Body.String(); got != "hello" {
		t.Errorf("the file reads %q after a refused save, want hello", got)
	}
}

// decodeInfo reads a CheckFileInfo answer.
func decodeInfo(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	wantStatus(t, rec, 200)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("CheckFileInfo body %q: %v", rec.Body.String(), err)
	}
	return out
}
