// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/office"
	"github.com/shukiv/selfcloud/internal/search"
	"github.com/shukiv/selfcloud/internal/store"
)

// Content search through the route (slice q3, decision 46): what a caller finds in the text of files, what each caller
// may see of it, and how the text follows the file through its life. The worker runs here in the test, one step at a
// time, so each test says when the text is extracted.

// testHit is one result of a search page, as the route writes it.
type testHit struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Snippet *struct {
		Text   string   `json:"text"`
		Ranges [][2]int `json:"ranges"`
	} `json:"snippet"`
}

// testPage is a search page.
type testPage struct {
	Items      []testHit `json:"items"`
	NextCursor string    `json:"next_cursor"`
}

// contentFixture is an account with one team folder, a viewer, a commenter and an editor, each granted on the folder.
type contentFixture struct {
	s       *Server
	key     string
	acc     accountJSON
	fld     folderJSON
	ann     userResponse
	annSess testSession
	cam     userResponse
	camSess testSession
	bob     userResponse
	bobSess testSession
}

func newContentFixture(t *testing.T, mutate ...func(*config.Config)) *contentFixture {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{}, mutate...)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fx := &contentFixture{s: s, key: key, acc: acc}
	fx.fld = newFolder(t, s, key, acc.ID, "Team")
	fx.ann, fx.annSess = verifiedUser(t, s, key, acc.ID, "ann@example.com")
	fx.cam, fx.camSess = verifiedUser(t, s, key, acc.ID, "cam@example.com")
	fx.bob, fx.bobSess = verifiedUser(t, s, key, acc.ID, "bob@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/nodes/"+fx.fld.RootNodeID+"/permissions/"+fx.ann.ID, key, map[string]any{"role": "viewer"}), 200)
	wantStatus(t, call(t, s, "PUT", "/api/v1/nodes/"+fx.fld.RootNodeID+"/permissions/"+fx.cam.ID, key, map[string]any{"role": "commenter"}), 200)
	wantStatus(t, call(t, s, "PUT", "/api/v1/nodes/"+fx.fld.RootNodeID+"/permissions/"+fx.bob.ID, key, map[string]any{"role": "editor"}), 200)
	return fx
}

// file adds a text file to the team folder and returns its node.
func (fx *contentFixture) file(t *testing.T, name, body string) nodeJSON {
	t.Helper()
	return putTyped(t, fx.s, fx.key, fx.fld.ID, name, "text/plain", body)
}

// find runs a content search as a session and returns the page's hits.
func (fx *contentFixture) find(t *testing.T, sess testSession, q string) []testHit {
	t.Helper()
	rec := callSession(t, fx.s, sess, "GET", "/api/v1/search?in=content&q="+strings.ReplaceAll(q, " ", "+"), nil)
	wantStatus(t, rec, 200)
	return decode[testPage](t, rec).Items
}

// findKey runs a content search with a credential (an account key or a token).
func (fx *contentFixture) findKey(t *testing.T, cred, q string) []testHit {
	t.Helper()
	rec := call(t, fx.s, "GET", "/api/v1/search?in=content&q="+strings.ReplaceAll(q, " ", "+"), cred, nil)
	wantStatus(t, rec, 200)
	return decode[testPage](t, rec).Items
}

// contentNames returns the names of hits, in order.
func contentNames(hits []testHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Name
	}
	return out
}

