// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/sweep"
)

// replaceContent writes new contents to a file as cred, with If-Match set to etag, and returns the file.
func replaceContent(t *testing.T, fx *sharingFixture, nodeID, etag, body string) nodeJSON {
	t.Helper()
	rec := rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+nodeID+"/content", fx.key, body, map[string]string{"If-Match": etag})
	wantStatus(t, rec, 200)
	return decode[nodeJSON](t, rec)
}

// versionsOf lists the versions of a file as the account key sees them.
func versionsOf(t *testing.T, fx *sharingFixture, nodeID string) []versionJSON {
	t.Helper()
	return decode[listResponse[versionJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+nodeID+"/versions", fx.key, nil)).Items
}

// TestReplaceKeepsAVersionAndGrowsUsage: a replacement keeps the content it replaces as a version. The list
// starts with the current content, and usage grows by the new size, since the old content still counts.
func TestReplaceKeepsAVersionAndGrowsUsage(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/one.txt", "one")
	before := fx.usage(t)

	replaceContent(t, fx, f.ID, f.ETag, "twenty")

	if after := fx.usage(t); after.used-before.used != 6 || after.files != before.files {
		t.Errorf("usage %+v -> %+v, want six more bytes and no more files", before, after)
	}
	vs := versionsOf(t, fx, f.ID)
	if len(vs) != 2 || !vs[0].Current || vs[0].SHA256 != sha("twenty") || vs[0].ID != "current" {
		t.Fatalf("versions = %+v, want the current content first", vs)
	}
	if vs[1].Current || vs[1].SHA256 != sha("one") || vs[1].Size != 3 || vs[1].ReplacedAt == nil {
		t.Errorf("the earlier version = %+v, want the old content", vs[1])
	}
}

// TestVersionContentDownloadHonoursNoDownload: a version reads back the old contents. A viewer who may not
// download the file cannot download a version either.
func TestVersionContentDownloadHonoursNoDownload(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/one.txt", "one")
	replaceContent(t, fx, f.ID, f.ETag, "two")
	vs := versionsOf(t, fx, f.ID)
	ver := vs[1].ID

	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+f.ID+"/versions/"+ver+"/content", fx.key, nil)
	wantStatus(t, rec, 200)
	if rec.Body.String() != "one" {
		t.Errorf("version content = %q, want the old one", rec.Body)
	}

	fx.grant(t, f.ID, fx.bob.ID, "viewer")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+f.ID, fx.key, map[string]any{"no_download": true}), 200)
	wantError(t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/nodes/"+f.ID+"/versions/"+ver+"/content", nil),
		403, apierr.DownloadRestricted)
}

// TestRestoreVersionSwapsContentAndHonoursIfMatch: the chosen version becomes the current content, with a new
// ETag, and the content it replaces becomes a version. A stale If-Match is refused, a missing one is 428, a
// viewer is 403 and a contributor is 200.
func TestRestoreVersionSwapsContentAndHonoursIfMatch(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/one.txt", "one")
	first := f.ETag
	mid := replaceContent(t, fx, f.ID, f.ETag, "two")
	ver := versionsOf(t, fx, f.ID)[1].ID
	url := "/api/v1/nodes/" + f.ID + "/versions/" + ver + "/restore"

	fx.grant(t, f.ID, fx.bob.ID, "viewer")
	wantStatus(t, rawSession(t, fx.s, fx.bobSess, "POST", url, "", map[string]string{"If-Match": mid.ETag}), 403)
	wantStatus(t, rawCall(t, fx.s, "POST", url, fx.key, "", nil), 428)
	wantStatus(t, rawCall(t, fx.s, "POST", url, fx.key, "", map[string]string{"If-Match": first}), 412)

	rec := rawCall(t, fx.s, "POST", url, fx.key, "", map[string]string{"If-Match": mid.ETag})
	wantStatus(t, rec, 200)
	back := decode[nodeJSON](t, rec)
	if back.SHA256 != sha("one") || back.ETag == mid.ETag {
		t.Fatalf("restored = %+v, want the old contents under a new ETag", back)
	}
	if got := rawCall(t, fx.s, "GET", "/api/v1/nodes/"+f.ID+"/content", fx.key, "", nil).Body.String(); got != "one" {
		t.Errorf("content after restore = %q", got)
	}
	// The current content is the restored one; the content it replaced is a version; and the chosen version
	// keeps its row, so it is still in the history.
	vs := versionsOf(t, fx, f.ID)
	if len(vs) != 3 || vs[0].SHA256 != sha("one") || vs[1].SHA256 != sha("two") || vs[2].ID != ver {
		t.Errorf("versions after restore = %+v, want the restored one current, two kept, and the chosen row still there", vs)
	}
	wantStatus(t, rawCall(t, fx.s, "POST", "/api/v1/nodes/"+f.ID+"/versions/ver_nosuchversion000000000000/restore",
		fx.key, "", map[string]string{"If-Match": back.ETag}), 404)
}

