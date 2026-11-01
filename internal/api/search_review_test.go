// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/office"
	"github.com/shukiv/selfcloud/internal/search"
)

// Review fixes to content search (security review of ade087c, S1 to S4 and the missing tests). The privacy tests compare
// what each caller gets; the policy tests read the converter's calls and the worker's rows.

// zipOf returns a ZIP container holding the given entries, the way an office document is stored.
func zipOf(t *testing.T, entries map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// workerWith is a content worker with the given options changed from the fixture's.
func (fx *contentFixture) workerWith(conv search.Converter, clock *fakeClock, change func(*search.WorkerOptions)) *search.Worker {
	o := search.WorkerOptions{Store: fx.s.store, Blobs: fx.s.blobs, Conv: conv, OfficeText: true,
		TextMax: int64(fx.s.cfg.Search.TextMaxBytes), FullTextMax: int64(fx.s.cfg.Search.FullTextMaxBytes),
		CapAccount: int64(fx.s.cfg.Search.MaxTextPerAccount), CapTotal: int64(fx.s.cfg.Search.MaxTextTotal), Now: clock.Now}
	change(&o)
	return search.NewWorker(o)
}

// labelFieldID defines a restricted user label, as the account key, and returns its ID.
func (fx *contentFixture) labelFieldID(t *testing.T) string {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/label-fields", fx.key,
		map[string]any{"name": "Lead", "type": "user", "restricted": true})
	wantStatus(t, rec, 201)
	return decode[labelFieldJSON](t, rec).ID
}

// TestContentRestrictedLabelMatchesOnlyAManager: a restricted label predicate on a content search matches for a manager of
// the file. For anyone else the answer is the same bytes as when the value is not set. Mutation: restrictedMatch skipped for
// content hits lets a viewer find the file by its restricted value.
func TestContentRestrictedLabelMatchesOnlyAManager(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	lead := fx.labelFieldID(t)
	n := fx.file(t, "lead.txt", "restrictedword")
	drainText(t, fx.s.textWorker)
	query := "/api/v1/search?in=content&q=restrictedword&label=" + lead + ":" + fx.bob.ID
	before := callSession(t, fx.s, fx.annSess, "GET", query, nil)
	setLabel(t, byCred(t, fx.s, fx.key), n.ID, lead, fx.bob.ID, http.StatusNoContent)
	after := callSession(t, fx.s, fx.annSess, "GET", query, nil)
	if before.Body.String() != after.Body.String() {
		t.Fatalf("a viewer's answer changed with a restricted value:\n%s\n%s", before.Body, after.Body)
	}
	if got := contentNames(decode[testPage](t, after).Items); len(got) != 0 {
		t.Fatalf("a viewer found the file by its restricted value: %v", got)
	}
	if got := contentNames(fx.find(t, fx.bobSess, "restrictedword")); fmt.Sprint(got) != "[lead.txt]" {
		t.Fatalf("the editor, a plain reader, found %v", got)
	}
	if got := contentNames(fx.findKey(t, fx.key, "restrictedword")); fmt.Sprint(got) != "[lead.txt]" {
		t.Fatalf("the account key found %v", got)
	}
}

// TestContentNoDownloadHoldsForATokenCappedAtViewer: a read-only token is capped at viewer, so the no-download rule binds
// it: a file marked no-download gives it no content hit, and a plain file of the same folder does.
func TestContentNoDownloadHoldsForATokenCappedAtViewer(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "plain.txt", "tokenword")
	guarded := fx.file(t, "guarded.txt", "tokenword")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+guarded.ID, fx.key, map[string]any{"no_download": true}), 200)
	drainText(t, fx.s.textWorker)
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, "")
	rec := call(t, fx.s, "GET", "/api/v1/search?in=content&q=tokenword", tok, nil)
	wantStatus(t, rec, 200)
	if got := contentNames(decode[testPage](t, rec).Items); fmt.Sprint(got) != "[plain.txt]" {
		t.Fatalf("a viewer token found %v; the no-download file must give no content hit", got)
	}
}

