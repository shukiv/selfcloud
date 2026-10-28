// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// alphabet is the body of the test files: byte i is 'a'+i, so a range's bytes name its offsets.
const alphabet = "abcdefghijklmnopqrstuvwxyz"

// rangeFixture makes an account and a 26-byte text file, alpha.txt, and returns the file.
func rangeFixture(t *testing.T, s *Server) (string, nodeJSON) {
	t.Helper()
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	return key, putNode(t, s, key, f.ID, "/alpha.txt", alphabet)
}

func TestNormalizeRanges(t *testing.T) {
	const size = 26
	ok := map[string]string{
		"bytes=0-4":        "bytes=0-4",
		"bytes=-3":         "bytes=23-25",
		"bytes=20-":        "bytes=20-25",
		"bytes=-100":       "bytes=0-25", // a suffix longer than the file is the whole file
		"bytes=0-99":       "bytes=0-25", // a last byte past the end is cut to the end
		"bytes=10-12,0-2":  "bytes=0-2,10-12",
		"bytes=0-5,3-10":   "bytes=0-10", // overlapping ranges merge, so no byte is sent twice
		"bytes=0-1,2-3":    "bytes=0-3",  // touching ranges merge too
		"bytes= 0-1 , 5-6": "bytes=0-1,5-6",
		"bytes=0-1,,5-6":   "bytes=0-1,5-6", // empty list elements are skipped, as RFC 9110 allows
		"bytes=0-1,":       "bytes=0-1",
		"bytes=0-0,30-40":  "bytes=0-0", // a range outside the file is dropped when another is inside
		"BYTES=0-0":        "bytes=0-0",
	}
	for in, want := range ok {
		got, err := normalizeRanges(in, size)
		if err != nil || got != want {
			t.Errorf("normalizeRanges(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// Another unit is ignored: the whole file is sent.
	if got, err := normalizeRanges("items=0-1", size); err != nil || got != "" {
		t.Errorf("other unit = %q, %v", got, err)
	}
	// Malformed, unsatisfiable, and empty ranges are range_not_satisfiable.
	bad := []string{"bytes=30-40", "bytes=5-2", "bytes=abc", "bytes=", "bytes", "bytes=-",
		"bytes=-0", "bytes=+1-2", "bytes=1-2-3", "bytes=0x1-2", "bytes=99999999999999999999-",
		"bytes=26-"}
	for _, in := range bad {
		_, err := normalizeRanges(in, size)
		if err == nil || apierr.From(err).Code != apierr.RangeNotSatisfiable {
			t.Errorf("normalizeRanges(%q) = %v; want range_not_satisfiable", in, err)
		}
	}
	// Sixteen ranges are allowed, seventeen are not.
	sixteen := strings.TrimSuffix(strings.Repeat("0-0,", 16), ",")
	if _, err := normalizeRanges("bytes="+sixteen, size); err != nil {
		t.Errorf("16 ranges refused: %v", err)
	}
	if _, err := normalizeRanges("bytes="+sixteen+",0-0", size); apierr.From(err).Code != apierr.RangeNotSatisfiable {
		t.Errorf("17 ranges = %v", err)
	}
	// An empty file has no byte to range over.
	if _, err := normalizeRanges("bytes=0-", 0); apierr.From(err).Code != apierr.RangeNotSatisfiable {
		t.Errorf("range of an empty file = %v", err)
	}
}

func TestRangedDownloads(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	key, doc := rangeFixture(t, s)
	get := func(method string, hdr map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		return rawCall(t, s, method, "/api/v1/nodes/"+doc.ID+"/content", key, "", hdr)
	}
	rng := func(v string) map[string]string { return map[string]string{"Range": v} }

	single := get("GET", rng("bytes=0-4"))
	wantStatus(t, single, 206)
	if single.Body.String() != "abcde" || single.Header().Get("Content-Range") != "bytes 0-4/26" ||
		single.Header().Get("Content-Length") != "5" || single.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("single range: %q %v", single.Body, single.Header())
	}
	if got := get("GET", rng("bytes=-3")).Body.String(); got != "xyz" {
		t.Errorf("suffix range = %q", got)
	}
	if got := get("GET", rng("bytes=20-")).Body.String(); got != "uvwxyz" {
		t.Errorf("open range = %q", got)
	}
	if got := get("GET", rng("bytes=0-5,3-10")); got.Code != 206 || got.Body.String() != "abcdefghijk" ||
		got.Header().Get("Content-Range") != "bytes 0-10/26" {
		t.Errorf("merged ranges = %d %q %v", got.Code, got.Body, got.Header())
	}

	// Several ranges come back as multipart/byteranges, one part per range.
	multi := get("GET", rng("bytes=0-2,5-6"))
	wantStatus(t, multi, 206)
	mt, params, err := mime.ParseMediaType(multi.Header().Get("Content-Type"))
	if err != nil || mt != "multipart/byteranges" {
		t.Fatalf("multi content type = %q, %v", multi.Header().Get("Content-Type"), err)
	}
	pr := multipart.NewReader(multi.Body, params["boundary"])
	var parts []string
	for {
		p, err := pr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		parts = append(parts, p.Header.Get("Content-Range")+"="+string(b))
	}
	if strings.Join(parts, ";") != "bytes 0-2/26=abc;bytes 5-6/26=fg" {
		t.Errorf("parts = %v", parts)
	}

	// Invalid ranges are JSON range_not_satisfiable with the size in Content-Range.
	for _, v := range []string{"bytes=30-40", "bytes=5-2", "bytes=abc", "bytes=-0"} {
		rec := get("GET", rng(v))
		wantError(t, rec, 416, apierr.RangeNotSatisfiable)
		if rec.Header().Get("Content-Range") != "bytes */26" {
			t.Errorf("%s: Content-Range = %q", v, rec.Header().Get("Content-Range"))
		}
	}
	tooMany := "bytes=" + strings.TrimSuffix(strings.Repeat("0-0,", 17), ",")
	if e := wantError(t, get("GET", rng(tooMany)), 416, apierr.RangeNotSatisfiable); e.Details["max_ranges"] != float64(16) {
		t.Errorf("too many ranges details = %v", e.Details)
	}
	// Another unit is ignored.
	if rec := get("GET", rng("items=0-4")); rec.Code != 200 || rec.Body.String() != alphabet {
		t.Errorf("other unit = %d %q", rec.Code, rec.Body)
	}
}

func TestIfRangeAndConditionals(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	key, doc := rangeFixture(t, s)
	get := func(hdr map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		return rawCall(t, s, "GET", "/api/v1/nodes/"+doc.ID+"/content", key, "", hdr)
	}
	first := get(nil)
	lastModified := first.Header().Get("Last-Modified")
	if lastModified == "" || first.Header().Get("ETag") != doc.ETag {
		t.Fatalf("validators = %v", first.Header())
	}

	// If-Range: a matching ETag or date keeps the range.
	wantStatus(t, get(map[string]string{"Range": "bytes=0-1", "If-Range": doc.ETag}), 206)
	wantStatus(t, get(map[string]string{"Range": "bytes=0-1", "If-Range": lastModified}), 206)
	// A mismatch sends the whole file, even when the range is one the file cannot satisfy: the
	// range is ignored, so it cannot fail.
	for _, ir := range []string{`"stale"`, "W/" + doc.ETag, "Mon, 01 Jan 2001 00:00:00 GMT"} {
		rec := get(map[string]string{"Range": "bytes=100-200", "If-Range": ir})
		if rec.Code != 200 || rec.Body.String() != alphabet {
			t.Errorf("If-Range %q: %d %q", ir, rec.Code, rec.Body)
		}
	}

	// If-None-Match wins over Range: a cached copy gets 304 even when the range is invalid.
	rec := get(map[string]string{"Range": "bytes=100-200", "If-None-Match": doc.ETag})
	if rec.Code != 304 || rec.Body.Len() != 0 {
		t.Errorf("304 over a bad range = %d %q", rec.Code, rec.Body)
	}
	wantStatus(t, get(map[string]string{"If-None-Match": `W/` + doc.ETag}), 304)
	// If-Modified-Since: not modified since a later date, modified since an earlier one.
	later := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
	wantStatus(t, get(map[string]string{"If-Modified-Since": later}), 304)
	wantStatus(t, get(map[string]string{"If-Modified-Since": "Mon, 01 Jan 2001 00:00:00 GMT"}), 200)

	// If-Match and If-Unmodified-Since fail with JSON precondition_failed.
	wantError(t, get(map[string]string{"If-Match": `"stale"`}), 412, apierr.PreconditionFailed)
	wantStatus(t, get(map[string]string{"If-Match": doc.ETag}), 200)
	wantError(t, get(map[string]string{"If-Unmodified-Since": "Mon, 01 Jan 2001 00:00:00 GMT"}), 412,
		apierr.PreconditionFailed)
}

func TestHeadDownloads(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	key, doc := rangeFixture(t, s)
	path := "/api/v1/nodes/" + doc.ID + "/content"

	head := rawCall(t, s, "HEAD", path, key, "", nil)
	wantStatus(t, head, 200)
	if head.Body.Len() != 0 || head.Header().Get("Content-Length") != "26" || head.Header().Get("ETag") != doc.ETag {
		t.Errorf("HEAD = %q %v", head.Body, head.Header())
	}
	ranged := rawCall(t, s, "HEAD", path, key, "", map[string]string{"Range": "bytes=0-4"})
	wantStatus(t, ranged, 206)
	if ranged.Body.Len() != 0 || ranged.Header().Get("Content-Length") != "5" {
		t.Errorf("HEAD range = %q %v", ranged.Body, ranged.Header())
	}
	wantStatus(t, rawCall(t, s, "HEAD", path, key, "", map[string]string{"If-None-Match": doc.ETag}), 304)
}

// putTyped writes a file of the given media type and returns it.
func putTyped(t *testing.T, s *Server, key, fld, name, mimeType, body string) nodeJSON {
	t.Helper()
	rec := rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/files?"+q("/"+name), key, body,
		map[string]string{"Content-Type": mimeType})
	wantStatus(t, rec, 201)
	return decode[nodeJSON](t, rec)
}

func TestInlineOnTheMainOrigin(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	cases := []struct {
		name, mimeType, body string
		inline               bool
		wantType             string
	}{
		{"pic.png", "image/png", "\x89PNG", true, "image/png"},
		{"clip.mp4", "video/mp4", "v", true, "video/mp4"},
		{"song.mp3", "audio/mpeg", "a", true, "audio/mpeg"},
		{"doc.pdf", "application/pdf", "%PDF", true, "application/pdf"},
		{"notes.txt", "text/plain", "hi", true, "text/plain; charset=utf-8"},
		{"page.html", "text/html", "<script>alert(1)</script>", false, "text/html"},
		{"logo.svg", "image/svg+xml", "<svg onload=x></svg>", false, "image/svg+xml"},
		{"data.xml", "application/xml", "<x/>", false, "application/xml"},
		{"app.js", "text/javascript", "alert(1)", false, "text/javascript"},
	}
	for _, c := range cases {
		n := putTyped(t, s, key, f.ID, c.name, c.mimeType, c.body)
		path := "/api/v1/nodes/" + n.ID + "/content"

		rec := rawCall(t, s, "GET", path+"?disposition=inline", key, "", nil)
		wantStatus(t, rec, 200)
		disp := rec.Header().Get("Content-Disposition")
		wantDisp := "attachment"
		if c.inline {
			wantDisp = "inline"
		}
		if !strings.HasPrefix(disp, wantDisp+";") || rec.Header().Get("Content-Type") != c.wantType {
			t.Errorf("%s inline: disposition %q, type %q; want %s, %s", c.name, disp,
				rec.Header().Get("Content-Type"), wantDisp, c.wantType)
		}
		if rec.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" ||
			rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: safety headers %v", c.name, rec.Header())
		}
		// Without the parameter the answer is always an attachment.
		plain := rawCall(t, s, "GET", path, key, "", nil)
		if !strings.HasPrefix(plain.Header().Get("Content-Disposition"), "attachment;") {
			t.Errorf("%s default: %q", c.name, plain.Header().Get("Content-Disposition"))
		}
	}
	wantError(t, rawCall(t, s, "GET", "/api/v1/nodes/"+putTyped(t, s, key, f.ID, "x.txt", "text/plain", "x").ID+
		"/content?disposition=download", key, "", nil), 400, apierr.InvalidRequest)
}