// drainText runs the content worker until it has nothing to do.
func drainText(t *testing.T, w *search.Worker) {
	t.Helper()
	for i := 0; i < 1_000_000; i++ { // returns as soon as the worker is idle; the bound only stops a loop that never ends
		did, err := w.Step(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !did {
			return
		}
	}
	t.Fatal("the content worker did not finish")
}

// fakeClock is the worker's clock: a test moves it forward to reach a retry.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

// fakeConverter stands in for Collabora. It records what it was asked, and answers with out, or with err.
type fakeConverter struct {
	calls  int
	ext    string
	format string
	out    []byte
	err    error
	// build is the converter's build hash, which is part of its key; "" is the fake's first build.
	build string
	// cut, when set, cancels the caller's context during the request, as a shutdown does, and waits for it.
	cut context.CancelFunc
}

// Capabilities is the fake's build: a fixed key, so a refusal is sticky under it.
func (f *fakeConverter) Capabilities(context.Context) (office.Caps, error) {
	hash := f.build
	if hash == "" {
		hash = "fake"
	}
	return office.Caps{Available: true, Version: "fake-26.4", VersionHash: hash}, nil
}

func (f *fakeConverter) ConvertBulk(ctx context.Context, src io.Reader, size int64, ext, format string, dst io.Writer) error {
	f.calls++
	f.ext, f.format = ext, format
	if f.cut != nil {
		// The request is in flight and the process is stopping: the caller's context ends before an answer comes.
		f.cut()
		<-ctx.Done()
		return ctx.Err()
	}
	if _, err := io.Copy(io.Discard, io.LimitReader(src, size)); err != nil {
		return err
	}
	if f.err != nil {
		return f.err
	}
	if _, err := dst.Write(f.out); err != nil {
		// The client wraps the writer's error as an outage; the worker ignores it when the writer is full.
		return fmt.Errorf("%w: write", office.ErrUnavailable)
	}
	return nil
}

// textRow is the content row of a file, read from the store.
type textRow struct {
	State     string
	Error     string
	Attempts  int64
	Truncated int64
}

// rowOf reads the content row of the file nodeID.
func rowOf(t *testing.T, s *Server, nodeID string) textRow {
	t.Helper()
	var r textRow
	err := s.store.Read(context.Background(), func(q store.Querier) error {
		return q.QueryRowContext(context.Background(), `SELECT st.state, st.error, st.attempts, st.truncated
			FROM nodes n JOIN search_texts st ON st.blob_id = n.blob_id WHERE n.id = ?`, nodeID).
			Scan(&r.State, &r.Error, &r.Attempts, &r.Truncated)
	})
	if err != nil {
		t.Fatalf("content row of %s: %v", nodeID, err)
	}
	return r
}

// docxBytes is a file whose content is a Word document: a real ZIP container with the OOXML entries; the true
// type is read from the entries. Its name is irrelevant: the content decides what it is.
func docxBytes(text string) string {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": text} {
		w, err := zw.Create(name)
		if err != nil {
			panic(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.String()
}

// TestContentSearchFindsHebrewAndArabicWords: a term matches the start of a word, with niqqud and without, in Hebrew and
// Arabic. A word with a prefix attached (a preposition, an article) is a different word, so it is not matched: the
// content index is words only (decision 46). The rows are the sample of the slice q proposal.
func TestContentSearchFindsHebrewAndArabicWords(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "niqqud.txt", "שָׁלוֹם לכולם")
	fx.file(t, "report-he.txt", "דוח שנתי 2026")
	fx.file(t, "house.txt", "הדוחות של בית הספר")
	fx.file(t, "inhouse.txt", "בבית")
	fx.file(t, "report-ar.txt", "تقرير سنوي")
	fx.file(t, "article-ar.txt", "التقرير النهائي")
	drainText(t, fx.s.textWorker)

	cases := []struct {
		q    string
		want string
	}{
		{"שלום", "[niqqud.txt]"},        // the niqqud form matches the plain query
		{"דוח", "[report-he.txt]"},      // "הדוחות" is another word, so it does not match
		{"בית", "[house.txt]"},          // "בבית" is another word: word search, not substring
		{"תקרא", "[]"},                  // no such word
		{"تقرير", "[report-ar.txt]"},    // Arabic, the word alone
		{"التقرير", "[article-ar.txt]"}, // the article form is its own word
		{"שלום לכולם", "[niqqud.txt]"},  // two terms: both in the text
		{"שלום תקרא", "[]"},             // one term missing: no hit
	}
	for _, c := range cases {
		if got := fmt.Sprint(contentNames(fx.find(t, fx.annSess, c.q))); got != c.want {
			t.Errorf("q=%q: found %s, want %s", c.q, got, c.want)
		}
	}
}

// TestContentSnippetIsTextAndRanges: a snippet is plain text with the byte ranges of the matched words. Markup in a file is
// text, and the response carries no markup.
func TestContentSnippetIsTextAndRanges(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "markup.txt", `<img src=x onerror=alert(1)> the report`)
	drainText(t, fx.s.textWorker)
	hits := fx.find(t, fx.annSess, "report")
	if len(hits) != 1 || hits[0].Snippet == nil {
		t.Fatalf("hits %+v", hits)
	}
	sn := hits[0].Snippet
	if !strings.Contains(sn.Text, "<img src=x onerror=alert(1)>") {
		t.Errorf("snippet text %q lost the file's text", sn.Text)
	}
	if len(sn.Ranges) != 1 || sn.Text[sn.Ranges[0][0]:sn.Ranges[0][1]] != "report" {
		t.Errorf("ranges %v do not mark the word in %q", sn.Ranges, sn.Text)
	}
}

// TestContentSearchIsPaged: a content search pages with a cursor, and no hit comes back twice.
func TestContentSearchIsPaged(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	want := map[string]bool{}
	for i := 0; i < 5; i++ {
		want[fx.file(t, fmt.Sprintf("p%d.txt", i), "pagedword").Name] = true
	}
	drainText(t, fx.s.textWorker)
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 10; page++ {
		url := "/api/v1/search?in=content&q=pagedword&limit=2"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		rec := callSession(t, fx.s, fx.annSess, "GET", url, nil)
		wantStatus(t, rec, 200)
		p := decode[testPage](t, rec)
		for _, h := range p.Items {
			if seen[h.Name] {
				t.Fatalf("hit %s came back twice", h.Name)
			}
			seen[h.Name] = true
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("pages found %v, want %v", seen, want)
	}
}

// TestContentPrivacyFollowsTheReachAndTheNoDownloadRule: a caller finds a file's text only where the caller reaches the
// node and may download it. A viewer or a commenter of a no-download file gets no hit from it, while its name stays visible
// to them as it always is. An editor and the account key see both files; another account sees neither.
func TestContentPrivacyFollowsTheReachAndTheNoDownloadRule(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "open.txt", "secretword open")
	closed := fx.file(t, "closed.txt", "secretword closed")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+closed.ID, fx.key, map[string]any{"no_download": true}), 200)
	drainText(t, fx.s.textWorker)
	_, otherKey := newAccount(t, fx.s, adminKey(t, fx.s), "Other")

	cases := []struct {
		who  string
		got  []string
		want string
	}{
		{"viewer", contentNames(fx.find(t, fx.annSess, "secretword")), "[open.txt]"},
		{"commenter", contentNames(fx.find(t, fx.camSess, "secretword")), "[open.txt]"},
		{"editor", contentNames(fx.find(t, fx.bobSess, "secretword")), "[open.txt closed.txt]"},
		{"account key", contentNames(fx.findKey(t, fx.key, "secretword")), "[open.txt closed.txt]"},
		{"another account's key", contentNames(fx.findKey(t, otherKey, "secretword")), "[]"},
	}
	for _, c := range cases {
		if got := fmt.Sprint(c.got); got != c.want {
			t.Errorf("%s: found %s, want %s", c.who, got, c.want)
		}
	}
	// The viewer still finds the closed file by its name, and that hit carries no snippet.
	rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?q=closed", nil)
	wantStatus(t, rec, 200)
	p := decode[testPage](t, rec)
	if len(p.Items) != 1 || p.Items[0].ID != closed.ID || p.Items[0].Snippet != nil {
		t.Errorf("the viewer's name search of the closed file: %+v", p.Items)
	}
}