// TestContentCursorIsRefusedUnderAnotherIdentityOrQuery: a content cursor opens only for the caller and the request that
// made it. Replayed under another user, or under another word, it is refused with invalid_request.
func TestContentCursorIsRefusedUnderAnotherIdentityOrQuery(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	for _, name := range []string{"c1.txt", "c2.txt", "c3.txt"} {
		fx.file(t, name, "cursorword")
	}
	drainText(t, fx.s.textWorker)
	first := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?in=content&q=cursorword&limit=1", nil)
	wantStatus(t, first, 200)
	cursor := decode[testPage](t, first).NextCursor
	if cursor == "" {
		t.Fatal("no cursor on the first page of three matches")
	}
	path := "/api/v1/search?in=content&q=cursorword&limit=1&cursor=" + url.QueryEscape(cursor)
	wantError(t, callSession(t, fx.s, fx.bobSess, "GET", path, nil), 400, "invalid_request")
	other := "/api/v1/search?in=content&q=otherword&limit=1&cursor=" + url.QueryEscape(cursor)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", other, nil), 400, "invalid_request")
	// The same identity and query still pages.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", path, nil), 200)
}

// TestContentSearchOfAPersonalFolderFollowsItsOwner: a personal folder's text is found by its user alone; another user with
// a grant on the team does not reach it.
func TestContentSearchOfAPersonalFolderFollowsItsOwner(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	pf, err := fx.s.store.PersonalFolder(context.Background(), fx.acc.ID, fx.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	putTyped(t, fx.s, fx.key, pf.ID, "private.txt", "text/plain", "personalword")
	drainText(t, fx.s.textWorker)
	if got := contentNames(fx.find(t, fx.bobSess, "personalword")); fmt.Sprint(got) != "[private.txt]" {
		t.Fatalf("the owner found %v in her personal folder", got)
	}
	if got := contentNames(fx.find(t, fx.annSess, "personalword")); len(got) != 0 {
		t.Fatalf("another user found %v in a personal folder", got)
	}
}

// TestContentSearchEndsWithADisabledOrDeletedUser: a session whose user is disabled or deleted gets no content hit: the
// route refuses it, and no text is read for it.
func TestContentSearchEndsWithADisabledOrDeletedUser(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "gone.txt", "endedword")
	drainText(t, fx.s.textWorker)
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/search?in=content&q=endedword", nil), 200)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key, nil), 204)
	rec := callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/search?in=content&q=endedword", nil)
	if rec.Code < 400 {
		t.Fatalf("a deleted user's session searched: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "gone.txt") {
		t.Fatalf("a deleted user's session saw the file: %s", rec.Body)
	}
}

// TestContentSearchForAnImpersonatingAdminIsTheUsersOwn: an installation admin signed in as a member searches with the
// member's reach and no more: a file the member cannot reach gives the impersonated session nothing.
func TestContentSearchForAnImpersonatingAdminIsTheUsersOwn(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	hidden := newFolder(t, fx.s, fx.accKey, fx.acc.ID, "Hidden")
	putTyped(t, fx.s, fx.accKey, hidden.ID, "hidden.txt", "text/plain", "impword")
	drainText(t, fx.s.textWorker)
	memberOwn := callSession(t, fx.s, fx.memSess, "GET", "/api/v1/search?in=content&q=impword", nil)
	wantStatus(t, memberOwn, 200)
	rec := fx.signInAs(t, fx.member.ID)
	wantStatus(t, rec, 200)
	imp := sessionFrom(t, fx.s, rec)
	got := callSession(t, fx.s, imp, "GET", "/api/v1/search?in=content&q=impword", nil)
	wantStatus(t, got, 200)
	if got.Body.String() != memberOwn.Body.String() {
		t.Fatalf("the impersonated search differs from the member's own:\n%s\n%s", got.Body, memberOwn.Body)
	}
	if strings.Contains(got.Body.String(), "hidden.txt") {
		t.Fatalf("an impersonated session found a file the member cannot reach: %s", got.Body)
	}
}

// TestUsageShowsTheSkippedTextCount: the owner sees the text the account holds and how many files were skipped for the
// account's cap; the usage answer names the cap. Text is not charged to quota.
func TestUsageShowsTheSkippedTextCount(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t, func(c *config.Config) {
		c.Search.TextMaxBytes = 1024
		c.Search.FullTextMaxBytes = 1 << 20
		c.Search.MaxTextPerAccount = 1024
	})
	body := strings.Repeat("filler ", 80) + " capword"
	fx.file(t, "one.txt", body)
	fx.file(t, "two.txt", body)
	drainText(t, fx.s.textWorker)
	rec := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/usage", fx.key, nil)
	wantStatus(t, rec, 200)
	u := decode[struct {
		Bytes   int64 `json:"search_text_bytes"`
		Cap     int64 `json:"search_text_cap"`
		Skipped int64 `json:"search_text_skipped"`
	}](t, rec)
	if u.Cap != 1024 || u.Skipped != 1 || u.Bytes <= 0 || u.Bytes > 1024 {
		t.Fatalf("usage text fields %+v; want cap 1024, one file skipped, and the first file's bytes", u)
	}
}

