// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/sweep"
)

// newQuotaAccount creates an account with the given quota (0 = unlimited) and an account key.
func newQuotaAccount(t *testing.T, s *Server, admin, name string, quota int64) (accountJSON, string) {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts", admin, map[string]any{"name": name, "quota_bytes": quota})
	wantStatus(t, rec, 201)
	acc := decode[accountJSON](t, rec)
	rec = call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/keys", admin, map[string]any{"name": "test"})
	wantStatus(t, rec, 201)
	return acc, decode[createdKey](t, rec).Key
}

// quotaSetup is an account with a quota and one folder, with the admin key that can change quotas.
func quotaSetup(t *testing.T, quota int64, mutate ...func(*config.Config)) (*Server, string, string, string, folderJSON) {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{}, mutate...)
	admin := adminKey(t, s)
	acc, key := newQuotaAccount(t, s, admin, "Acme", quota)
	f := newFolder(t, s, key, acc.ID, "Docs")
	return s, admin, key, acc.ID, f
}

// writeFile creates a small file at path in the folder, from body.
func writeFile(t *testing.T, s *Server, key, fld, p, body string) *httptest.ResponseRecorder {
	t.Helper()
	return rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/files?path="+p, key, body, nil)
}

// nodeOf decodes a node reply.
func nodeOf(t *testing.T, rec *httptest.ResponseRecorder) nodeJSON {
	t.Helper()
	wantStatus(t, rec, 201)
	return decode[nodeJSON](t, rec)
}

// quotaDetails checks a quota_exceeded refusal's numbers and scope.
func quotaDetails(t *testing.T, rec *httptest.ResponseRecorder, code apierr.Code, required, available int64, scope string) {
	t.Helper()
	e := wantError(t, rec, 507, code)
	if e.Details["required_bytes"] != float64(required) || e.Details["available_bytes"] != float64(available) ||
		e.Details["scope"] != scope {
		t.Errorf("details = %v; want required %d, available %d, scope %s", e.Details, required, available, scope)
	}
}

// sweepAt runs one housekeeping pass as if it were now.
func sweepAt(t *testing.T, s *Server, now time.Time) sweep.Result {
	t.Helper()
	sw := sweep.New(sweep.Options{Store: s.store, Blobs: s.blobs, DeleteGrace: time.Hour,
		UploadExpiry: s.cfg.Upload.Expiry.D(), UploadMaxLifetime: s.cfg.Upload.MaxLifetime.D()})
	res, err := sw.Once(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestQuotaRefusesSmallFileOverQuota(t *testing.T) {
	t.Parallel()
	s, _, key, accID, f := quotaSetup(t, 100)
	wantStatus(t, writeFile(t, s, key, f.ID, "/a.bin", strings.Repeat("a", 60)), 201)
	quotaDetails(t, writeFile(t, s, key, f.ID, "/b.bin", strings.Repeat("b", 50)), apierr.QuotaExceeded, 50, 40, "quota")
	u := usageOf(t, s, key, accID)
	if u.UsedBytes != 60 || u.Files != 1 || u.UsedFiles != 1 || u.Folders != 1 {
		t.Errorf("usage after refusal = %+v; the refused file must leave nothing behind", u)
	}
	if u.QuotaBytes == nil || *u.QuotaBytes != 100 || u.AvailableBytes == nil || *u.AvailableBytes != 40 {
		t.Errorf("quota and available = %v, %v; want 100 and 40", u.QuotaBytes, u.AvailableBytes)
	}
	// Exactly the free space fits.
	wantStatus(t, writeFile(t, s, key, f.ID, "/c.bin", strings.Repeat("c", 40)), 201)
}

// TestQuotaReplaceCountsNewContentInFull: a replacement keeps the old content as a version, which counts
// toward the quota, so the new content counts in full. A replacement is not a difference any more: it needs
// its whole size free, even when it is smaller than what it replaces.
func TestQuotaReplaceCountsNewContentInFull(t *testing.T) {
	t.Parallel()
	s, admin, key, accID, f := quotaSetup(t, 200)
	n := nodeOf(t, writeFile(t, s, key, f.ID, "/a.bin", strings.Repeat("a", 60)))
	put := func(body, etag string) *httptest.ResponseRecorder {
		return rawCall(t, s, "PUT", "/api/v1/nodes/"+n.ID+"/content", key, body, map[string]string{"If-Match": etag})
	}
	rec := put(strings.Repeat("b", 90), n.ETag) // the 60 bytes become a version: 60 + 90 = 150 of 200
	wantStatus(t, rec, 200)
	etag := decode[nodeJSON](t, rec).ETag
	if u := usageOf(t, s, key, accID); u.UsedBytes != 150 {
		t.Fatalf("usage after replace = %+v, want 150 (the version and the file)", u)
	}
	// The next 60 needs 60 free, and only 50 are.
	quotaDetails(t, put(strings.Repeat("c", 60), etag), apierr.QuotaExceeded, 60, 50, "quota")
	if u := usageOf(t, s, key, accID); u.UsedBytes != 150 {
		t.Fatalf("a refused PUT changed usage: %+v", u)
	}
	// Lowering the quota below usage keeps the file, and a replacement then needs its full size free: a
	// smaller one is refused too, since it adds a version of what it replaces.
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+accID, admin, map[string]any{"quota_bytes": 100}), 200)
	quotaDetails(t, put(strings.Repeat("d", 3), etag), apierr.QuotaExceeded, 3, 0, "quota")
	// A permanent delete takes the file and its version off the usage.
	wantStatus(t, rawCall(t, s, "DELETE", "/api/v1/nodes/"+n.ID+"?permanent=true", key, "", nil), 204)
	if u := usageOf(t, s, key, accID); u.UsedBytes != 0 || u.Files != 0 {
		t.Errorf("usage after permanent delete = %+v", u)
	}
}