// TestNoDownloadHitLooksLikeNoMatch: for a viewer, a no-download file whose text matches gives the same page as one whose
// text does not match. The answer carries nothing of the text.
func TestNoDownloadHitLooksLikeNoMatch(t *testing.T) {
	t.Parallel()
	viewerPage := func(closedText string) []string {
		fx := newContentFixture(t)
		fx.file(t, "open.txt", "plainword")
		closed := fx.file(t, "closed.txt", closedText)
		wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+closed.ID, fx.key, map[string]any{"no_download": true}), 200)
		drainText(t, fx.s.textWorker)
		return contentNames(fx.find(t, fx.annSess, "plainword"))
	}
	withMatch := viewerPage("plainword secretword")
	withoutMatch := viewerPage("nothing here")
	if fmt.Sprint(withMatch) != fmt.Sprint(withoutMatch) {
		t.Errorf("the viewer's answer depends on the restricted text: %v vs %v", withMatch, withoutMatch)
	}
}

// TestContentLifecycle: the text follows the file. A new version replaces the old text; a restored version brings it back;
// a rename and a move keep it; trashing hides it, and restoring from the trash shows it again; a copy finds its own text;
// purging the file removes it from the index.
func TestContentLifecycle(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "life.txt", "alphaword")
	drainText(t, fx.s.textWorker)
	mustFind := func(step, q string, want int) {
		t.Helper()
		if hits := fx.find(t, fx.bobSess, q); len(hits) != want {
			t.Fatalf("%s: %q found %d, want %d (%v)", step, q, len(hits), want, contentNames(hits))
		}
	}
	mustFind("created", "alphaword", 1)

	wantStatus(t, rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+n.ID+"/content", fx.key, "betaword",
		map[string]string{"Content-Type": "text/plain", "If-Match": etagOf(t, fx, n.ID)}), 200)
	drainText(t, fx.s.textWorker)
	mustFind("new version", "alphaword", 0)
	mustFind("new version", "betaword", 1)

	vers := call(t, fx.s, "GET", "/api/v1/nodes/"+n.ID+"/versions", fx.key, nil)
	wantStatus(t, vers, 200)
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(vers.Body.Bytes(), &list); err != nil || len(list.Items) == 0 {
		t.Fatalf("versions: %v %s", err, vers.Body.String())
	}
	wantStatus(t, rawCall(t, fx.s, "POST", "/api/v1/nodes/"+n.ID+"/versions/"+list.Items[len(list.Items)-1].ID+"/restore",
		fx.key, "", map[string]string{"If-Match": etagOf(t, fx, n.ID)}), 200)
	drainText(t, fx.s.textWorker)
	mustFind("restored version", "alphaword", 1)
	mustFind("restored version", "betaword", 0)

	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+n.ID, fx.key, map[string]any{"name": "renamed.txt"}), 200)
	mustFind("renamed", "alphaword", 1)

	dir := newDirectory(t, fx.s, fx.key, fx.fld.ID, "Sub")
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+n.ID+"/move", fx.key, map[string]any{"folder": fx.fld.ID, "parent": dir.ID}), 200)
	mustFind("moved", "alphaword", 1)

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+n.ID, fx.key, nil), 204)
	mustFind("trashed", "alphaword", 0)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+n.ID+"/restore", fx.key, nil), 200)
	mustFind("restored from trash", "alphaword", 1)

	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+n.ID+"/copy", fx.key, map[string]any{"name": "copy.txt"}), 201)
	drainText(t, fx.s.textWorker)
	mustFind("copied", "alphaword", 2)

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+n.ID, fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+n.ID, fx.key, nil), 204)
	mustFind("purged", "alphaword", 1) // the copy keeps the blob, so its text stays; the purged file's name is gone
}