// TestRestoreVersionByViewerIsForbidden is the viewer case of the restore on its own: the role check comes
// before the If-Match check, and a viewer gets 403 with a valid ETag.
func TestRestoreVersionByViewerIsForbidden(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/one.txt", "one")
	replaceContent(t, fx, f.ID, f.ETag, "two")
	ver := versionsOf(t, fx, f.ID)[1].ID
	fx.grant(t, f.ID, fx.bob.ID, "viewer")
	cur := fx.nodeOf(t, f.ID).ETag
	rec := rawSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+f.ID+"/versions/"+ver+"/restore", "",
		map[string]string{"If-Match": cur})
	wantError(t, rec, 403, apierr.Forbidden)
}

// newVersionedServer is a server whose versions.max_count is small, so pruning is easy to see. It returns the
// server, the account's ID, the account key and a folder.
func newVersionedServer(t *testing.T, maxCount int) (*Server, string, string, string) {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Versions.MaxCount = maxCount })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	return s, acc.ID, key, f.ID
}

// TestKeepForeverSurvivesCountPruning: with a count of two, the non-kept versions beyond two go, oldest first,
// and a version kept forever stays however old it is. Kept versions do not count toward the limit, and the
// usage falls with each pruned version.
func TestKeepForeverSurvivesCountPruning(t *testing.T) {
	t.Parallel()
	s, accID, key, fld := newVersionedServer(t, 2)
	f := nodeOf(t, writeFile(t, s, key, fld, "/a.txt", "v0v0"))
	etag := f.ETag
	list := func() []versionJSON {
		return decode[listResponse[versionJSON]](t, call(t, s, "GET", "/api/v1/nodes/"+f.ID+"/versions", key, nil)).Items
	}
	rec := rawCall(t, s, "PUT", "/api/v1/nodes/"+f.ID+"/content", key, "v1v1", map[string]string{"If-Match": etag})
	wantStatus(t, rec, 200)
	etag = decode[nodeJSON](t, rec).ETag
	kept := list()[1]
	wantStatus(t, call(t, s, "PATCH", "/api/v1/nodes/"+f.ID+"/versions/"+kept.ID, key,
		map[string]any{"keep_forever": true}), 200)

	for _, body := range []string{"v2v2", "v3v3", "v4v4"} {
		rec := rawCall(t, s, "PUT", "/api/v1/nodes/"+f.ID+"/content", key, body, map[string]string{"If-Match": etag})
		wantStatus(t, rec, 200)
		etag = decode[nodeJSON](t, rec).ETag
	}

	got := list()
	var shas []string
	for _, v := range got {
		shas = append(shas, v.SHA256)
	}
	// Current v4, kept v0, and the two newest non-kept versions v2 and v3. v1 was pruned.
	if len(got) != 4 || !got[len(got)-1].KeepForever || hasSHA(shas, sha("v1v1")) ||
		!hasSHA(shas, sha("v2v2")) || !hasSHA(shas, sha("v3v3")) {
		t.Errorf("versions = %v, want v4 current, v0 kept, v2 and v3 kept by the count, v1 pruned", shas)
	}
	// Five contents of four bytes were written; v1 went, so four remain: the current one and three versions.
	if u := usageOf(t, s, key, accID); u.UsedBytes != 16 {
		t.Errorf("usage = %d bytes, want 16: the current content, and three versions of four bytes", u.UsedBytes)
	}
}

