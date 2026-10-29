// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// setQuota gives the account a quota, as the installation admin.
func (fx *dropFixture) setQuota(t *testing.T, quota int64) {
	t.Helper()
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin,
		map[string]any{"quota_bytes": quota}), 200)
}

// dropDeclare starts a drop upload that declares length bytes, and returns its ID.
func dropDeclare(t *testing.T, s *Server, tok, name string, length int64) string {
	t.Helper()
	return uploadID(t, createUpload(t, s, tok, length, md("name", name)))
}

// TestDropUploadsReserveAsReceived (item 1, M1): a drop upload holds only the bytes it has received, so twenty
// drop uploads each declaring the largest file hold nothing until bytes arrive, and the owner's upload still fits.
func TestDropUploadsReserveAsReceived(t *testing.T) {
	fx := newDropFixture(t)
	fx.setQuota(t, 100)
	fx.s.cfg.Upload.MaxFileSize = 64
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	for i := range 20 {
		dropDeclare(t, fx.s, tok, "big"+strconv.Itoa(i), 64)
	}
	if got := fx.reservedBytes(t); got != 0 {
		t.Fatalf("twenty drop uploads hold %d bytes before any arrived; want 0", got)
	}
	// The owner's own upload fits beside them.
	own := uploadID(t, createUpload(t, fx.s, fx.key, 50, md("folder", fx.fld.ID, "name", "own.bin")))
	wantStatus(t, piece(t, fx.s, fx.key, own, 0, strings.Repeat("o", 50), nil), 204)
	if got := fx.reservedBytes(t); got != 0 {
		t.Errorf("after the owner's upload completed, %d bytes are reserved; want 0", got)
	}
}

// TestDropRefusalsHideTheAccountsNumbers (item 2, M2): a drop visitor is refused a full quota or a full disk with
// one generic answer, with no numbers and no scope. The owner still gets the detail.
func TestDropRefusalsHideTheAccountsNumbers(t *testing.T) {
	const generic = "this link cannot take the file now"
	fx := newDropFixture(t)
	fx.setQuota(t, 100)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	first := dropDeclare(t, fx.s, tok, "a.bin", 64)
	wantStatus(t, piece(t, fx.s, tok, first, 0, strings.Repeat("a", 60), nil), 204)
	second := dropDeclare(t, fx.s, tok, "b.bin", 64)
	rec := piece(t, fx.s, tok, second, 0, strings.Repeat("b", 50), nil)
	assertGenericRefusal(t, rec, generic)

	// A full disk, too, answers the same way.
	fx.s.freeBytes = func(string) (uint64, error) { return 4, nil }
	third := dropDeclare(t, fx.s, tok, "c.bin", 8)
	assertGenericRefusal(t, piece(t, fx.s, tok, third, 0, "cccccccc", nil), generic)

	// The owner's refusal keeps its numbers and its scope.
	fx.s.freeBytes = func(string) (uint64, error) { return 1 << 40, nil }
	fx.setQuota(t, 10)
	e := wantError(t, createUpload(t, fx.s, fx.key, 50, md("folder", fx.fld.ID, "name", "x.bin")), 507, apierr.QuotaExceeded)
	if e.Details["scope"] != "quota" || e.Details["required_bytes"] == nil {
		t.Errorf("the owner's refusal lost its detail: %v", e.Details)
	}
}

// assertGenericRefusal wants the one answer a drop visitor gets for a full quota or disk.
func assertGenericRefusal(t *testing.T, rec *httptest.ResponseRecorder, message string) {
	t.Helper()
	e := wantError(t, rec, 507, apierr.QuotaExceeded)
	if e.Message != message {
		t.Errorf("message %q; want %q", e.Message, message)
	}
	for _, k := range []string{"scope", "required_bytes", "available_bytes", "limit_bytes"} {
		if _, ok := e.Details[k]; ok {
			t.Errorf("the visitor is told %s: %v", k, e.Details)
		}
	}
}