// etagOf returns the current ETag of a node, which a write's If-Match must carry.
func etagOf(t *testing.T, fx *contentFixture, nodeID string) string {
	t.Helper()
	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+nodeID, fx.key, nil)
	wantStatus(t, rec, 200)
	return decode[nodeJSON](t, rec).ETag
}

// newDirectory makes a directory under the folder's root and returns it.
func newDirectory(t *testing.T, s *Server, key, fld, name string) nodeJSON {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/folders/"+fld+"/directories", key, map[string]any{"path": "/" + name})
	wantStatus(t, rec, 201)
	return decode[nodeJSON](t, rec)
}

// TestContentHostileFiles: the content decides what a file is, not its name. Binary is not text, whatever it is called; a
// document with no converter is skipped and its name stays searchable; a text file with an odd name is searched; NUL and
// bad UTF-8 are not text; a text over the size limit is cut, and a file over the source limit is not read at all.
func TestContentHostileFiles(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t, func(c *config.Config) {
		c.Search.TextMaxBytes = 1024
		c.Search.FullTextMaxBytes = 4096
	})
	fx.file(t, "photo.txt", "\x89PNG\r\n\x1a\nfakeword")
	fx.file(t, "data.bin", "binaryword plain text in a bin")
	fx.file(t, "nul.txt", "nulword\x00after")
	fx.file(t, "bad.txt", "badword\xff")
	fx.file(t, "scan.docx", docxBytes("officeword inside"))
	fx.file(t, "big.txt", strings.Repeat("a", 5000)+" bigword")
	fx.file(t, "long.txt", "longword "+strings.Repeat("x ", 2000)+" tailword")
	fx.file(t, "huge.txt", strings.Repeat("y", 8000)+" hugeword")
	drainText(t, fx.s.textWorker)

	found := func(q string) string { return fmt.Sprint(contentNames(fx.find(t, fx.bobSess, q))) }
	if got := found("binaryword"); got != "[data.bin]" {
		t.Errorf("text in a file named .bin: %s", got)
	}
	for _, q := range []string{"fakeword", "nulword", "badword", "officeword", "bigword", "tailword", "hugeword"} {
		if got := found(q); got != "[]" {
			t.Errorf("q=%q found %s, want nothing", q, got)
		}
	}
	if got := found("longword"); got != "[long.txt]" {
		t.Errorf("the text before the cut: %s", got)
	}
	// The office document is skipped while office is off, and its name is still searchable by name.
	rec := callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/search?q=scan", nil)
	wantStatus(t, rec, 200)
	if p := decode[testPage](t, rec); len(p.Items) != 1 {
		t.Errorf("the name of the skipped document is not searchable: %+v", p.Items)
	}
	if r := rowOf(t, fx.s, fileNamed(t, fx, "scan.docx")); r.State != "skipped" || r.Error != "office text search is off" {
		t.Errorf("the office document row: %+v", r)
	}
	if r := rowOf(t, fx.s, fileNamed(t, fx, "huge.txt")); r.State != "skipped" || r.Error != "the file is over the size limit" {
		t.Errorf("the file over the source limit: %+v", r)
	}
}

