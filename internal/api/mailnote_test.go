// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// newFixtureWithMailer is newSharingFixture with a mail sender the test can read.
func newFixtureWithMailer(t *testing.T, rec mail.Sender) *sharingFixture {
	t.Helper()
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fx := &sharingFixture{s: s, acc: acc, key: key, admin: admin}
	fx.fld = newFolder(t, s, key, acc.ID, "Team")
	fx.a = mkdirs(t, s, key, fx.fld.ID, "/a")
	fx.b = mkdirs(t, s, key, fx.fld.ID, "/a/b")
	fx.c = mkdirs(t, s, key, fx.fld.ID, "/a/c")
	fx.deep = putNode(t, s, key, fx.fld.ID, "/a/b/file.txt", "deep")
	fx.sib = putNode(t, s, key, fx.fld.ID, "/a/c/x.txt", "sib")
	fx.ann, fx.annSess = verifiedUser(t, s, key, acc.ID, "ann@example.com")
	fx.bob, fx.bobSess = verifiedUser(t, s, key, acc.ID, "bob@example.com")
	return fx
}

// TestNoteWithControlsIsCleanedAndMailedClean checks that a note carrying a bare CR, an SMTP end-of-data
// sequence and a NUL is stored without them, and mailed without them: a body must go out with CRLF only.
func TestNoteWithControlsIsCleanedAndMailedClean(t *testing.T) {
	t.Parallel()
	bad := "hello\rworld\r\n.\r\nend\x00tail\tok"
	if got := cleanNote(bad); strings.ContainsAny(got, "\r\x00") || !strings.Contains(got, "\tok") {
		t.Fatalf("cleanNote(%q) = %q", bad, got)
	}
	rec := &mail.Recorder{}
	fx := newFixtureWithMailer(t, rec)
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.b.ID+"/permissions/"+fx.ann.ID, fx.key,
		map[string]any{"role": "viewer", "message": bad}), 200)
	msgs := rec.Messages()
	if len(msgs) == 0 {
		t.Fatal("no mail was sent")
	}
	for _, m := range msgs {
		if strings.ContainsAny(m.Body, "\r\x00") {
			t.Errorf("mail body carries a raw CR or NUL: %q", m.Body)
		}
	}
	// The stored form of an access request is cleaned too.
	ann := fx.annSess
	// Ann reaches /a/b through its viewer permission, so the request is recorded.
	wantStatus(t, callSession(t, fx.s, ann, "POST", "/api/v1/nodes/"+fx.b.ID+"/access-requests",
		map[string]any{"role": "editor", "message": bad}), 202)
	reqs, _, err := fx.s.store.NodeAccessRequests(t.Context(), fx.acc.ID, fx.b.ID, store.Page{Limit: 10})
	if err != nil || len(reqs) != 1 {
		t.Fatalf("requests = %v, %v", reqs, err)
	}
	if strings.ContainsAny(reqs[0].Message, "\r\x00") {
		t.Errorf("stored message carries a raw CR or NUL: %q", reqs[0].Message)
	}
}