func hasSHA(shas []string, want string) bool {
	for _, s := range shas {
		if s == want {
			return true
		}
	}
	return false
}

// TestAgePruningAdjustsUsageAndKeepsKeptVersions: the sweep prunes versions older than versions.max_age, and
// not the kept ones, and each pruned version comes off the usage.
func TestAgePruningAdjustsUsageAndKeepsKeptVersions(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/one.txt", "aa")
	etag := f.ETag
	for _, body := range []string{"bb", "cc", "dd"} {
		etag = replaceContent(t, fx, f.ID, etag, body).ETag
	}
	vs := versionsOf(t, fx, f.ID)
	if len(vs) != 4 {
		t.Fatalf("versions = %d, want four (three versions and the current)", len(vs))
	}
	keep := vs[len(vs)-1] // the first content, the oldest version
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+f.ID+"/versions/"+keep.ID, fx.key,
		map[string]any{"keep_forever": true}), 200)
	before := fx.usage(t)

	sw := sweep.New(sweep.Options{Store: fx.s.store, Blobs: fx.s.blobs, Interval: time.Hour,
		VersionsMaxCount: 100, VersionsMaxAge: 30 * 24 * time.Hour})
	res, err := sw.Once(context.Background(), store.Now().Add(31*24*time.Hour))
	if err != nil || res.Versions != 2 {
		t.Fatalf("pass = %+v, %v; want two versions pruned", res, err)
	}
	after := versionsOf(t, fx, f.ID)
	if len(after) != 2 || after[1].ID != keep.ID {
		t.Errorf("versions after the age pass = %+v, want the current and the kept one", after)
	}
	if got := fx.usage(t).used; before.used-got != 4 {
		t.Errorf("usage fell by %d, want the two pruned versions' four bytes", before.used-got)
	}
}

// TestDeletingTheCurrentVersionIsRefused: the current content is not a version, so it cannot be deleted by
// version; the answer is a conflict that says what to do instead.
func TestDeletingTheCurrentVersionIsRefused(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/one.txt", "one")
	replaceContent(t, fx, f.ID, f.ETag, "two")
	wantError(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+f.ID+"/versions/current", fx.key, nil), 409, apierr.Conflict)
	if got := len(versionsOf(t, fx, f.ID)); got != 2 {
		t.Errorf("versions = %d after a refused delete, want 2", got)
	}
}

// TestCopyHasNoVersions: a copy of a file starts with its current content only.
func TestCopyHasNoVersions(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/one.txt", "one")
	replaceContent(t, fx, f.ID, f.ETag, "two")
	rc := call(t, fx.s, "POST", "/api/v1/nodes/"+f.ID+"/copy", fx.key, map[string]any{"name": "copy.txt"})
	wantStatus(t, rc, 201)
	cp := decode[nodeJSON](t, rc)
	if vs := versionsOf(t, fx, cp.ID); len(vs) != 1 || !vs[0].Current {
		t.Errorf("the copy's versions = %+v, want the current content only", vs)
	}
}