// fileNamed returns the ID of a file of the team folder by its name, through the name search.
func fileNamed(t *testing.T, fx *contentFixture, name string) string {
	t.Helper()
	rec := callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/search?q="+strings.TrimSuffix(name, ".docx"), nil)
	wantStatus(t, rec, 200)
	for _, h := range decode[testPage](t, rec).Items {
		if h.Name == name {
			return h.ID
		}
	}
	t.Fatalf("no file named %s", name)
	return ""
}

// TestContentSearchIsRefusedWithoutTermsOrForDirectories: in=content needs q, and finds files only.
func TestContentSearchIsRefusedWithoutTermsOrForDirectories(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?in=content", nil), 400, apierr.InvalidRequest)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?in=content&q=x&kind=dir", nil), 400, apierr.InvalidRequest)
}

// TestConverterTextIsIndexedAndDocumentsAreSentByContent: a document goes to the converter for plain text, asked by its
// content (its name is not sent), and its text is indexed.
func TestConverterTextIsIndexedAndDocumentsAreSentByContent(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "notes.dat", docxBytes("x"))
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	conv := &fakeConverter{out: []byte("\xef\xbb\xbfconverted שלום text")}
	drainText(t, fx.worker(conv, clock))
	if conv.calls != 1 || conv.format != "txt" || conv.ext != "docx" {
		t.Fatalf("converter asked %d times, format %q, ext %q; want one txt request for the docx content", conv.calls, conv.format, conv.ext)
	}
	if got := fmt.Sprint(contentNames(fx.find(t, fx.bobSess, "שלום"))); got != "[notes.dat]" {
		t.Errorf("converted text not found: %s", got)
	}
	if r := rowOf(t, fx.s, n.ID); r.State != "ready" {
		t.Errorf("row state %q, want ready", r.State)
	}
}