// TestDropPausedLinkKeepsItsUploads (item 3, M3): a paused link or a trashed directory refuses the visitor's piece
// with not_found but keeps the upload, so the visitor resumes once the link is back. A deleted, reset or expired link
// still ends it (see TestDropMidUploadEndsWhenItsLinkDies).
func TestDropPausedLinkKeepsItsUploads(t *testing.T) {
	cases := map[string]struct {
		off func(t *testing.T, fx *dropFixture)
		on  func(t *testing.T, fx *dropFixture)
	}{
		"public links off": {
			off: func(t *testing.T, fx *dropFixture) { setPolicy(t, fx.s, fx.key, fx.acc.ID, false) },
			on:  func(t *testing.T, fx *dropFixture) { setPolicy(t, fx.s, fx.key, fx.acc.ID, true) },
		},
		"links off in the space": {
			off: func(t *testing.T, fx *dropFixture) { fx.settings(t, fx.fld.ID, map[string]any{"allow_links": false}) },
			on:  func(t *testing.T, fx *dropFixture) { fx.settings(t, fx.fld.ID, map[string]any{"allow_links": true}) },
		},
		"directory trashed": {
			off: func(t *testing.T, fx *dropFixture) {
				wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID, fx.key, nil), 204)
			},
			on: func(t *testing.T, fx *dropFixture) {
				wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/restore", fx.key, nil), 200)
			},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDropFixture(t)
			_, secret := fx.makeDrop(t, fx.b.ID, nil)
			tok := fx.visitDrop(t, secret, "")
			upl := dropDeclare(t, fx.s, tok, "held.bin", 10)
			wantStatus(t, piece(t, fx.s, tok, upl, 0, "12345", nil), 204)
			c.off(t, fx)
			deadVisitor(t, piece(t, fx.s, tok, upl, 5, "67890", nil))
			if _, err := fx.s.store.Upload(context.Background(), fx.acc.ID, upl); err != nil {
				t.Fatalf("the paused upload was ended: %v", err)
			}
			if got := fx.reservedBytes(t); got != 5 {
				t.Errorf("the paused upload holds %d bytes; want 5", got)
			}
			c.on(t, fx)
			wantStatus(t, piece(t, fx.s, tok, upl, 5, "67890", nil), 204)
			if fx.reservedBytes(t) != 0 {
				t.Errorf("the finished upload still reserves bytes")
			}
		})
	}
}

// TestDropFinishesAfterItsDirectoryMoves (item 3, M3): a directory moved to another folder must not destroy a finished
// transfer. The file lands in the directory where it now is.
func TestDropFinishesAfterItsDirectoryMoves(t *testing.T) {
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	upl := dropDeclare(t, fx.s, tok, "moved.bin", 10)
	wantStatus(t, piece(t, fx.s, tok, upl, 0, "12345", nil), 204)

	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	fx.settings(t, other.ID, map[string]any{"allow_links": true})
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/move", fx.key,
		map[string]any{"folder": other.ID}), 200)

	wantStatus(t, piece(t, fx.s, tok, upl, 5, "67890", nil), 204)
	if !strings.Contains(strings.Join(fx.childNames(t, fx.b.ID), ","), "moved.bin") {
		t.Errorf("the finished file is not in the directory after its move: %v", fx.childNames(t, fx.b.ID))
	}
}

// TestDropDeleteAndResetReleaseTheirUploads (item 4, L7): deleting or resetting a drop link releases its open uploads
// at once, with their rows, their files and their reservations, not on the visitor's next request.
func TestDropDeleteAndResetReleaseTheirUploads(t *testing.T) {
	for name, end := range map[string]func(fx *dropFixture) *httptest.ResponseRecorder{
		"deleted": func(fx *dropFixture) *httptest.ResponseRecorder {
			return call(t, fx.s, "DELETE", "/api/v1/share-links/"+fx.dropID(t), fx.key, nil)
		},
		"reset": func(fx *dropFixture) *httptest.ResponseRecorder {
			return call(t, fx.s, "POST", "/api/v1/share-links/"+fx.dropID(t)+"/reset", fx.key, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newDropFixture(t)
			_, secret := fx.makeDrop(t, fx.b.ID, nil)
			tok := fx.visitDrop(t, secret, "")
			upl := dropDeclare(t, fx.s, tok, "open.bin", 10)
			wantStatus(t, piece(t, fx.s, tok, upl, 0, "1234", nil), 204)
			if fx.reservedBytes(t) != 4 {
				t.Fatalf("the open upload holds %d bytes; want 4", fx.reservedBytes(t))
			}
			file := uploadFile(fx.s, fx.acc.ID, upl)
			if _, err := os.Stat(file); err != nil {
				t.Fatalf("the upload file is missing before the link ends: %v", err)
			}
			if rec := end(fx); rec.Code != http.StatusNoContent && rec.Code != http.StatusCreated {
				t.Fatalf("ending the link answered %d", rec.Code)
			}
			if got := fx.reservedBytes(t); got != 0 {
				t.Errorf("%d bytes stay reserved after the link ended; want 0", got)
			}
			if _, err := fx.s.store.Upload(context.Background(), fx.acc.ID, upl); err == nil {
				t.Error("the upload row survived the end of its link")
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Errorf("the upload file survived the end of its link: %v", err)
			}
		})
	}
}

// countingMailer counts the sends it is asked for, and fails them all when fail is set.
type countingMailer struct {
	mu   sync.Mutex
	fail bool
	sent int
}

