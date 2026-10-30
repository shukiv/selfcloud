// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	_ "modernc.org/sqlite"
)

// Slice p1: per-user limits on My files, the account default, and the storage routes.

// p1Fixture is a team space and two members with their My files. Ann's limit is 100 bytes.
type p1Fixture struct {
	*sharingFixture
	annPF   string // Ann's personal folder
	annRoot string // its root node
}

func newP1Fixture(t *testing.T) *p1Fixture {
	t.Helper()
	fx := &p1Fixture{sharingFixture: newSharingFixture(t)}
	pf, err := fx.s.store.PersonalFolder(context.Background(), fx.acc.ID, fx.ann.ID)
	if err != nil {
		t.Fatal(err)
	}
	fx.annPF, fx.annRoot = pf.ID, fx.rootOf(t, pf.ID)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key,
		map[string]any{"quota_bytes": 100}), 200)
	return fx
}

// bytesOf is a body of n bytes.
func bytesOf(n int) string { return strings.Repeat("x", n) }

var (
	digitsRE    = regexp.MustCompile(`\d`)
	requestIDRE = regexp.MustCompile(`"request_id":"[^"]*"`)
)

// shownNumbers reports whether a reply shows a number, not counting its request ID.
func shownNumbers(body string) bool {
	return digitsRE.MatchString(requestIDRE.ReplaceAllString(body, ""))
}

// refusedForUser checks a refusal for a user's limit. withNumbers: the numbers must be the ones given. Without them,
// neither the details nor the message may carry a number.
func refusedForUser(t *testing.T, rec *httptest.ResponseRecorder, withNumbers bool, required, available int64) {
	t.Helper()
	e := wantError(t, rec, 507, apierr.QuotaExceeded)
	if e.Details["scope"] != "user" {
		t.Fatalf("scope = %v; want user (details %v)", e.Details["scope"], e.Details)
	}
	if withNumbers {
		if e.Details["required_bytes"] != float64(required) || e.Details["available_bytes"] != float64(available) {
			t.Errorf("details = %v; want required %d, available %d", e.Details, required, available)
		}
		return
	}
	if _, ok := e.Details["required_bytes"]; ok {
		t.Errorf("the caller may not see the numbers, yet details = %v", e.Details)
	}
	if _, ok := e.Details["available_bytes"]; ok {
		t.Errorf("the caller may not see the numbers, yet details = %v", e.Details)
	}
	if shownNumbers(rec.Body.String()) {
		t.Errorf("the refusal shows a number to a caller who may not see one: %s", rec.Body.String())
	}
}

// TestUserLimitRefusesMyFilesOverLimit: a write into a member's My files is refused over the member's limit while
// the account has room; the same bytes in a team space are not charged to the member.
func TestUserLimitRefusesMyFilesOverLimit(t *testing.T) {
	fx := newP1Fixture(t)
	wantStatus(t, writeFile(t, fx.s, fx.key, fx.annPF, "/a.bin", bytesOf(60)), 201)
	refusedForUser(t, writeFile(t, fx.s, fx.key, fx.annPF, "/b.bin", bytesOf(50)), true, 50, 40)
	wantStatus(t, writeFile(t, fx.s, fx.key, fx.fld.ID, "/big.bin", bytesOf(500)), 201)
}

// TestUserLimitHidesNumbersFromAContributor: a contributor in a member's My files learns the limit is passed, and
// not the numbers.
func TestUserLimitHidesNumbersFromAContributor(t *testing.T) {
	fx := newP1Fixture(t)
	fx.setPerm(t, fx.annRoot, fx.bob.ID, "contributor")
	wantStatus(t, writeFile(t, fx.s, fx.key, fx.annPF, "/a.bin", bytesOf(60)), 201)
	rec := rawSession(t, fx.s, fx.bobSess, "POST", "/api/v1/folders/"+fx.annPF+"/files?path=/c.bin",
		bytesOf(50), map[string]string{"Origin": fx.s.origin, "X-CSRF-Token": fx.bobSess.csrf})
	refusedForUser(t, rec, false, 0, 0)
}