// TestOfficeTextOffMakesNoRequest: with search.office_text off, a document is skipped with its reason and the converter is
// not asked (review S4). Mutation: a worker that converts with OfficeText off calls the converter.
func TestOfficeTextOffMakesNoRequest(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "letter.docx", docxBytes("officeoffword"))
	conv := &fakeConverter{out: []byte("officeoffword")}
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	drainText(t, fx.workerWith(conv, clock, func(o *search.WorkerOptions) { o.OfficeText = false }))
	if conv.calls != 0 {
		t.Fatalf("the converter was asked %d times with search.office_text off", conv.calls)
	}
	if r := rowOf(t, fx.s, fileNamed(t, fx, "letter.docx")); r.State != "skipped" || r.Error != "office text search is off" {
		t.Fatalf("row %+v, want skipped for the setting", r)
	}
}

// TestTrashedDocumentIsNotSentUntilRestored: a document in the trash is not sent to the converter, and is sent after it is
// restored (review S4). Mutation: the trash test skipped in the worker sends the trashed document.
func TestTrashedDocumentIsNotSentUntilRestored(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "bin.docx", docxBytes("trashedoffice"))
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+n.ID, fx.key, nil), 204)
	conv := &fakeConverter{out: []byte("trashedoffice")}
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	w := fx.worker(conv, clock)
	drainText(t, w)
	if conv.calls != 0 {
		t.Fatalf("a trashed document was sent to the converter %d times", conv.calls)
	}
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+n.ID+"/restore", fx.key, nil), 200)
	clock.now = clock.now.Add(2 * time.Hour)
	drainText(t, w)
	if conv.calls != 1 {
		t.Fatalf("the restored document was sent %d times, want once", conv.calls)
	}
}

// TestDocumentKeepsItsTrueType: the converter is told the document's true type, read from its entries (review S4), so an
// xlsx is not sent as a Word file. Mutation: every ZIP document sent as docx.
func TestDocumentKeepsItsTrueType(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "sheet.xlsx", zipOf(t, map[string]string{"[Content_Types].xml": "<Types/>", "xl/workbook.xml": "<wb/>"}))
	conv := &fakeConverter{out: []byte("sheet")}
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	drainText(t, fx.worker(conv, clock))
	if conv.ext != "xlsx" {
		t.Fatalf("the converter was told %q for an xlsx, want xlsx", conv.ext)
	}
}

// TestOutageIsOneProbePerPause: when the converter cannot be reached, the worker makes one request per pause, however many
// documents wait. Mutation: a row-by-row retry asks once per document in each pass.
func TestOutageIsOneProbePerPause(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	for i := range 5 {
		fx.file(t, fmt.Sprintf("down-%d.docx", i), docxBytes("downword"))
	}
	conv := &fakeConverter{err: office.ErrUnreachable}
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	w := fx.worker(conv, clock)
	drainText(t, w)
	if conv.calls != 1 {
		t.Fatalf("five waiting documents made %d requests in one pass, want one probe", conv.calls)
	}
	drainText(t, w) // still paused: no request at all
	if conv.calls != 1 {
		t.Fatalf("a request was made during the pause: %d", conv.calls)
	}
	clock.now = clock.now.Add(time.Minute)
	drainText(t, w)
	if conv.calls != 2 {
		t.Fatalf("after one minute the health check made %d requests in total, want 2", conv.calls)
	}
}

// TestSearchAccountLimitHoldsAcrossCallers (review S5): one caller may search a minute, and an account ten times that. Eleven
// users of one account each search once; the eleventh is refused by the account's bucket, not its own. Mutation: the
// per-account limit removed lets the eleventh through.
func TestSearchAccountLimitHoldsAcrossCallers(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t, func(c *config.Config) { c.Search.RatePerMinute = 1 })
	for i := range 11 {
		_, sess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, fmt.Sprintf("caller%02d@example.com", i))
		rec := callSession(t, fx.s, sess, "GET", "/api/v1/search?in=content&q=rateword", nil)
		if i < 10 && rec.Code != http.StatusOK {
			t.Fatalf("caller %d refused (%d) before the account's share is spent: %s", i+1, rec.Code, rec.Body)
		}
		if i == 10 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("the eleventh caller of the account got %d; want 429 from the account's bucket", rec.Code)
		}
	}
}
