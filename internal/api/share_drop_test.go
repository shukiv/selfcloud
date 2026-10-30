// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// dropFixture is the anyone-link fixture with the owner's switch on, so drop links can be made.
type dropFixture struct {
	*anyoneFixture
}

func newDropFixture(t *testing.T) *dropFixture {
	t.Helper()
	return &dropFixture{newAnyoneFixture(t)}
}

// makeDrop makes a drop link on nodeID as the account key, with extra fields merged into the body. It returns the
// link and its secret.
func (fx *dropFixture) makeDrop(t *testing.T, nodeID string, extra map[string]any) (shareLinkJSON, string) {
	t.Helper()
	body := map[string]any{"upload_only": true}
	for k, v := range extra {
		body[k] = v
	}
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+nodeID+"/share-links", fx.key, body)
	wantStatus(t, rec, 201)
	out := decode[shareLinkCreated](t, rec)
	return out.Link, secretOf(t, out.URL)
}

// visitDrop opens a drop link without a session and wants it to work. It returns the visitor's credential, and
// fails if the answer names a node or a link.
func (fx *dropFixture) visitDrop(t *testing.T, secret, password string) string {
	t.Helper()
	tok, rec := fx.tryAnyone(t, secret, password)
	wantStatus(t, rec, 200)
	body := rec.Body.String()
	if !strings.Contains(body, `"upload_only":true`) || strings.Contains(body, `"node"`) || strings.Contains(body, `"link"`) {
		t.Errorf("a drop link's open answer is %s; it must say upload_only and name no node or link", body)
	}
	return tok
}

// dropUpload starts an upload of body named name through a visitor's credential, and sends all of it in one piece.
// It returns the answer of the completing piece.
func dropUpload(t *testing.T, s *Server, tok, name, body string, extra map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	meta := []string{"name", name}
	for k, v := range extra {
		meta = append(meta, k, v)
	}
	upl := uploadID(t, createUpload(t, s, tok, int64(len(body)), md(meta...)))
	return piece(t, s, tok, upl, 0, body, nil)
}

// childNames lists the names in a directory, as the account key sees them.
func (fx *dropFixture) childNames(t *testing.T, nodeID string) []string {
	t.Helper()
	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+nodeID+"/children", fx.key, nil)
	wantStatus(t, rec, 200)
	var out []string
	for _, n := range decode[listResponse[nodeJSON]](t, rec).Items {
		out = append(out, n.Name)
	}
	return out
}

// dbExec runs a statement against the test database, through the link fixture's helper.
func (fx *dropFixture) dbExec(t *testing.T, query string, args ...any) {
	t.Helper()
	fx.execDB(t, query, args...)
}

// reservedBytes is the account's declared-but-unwritten upload bytes, as the usage route reports them.
func (fx *dropFixture) reservedBytes(t *testing.T) int64 {
	t.Helper()
	rec := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/usage", fx.key, nil)
	wantStatus(t, rec, 200)
	return decode[usageJSON](t, rec).ReservedBytes
}