// TestMoveIntoMembersFolderChecksItsLimit: a move from the team space into a member's My files is checked against
// that member, and the file stays where it was when refused.
func TestMoveIntoMembersFolderChecksItsLimit(t *testing.T) {
	fx := newP1Fixture(t)
	wantStatus(t, writeFile(t, fx.s, fx.key, fx.annPF, "/a.bin", bytesOf(60)), 201)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/moving.bin", bytesOf(80))
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+f.ID+"/move", fx.key, map[string]any{"folder": fx.annPF})
	refusedForUser(t, rec, true, 80, 40)
	got := call(t, fx.s, "GET", "/api/v1/nodes/"+f.ID, fx.key, nil)
	wantStatus(t, got, 200)
	if !strings.Contains(got.Body.String(), fx.fld.ID) {
		t.Errorf("the refused file left the team space: %s", got.Body.String())
	}
}

// TestOwnershipTransferChecksTheRecipient: an item moved by an ownership transfer out of a member's My files into
// another member's My files is checked against the recipient, as a move is. (A transfer inside a team space moves
// no bytes, so it is not checked.)
func TestOwnershipTransferChecksTheRecipient(t *testing.T) {
	fx := newP1Fixture(t)
	wantStatus(t, writeFile(t, fx.s, fx.key, fx.annPF, "/a.bin", bytesOf(60)), 201)
	bobPF, err := fx.s.store.PersonalFolder(context.Background(), fx.acc.ID, fx.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := nodeOf(t, writeFile(t, fx.s, fx.key, bobPF.ID, "/handed.bin", bytesOf(80)))
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+f.ID+"/transfer-ownership", fx.key, map[string]any{"to": fx.ann.ID})
	refusedForUser(t, rec, true, 80, 40)
}

// TestTransferToIsNeverRefused: deleting a member with transfer_to moves its files even past the recipient's limit
// (accepted limit of slice p1).
func TestTransferToIsNeverRefused(t *testing.T) {
	fx := newP1Fixture(t)
	wantStatus(t, writeFile(t, fx.s, fx.key, fx.annPF, "/a.bin", bytesOf(60)), 201)
	bobPF, err := fx.s.store.PersonalFolder(context.Background(), fx.acc.ID, fx.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, writeFile(t, fx.s, fx.key, bobPF.ID, "/bobs.bin", bytesOf(200)), 201)
	rec := call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID+"?transfer_to="+fx.ann.ID, fx.key, nil)
	if rec.Code >= 300 {
		t.Fatalf("delete with transfer_to refused: %d %s", rec.Code, rec.Body.String())
	}
}

// TestLoweringLimitKeepsFilesAndRefusesGrowth: a limit below current use keeps the files; growth is refused.
func TestLoweringLimitKeepsFilesAndRefusesGrowth(t *testing.T) {
	fx := newP1Fixture(t)
	wantStatus(t, writeFile(t, fx.s, fx.key, fx.annPF, "/a.bin", bytesOf(60)), 201)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key,
		map[string]any{"quota_bytes": 10}), 200)
	refusedForUser(t, writeFile(t, fx.s, fx.key, fx.annPF, "/b.bin", bytesOf(1)), true, 1, 0)
	wantStatus(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.annRoot+"/children", fx.key, nil), 200)
}

// TestUserLimitNullFollowsAccountDefault: a null limit follows the account default; 0 is no limit.
func TestUserLimitNullFollowsAccountDefault(t *testing.T) {
	fx := newP1Fixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/storage-policy", fx.key,
		map[string]any{"user_quota_bytes": 30}), 200)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key,
		map[string]any{"quota_bytes": nil}), 200)
	bobPF, err := fx.s.store.PersonalFolder(context.Background(), fx.acc.ID, fx.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	refusedForUser(t, writeFile(t, fx.s, fx.key, bobPF.ID, "/a.bin", bytesOf(40)), true, 40, 30)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key,
		map[string]any{"quota_bytes": 0}), 200)
	wantStatus(t, writeFile(t, fx.s, fx.key, bobPF.ID, "/a.bin", bytesOf(40)), 201)
}