func TestQuotaCopyCountsInFullAndDisplacedStaysInTrash(t *testing.T) {
	t.Parallel()
	s, _, key, accID, f := quotaSetup(t, 100)
	a := nodeOf(t, writeFile(t, s, key, f.ID, "/a.bin", strings.Repeat("a", 50)))
	nodeOf(t, writeFile(t, s, key, f.ID, "/b.bin", strings.Repeat("b", 40))) // used 90
	copyTo := func(body map[string]any) *httptest.ResponseRecorder {
		return call(t, s, "POST", "/api/v1/nodes/"+a.ID+"/copy", key, body)
	}
	// A plain copy counts in full: 50 more would reach 140.
	quotaDetails(t, copyTo(map[string]any{"name": "c.bin"}), apierr.QuotaExceeded, 50, 10, "quota")
	// Copied over b.bin, the copy displaces b.bin, which goes to the trash and keeps its 40 bytes in usage.
	// So the copy's 50 still needs 50 free, and is refused.
	quotaDetails(t, copyTo(map[string]any{"name": "b.bin", "on_conflict": "replace"}), apierr.QuotaExceeded, 50, 10, "quota")
	if u := usageOf(t, s, key, accID); u.UsedBytes != 90 || u.Files != 2 {
		t.Errorf("usage after refused copies = %+v; want 90 bytes, 2 files", u)
	}
}

func TestQuotaCopyJobIsChecked(t *testing.T) {
	t.Parallel()
	s, _, key, accID, f := quotaSetup(t, 100)
	s.copySyncNodes = 0 // every copy becomes a job
	a := nodeOf(t, writeFile(t, s, key, f.ID, "/a.bin", strings.Repeat("a", 60)))
	// A chunked copy reserves its whole cost when it is asked for (decision 34): 60 more would reach 120 of 100, so the
	// request is refused before any job or staging folder is made.
	rec := call(t, s, "POST", "/api/v1/nodes/"+a.ID+"/copy", key, map[string]any{"name": "c.bin"})
	wantStatus(t, rec, 507)
	if u := usageOf(t, s, key, accID); u.UsedBytes != 60 || u.Files != 1 || u.ReservedBytes != 0 {
		t.Errorf("usage after refused copy = %+v", u)
	}
}