// TestBlobCollectionKeepsVersionBlobsAndFreesPrunedOnes: a version's blob stays until the version goes; when a
// deleted version's blob has no other reference, the collector removes it after the grace period.
func TestBlobCollectionKeepsVersionBlobsAndFreesPrunedOnes(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/one.txt", "one")
	// The fixture's own two files are on disk already: the baseline is taken before the replace.
	base := blobFiles(t, fx.s)
	replaceContent(t, fx, f.ID, f.ETag, "two")
	if _, err := sweeper(fx.s, 0).Once(context.Background(), store.Now()); err != nil {
		t.Fatal(err)
	}
	if n := blobFiles(t, fx.s); n != base+1 {
		t.Fatalf("blob files = %d, want %d: the version's blob is kept, with the current one", n, base+1)
	}

	ver := versionsOf(t, fx, f.ID)[1].ID
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+f.ID+"/versions/"+ver, fx.key, nil), 204)
	res, err := sweeper(fx.s, 0).Once(context.Background(), store.Now())
	if err != nil || res.Orphaned != 1 || res.Removed != 1 {
		t.Fatalf("pass after the delete = %+v, %v; want the version's blob collected", res, err)
	}
	if n := blobFiles(t, fx.s); n != base {
		t.Errorf("blob files = %d, want %d", n, base)
	}
}

// TestRecountMatchesTheCountersAfterAMix: after replaces, a restore, a trash, a purge and a version delete, a
// full recount gives the same usage the counters kept as they went.
func TestRecountMatchesTheCountersAfterAMix(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/one.txt", "one")
	etag := replaceContent(t, fx, f.ID, f.ETag, "two").ETag
	ver := versionsOf(t, fx, f.ID)[1].ID
	wantStatus(t, rawCall(t, fx.s, "POST", "/api/v1/nodes/"+f.ID+"/versions/"+ver+"/restore", fx.key, "",
		map[string]string{"If-Match": etag}), 200)
	replaceContent(t, fx, f.ID, versionsOfCurrentETag(t, fx, f.ID), "three!")
	doomed := putNode(t, fx.s, fx.key, fx.fld.ID, "/doomed.txt", "doomed")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+doomed.ID, fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+doomed.ID, fx.key, nil), 204)

	before := fx.usage(t)
	var counted store.Usage
	err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		_, after, err := tx.RecountAccount(context.Background(), fx.acc.ID)
		counted = after
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if counted.Bytes != before.used || counted.Files != before.files {
		t.Errorf("recount = %d bytes and %d files, counters kept %d and %d", counted.Bytes, counted.Files, before.used, before.files)
	}
}

// versionsOfCurrentETag returns the current ETag of a file, as the key reads it.
func versionsOfCurrentETag(t *testing.T, fx *sharingFixture, nodeID string) string {
	t.Helper()
	return fx.nodeOf(t, nodeID).ETag
}

// TestRestoreVersionRecheckRefusesAfterRoleRevoked: a contributor whose role is revoked between the early check
// and the transaction is refused, and the content stays as it was.
func TestRestoreVersionRecheckRefusesAfterRoleRevoked(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/one.txt", "one")
	replaceContent(t, fx, f.ID, f.ETag, "two")
	ver := versionsOf(t, fx, f.ID)[1].ID
	fx.grant(t, f.ID, fx.ann.ID, "contributor")
	cur := fx.nodeOf(t, f.ID).ETag
	onceBefore(fx.s, func() { fx.revoke(t, f.ID, fx.ann.ID) })

	rec := rawSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+f.ID+"/versions/"+ver+"/restore", "",
		map[string]string{"If-Match": cur})
	refused(t, rec)
	if got := rawCall(t, fx.s, "GET", "/api/v1/nodes/"+f.ID+"/content", fx.key, "", nil).Body.String(); got != "two" {
		t.Errorf("content after a refused restore = %q, want two", got)
	}
}