// TestStorageRoutesAccess: who may set limits and read storage. A member gets nothing; an owner sees members'
// totals and team-space names, never a name from a member's My files; the key sees every name; an admin sees
// totals and no names.
func TestStorageRoutesAccess(t *testing.T) {
	fx := newP1Fixture(t)
	wantStatus(t, writeFile(t, fx.s, fx.key, fx.annPF, "/secret-name.txt", bytesOf(60)), 201)
	wantStatus(t, writeFile(t, fx.s, fx.key, fx.fld.ID, "/team-name.txt", bytesOf(30)), 201)
	base := "/api/v1/accounts/" + fx.acc.ID

	for _, p := range []string{base + "/storage", base + "/storage/largest", base + "/storage-policy"} {
		rec := callSession(t, fx.s, fx.annSess, "GET", p, nil)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
			t.Errorf("member GET %s = %d; want refused", p, rec.Code)
		}
	}
	rec := callSession(t, fx.s, fx.annSess, "PATCH", base+"/users/"+fx.bob.ID, map[string]any{"quota_bytes": 5})
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Errorf("member PATCH limit = %d; want refused", rec.Code)
	}

	rec = call(t, fx.s, "GET", base+"/storage/largest", fx.key, nil)
	wantStatus(t, rec, 200)
	if !strings.Contains(rec.Body.String(), "secret-name.txt") {
		t.Errorf("the key does not see the names: %s", rec.Body.String())
	}

	owner := ownerSession(t, fx, "owner@example.com")
	rec = callSession(t, fx.s, owner, "GET", base+"/storage", nil)
	wantStatus(t, rec, 200)
	body := rec.Body.String()
	if !strings.Contains(body, fx.ann.ID) || !strings.Contains(body, `"name":"Team"`) {
		t.Errorf("owner storage lacks the members or the team space names: %s", body)
	}
	if strings.Contains(body, "secret-name") {
		t.Errorf("owner storage shows a file name from a member's My files: %s", body)
	}
	rec = callSession(t, fx.s, owner, "GET", base+"/storage/largest", nil)
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), "secret-name") || !strings.Contains(rec.Body.String(), "team-name") {
		t.Errorf("owner largest = %s; want team-space names only", rec.Body.String())
	}
	if rec := callSession(t, fx.s, owner, "GET", base+"/storage/largest?folder="+fx.annPF, nil); rec.Code != http.StatusForbidden {
		t.Errorf("owner largest of a member's My files = %d; want 403", rec.Code)
	}
	wantStatus(t, callSession(t, fx.s, owner, "PATCH", base+"/users/"+fx.bob.ID, map[string]any{"quota_bytes": 500}), 200)

	rec = call(t, fx.s, "GET", base+"/storage", fx.admin, nil)
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), `"users"`) || strings.Contains(rec.Body.String(), `"name"`) {
		t.Errorf("admin storage shows names or members: %s", rec.Body.String())
	}
	if rec := call(t, fx.s, "GET", base+"/storage/largest", fx.admin, nil); rec.Code != http.StatusForbidden {
		t.Errorf("admin largest = %d; want 403", rec.Code)
	}
	wantStatus(t, call(t, fx.s, "PATCH", base+"/storage-policy", fx.admin, map[string]any{"user_quota_bytes": 0}), 200)
}

// TestMeStorageAndLargest: the member's own meter and its largest files, and nothing of anyone else's.
func TestMeStorageAndLargest(t *testing.T) {
	fx := newP1Fixture(t)
	wantStatus(t, writeFile(t, fx.s, fx.key, fx.annPF, "/a.bin", bytesOf(60)), 201)
	rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/me", nil)
	wantStatus(t, rec, 200)
	body := rec.Body.String()
	if !strings.Contains(body, `"used_bytes":60`) || !strings.Contains(body, `"quota_bytes":100`) ||
		!strings.Contains(body, `"available_bytes":40`) {
		t.Errorf("GET /me storage = %s; want used 60, quota 100, available 40", body)
	}
	rec = callSession(t, fx.s, fx.annSess, "GET", "/api/v1/me/storage/largest", nil)
	wantStatus(t, rec, 200)
	if !strings.Contains(rec.Body.String(), "a.bin") || !strings.Contains(rec.Body.String(), `"in_trash":false`) {
		t.Errorf("largest = %s; want a.bin, not in trash", rec.Body.String())
	}
	rec = callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/me/storage/largest", nil)
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), "a.bin") {
		t.Errorf("bob's largest shows ann's file: %s", rec.Body.String())
	}
}