func TestQuotaReservationCountsAndRefuses(t *testing.T) {
	t.Parallel()
	s, _, key, accID, f := quotaSetup(t, 100)
	upl := uploadID(t, createUpload(t, s, key, 60, md("folder", f.ID, "name", "a.bin")))
	if u := usageOf(t, s, key, accID); u.ReservedBytes != 60 || *u.AvailableBytes != 40 {
		t.Fatalf("usage with a 60-byte reservation = %+v", u)
	}
	quotaDetails(t, createUpload(t, s, key, 50, md("folder", f.ID, "name", "b.bin")), apierr.QuotaExceeded, 50, 40, "quota")
	if u := usageOf(t, s, key, accID); u.ReservedBytes != 60 {
		t.Errorf("a refused creation changed the reservation: %+v", u)
	}
	wantStatus(t, upCall(t, s, "DELETE", "/api/v1/uploads/"+upl, key, nil, nil, 0), 204)
	if u := usageOf(t, s, key, accID); u.ReservedBytes != 0 {
		t.Errorf("terminated upload kept its reservation: %+v", u)
	}
	wantStatus(t, createUpload(t, s, key, 100, md("folder", f.ID, "name", "c.bin")), 201)
}

func TestUploadCompletionIsNeverRefused(t *testing.T) {
	t.Parallel()
	s, admin, key, accID, f := quotaSetup(t, 100)
	upl := uploadID(t, createUpload(t, s, key, 40, md("folder", f.ID, "name", "a.bin")))
	wantStatus(t, piece(t, s, key, upl, 0, strings.Repeat("a", 20), nil), 204)
	// The quota falls below what is reserved while the upload is going.
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+accID, admin, map[string]any{"quota_bytes": 10}), 200)
	rec := piece(t, s, key, upl, 20, strings.Repeat("a", 20), nil)
	wantStatus(t, rec, 204)
	if rec.Header().Get(nodeHeader) == "" {
		t.Error("completed upload did not name its file")
	}
	u := usageOf(t, s, key, accID)
	if u.UsedBytes != 40 || u.Files != 1 || u.ReservedBytes != 0 {
		t.Errorf("usage after completion = %+v; want 40 bytes in 1 file, nothing reserved", u)
	}
	// A new upload does not fit under the lowered quota.
	quotaDetails(t, createUpload(t, s, key, 1, md("folder", f.ID, "name", "b.bin")), apierr.QuotaExceeded, 1, 0, "quota")
}