func TestInlineOnTheContentOrigin(t *testing.T) {
	const contentHost = "files.example.test"
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.ContentURL = "https://" + contentHost })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	png := putTyped(t, s, key, f.ID, "pic.png", "image/png", "\x89PNG")
	page := putTyped(t, s, key, f.ID, "page.html", "text/html", "<b>hi</b>")

	// With content_url set, the main origin never shows a file inline, not even a picture.
	rec := rawCall(t, s, "GET", "/api/v1/nodes/"+png.ID+"/content?disposition=inline", key, "", nil)
	wantStatus(t, rec, 200)
	if !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment;") {
		t.Errorf("main origin with content_url: %q", rec.Header().Get("Content-Disposition"))
	}

	// A signed link made for inline shows an HTML file inline on the content origin, under the
	// sandbox policy, and its URL is on the content origin.
	link := makeLink(t, s, key, page.ID, `{"disposition":"inline"}`)
	if !strings.HasPrefix(link.URL, "https://"+contentHost+"/api/v1/links/") {
		t.Fatalf("link URL = %q", link.URL)
	}
	lu := urlPath(t, link.URL)
	got := fromHost(t, s, contentHost, "GET", lu, nil, nil)
	wantStatus(t, got, 200)
	if !strings.HasPrefix(got.Header().Get("Content-Disposition"), "inline;") ||
		got.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" {
		t.Errorf("content origin inline = %q, %v", got.Header().Get("Content-Disposition"), got.Header())
	}
	if got.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Errorf("CORP on the content origin = %q", got.Header().Get("Cross-Origin-Resource-Policy"))
	}

	// The content origin serves nothing but signed links: not the API, the interface, or health,
	// even with a credential or a session cookie that would work on the main origin.
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	for _, p := range []string{
		"/api/v1/nodes/" + png.ID + "/content",
		"/api/v1/nodes/" + png.ID,
		"/api/v1/me",
		"/api/v1/links/../../nodes/" + png.ID + "/content",
		"/healthz",
		"/",
	} {
		rec := fromHost(t, s, contentHost, "GET", p, map[string]string{"Authorization": "Bearer " + key}, nil)
		wantError(t, rec, 404, apierr.NotFound)
		rec = fromHost(t, s, contentHost, "GET", p, nil, sess.cookie)
		wantError(t, rec, 404, apierr.NotFound)
	}
	// And the main origin still answers the API, from the same process.
	wantStatus(t, rawCall(t, s, "GET", "/api/v1/nodes/"+png.ID, key, "", nil), 200)
}