// TestDropIntoMyFilesIsChargedToItsUser: a drop into a member's My files counts against that member, and the
// visitor gets the one generic refusal with no numbers.
func TestDropIntoMyFilesIsChargedToItsUser(t *testing.T) {
	fx := newDropFixture(t)
	ctx := context.Background()
	ann, _ := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "drop-owner@example.com")
	pf, err := fx.s.store.PersonalFolder(ctx, fx.acc.ID, ann.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+ann.ID, fx.key,
		map[string]any{"quota_bytes": 10}), 200)
	dir := mkdirs(t, fx.s, fx.key, pf.ID, "/drop")
	_, secret := fx.makeDrop(t, dir.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	rec := dropUpload(t, fx.s, tok, "x.bin", bytesOf(50), nil)
	e := wantError(t, rec, 507, apierr.QuotaExceeded)
	if _, ok := e.Details["scope"]; ok {
		t.Errorf("the visitor sees a scope: %v", e.Details)
	}
	if shownNumbers(rec.Body.String()) {
		t.Errorf("the visitor sees a number: %s", rec.Body.String())
	}
}

// ownerSession makes a verified owner in the account and signs it in.
func ownerSession(t *testing.T, fx *p1Fixture, email string) testSession {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": email, "verification": "link", "role": "owner"})
	wantStatus(t, rec, 201)
	created := decode[userResponse](t, rec)
	rec = call(t, fx.s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": created.Verification.Token, "password": testPassword})
	wantStatus(t, rec, 200)
	return sessionFrom(t, fx.s, rec)
}

// driftedAccounts counts the accounts whose counters differ from the sum of their folders' (slice p1).
func driftedAccounts(t *testing.T, s *Server) int {
	t.Helper()
	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM accounts a WHERE
		a.used_bytes <> (SELECT COALESCE(SUM(f.used_bytes), 0) FROM folders f WHERE f.account_id = a.id)
		OR a.used_files <> (SELECT COALESCE(SUM(f.used_files), 0) FROM folders f WHERE f.account_id = a.id)
		OR a.reserved_bytes <> (SELECT COALESCE(SUM(f.reserved_bytes), 0) FROM folders f WHERE f.account_id = a.id)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCountersStayConsistentThroughTheAPI runs the real paths that move usage (upload, replace, copy, move, trash,
// restore, purge, a tus upload into a member's My files) and checks the folders against the account after each.
func TestCountersStayConsistentThroughTheAPI(t *testing.T) {
	fx := newP1Fixture(t)
	// Ann's replaced content and its version take 100 bytes; lift the limit, so the paths below are not refused.
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key,
		map[string]any{"quota_bytes": 0}), 200)
	check := func(step string) {
		t.Helper()
		if n := driftedAccounts(t, fx.s); n != 0 {
			t.Fatalf("after %s: %d account(s) disagree with their folders", step, n)
		}
	}
	f := nodeOf(t, writeFile(t, fx.s, fx.key, fx.annPF, "/a.bin", bytesOf(60)))
	check("upload")

	rec := rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+f.ID+"/content", fx.key, bytesOf(40),
		map[string]string{"If-Match": f.ETag, "Content-Type": "text/plain"})
	wantStatus(t, rec, 200)
	check("replace (keeps a version)")

	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+f.ID+"/copy", fx.key, map[string]any{"folder": fx.fld.ID}), 201)
	check("copy into a team space")

	moved := nodeOf(t, writeFile(t, fx.s, fx.key, fx.annPF, "/m.bin", bytesOf(10)))
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+moved.ID+"/move", fx.key, map[string]any{"folder": fx.fld.ID}), 200)
	check("move across folders")

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+f.ID, fx.key, nil), 204)
	check("trash")
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+f.ID+"/restore", fx.key, nil), 200)
	check("restore")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+f.ID, fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+f.ID, fx.key, nil), 204)
	check("purge")

	upl := uploadID(t, createUpload(t, fx.s, fx.key, 5, md("name", "t.bin", "folder", fx.annPF)))
	wantStatus(t, piece(t, fx.s, fx.key, upl, 0, "hello", nil), 204)
	check("tus upload into My files")
}