// TestReservationsReleasedOnEveryPath: every way an upload ends gives its reservation back.
func TestReservationsReleasedOnEveryPath(t *testing.T) {
	t.Parallel()
	s, _, key, accID, f := quotaSetup(t, 10000)
	reserved := func() int64 { return usageOf(t, s, key, accID).ReservedBytes }

	// A creation whose first piece fails its checksum is dropped whole.
	rec := upCall(t, s, "POST", "/api/v1/uploads", key, map[string]string{
		"Upload-Length": "3", "Upload-Metadata": md("folder", f.ID, "name", "sum.bin"),
		"Content-Type": uploadBodyType, "Upload-Checksum": "sha256 " + b64sum("xyz")}, strings.NewReader("abc"), 3)
	wantError(t, rec, 460, apierr.ChecksumMismatch)
	if r := reserved(); r != 0 {
		t.Errorf("failed creation kept %d bytes reserved", r)
	}

	// An upload that expires is removed by the sweep, and released.
	createUpload(t, s, key, 200, md("folder", f.ID, "name", "old.bin"))
	if r := reserved(); r != 200 {
		t.Fatalf("reserved = %d; want 200", r)
	}
	if res := sweepAt(t, s, time.Now().Add(25*time.Hour)); res.Uploads != 1 {
		t.Errorf("sweep expired %d uploads; want 1", res.Uploads)
	}
	if r := reserved(); r != 0 {
		t.Errorf("expired upload kept %d bytes reserved", r)
	}

	// An upload waiting for a name (awaiting_commit) keeps its reservation until it expires.
	// The name is taken only after the upload has started, so the completion, not the creation, is refused.
	waiting := uploadID(t, createUpload(t, s, key, 10, md("folder", f.ID, "name", "taken.bin")))
	wantStatus(t, writeFile(t, s, key, f.ID, "/taken.bin", "x"), 201)
	wantError(t, piece(t, s, key, waiting, 0, "0123456789", nil), 409, apierr.Conflict)
	if r := reserved(); r != 10 {
		t.Fatalf("waiting upload reserved %d; want 10", r)
	}
	sweepAt(t, s, time.Now().Add(25*time.Hour))
	if r := reserved(); r != 0 {
		t.Errorf("expired waiting upload kept %d bytes reserved", r)
	}

	// Deleting a folder takes the uploads that target it.
	other := newFolder(t, s, key, accID, "Other")
	createUpload(t, s, key, 70, md("folder", other.ID, "name", "o.bin"))
	if r := reserved(); r != 70 {
		t.Fatalf("reserved = %d; want 70", r)
	}
	wantStatus(t, call(t, s, "DELETE", "/api/v1/accounts/"+accID+"/folders/"+other.ID, key, nil), 204)
	if r := reserved(); r != 0 {
		t.Errorf("deleted folder left %d bytes reserved", r)
	}

	// Deleting a directory takes the uploads that target it, and leaves the others.
	dir := nodeOf(t, call(t, s, "POST", "/api/v1/folders/"+f.ID+"/directories", key, map[string]any{"path": "d"}))
	createUpload(t, s, key, 30, md("folder", f.ID, "name", "in.bin", "parent", dir.ID))
	createUpload(t, s, key, 5, md("folder", f.ID, "name", "root.bin"))
	if r := reserved(); r != 35 {
		t.Fatalf("reserved = %d; want 35", r)
	}
	wantStatus(t, rawCall(t, s, "DELETE", "/api/v1/nodes/"+dir.ID, key, "", nil), 204)
	if r := reserved(); r != 5 {
		t.Errorf("deleted directory left %d bytes reserved; want 5", r)
	}
}

// TestUnlimitedAccountReservationCap: an account with no quota is held to upload.max_reserved_per_account.
func TestUnlimitedAccountReservationCap(t *testing.T) {
	t.Parallel()
	s, _, key, accID, f := quotaSetup(t, 0, func(c *config.Config) { c.Upload.MaxReservedPerAccount = 100 })
	createUpload(t, s, key, 60, md("folder", f.ID, "name", "a.bin"))
	quotaDetails(t, createUpload(t, s, key, 50, md("folder", f.ID, "name", "b.bin")),
		apierr.QuotaExceeded, 50, 40, "account_reservations")
	u := usageOf(t, s, key, accID)
	if u.QuotaBytes != nil || u.AvailableBytes != nil {
		t.Errorf("an unlimited account reports quota %v and available %v; want null", u.QuotaBytes, u.AvailableBytes)
	}
	if u.ReservedBytes != 60 {
		t.Errorf("reserved = %d; want 60", u.ReservedBytes)
	}
}