// worker is a content worker on the fixture's store and blobs, with a fake converter and a clock the test moves.
func (fx *contentFixture) worker(conv search.Converter, clock *fakeClock) *search.Worker {
	return search.NewWorker(search.WorkerOptions{Store: fx.s.store, Blobs: fx.s.blobs, Conv: conv, OfficeText: true,
		TextMax: int64(fx.s.cfg.Search.TextMaxBytes), FullTextMax: int64(fx.s.cfg.Search.FullTextMaxBytes),
		CapAccount: int64(fx.s.cfg.Search.MaxTextPerAccount), CapTotal: int64(fx.s.cfg.Search.MaxTextTotal), Now: clock.Now})
}

// TestConverterRefusalIsStickyWithAFixedReason: a refusal makes the row failed with the server's own message, nothing is
// indexed, and the file is tried again after a day.
func TestConverterRefusalIsStickyWithAFixedReason(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "refused.docx", docxBytes("refusedword"))
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	conv := &fakeConverter{err: office.ErrRefused}
	w := fx.worker(conv, clock)
	drainText(t, w)
	if r := rowOf(t, fx.s, n.ID); r.State != "failed" || r.Error != "the converter refused the document" {
		t.Fatalf("row %+v, want failed with the refusal message", r)
	}
	if got := fx.find(t, fx.bobSess, "refusedword"); len(got) != 0 {
		t.Errorf("a refused document was indexed: %v", contentNames(got))
	}
	// Sticky: not tried again by the clock, however long it is, and tried once when the converter's key changes.
	clock.now = clock.now.Add(400 * 24 * time.Hour)
	drainText(t, w)
	if conv.calls != 1 {
		t.Errorf("a refusal was tried again by the clock: %d calls", conv.calls)
	}
	conv.build = "fake-next-build"
	drainText(t, w)
	if conv.calls != 2 {
		t.Errorf("a refusal was not tried again after the converter's key changed: %d calls", conv.calls)
	}
	if r := rowOf(t, fx.s, n.ID); r.State != "failed" {
		t.Errorf("the refusal under the new key: %+v, want failed again", r)
	}
}

// TestConverterDailyRefusalIsNotResent: a PDF the converter refuses is sent once per converter key, not once
// a day for ever. Mutation: a refusal with a day's wait (the earlier rule) sends it again on the second day.
func TestConverterDailyRefusalIsNotResent(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "scan.pdf", "%PDF-1.7\nrefusedpdf")
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	conv := &fakeConverter{err: office.ErrRefused}
	w := fx.worker(conv, clock)
	drainText(t, w)
	for day := 0; day < 30; day++ {
		clock.now = clock.now.Add(25 * time.Hour)
		drainText(t, w)
	}
	if conv.calls != 1 {
		t.Errorf("a refused PDF was sent %d times in 30 days, want once", conv.calls)
	}
}

