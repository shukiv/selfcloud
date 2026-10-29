// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// pausingReader runs onFirstRead once, before the first byte it returns, so a test can change the world while a
// piece's body is being read.
type pausingReader struct {
	r           io.Reader
	onFirstRead func()
	done        bool
}

func (p *pausingReader) Read(b []byte) (int, error) {
	if !p.done {
		p.done = true
		p.onFirstRead()
	}
	return p.r.Read(b)
}

// TestDropPiecesInFlightCountAgainstTheDisk (round 2, item 1): a drop piece whose body is still arriving has already
// passed its disk check, so another piece must count its bytes too. Two pieces of 4 bytes each fit a 6-byte disk
// one at a time, not together.
func TestDropPiecesInFlightCountAgainstTheDisk(t *testing.T) {
	fx := newDropFixture(t)
	fx.s.freeBytes = func(string) (uint64, error) { return 6, nil }
	// The account's share is lifted here, so only the in-flight count can refuse the second piece.
	fx.s.cfg.Upload.MaxDiskSharePercent = 100
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	a := dropDeclare(t, fx.s, tok, "a.bin", 4)
	b := dropDeclare(t, fx.s, tok, "b.bin", 4)
	pr, pw := io.Pipe()
	type answer struct{ code int }
	ans := make(chan answer, 1)
	go func() {
		rec := upCall(t, fx.s, "PATCH", "/api/v1/uploads/"+a, tok, map[string]string{
			"Content-Type": uploadBodyType, "Upload-Offset": "0"}, pr, 4)
		ans <- answer{rec.Code}
	}()
	// The first two bytes go through only once piece a has passed its check and is reading its body.
	if _, err := pw.Write([]byte("12")); err != nil {
		t.Fatal(err)
	}
	rec := piece(t, fx.s, tok, b, 0, "5678", nil)
	if rec.Code != http.StatusInsufficientStorage {
		t.Errorf("the second piece answered %d while the first was in flight; want 507 storage_full", rec.Code)
	}
	if _, err := pw.Write([]byte("34")); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	if got := (<-ans).code; got != http.StatusNoContent {
		t.Errorf("the first piece answered %d; want 204", got)
	}
}

// TestDropDiskShareAppliesToDropPieces (round 2, item 1): a drop link's visitors may not hold more of the disk than
// upload.max_disk_share_percent, even on an account with no quota, as other uploads may not.
func TestDropDiskShareAppliesToDropPieces(t *testing.T) {
	fx := newDropFixture(t)
	fx.s.freeBytes = func(string) (uint64, error) { return 100, nil }
	fx.s.cfg.Upload.MaxDiskSharePercent = 50
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	upl := dropDeclare(t, fx.s, tok, "big.bin", 60)
	wantStatus(t, piece(t, fx.s, tok, upl, 0, strings.Repeat("a", 20), nil), 204)
	wantStatus(t, piece(t, fx.s, tok, upl, 20, strings.Repeat("b", 20), nil), 204)
	rec := piece(t, fx.s, tok, upl, 40, strings.Repeat("c", 20), nil)
	// A drop visitor gets the generic refusal of item 2, whatever the limit was: the code is quota_exceeded.
	wantError(t, rec, http.StatusInsufficientStorage, apierr.QuotaExceeded)
	if rec.Header().Get("Upload-Offset") == "60" {
		t.Error("the refused piece moved the offset")
	}
	if got := fx.reservedBytes(t); got != 40 {
		t.Errorf("the account reserves %d bytes for the drop bytes received; want the 40 received", got)
	}
}

// TestDropFailedRecordLeavesNoBytes (round 2, item 2): when the link is paused while a piece's body arrives, the
// piece is refused and its bytes are cut off the file, as every other failed piece is.
func TestDropFailedRecordLeavesNoBytes(t *testing.T) {
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	upl := dropDeclare(t, fx.s, tok, "pause.bin", 4)
	body := &pausingReader{r: strings.NewReader("1234"), onFirstRead: func() {
		setPolicy(t, fx.s, fx.key, fx.acc.ID, false)
	}}
	rec := upCall(t, fx.s, "PATCH", "/api/v1/uploads/"+upl, tok, map[string]string{
		"Content-Type": uploadBodyType, "Upload-Offset": "0"}, body, 4)
	wantError(t, rec, http.StatusNotFound, apierr.NotFound)
	info, err := os.Stat(uploadFile(fx.s, fx.acc.ID, upl))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("the refused piece left %d bytes in the file; want 0", info.Size())
	}
}