func TestDropLinkMakingRules(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	link, _ := fx.makeDrop(t, fx.b.ID, map[string]any{"title": "Send us files", "message": "Line one\nline two",
		"ask_name": true, "notify": true})
	if link.Audience != store.AudienceAnyone || link.Role != "" || !link.UploadOnly || link.Title != "Send us files" ||
		link.Message != "Line one line two" || !link.AskName {
		t.Errorf("the drop link is %+v; want anyone, no role, title kept, message on one line", link)
	}
	if link.ExpiresAt == nil || time.Until(*link.ExpiresAt) < 29*24*time.Hour || time.Until(*link.ExpiresAt) > 31*24*time.Hour {
		t.Errorf("the default end is %v; want 30 days from now", link.ExpiresAt)
	}
	// One drop link per node, beside its access link.
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", fx.key, map[string]any{"upload_only": true})
	if e := wantError(t, rec, 409, apierr.Conflict); e.Details["reason"] != "exists" {
		t.Errorf("a second drop link: reason %v", e.Details["reason"])
	}
	fx.makeAnyone(t, fx.b.ID, nil)
	fx.makeLink(t, fx.root, "viewer")

	// Directories only.
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/share-links", fx.key,
		map[string]any{"upload_only": true}), 400, apierr.InvalidRequest)
	// An end is required: null is refused, and past the 365-day cap is refused.
	rawPost := func(body string) *httptest.ResponseRecorder {
		return rawCall(t, fx.s, "POST", "/api/v1/nodes/"+fx.root+"/share-links", fx.key, body,
			map[string]string{"Content-Type": "application/json"})
	}
	wantError(t, rawPost(`{"upload_only":true,"expires_at":null}`), 400, apierr.InvalidRequest)
	far := time.Now().Add(400 * 24 * time.Hour).UTC().Format(time.RFC3339)
	e := wantError(t, rawPost(`{"upload_only":true,"expires_at":"`+far+`"}`), 400, apierr.InvalidRequest)
	if e.Details["max_expiry_seconds"] != float64(365*24*3600) {
		t.Errorf("details = %v; want max_expiry_seconds", e.Details)
	}
	// The access fields do not belong to an access link, and a drop link gives no role.
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.root+"/share-links", fx.key,
		map[string]any{"audience": "organisation", "title": "x"}), 400, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.root+"/share-links", fx.key,
		map[string]any{"upload_only": true, "role": "viewer"}), 400, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.root+"/share-links", fx.key,
		map[string]any{"upload_only": true, "audience": "organisation"}), 400, apierr.InvalidRequest)
	// Title and message length.
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.root+"/share-links", fx.key,
		map[string]any{"upload_only": true, "title": strings.Repeat("t", 101)}), 400, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.root+"/share-links", fx.key,
		map[string]any{"upload_only": true, "message": strings.Repeat("m", 1001)}), 400, apierr.InvalidRequest)
}

func TestDropLinkWhoMayMakeIt(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	post := func(sess testSession) *httptest.ResponseRecorder {
		return callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links",
			map[string]any{"upload_only": true})
	}
	// An editor may not make one, even where editors_can_share is on.
	fx.flag(t, fx.b.ID, map[string]any{"editors_can_share": true})
	fx.setPerm(t, fx.b.ID, fx.bob.ID, store.RoleEditor)
	wantError(t, post(fx.bobSess), 403, apierr.Forbidden)
	// A viewer may not.
	fx.setPerm(t, fx.b.ID, fx.bob.ID, store.RoleViewer)
	wantError(t, post(fx.bobSess), 403, apierr.Forbidden)
	// A manager may.
	fx.setPerm(t, fx.b.ID, fx.bob.ID, store.RoleManager)
	wantStatus(t, post(fx.bobSess), 201)
	// A delegated token never manages links.
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, "")
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.a.ID+"/share-links", tok, map[string]any{"upload_only": true})
	wantError(t, rec, 403, apierr.Forbidden)
	// Policy: the account switch, the team space's switch and the members-only rule refuse at creation.
	setPolicy(t, fx.s, fx.key, fx.acc.ID, false)
	e := wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.a.ID+"/share-links", fx.key,
		map[string]any{"upload_only": true}), 403, apierr.Forbidden)
	if e.Details["reason"] != "public_links_off" {
		t.Errorf("reason %v; want public_links_off", e.Details["reason"])
	}
	setPolicy(t, fx.s, fx.key, fx.acc.ID, true)
	fx.settings(t, fx.fld.ID, map[string]any{"allow_links": false})
	e = wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.a.ID+"/share-links", fx.key,
		map[string]any{"upload_only": true}), 403, apierr.Forbidden)
	if e.Details["reason"] != "links_off" {
		t.Errorf("reason %v; want links_off", e.Details["reason"])
	}
	fx.settings(t, fx.fld.ID, map[string]any{"allow_links": true, "members_only": true})
	e2 := wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.a.ID+"/share-links", fx.key,
		map[string]any{"upload_only": true}), 409, apierr.Conflict)
	if e2.Details["reason"] != "members_only" {
		t.Errorf("reason %v; want members_only", e2.Details["reason"])
	}
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.root+"/share-links", fx.key,
		map[string]any{"upload_only": true}), 201)
}