// TestConverterFailureInFlightIsCountedThenSticky: a converter that fails while it has the document is a
// counted try, so a document that crashes it is not retried for ever: after the third try the row is sticky under the key.
// Mutation: an in-flight failure left uncounted (the earlier default case) never reaches the third try.
func TestConverterFailureInFlightIsCountedThenSticky(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "crash.docx", docxBytes("crashword"))
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	conv := &fakeConverter{err: office.ErrUnavailable}
	w := fx.worker(conv, clock)
	for want := int64(1); want <= 2; want++ {
		drainText(t, w)
		if r := rowOf(t, fx.s, n.ID); r.State != "pending" || r.Attempts != want {
			t.Fatalf("try %d: %+v, want pending with %d attempts", want, r, want)
		}
		clock.now = clock.now.Add(time.Hour)
	}
	drainText(t, w)
	if r := rowOf(t, fx.s, n.ID); r.State != "failed" || r.Attempts != 3 {
		t.Fatalf("third try: %+v, want failed after three counted tries", r)
	}
	calls := conv.calls
	clock.now = clock.now.Add(400 * 24 * time.Hour)
	drainText(t, w)
	if conv.calls != calls {
		t.Errorf("a document that failed three times was sent again by the clock")
	}
}

// TestConverterOutageIsRetriedAndNotCounted: an outage keeps the row pending, due a minute later, with no attempt counted,
// so the file is not marked empty for good and does not fail on an outage alone.
func TestConverterOutageIsRetriedAndNotCounted(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "down.docx", docxBytes("downword"))
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	// An outage is a connection that cannot be made: the worker pauses, and no row is counted or written.
	conv := &fakeConverter{err: office.ErrUnreachable}
	w := fx.worker(conv, clock)
	for i := 0; i < 5; i++ {
		drainText(t, w)
		if r := rowOf(t, fx.s, n.ID); r.State != "pending" || r.Attempts != 0 {
			t.Fatalf("after outage %d: %+v, want pending with no attempt counted", i+1, r)
		}
		clock.now = clock.now.Add(time.Minute)
	}
	conv.err = nil
	conv.out = []byte("downword came back")
	clock.now = clock.now.Add(10 * time.Minute) // past the longest pause: the health check is the next conversion
	drainText(t, w)
	if got := fmt.Sprint(contentNames(fx.find(t, fx.bobSess, "downword"))); got != "[down.docx]" {
		t.Errorf("the file was not converted after the outage: %s", got)
	}
}

// TestConverterTimeoutCountsThreeTriesThenWaitsADay: a time-out is counted: one minute, then ten, then a day.
func TestConverterTimeoutCountsThreeTriesThenWaitsADay(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "slow.docx", docxBytes("slowword"))
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	conv := &fakeConverter{err: context.DeadlineExceeded}
	w := fx.worker(conv, clock)
	drainText(t, w)
	if r := rowOf(t, fx.s, n.ID); r.Attempts != 1 || r.State != "pending" {
		t.Fatalf("first time-out: %+v", r)
	}
	clock.now = clock.now.Add(time.Minute)
	drainText(t, w)
	if r := rowOf(t, fx.s, n.ID); r.Attempts != 2 || r.State != "pending" {
		t.Fatalf("second time-out: %+v", r)
	}
	clock.now = clock.now.Add(10 * time.Minute)
	drainText(t, w)
	if r := rowOf(t, fx.s, n.ID); r.Attempts != 3 || r.State != "failed" {
		t.Fatalf("third time-out: %+v, want failed and sticky under the converter key", r)
	}
	// After the third try the document waits for a new converter key, as a refusal does (decision 46). It is not
	// sent again by the clock: a fourth, fifth and sixth day make no request.
	sent := conv.calls
	for day := 1; day <= 3; day++ {
		clock.now = clock.now.Add(24 * time.Hour)
		drainText(t, w)
		if conv.calls != sent {
			t.Fatalf("day %d after the third time-out: the document was sent again (%d requests, want %d)", day, conv.calls, sent)
		}
		if r := rowOf(t, fx.s, n.ID); r.Attempts != 3 || r.State != "failed" {
			t.Fatalf("day %d after the third time-out: %+v", day, r)
		}
	}
	// A new converter key releases it, once.
	conv.build = "build-2"
	conv.err = nil
	conv.out = []byte("slowword")
	drainText(t, w)
	if conv.calls != sent+1 {
		t.Fatalf("after a new converter key: %d requests, want %d", conv.calls, sent+1)
	}
	if r := rowOf(t, fx.s, n.ID); r.State != "ready" {
		t.Fatalf("after a new converter key: %+v, want ready", r)
	}
}