// TestVersionUpdateIsAuditedAndLabelled: a label is checked for length, set, and the update is audited.
func TestVersionUpdateIsAuditedAndLabelled(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/one.txt", "one")
	replaceContent(t, fx, f.ID, f.ETag, "two")
	ver := versionsOf(t, fx, f.ID)[1].ID
	label := strings.Repeat("l", 101)
	wantError(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+f.ID+"/versions/"+ver, fx.key,
		map[string]any{"label": label}), 400, apierr.InvalidRequest)
	rec := call(t, fx.s, "PATCH", "/api/v1/nodes/"+f.ID+"/versions/"+ver, fx.key,
		map[string]any{"label": "before the rename"})
	wantStatus(t, rec, 200)
	if got := decode[versionJSON](t, rec); got.Label == nil || *got.Label != "before the rename" {
		t.Errorf("label = %v", got.Label)
	}
	log := decode[listResponse[auditJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil))
	found := false
	for _, e := range log.Items {
		if e.Action == "node.version.update" && e.Target == f.ID {
			found = true
		}
	}
	if !found {
		t.Error("no node.version.update audit entry")
	}
}

// TestUploadReplaceKeepsAVersion: a resumable upload that completes over an existing file with on_conflict
// replace keeps the file's old content as a version, and counts the new content in full.
func TestUploadReplaceKeepsAVersion(t *testing.T) {
	t.Parallel()
	s, key, accID, f := uploadSetup(t)
	putNode(t, s, key, f.ID, "/same.txt", "old")
	old := nodeIn(t, s, key, f.ID, "same.txt")
	before := usageOf(t, s, key, accID)
	// A link to the old content dies when the upload replaces it.
	oldLink := makeLink(t, s, key, old, "")
	wantStatus(t, rawCall(t, s, "GET", urlPath(t, oldLink.URL), "", "", nil), 200)

	upl := uploadID(t, createUpload(t, s, key, 8, md("folder", f.ID, "name", "same.txt", "on_conflict", "replace")))
	wantStatus(t, piece(t, s, key, upl, 0, "replaced", nil), 204)
	wantError(t, rawCall(t, s, "GET", urlPath(t, oldLink.URL), "", "", nil), 404, apierr.NotFound)

	if got := usageOf(t, s, key, accID); got.UsedBytes-before.UsedBytes != 8 {
		t.Errorf("usage grew by %d, want the new eight bytes", got.UsedBytes-before.UsedBytes)
	}
	vs := decode[listResponse[versionJSON]](t, call(t, s, "GET", "/api/v1/nodes/"+old+"/versions", key, nil)).Items
	if len(vs) != 2 || vs[0].SHA256 != sha("replaced") || vs[1].SHA256 != sha("old") {
		t.Errorf("versions = %+v, want the replaced content current and the old content kept", vs)
	}
}

// TestUploadReplaceUnderQuotaCountsItsBytesOnce: a 60-byte resumable upload that replaces a 10-byte file
// on a 100-byte quota completes, and usage ends at 70 (the kept 10 bytes and the new 60). The reservation
// of 60 is released as the bytes are counted, so they are not checked twice.
func TestUploadReplaceUnderQuotaCountsItsBytesOnce(t *testing.T) {
	t.Parallel()
	s, _, key, accID, f := quotaSetup(t, 100)
	putNode(t, s, key, f.ID, "/same.txt", "0123456789")

	upl := uploadID(t, createUpload(t, s, key, 60, md("folder", f.ID, "name", "same.txt", "on_conflict", "replace")))
	data := strings.Repeat("r", 60)
	wantStatus(t, piece(t, s, key, upl, 0, data, nil), 204)

	got := usageOf(t, s, key, accID)
	if got.UsedBytes != 70 || got.ReservedBytes != 0 {
		t.Errorf("usage = %d used and %d reserved, want 70 and 0", got.UsedBytes, got.ReservedBytes)
	}
}