// TestOwnerFoldersAreTeamSpacesOnly: an owner's storage lists the team spaces and no personal folder (the members'
// My files are seen only as totals through the users). The account key lists every folder.
func TestOwnerFoldersAreTeamSpacesOnly(t *testing.T) {
	fx := newP1Fixture(t)
	base := "/api/v1/accounts/" + fx.acc.ID
	owner := ownerSession(t, fx, "owner-folders@example.com")
	rec := callSession(t, fx.s, owner, "GET", base+"/storage", nil)
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), `"kind":"personal"`) || strings.Contains(rec.Body.String(), fx.annPF) {
		t.Errorf("owner storage lists a personal folder: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"kind":"team"`) {
		t.Errorf("owner storage lists no team space: %s", rec.Body.String())
	}
	rec = call(t, fx.s, "GET", base+"/storage", fx.key, nil)
	wantStatus(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"kind":"personal"`) || !strings.Contains(rec.Body.String(), fx.annPF) {
		t.Errorf("the key's storage lacks the personal folders: %s", rec.Body.String())
	}
}

// TestUploadFolderFollowsItsParentAtInsert: a directory moved to another folder after the creation's first check, and
// before the upload row is written, must not leave the upload in the folder the directory left. The creation is
// refused, or the upload is in the directory's folder: never a row that cannot complete.
func TestUploadFolderFollowsItsParentAtInsert(t *testing.T) {
	fx := newSharingFixture(t)
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	otherRoot := fx.rootOf(t, other.ID)
	moved := false
	fx.s.beforeWrite = func() {
		if moved {
			return
		}
		moved = true
		wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.a.ID+"/move", fx.key,
			map[string]any{"folder": other.ID, "parent": otherRoot}), 200)
	}
	rec := createUpload(t, fx.s, fx.key, 5, md("name", "late.bin", "folder", fx.fld.ID, "parent", fx.a.ID))
	if !moved {
		t.Fatal("the hook did not run before the write")
	}
	if rec.Code != http.StatusCreated {
		wantError(t, rec, http.StatusNotFound, apierr.NotFound)
		return
	}
	upl := uploadID(t, rec)
	db, err := sql.Open("sqlite", fx.s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var upFolder, parentFolder string
	if err := db.QueryRow("SELECT folder_id FROM uploads WHERE id = ?", upl).Scan(&upFolder); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT folder_id FROM nodes WHERE id = ?", fx.a.ID).Scan(&parentFolder); err != nil {
		t.Fatal(err)
	}
	if upFolder != parentFolder {
		t.Errorf("the upload is in folder %s but its directory is in %s: it could never complete", upFolder, parentFolder)
	}
}

// TestDropPieceOverTheLimitNeverReachesDisk: a drop piece that the account's quota cannot take is refused before its
// bytes are written. The piece's file stays empty and the reservation is unchanged.
func TestDropPieceOverTheLimitNeverReachesDisk(t *testing.T) {
	fx := newDropFixture(t)
	fx.setQuota(t, 10)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	upl := uploadID(t, createUpload(t, fx.s, tok, 100, md("name", "big.bin")))
	before := fx.reservedBytes(t)
	reached := false
	fx.s.afterPieceWrite = func(string) { reached = true }
	rec := piece(t, fx.s, tok, upl, 0, bytesOf(100), nil)
	wantError(t, rec, http.StatusInsufficientStorage, apierr.QuotaExceeded)
	if reached {
		t.Error("the piece's bytes were written to disk before the refusal")
	}
	f, err := fx.s.blobs.OpenUpload(fx.acc.ID, upl)
	if err == nil {
		defer f.Close()
		if st, err := f.Stat(); err != nil || st.Size() != 0 {
			t.Errorf("the piece's file holds %v bytes (err %v); want none", st, err)
		}
	}
	if got := fx.reservedBytes(t); got != before {
		t.Errorf("reserved bytes = %d after the refusal; want %d", got, before)
	}
}