// TestConverterOutputOverTheLimitIsCutAndMarked: the converter writes more than search.text_max_bytes; the kept text is cut
// at the limit, marked truncated, and a word past the cut is not found.
func TestConverterOutputOverTheLimitIsCutAndMarked(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t, func(c *config.Config) { c.Search.TextMaxBytes = 1024 })
	n := fx.file(t, "long.docx", docxBytes("x"))
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	drainText(t, fx.worker(&fakeConverter{out: []byte(strings.Repeat("a", 5000) + " pastword")}, clock))
	if r := rowOf(t, fx.s, n.ID); r.State != "ready" || r.Truncated != 1 {
		t.Fatalf("row %+v, want ready and truncated", r)
	}
	if got := fx.find(t, fx.bobSess, "pastword"); len(got) != 0 {
		t.Errorf("a word past the cut was found: %v", contentNames(got))
	}
}

// TestConverterAnswerThatIsNotTextFails: an answer with a NUL is not text: a sticky failure with the fixed message.
func TestConverterAnswerThatIsNotTextFails(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "bad.docx", docxBytes("x"))
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	drainText(t, fx.worker(&fakeConverter{out: []byte("bad\x00output")}, clock))
	if r := rowOf(t, fx.s, n.ID); r.State != "failed" || r.Error != "the converter answered with another type of file" {
		t.Fatalf("row %+v", r)
	}
}

// TestOfficeOffSkipsDocumentsWithAReason: with search.office_text off a document is skipped with the reason "office text
// search is off" and no request is made, and that row is released when the setting is turned on.
func TestOfficeOffSkipsDocumentsWithAReason(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "off.docx", docxBytes("x"))
	drainText(t, fx.s.textWorker)
	if r := rowOf(t, fx.s, n.ID); r.State != "skipped" || r.Error != store.TextErrOfficeTextOff {
		t.Fatalf("row %+v", r)
	}
	if _, err := fx.s.store.ReleaseTextSkipped(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := rowOf(t, fx.s, n.ID); r.State != "pending" {
		t.Errorf("after release: %+v, want pending", r)
	}
}

// TestSearchRouteAdmitsNoShareLinkVisitor: the search route has no link flag, so a share-link visitor is refused by the
// route, as name search is.
func TestSearchRouteAdmitsNoShareLinkVisitor(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	found := false
	for _, rt := range fx.s.routes() {
		if rt.path == "/api/v1/search" {
			found = true
			if rt.link {
				t.Fatal("the search route admits share-link visitors")
			}
		}
	}
	if !found {
		t.Fatal("no search route")
	}
}

// TestTokenFindsOnlyItsFolder: a delegated token minted for one folder finds nothing of another.
func TestTokenFindsOnlyItsFolder(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "mine.txt", "tokenword")
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	putTyped(t, fx.s, fx.key, other.ID, "theirs.txt", "text/plain", "tokenword")
	drainText(t, fx.s.textWorker)
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, "")
	if got := fmt.Sprint(contentNames(fx.findKey(t, tok, "tokenword"))); got != "[mine.txt]" {
		t.Errorf("a token for one folder found %s", got)
	}
}