func (m *countingMailer) Send(ctx context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent++
	if m.fail {
		return mail.ErrFake
	}
	return nil
}

// TestDropFailedMailBacksOff (item 5, L4): a mail that does not go does not make the notifier try again every minute.
// The files stay counted, and the next try is an hour after the failed one.
func TestDropFailedMailBacksOff(t *testing.T) {
	fx, secret, _ := dropNotifyFixture(t)
	m := &countingMailer{fail: true}
	fx.s.mailer = m
	ctx := context.Background()
	wantStatus(t, dropUploadCall(t, fx, fx.visitDrop(t, secret, ""), "one.txt", "1"), 204)
	now := time.Now()
	fx.s.sendDropNotices(ctx, now)
	if m.sent != 1 {
		t.Fatalf("the first run sent %d mails; want 1", m.sent)
	}
	if n := fx.queryString(t, "SELECT unreported_files FROM share_links WHERE upload_only = 1"); n != "1" {
		t.Errorf("after a failed mail, unreported_files = %s; want 1 kept", n)
	}
	fx.s.sendDropNotices(ctx, now.Add(time.Minute))
	fx.s.sendDropNotices(ctx, now.Add(30*time.Minute))
	if m.sent != 1 {
		t.Errorf("the notifier tried again inside the hour: %d sends; want 1", m.sent)
	}
	m.fail = false
	fx.s.sendDropNotices(ctx, now.Add(61*time.Minute))
	if m.sent != 2 {
		t.Errorf("after the hour the notifier sent %d; want 2", m.sent)
	}
}

// TestDropCompletionSendsNoNodeID (item 6, L5): the completing piece of a drop upload does not name the new node to
// the visitor. An owner's completing piece still does.
func TestDropCompletionSendsNoNodeID(t *testing.T) {
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	upl := dropDeclare(t, fx.s, tok, "quiet.txt", 3)
	rec := piece(t, fx.s, tok, upl, 0, "abc", nil)
	wantStatus(t, rec, 204)
	if got := rec.Header().Get(nodeHeader); got != "" {
		t.Errorf("the visitor was sent the new node's ID %q", got)
	}
	ownUpl := uploadID(t, createUpload(t, fx.s, fx.key, 3, md("folder", fx.fld.ID, "name", "mine.txt")))
	orec := piece(t, fx.s, fx.key, ownUpl, 0, "abc", nil)
	wantStatus(t, orec, 204)
	if orec.Header().Get(nodeHeader) == "" {
		t.Error("the owner's completing piece lost the node ID")
	}
}

// TestDropNodeOwnerFollowsTheStore (item 6, L5): the owner the completed node reports is the one the store gives it:
// the folder's user in a personal folder, and the link's creator elsewhere.
func TestDropNodeOwnerFollowsTheStore(t *testing.T) {
	personal := &store.Folder{Kind: store.KindPersonal, OwnerUserID: "usr_personal"}
	team := &store.Folder{Kind: store.KindTeam}
	if got := dropNodeOwner(personal, "usr_creator"); got != "usr_personal" {
		t.Errorf("a personal folder's node is owned by %q; want its user", got)
	}
	if got := dropNodeOwner(team, "usr_creator"); got != "usr_creator" {
		t.Errorf("a team node is owned by %q; want the creator", got)
	}
}

// TestDropUploaderFieldsStripControls (item 7, L6): every control character and the Unicode line and paragraph
// separators are replaced by a space before the uploader's name or email is stored or audited.
func TestDropUploaderFieldsStripControls(t *testing.T) {
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, map[string]any{"ask_name": true})
	tok := fx.visitDrop(t, secret, "")
	rec := dropUpload(t, fx.s, tok, "ok.txt", "x", map[string]string{
		"uploader_name":  "Ann\x00\tLevi Cohen\u0085",
		"uploader_email": "ann@example.com",
	})
	wantStatus(t, rec, 204)
	audit := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil)
	var found bool
	for _, e := range decode[listResponse[auditJSON]](t, audit).Items {
		if e.Action != "node.create" || e.Details["via"] != "share_link" {
			continue
		}
		found = true
		if got := e.Details["uploader_name"]; got != "Ann  Levi Cohen" {
			t.Errorf("uploader_name in the audit is %q; want %q", got, "Ann  Levi Cohen")
		}
	}
	if !found {
		t.Fatal("no node.create entry from the drop link")
	}
	// An email with a control character in it is not an address.
	wantError(t, createUpload(t, fx.s, tok, 1, md("name", "e.txt", "uploader_name", "Ann",
		"uploader_email", "ann\x00@example.com")), 400, apierr.InvalidRequest)
}