// TestDiskShareStopsOneAccountFillingThePool: an account that opens many large uploads is stopped at
// its share of the disk, and another account's small upload still fits.
func TestDiskShareStopsOneAccountFillingThePool(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	s.freeBytes = func(string) (uint64, error) { return 1000, nil }
	admin := adminKey(t, s)
	accA, keyA := newQuotaAccount(t, s, admin, "Greedy", 0)
	accB, keyB := newQuotaAccount(t, s, admin, "Small", 0)
	fa := newFolder(t, s, keyA, accA.ID, "Docs")
	fb := newFolder(t, s, keyB, accB.ID, "Docs")

	wantStatus(t, createUpload(t, s, keyA, 500, md("folder", fa.ID, "name", "a1.bin")), 201)
	// Half of the 1000 bytes is the most one account may hold for uploads (the default share).
	quotaDetails(t, createUpload(t, s, keyA, 1, md("folder", fa.ID, "name", "a2.bin")),
		apierr.StorageFull, 501, 500, "account")
	if u := usageOf(t, s, keyA, accA.ID); u.ReservedBytes != 500 {
		t.Errorf("a refused creation reserved bytes: %+v", u)
	}
	// The other account's small upload fits in what is left.
	wantStatus(t, createUpload(t, s, keyB, 100, md("folder", fb.ID, "name", "b.bin")), 201)
	// The pool as a whole still holds: 450 more would need 1050 of 1000, and the pool is checked first.
	quotaDetails(t, createUpload(t, s, keyB, 450, md("folder", fb.ID, "name", "b2.bin")),
		apierr.StorageFull, 1050, 1000, "server")
}

// TestUploadLifetimeCap: an upload expires max_lifetime after its creation, however often it is written
// to, and the Upload-Expires header says so.
func TestUploadLifetimeCap(t *testing.T) {
	t.Parallel()
	const life = 2 * time.Hour
	s, _, key, _, f := quotaSetup(t, 0, func(c *config.Config) { c.Upload.MaxLifetime = config.Duration(life) })
	created := time.Now()
	deadline := created.Add(life + time.Second)
	upl := uploadID(t, createUpload(t, s, key, 10, md("folder", f.ID, "name", "long.bin")))
	expires := func(rec *httptest.ResponseRecorder) time.Time {
		t.Helper()
		v := rec.Header().Get("Upload-Expires")
		tm, err := http.ParseTime(v)
		if err != nil {
			t.Fatalf("Upload-Expires %q: %v", v, err)
		}
		return tm
	}
	// The creation already reports the cap, not upload.expiry (24h).
	if got := expires(createUpload(t, s, key, 1, md("folder", f.ID, "name", "x.bin"))); got.After(deadline) {
		t.Errorf("creation expires at %v; the cap is %v", got, created.Add(life))
	}
	// Writing refreshes the idle expiry, but never past the cap.
	rec := piece(t, s, key, upl, 0, "abc", nil)
	wantStatus(t, rec, 204)
	if got := expires(rec); got.After(deadline) {
		t.Errorf("PATCH expires at %v; the cap is %v", got, created.Add(life))
	}
	// The sweep expires it once the cap has passed.
	if res := sweepAt(t, s, created.Add(life+time.Minute)); res.Uploads < 1 {
		t.Errorf("sweep removed %d uploads past their lifetime; want at least 1", res.Uploads)
	}
	wantError(t, headUpload(t, s, key, upl), 404, apierr.NotFound)
}

// TestUploadPastLifetimeRefused: a request for an upload past its lifetime is refused even before the
// sweep has run.
func TestUploadPastLifetimeRefused(t *testing.T) {
	t.Parallel()
	s, _, key, _, f := quotaSetup(t, 0)
	upl := uploadID(t, createUpload(t, s, key, 10, md("folder", f.ID, "name", "late.bin")))
	s.cfg.Upload.MaxLifetime = config.Duration(time.Nanosecond) // the upload was created before this
	wantError(t, piece(t, s, key, upl, 0, "abc", nil), 410, apierr.UploadExpired)
	wantError(t, headUpload(t, s, key, upl), 410, apierr.UploadExpired)
}