func TestDropCredentialReachesNoNode(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/nodes/" + fx.b.ID},
		{"GET", "/api/v1/nodes/" + fx.b.ID + "/children"},
		{"GET", "/api/v1/nodes/" + fx.deep.ID + "/content"},
		{"HEAD", "/api/v1/nodes/" + fx.b.ID + "/content"},
		{"POST", "/api/v1/nodes/" + fx.b.ID + "/links"},
		{"GET", "/api/v1/nodes/" + fx.b.ID + "/share-links"},
		{"GET", "/api/v1/nodes/" + fx.b.ID + "/permissions"},
	} {
		rec := call(t, fx.s, c.method, c.path, tok, map[string]any{})
		if rec.Code == http.StatusOK || rec.Code == http.StatusCreated || rec.Code == http.StatusNoContent {
			t.Errorf("%s %s answered %d to a drop visitor", c.method, c.path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"reason":"link_route"`) {
			t.Errorf("%s %s: %s; want the route refused as link_route", c.method, c.path, rec.Body.String())
		}
	}
	// A drop credential may upload; an access credential may not.
	wantStatus(t, dropUploadCall(t, fx, tok, "a.txt", "hello"), 204)
	_, access := fx.makeAnyone(t, fx.b.ID, nil)
	accessTok := fx.visitAnyone(t, access, "")
	rec := createUpload(t, fx.s, accessTok, 3, md("name", "x.txt"))
	wantError(t, rec, 403, apierr.Forbidden)
	if !strings.Contains(rec.Body.String(), `"reason":"link_route"`) {
		t.Errorf("an access visitor's upload: %s; want link_route", rec.Body.String())
	}
	// Its info is for a drop credential only.
	wantError(t, call(t, fx.s, "GET", "/api/v1/share/info", accessTok, nil), 403, apierr.Forbidden)
}

// dropUploadCall uploads body as name through a drop visitor, and returns the completing answer.
func dropUploadCall(t *testing.T, fx *dropFixture, tok, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	return dropUpload(t, fx.s, tok, name, body, nil)
}

func TestDropRouteWalkCoversBothKinds(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	placeholder := strings.NewReplacer("{nod}", "x", "{shl}", "x", "{upl}", "x", "{acc}", "x", "{fld}", "x",
		"{usr}", "x", "{grp}", "x", "{key}", "x", "{subject}", "x", "{arq}", "x", "{ver}", "x", "{job}", "x")
	var allowed []string
	for _, rt := range fx.s.routes() {
		if !rt.auth && !rt.link && !rt.drop {
			continue
		}
		key := rt.method + " " + rt.path
		if rt.drop {
			allowed = append(allowed, key)
		}
		rec := call(t, fx.s, rt.method, placeholder.Replace(rt.path), tok, map[string]any{})
		refused := strings.Contains(rec.Body.String(), `"reason":"link_route"`)
		switch {
		case rt.drop && refused:
			t.Errorf("%s is refused a drop visitor, but it is on the drop allowlist", key)
		case !rt.drop && !refused:
			t.Errorf("%s answered %d to a drop visitor, want 403 link_route: %s", key, rec.Code, rec.Body.String())
		}
	}
	slices.Sort(allowed)
	want := []string{"DELETE /api/v1/uploads/{upl}", "GET /api/v1/share/info", "HEAD /api/v1/uploads/{upl}",
		"PATCH /api/v1/uploads/{upl}", "POST /api/v1/share/renew", "POST /api/v1/uploads"}
	if !slices.Equal(allowed, want) {
		t.Errorf("the drop allowlist is\n  %v\nwant\n  %v", allowed, want)
	}
}

func TestDropVisitorOpenAnswerAndInfo(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, map[string]any{"title": "Invoices", "message": "Drop them here",
		"ask_name": true})
	tok := fx.visitDrop(t, secret, "")
	rec := call(t, fx.s, "GET", "/api/v1/share/info", tok, nil)
	wantStatus(t, rec, 200)
	info := decode[dropInfo](t, rec)
	if info.Title != "Invoices" || info.Message != "Drop them here" || !info.AskName || info.MaxFileSize != int64(fx.s.cfg.Upload.MaxFileSize) {
		t.Errorf("info = %+v", info)
	}
	// A dead link answers not_found to the info too.
	fx.execDB(t, "UPDATE share_links SET credentials_at = ? WHERE upload_only = 1", time.Now().Add(time.Second).UnixMilli())
	deadLinkInfo := call(t, fx.s, "GET", "/api/v1/share/info", tok, nil)
	deadVisitor(t, deadLinkInfo)
}

func TestDropUploadsAreTheirVisitorsOwn(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	a := fx.visitDrop(t, secret, "")
	b := fx.visitDrop(t, secret, "")
	upl := uploadID(t, createUpload(t, fx.s, a, 10, md("name", "mine.bin")))
	wantStatus(t, piece(t, fx.s, a, upl, 0, "12345", nil), 204)
	deadVisitor(t, headUpload(t, fx.s, b, upl))
	deadVisitor(t, piece(t, fx.s, b, upl, 5, "67890", nil))
	deadVisitor(t, upCall(t, fx.s, "DELETE", "/api/v1/uploads/"+upl, b, nil, nil, 0))
	// The owner still has its upload, and it is untouched.
	if h := headUpload(t, fx.s, a, upl); h.Header().Get("Upload-Offset") != "5" {
		t.Errorf("the owner's offset is %q; want 5", h.Header().Get("Upload-Offset"))
	}
	wantStatus(t, piece(t, fx.s, a, upl, 5, "67890", nil), 204)
}

func TestDropUploadsLandOnlyInTheLinkDirectory(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	// A parent elsewhere and a folder the visitor names are ignored: the file lands in the link's directory.
	wantStatus(t, dropUpload(t, fx.s, tok, "here.txt", "one", map[string]string{"parent": fx.a.ID,
		"folder": "fld_0000000000000000000000000", "on_conflict": "fail"}), 204)
	// A name already in the directory is renamed, and the visitor is not told.
	wantStatus(t, dropUpload(t, fx.s, tok, "file.txt", "two", nil), 204)
	// The client's mime type is ignored; the file's name decides it.
	wantStatus(t, dropUpload(t, fx.s, tok, "note.txt", "three", map[string]string{"mime_type": "text/html"}), 204)
	names := fx.childNames(t, fx.b.ID)
	for _, want := range []string{"here.txt", "file.txt", "file (1).txt", "note.txt"} {
		if !slices.Contains(names, want) {
			t.Errorf("the directory holds %v; want %s", names, want)
		}
	}
	if slices.Contains(fx.childNames(t, fx.a.ID), "here.txt") {
		t.Error("an upload named another parent landed in it")
	}
	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.b.ID+"/children", fx.key, nil)
	for _, n := range decode[listResponse[nodeJSON]](t, rec).Items {
		if n.Name == "note.txt" && !strings.HasPrefix(n.MimeType, "text/plain") {
			t.Errorf("note.txt has mime %q; the client's text/html must be ignored", n.MimeType)
		}
	}
	// Paths and parents are refused.
	wantError(t, createUpload(t, fx.s, tok, 1, md("name", "p.txt", "path", "/x")), 400, apierr.InvalidRequest)
	wantError(t, createUpload(t, fx.s, tok, 1, md("name", "p.txt", "parents", "true")), 400, apierr.InvalidRequest)
}

func TestDropLimits(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	if fx.s.limits["drop.ip"].burst != 30 || fx.s.limits["drop.link"].burst != 300 {
		t.Errorf("the drop limits are %d an hour per address and %d per link; want 30 and 300",
			fx.s.limits["drop.ip"].burst, fx.s.limits["drop.link"].burst)
	}
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	// Twenty open uploads per link, across its visitors; the twenty-first is refused.
	other := fx.visitDrop(t, secret, "")
	for i := range 10 {
		uploadID(t, createUpload(t, fx.s, tok, 5, md("name", "f"+strconv.Itoa(i))))
		uploadID(t, createUpload(t, fx.s, other, 5, md("name", "g"+strconv.Itoa(i))))
	}
	e := wantError(t, createUpload(t, fx.s, tok, 5, md("name", "last")), 429, apierr.TooManyUploads)
	if e.Details["scope"] != "link" || e.Details["limit"] != float64(20) {
		t.Errorf("details = %v; want scope link, limit 20", e.Details)
	}
}

func TestDropRateLimitsPerAddressAndPerLink(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	// The defaults: 30 an hour per address, 300 an hour per link.
	if fx.s.limits["drop.ip"].burst != 30 || fx.s.limits["drop.link"].burst != 300 {
		t.Errorf("the drop limits are %d an hour per address and %d per link; want 30 and 300",
			fx.s.limits["drop.ip"].burst, fx.s.limits["drop.link"].burst)
	}
	// Per address: the hourly budget of uploads.
	fx.s.limits["drop.ip"] = newLimiterRate(2, time.Hour)
	for i := range 2 {
		uploadID(t, createUpload(t, fx.s, tok, 5, md("name", "a"+strconv.Itoa(i))))
	}
	wantError(t, createUpload(t, fx.s, tok, 5, md("name", "z")), 429, apierr.RateLimited)
	// Per link: its own budget, whatever the address.
	fx.s.limits["drop.ip"] = newLimiterRate(30, time.Hour)
	fx.s.limits["drop.link"] = newLimiterRate(1, time.Hour)
	fresh := fx.visitDrop(t, secret, "")
	uploadID(t, createUpload(t, fx.s, fresh, 5, md("name", "y")))
	wantError(t, createUpload(t, fx.s, fresh, 5, md("name", "y2")), 429, apierr.RateLimited)
}

func TestDropUploadSizeIsTheAccountsLimit(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	fx.s.cfg.Upload.MaxFileSize = 16
	wantError(t, createUpload(t, fx.s, tok, 17, md("name", "big")), 413, apierr.TooLarge)
	wantStatus(t, dropUploadCall(t, fx, tok, "fits.txt", "0123456789abcdef"), 204)
}

func TestDropMidUploadEndsWhenItsLinkDies(t *testing.T) {
	t.Parallel()
	// gone says whether the link's end takes the upload with it. A paused or trashed link keeps it (item 3).
	type ending struct {
		gone bool
		kill func(fx *dropFixture, secret string)
	}
	cases := map[string]ending{
		"deleted": {true, func(fx *dropFixture, secret string) {
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/share-links/"+fx.dropID(t), fx.key, nil), 204)
		}},
		"reset": {true, func(fx *dropFixture, secret string) {
			wantStatus(t, call(t, fx.s, "POST", "/api/v1/share-links/"+fx.dropID(t)+"/reset", fx.key, nil), 201)
		}},
		"expired": {true, func(fx *dropFixture, secret string) {
			fx.setExpiry(t, fx.dropID(t), time.Now().Add(-time.Hour))
		}},
		"paused": {false, func(fx *dropFixture, secret string) {
			setPolicy(t, fx.s, fx.key, fx.acc.ID, false)
		}},
		"trashed": {false, func(fx *dropFixture, secret string) {
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID, fx.key, nil), 204)
		}},
	}
	for name, c := range cases {
		kill := c.kill
		t.Run(name, func(t *testing.T) {
			fx := newDropFixture(t)
			_, secret := fx.makeDrop(t, fx.b.ID, nil)
			tok := fx.visitDrop(t, secret, "")
			upl := uploadID(t, createUpload(t, fx.s, tok, 10, md("name", "cut.bin")))
			wantStatus(t, piece(t, fx.s, tok, upl, 0, "12345", nil), 204)
			if fx.reservedBytes(t) != 5 {
				t.Fatalf("the upload reserves %d bytes; want the 5 it received", fx.reservedBytes(t))
			}
			kill(fx, secret)
			// The next piece, the one that completes the file, is refused, and the upload is gone with its reservation.
			deadVisitor(t, piece(t, fx.s, tok, upl, 5, "67890", nil))
			_, err := fx.s.store.Upload(context.Background(), fx.acc.ID, upl)
			if !c.gone {
				if err != nil || fx.reservedBytes(t) != 5 {
					t.Errorf("a paused or trashed link ended its upload (err %v, reserved %d); want it kept", err, fx.reservedBytes(t))
				}
				return
			}
			if got := fx.reservedBytes(t); got != 0 {
				t.Errorf("after the refusal %d bytes stay reserved; want 0", got)
			}
			if err == nil {
				t.Error("the upload survived its link")
			}
			deadVisitor(t, headUpload(t, fx.s, tok, upl))
			// The file was not completed: nothing named cut.bin exists in the directory, if the directory is still reachable.
			if rec := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.b.ID+"/children", fx.key, nil); rec.Code == http.StatusOK {
				for _, n := range decode[listResponse[nodeJSON]](t, rec).Items {
					if n.Name == "cut.bin" {
						t.Error("a completed file landed after its link died")
					}
				}
			}
		})
	}
}

// dropID is the drop link on the fixture's directory, read from the store.
func (fx *dropFixture) dropID(t *testing.T) string {
	t.Helper()
	return fx.queryString(t, "SELECT id FROM share_links WHERE upload_only = 1 AND node_id = ?", fx.b.ID)
}

func TestDropUploaderFields(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, map[string]any{"ask_name": true})
	tok := fx.visitDrop(t, secret, "")
	longName := strings.Repeat("n", 101)
	longEmail := strings.Repeat("e", 250) + "@x.io"
	for _, meta := range []string{
		md("name", "f.txt", "uploader_email", "ann@example.com"),
		md("name", "f.txt", "uploader_name", "Ann"),
		md("name", "f.txt", "uploader_name", "Ann", "uploader_email", "not an address"),
		md("name", "f.txt", "uploader_name", longName, "uploader_email", "ann@example.com"),
		md("name", "f.txt", "uploader_name", "Ann", "uploader_email", longEmail),
	} {
		wantError(t, createUpload(t, fx.s, tok, 1, meta), 400, apierr.InvalidRequest)
	}
	// Line breaks are not kept.
	rec := dropUpload(t, fx.s, tok, "ok.txt", "x", map[string]string{"uploader_name": "Ann\r\nLevi",
		"uploader_email": "ann@example.com"})
	wantStatus(t, rec, 204)
	audit := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil)
	body := audit.Body.String()
	if !strings.Contains(body, `"uploader_name":"Ann Levi"`) || !strings.Contains(body, `"via":"share_link"`) {
		t.Errorf("the audit does not carry the uploader and its via: %s", body)
	}
	// The link's own entries never carry the uploader's email or the secret.
	for _, e := range decode[listResponse[auditJSON]](t, audit).Items {
		if strings.HasPrefix(e.Action, "link.") && (strings.Contains(e.Target, "@") || strings.Contains(strings.Join(anyStrings(e.Details), " "), "ann@example.com")) {
			t.Errorf("a link entry names the uploader: %+v", e)
		}
	}
}

func anyStrings(m map[string]any) []string {
	var out []string
	for _, v := range m {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestDropPasswordFlow(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, map[string]any{"password": "drop-password"})
	if _, rec := fx.tryAnyone(t, secret, ""); rec.Code != http.StatusUnauthorized ||
		!strings.Contains(rec.Body.String(), "password_required") {
		t.Errorf("no password: %d %s", rec.Code, rec.Body.String())
	}
	if _, rec := fx.tryAnyone(t, secret, "wrong-password"); !strings.Contains(rec.Body.String(), "password_wrong") {
		t.Errorf("wrong password: %s", rec.Body.String())
	}
	tok := fx.visitDrop(t, secret, "drop-password")
	wantStatus(t, dropUploadCall(t, fx, tok, "p.txt", "x"), 204)
}

// dropNotifyFixture makes a manager of the directory, who makes a drop link that notifies.
func dropNotifyFixture(t *testing.T) (*dropFixture, string, testSession) {
	t.Helper()
	fx := newDropFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, store.RoleManager)
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links",
		map[string]any{"upload_only": true, "notify": true})
	wantStatus(t, rec, 201)
	return fx, secretOf(t, decode[shareLinkCreated](t, rec).URL), fx.annSess
}

// drops returns the mails about drop links, from the recorder.
func drops(t *testing.T, fx *dropFixture) []mail.Message {
	t.Helper()
	rec, ok := fx.s.mailer.(*mail.Recorder)
	if !ok {
		t.Fatal("the test server does not record mail")
	}
	var out []mail.Message
	for _, m := range rec.Messages() {
		if strings.Contains(m.Body, "/n/") && strings.Contains(m.Subject, "arrived") {
			out = append(out, m)
		}
	}
	return out
}

func TestDropNotificationOncePerHour(t *testing.T) {
	t.Parallel()
	fx, secret, _ := dropNotifyFixture(t)
	ctx := context.Background()
	tok := fx.visitDrop(t, secret, "")
	wantStatus(t, dropUploadCall(t, fx, tok, "one.txt", "1"), 204)
	now := time.Now()
	fx.s.sendDropNotices(ctx, now)
	if got := drops(t, fx); len(got) != 1 || !strings.Contains(got[0].Subject, "A file arrived") {
		t.Fatalf("the first mail: %+v; want one mail for one file", got)
	}
	// Two more files inside the hour: no mail yet.
	wantStatus(t, dropUploadCall(t, fx, tok, "two.txt", "2"), 204)
	wantStatus(t, dropUploadCall(t, fx, tok, "three.txt", "3"), 204)
	fx.s.sendDropNotices(ctx, now.Add(30*time.Minute))
	if got := drops(t, fx); len(got) != 1 {
		t.Fatalf("%d mails inside the hour; want 1", len(got))
	}
	// After the hour, the late files are reported together, once.
	fx.s.sendDropNotices(ctx, now.Add(61*time.Minute))
	fx.s.sendDropNotices(ctx, now.Add(62*time.Minute))
	got := drops(t, fx)
	if len(got) != 2 || !strings.Contains(got[1].Subject, "2 files arrived in") {
		t.Errorf("the mails are %+v; want a second mail for two files", got)
	}
}

func TestDropNotificationOnlyForAManagingUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// A key creator is mailed nothing.
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, map[string]any{"notify": true})
	wantStatus(t, dropUploadCall(t, fx, fx.visitDrop(t, secret, ""), "k.txt", "k"), 204)
	fx.s.sendDropNotices(ctx, time.Now())
	if got := drops(t, fx); len(got) != 0 {
		t.Errorf("a key creator was mailed: %+v", got)
	}
	// A user who has lost the manager role is not mailed, and its files are not held for a later mail.
	fx2, secret2, sess := dropNotifyFixture(t)
	wantStatus(t, dropUploadCall(t, fx2, fx2.visitDrop(t, secret2, ""), "u.txt", "u"), 204)
	fx2.setPerm(t, fx2.b.ID, fx2.ann.ID, store.RoleViewer)
	_ = sess
	fx2.s.sendDropNotices(ctx, time.Now())
	if got := drops(t, fx2); len(got) != 0 {
		t.Errorf("a user who lost manager was mailed: %+v", got)
	}
	if n := fx2.queryString(t, "SELECT unreported_files FROM share_links WHERE upload_only = 1"); n != "0" {
		t.Errorf("unreported_files = %s; want 0 once the creator is no manager", n)
	}
}

func TestDropEditorsAndSwitchOffPauseTheLink(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	setPolicy(t, fx.s, fx.key, fx.acc.ID, false)
	deadVisitor(t, fx.visitorCall(t, secret))
	setPolicy(t, fx.s, fx.key, fx.acc.ID, true)
	tok := fx.visitDrop(t, secret, "")
	wantStatus(t, dropUploadCall(t, fx, tok, "again.txt", "a"), 204)
	// The count of anyone links includes drop links.
	rec := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/sharing-policy", fx.key, nil)
	if n := decode[sharingPolicyJSON](t, rec).AnyoneLinks; n != 1 {
		t.Errorf("anyone_links = %d; want 1 for the drop link", n)
	}
}

// visitorCall opens a drop link's secret and returns the answer, for a test that expects it refused.
func (fx *dropFixture) visitorCall(t *testing.T, secret string) *httptest.ResponseRecorder {
	t.Helper()
	_, rec := fx.tryAnyone(t, secret, "")
	return rec
}

// keep the imports used by the helpers above
var _ = path.Base
var _ = base64.StdEncoding
var _ = auth.KindShareLink

// TestDropVisitorReachesNoNodeInTheAccessWalk checks the access walk itself, not only the route table: a drop
// visitor is not found on any node, even on a route that would admit it.
func TestDropVisitorReachesNoNodeInTheAccessWalk(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	body, _, err := splitShareCredential(tok)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := parseCredentialPayload(body)
	if err != nil {
		t.Fatal(err)
	}
	p := &auth.Principal{Kind: auth.KindShareLink, ID: pl.LinkID, AccountID: pl.AccountID,
		Link: &auth.LinkScope{Node: pl.NodeID, Role: "", UploadOnly: true, VisitorID: pl.VisitorID,
			IssuedAt: time.UnixMilli(pl.IssuedAt)}}
	for _, nod := range []string{fx.b.ID, fx.deep.ID} {
		if _, _, err := nodeAccessWith(context.Background(), fx.s.store, p, nod); apierr.From(err).Code != apierr.NotFound {
			t.Errorf("the access walk gave a drop visitor %s: %v; want not_found", nod, err)
		}
	}
}