func TestContentDispositionEncoding(t *testing.T) {
	cases := map[string]string{
		"plain.txt":        `attachment; filename="plain.txt"; filename*=UTF-8''plain.txt`,
		`a"b\c ü.txt`:      `attachment; filename="a_b_c _.txt"; filename*=UTF-8''a%22b%5Cc%20%C3%BC.txt`,
		"文件.pdf":           `attachment; filename="__.pdf"; filename*=UTF-8''%E6%96%87%E4%BB%B6.pdf`,
		"it's (1) & more!": `attachment; filename="it's (1) & more!"; filename*=UTF-8''it%27s%20%281%29%20&%20more!`,
		"tab\tname":        `attachment; filename="tab_name"; filename*=UTF-8''tab%09name`,
		"back`tick|pipe~^": "attachment; filename=\"back`tick|pipe~^\"; filename*=UTF-8''back`tick|pipe~^",
	}
	for name, want := range cases {
		if got := contentDisposition("attachment", name); got != want {
			t.Errorf("contentDisposition(%q)\n got %s\nwant %s", name, got, want)
		}
		// The header must parse back, with the true name in filename*.
		_, params, err := mime.ParseMediaType(contentDisposition("inline", name))
		if err != nil {
			t.Errorf("%q does not parse: %v", name, err)
		}
		if params["filename"] != name && params["filename"] != strings.Map(asciiOrUnderscore, name) {
			t.Errorf("%q parsed as %q", name, params["filename"])
		}
	}
}

// asciiOrUnderscore is the plain-name rule of contentDisposition, for the parse check above.
func asciiOrUnderscore(r rune) rune {
	if r >= 0x20 && r < 0x7f && r != '"' && r != '\\' {
		return r
	}
	return '_'
}