// refusedForAccount checks the account's own refusal. withNumbers: the numbers and the scope must be the ones given.
// Without them, the refusal may carry no details at all and no number in its body, so the caller learns nothing of
// the account's space or of how its quota is set.
func refusedForAccount(t *testing.T, rec *httptest.ResponseRecorder, withNumbers bool, required, available int64) {
	t.Helper()
	e := wantError(t, rec, 507, apierr.QuotaExceeded)
	if withNumbers {
		if e.Details["required_bytes"] != float64(required) || e.Details["available_bytes"] != float64(available) ||
			e.Details["scope"] != scopeQuota {
			t.Errorf("details = %v; want required %d, available %d, scope %s", e.Details, required, available, scopeQuota)
		}
		return
	}
	if len(e.Details) != 0 {
		t.Errorf("the caller may not see the account's numbers, yet details = %v", e.Details)
	}
	if shownNumbers(rec.Body.String()) {
		t.Errorf("the refusal shows a number to a caller who may not see one: %s", rec.Body.String())
	}
}

// TestAccountQuotaNumbersGoToOwnerAndKeyOnly: the account's own refusal shows its numbers to an account owner and to
// the account key. A member and a token acting for a member get the same refusal with no numbers.
func TestAccountQuotaNumbersGoToOwnerAndKeyOnly(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin, map[string]any{"quota_bytes": 100}), 200)
	a := nodeOf(t, writeFile(t, fx.s, fx.key, fx.fld.ID, "/a.bin", bytesOf(60)))
	filesAt := func(name string) string { return "/api/v1/folders/" + fx.fld.ID + "/files?path=/" + name + ".bin" }
	owner := ownerSession(t, &p1Fixture{sharingFixture: fx}, "owner@example.com")
	refusedForAccount(t, rawSession(t, fx.s, owner, "POST", filesAt("owner"), bytesOf(50), nil), true, 50, 33)
	refusedForAccount(t, writeFile(t, fx.s, fx.key, fx.fld.ID, "/key.bin", bytesOf(50)), true, 50, 33)

	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.rootOf(t, fx.fld.ID)+"/permissions/"+fx.ann.ID, fx.key,
		map[string]any{"role": "contributor"}), 200)
	refusedForAccount(t, rawSession(t, fx.s, fx.annSess, "POST", filesAt("member"), bytesOf(50), nil), false, 0, 0)

	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID,
		"folders": []string{fx.fld.ID}, "permissions": "write"})
	refusedForAccount(t, rawCall(t, fx.s, "POST", filesAt("token"), tok, bytesOf(50), nil), false, 0, 0)
	// A tus creation and a copy by the token meet the same refusal.
	refusedForAccount(t, createUpload(t, fx.s, tok, 50, md("folder", fx.fld.ID, "name", "t.bin")), false, 0, 0)
	refusedForAccount(t, call(t, fx.s, "POST", "/api/v1/nodes/"+a.ID+"/copy", tok, map[string]any{"name": "t-copy.bin"}),
		false, 0, 0)
}

// TestAccountQuotaDropVisitorGetsNoScopeOrNumbers: a drop visitor's piece over the account's quota gets the one
// generic refusal, with neither a scope nor a number.
func TestAccountQuotaDropVisitorGetsNoScopeOrNumbers(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin, map[string]any{"quota_bytes": 100}), 200)
	wantStatus(t, writeFile(t, fx.s, fx.key, fx.fld.ID, "/a.bin", bytesOf(60)), 201)
	dir := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/drop")
	_, secret := fx.makeDrop(t, dir.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	refusedForAccount(t, dropUpload(t, fx.s, tok, "x.bin", bytesOf(50), nil), false, 0, 0)
}

// TestUsageMetricsReportTotals: the installation gauges sum every account.
func TestUsageMetricsReportTotals(t *testing.T) {
	t.Parallel()
	s, _, key, _, f := quotaSetup(t, 0)
	wantStatus(t, writeFile(t, s, key, f.ID, "/a.bin", strings.Repeat("a", 12)), 201)
	createUpload(t, s, key, 34, md("folder", f.ID, "name", "b.bin"))
	rec := httptest.NewRecorder()
	s.metrics.handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{"selfcloud_storage_used_bytes 12\n", "selfcloud_storage_reserved_bytes 34\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q", strings.TrimSpace(want))
		}
	}
}