// TestDropDeadLinkKeepsFileWhenDeleteFails (round 2, item 3): when the link dies while a piece's body arrives and the
// database then refuses to delete the upload, the upload's file stays, since its row stays.
func TestDropDeadLinkKeepsFileWhenDeleteFails(t *testing.T) {
	fx := newDropFixture(t)
	link, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	upl := dropDeclare(t, fx.s, tok, "dead.bin", 10)
	fx.dbExec(t, "CREATE TRIGGER refuse_upload_delete BEFORE DELETE ON uploads BEGIN SELECT RAISE(ABORT, 'refused'); END")
	body := &pausingReader{r: strings.NewReader("1234"), onFirstRead: func() {
		fx.setExpiry(t, link.ID, time.Now().Add(-time.Minute))
	}}
	wantStatus(t, upCall(t, fx.s, "PATCH", "/api/v1/uploads/"+upl, tok, map[string]string{
		"Content-Type": uploadBodyType, "Upload-Offset": "0"}, body, 4), 404)
	if _, err := os.Stat(uploadFile(fx.s, fx.acc.ID, upl)); err != nil {
		t.Errorf("the upload's file is gone though its row stayed: %v", err)
	}
	if _, err := fx.s.store.Upload(context.Background(), fx.acc.ID, upl); err != nil {
		t.Errorf("the upload's row is gone: %v", err)
	}
}

// TestDropPasswordChangeReleasesItsUploads (round 2, item 4): changing or removing a drop link's password ends its
// visitors' credentials, and releases their open uploads with them.
func TestDropPasswordChangeReleasesItsUploads(t *testing.T) {
	for name, tc := range map[string]struct {
		password string // the link's password at creation
		change   string // the new password, "" to remove it
		visit    string // the password the visitor opens with
	}{
		"changed": {password: "", change: "new password", visit: ""},
		"removed": {password: "old password", change: "", visit: "old password"},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newDropFixture(t)
			var extra map[string]any
			if tc.password != "" {
				extra = map[string]any{"password": tc.password}
			}
			link, secret := fx.makeDrop(t, fx.b.ID, extra)
			tok := fx.visitDrop(t, secret, tc.visit)
			upl := dropDeclare(t, fx.s, tok, "p.bin", 10)
			wantStatus(t, piece(t, fx.s, tok, upl, 0, "1234", nil), 204)
			file := uploadFile(fx.s, fx.acc.ID, upl)
			wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+link.ID, fx.key,
				map[string]any{"password": tc.change}), 200)
			if got := fx.reservedBytes(t); got != 0 {
				t.Errorf("%d bytes stay reserved after the password changed; want 0", got)
			}
			if _, err := fx.s.store.Upload(context.Background(), fx.acc.ID, upl); err == nil {
				t.Error("the upload row survived the password change")
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Errorf("the upload file survived the password change: %v", err)
			}
		})
	}
}

// TestDropPiecesInFlightCountAgainstTheQuota (round 2, item 1): the same race on the account's quota. Two pieces of 4
// bytes fit a quota of 6 one at a time, not together, so the second is refused while the first is in flight.
func TestDropPiecesInFlightCountAgainstTheQuota(t *testing.T) {
	fx := newDropFixture(t)
	// The quota leaves room for six bytes beyond what the fixture's files already use.
	usage := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/usage", fx.key, nil)
	wantStatus(t, usage, 200)
	fx.setQuota(t, decode[usageJSON](t, usage).UsedBytes+6)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	a := dropDeclare(t, fx.s, tok, "qa.bin", 4)
	b := dropDeclare(t, fx.s, tok, "qb.bin", 4)
	pr, pw := io.Pipe()
	ans := make(chan int, 1)
	go func() {
		rec := upCall(t, fx.s, "PATCH", "/api/v1/uploads/"+a, tok, map[string]string{
			"Content-Type": uploadBodyType, "Upload-Offset": "0"}, pr, 4)
		ans <- rec.Code
	}()
	if _, err := pw.Write([]byte("12")); err != nil {
		t.Fatal(err)
	}
	refused := piece(t, fx.s, tok, b, 0, "5678", nil)
	wantError(t, refused, http.StatusInsufficientStorage, apierr.QuotaExceeded)
	if _, err := pw.Write([]byte("34")); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	if got := <-ans; got != http.StatusNoContent {
		t.Errorf("the first piece answered %d; want 204", got)
	}
}

// TestDropPiecesInFlightCountAcrossAccounts (round 2, item 1): a drop piece in flight for one account counts against
// the install-wide pool for another, so two accounts' pieces cannot both pass on the same free space.
func TestDropPiecesInFlightCountAcrossAccounts(t *testing.T) {
	fx := newDropFixture(t)
	fx.s.freeBytes = func(string) (uint64, error) { return 6, nil }
	fx.s.cfg.Upload.MaxDiskSharePercent = 100
	other, _ := newAccount(t, fx.s, fx.admin, "Other")
	ctx := context.Background()
	release, err := fx.s.admitDropPiece(ctx, &store.Upload{ID: "upl_a", AccountID: fx.acc.ID}, 4)
	if err != nil {
		t.Fatalf("the first piece was refused: %v", err)
	}
	if _, err := fx.s.admitDropPiece(ctx, &store.Upload{ID: "upl_b", AccountID: other.ID}, 4); err == nil {
		t.Error("a second account's piece passed while the first account's 4 bytes were in flight on a 6-byte disk")
	}
	release()
	again, err := fx.s.admitDropPiece(ctx, &store.Upload{ID: "upl_b", AccountID: other.ID}, 4)
	if err != nil {
		t.Errorf("after the first piece was released, the second was refused: %v", err)
	}
	if again != nil {
		again()
	}
}