// TestRestoreKeepsTheChosenVersionKept: a contributor restores a version kept forever. The version is still
// kept afterwards, with its label, and a later replace does not make it prunable even at a count of one.
func TestRestoreKeepsTheChosenVersionKept(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.s.cfg.Versions.MaxCount = 1
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/one.txt", "one")
	replaceContent(t, fx, f.ID, f.ETag, "two")
	kept := versionsOf(t, fx, f.ID)[1]
	label := "first draft"
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+f.ID+"/versions/"+kept.ID, fx.key,
		map[string]any{"label": label, "keep_forever": true}), 200)

	fx.grant(t, f.ID, fx.ann.ID, "contributor")
	cur := fx.nodeOf(t, f.ID).ETag
	wantStatus(t, rawSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+f.ID+"/versions/"+kept.ID+"/restore", "",
		map[string]string{"If-Match": cur}), 200)

	check := func(when string) {
		t.Helper()
		var found *versionJSON
		vs := versionsOf(t, fx, f.ID)
		for i := range vs {
			if vs[i].ID == kept.ID {
				found = &vs[i]
			}
		}
		if found == nil || !found.KeepForever || found.Label == nil || *found.Label != label {
			t.Errorf("%s: the chosen version = %+v, want it still kept and labelled", when, found)
		}
	}
	check("after the restore")

	// Two replacements with a count of one: the non-kept versions go, the kept one stays.
	etag := fx.nodeOf(t, f.ID).ETag
	etag = replaceContent(t, fx, f.ID, etag, "three").ETag
	replaceContent(t, fx, f.ID, etag, "four")
	check("after two replacements")
}

// TestRestoreVersionChecksTheQuota: the restored version counts twice once it is current, so a restore that
// would take usage past the quota is refused with the content unchanged.
func TestRestoreVersionChecksTheQuota(t *testing.T) {
	t.Parallel()
	s, _, key, _, f := quotaSetup(t, 100)
	first := nodeOf(t, writeFile(t, s, key, f.ID, "/q.txt", strings.Repeat("a", 60)))
	rec := rawCall(t, s, "PUT", "/api/v1/nodes/"+first.ID+"/content", key, strings.Repeat("b", 30),
		map[string]string{"If-Match": first.ETag})
	wantStatus(t, rec, 200)
	cur := decode[nodeJSON](t, rec)
	ver := decode[listResponse[versionJSON]](t, call(t, s, "GET", "/api/v1/nodes/"+first.ID+"/versions", key, nil)).Items[1].ID

	rec = rawCall(t, s, "POST", "/api/v1/nodes/"+first.ID+"/versions/"+ver+"/restore", key, "",
		map[string]string{"If-Match": cur.ETag})
	quotaDetails(t, rec, apierr.QuotaExceeded, 60, 10, "quota")
	if got := rawCall(t, s, "GET", "/api/v1/nodes/"+first.ID+"/content", key, "", nil).Body.String(); got != strings.Repeat("b", 30) {
		t.Errorf("content after a refused restore = %d bytes, want the 30 it had", len(got))
	}
}

// TestKeepForeverIsCappedPerFile: with versions.max_kept at two, a third version cannot be kept forever until
// one of the two is unmarked. The refusal is a conflict that names the limit.
func TestKeepForeverIsCappedPerFile(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.s.cfg.Versions.MaxKept = 2
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/k.txt", "v0")
	etag := f.ETag
	for _, body := range []string{"v1", "v2", "v3"} {
		etag = replaceContent(t, fx, f.ID, etag, body).ETag
	}
	vs := versionsOf(t, fx, f.ID)[1:]
	keep := func(id string, on bool) *httptest.ResponseRecorder {
		return call(t, fx.s, "PATCH", "/api/v1/nodes/"+f.ID+"/versions/"+id, fx.key, map[string]any{"keep_forever": on})
	}
	wantStatus(t, keep(vs[0].ID, true), 200)
	wantStatus(t, keep(vs[1].ID, true), 200)
	e := wantError(t, keep(vs[2].ID, true), 409, apierr.Conflict)
	if e.Details["max_kept"] != float64(2) {
		t.Errorf("details = %v, want the limit of two named", e.Details)
	}
	wantStatus(t, keep(vs[0].ID, false), 200)
	wantStatus(t, keep(vs[2].ID, true), 200)
}
