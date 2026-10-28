// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Downloads (design sections 6, 9 and 13): the contents of a file, with single and multiple byte
// ranges, conditional requests, and the rules for showing a file inline.
//
// The body goes through http.ServeContent over the blob's *os.File, so a single range or the
// whole file leaves the server by sendfile(2) where the platform allows it. Everything that
// ServeContent answers with plain text, or would answer differently from the API's error format,
// is decided here first and answered as an apierr.

import (
	"fmt"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// maxRanges caps the ranges one request may name. More is refused, so a request cannot make the
// server send many copies of a large file.
const maxRanges = 16

// contentCSP is the policy on every content response: nothing in it may run or load anything.
const contentCSP = "sandbox; default-src 'none'"

// Cross-Origin-Resource-Policy values. Authenticated content is for the main origin only;
// signed links are meant for other sites to embed.
const (
	corpSameOrigin  = "same-origin"
	corpCrossOrigin = "cross-origin"
)

// setValidators sets the ETag and Last-Modified of a response that carries the file.
func setValidators(h http.Header, etag string, mod time.Time) {
	h.Set("ETag", etag)
	h.Set("Last-Modified", mod.Format(http.TimeFormat))
}

// onContentHost reports whether the request arrived on the content origin.
func (s *Server) onContentHost(r *http.Request) bool {
	return s.contentHost != "" && canonicalHost(r.Host, s.contentScheme) == s.contentHost
}

// canonicalHost lower-cases a Host value and drops the port when it is the scheme's default, so
// "files.example.com:443" and "files.example.com" name the same origin over https.
func canonicalHost(host, scheme string) string {
	h := strings.ToLower(host)
	switch {
	case scheme == "https" && strings.HasSuffix(h, ":443"):
		return strings.TrimSuffix(h, ":443")
	case scheme == "http" && strings.HasSuffix(h, ":80"):
		return strings.TrimSuffix(h, ":80")
	}
	return h
}

// safeInlineType returns the Content-Type to show a stored type inline, and whether it may be
// shown inline at all on the main origin. Images other than SVG, PDF, audio, video and plain text
// qualify. Plain text is always sent as UTF-8.
func safeInlineType(stored string) (string, bool) {
	mt, _, err := mime.ParseMediaType(stored)
	if err != nil {
		return "", false
	}
	switch {
	case mt == "text/plain":
		return "text/plain; charset=utf-8", true
	case mt == "application/pdf":
		return mt, true
	case mt == "image/svg+xml":
		// SVG is a document with script in it, whatever its type says.
		return "", false
	case strings.HasPrefix(mt, "image/"), strings.HasPrefix(mt, "audio/"), strings.HasPrefix(mt, "video/"):
		return mt, true
	}
	return "", false
}

// inlinePolicy returns the Content-Type for an inline response and whether it may be inline. On
// the content origin any type may be inline, because the sandbox policy stops it running script
// there. On the main origin, and when content_url is not configured, only the safe types may be,
// and not at all when content_url is configured: that origin is where files are shown inline.
func (s *Server) inlinePolicy(r *http.Request, stored string) (string, bool) {
	safe, ok := safeInlineType(stored)
	if s.onContentHost(r) {
		if ok {
			return safe, true
		}
		return stored, true
	}
	if s.cfg.ContentURL != "" || !ok {
		return stored, false
	}
	return safe, true
}

// contentDisposition builds a Content-Disposition for a file name. The plain filename is ASCII
// only, with quotes, backslashes and controls replaced by "_"; filename* carries the exact name
// in UTF-8 per RFC 5987, so clients that understand it get the true name.
func contentDisposition(kind, name string) string {
	var plain strings.Builder
	for _, c := range name {
		if c >= 0x20 && c < 0x7f && c != '"' && c != '\\' {
			plain.WriteRune(c)
		} else {
			plain.WriteByte('_')
		}
	}
	var enc strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if isAttrChar(c) {
			enc.WriteByte(c)
		} else {
			fmt.Fprintf(&enc, "%%%02X", c)
		}
	}
	return kind + `; filename="` + plain.String() + `"; filename*=UTF-8''` + enc.String()
}

// isAttrChar reports whether c may appear unencoded in an RFC 5987 value.
func isAttrChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}

// etagListMatches reports whether an If-Match or If-None-Match list names etag. The strong
// comparison used for If-Match refuses weak tags; the weak one used for If-None-Match does not.
func etagListMatches(list, etag string, strong bool) bool {
	for _, t := range strings.Split(list, ",") {
		t = strings.TrimSpace(t)
		if t == "*" {
			return true
		}
		if strings.HasPrefix(t, "W/") {
			if strong {
				continue
			}
			t = t[2:]
		}
		if t == etag {
			return true
		}
	}
	return false
}

// preconditions evaluates the conditional headers in the order RFC 9110 section 13.2.2 gives. It
// returns 412 when a precondition fails, 304 when a GET or HEAD is answered from the cache, and 0
// when the request goes on. The caller writes the answer.
func preconditions(r *http.Request, etag string, mod time.Time) (int, error) {
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	if im := r.Header.Get("If-Match"); im != "" {
		if !etagListMatches(im, etag, true) {
			return http.StatusPreconditionFailed, apierr.New(apierr.PreconditionFailed,
				"the file changed since it was read; read it again")
		}
	} else if ius := r.Header.Get("If-Unmodified-Since"); ius != "" {
		if t, err := http.ParseTime(ius); err == nil && mod.After(t) {
			return http.StatusPreconditionFailed, apierr.New(apierr.PreconditionFailed,
				"the file changed since the date given")
		}
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if get && etagListMatches(inm, etag, false) {
			return http.StatusNotModified, nil
		}
	} else if ims := r.Header.Get("If-Modified-Since"); ims != "" && get {
		if t, err := http.ParseTime(ims); err == nil && !mod.After(t) {
			return http.StatusNotModified, nil
		}
	}
	return 0, nil
}

// ifRangeHolds reports whether a Range applies: with no If-Range, always; with one, only when it
// is the current strong ETag or the exact Last-Modified date. When it does not hold, the whole
// file is sent, as RFC 9110 section 13.1.5 requires.
func ifRangeHolds(r *http.Request, etag string, mod time.Time) bool {
	ir := r.Header.Get("If-Range")
	if ir == "" {
		return true
	}
	if strings.HasPrefix(ir, `"`) || strings.HasPrefix(ir, "W/") {
		return ir == etag
	}
	t, err := http.ParseTime(ir)
	return err == nil && mod.Equal(t)
}

// span is one byte range of a file, both ends inclusive.
type span struct{ first, last int64 }

// normalizeRanges checks a Range header against a file of size bytes. It returns the header to
// give ServeContent, in which the ranges are absolute, sorted and merged where they touch or
// overlap, so no byte is sent twice. A header in another unit is ignored, and comes back empty.
// A malformed header, more than maxRanges ranges, or no range inside the file is a
// range_not_satisfiable error.
func normalizeRanges(h string, size int64) (string, error) {
	notSatisfiable := func(msg string) *apierr.Error {
		return apierr.New(apierr.RangeNotSatisfiable, msg).With("size_bytes", size)
	}
	unit, set, ok := strings.Cut(h, "=")
	if !ok {
		return "", notSatisfiable("the Range header is not valid: use bytes=first-last, bytes=first- or bytes=-count")
	}
	if !strings.EqualFold(strings.TrimSpace(unit), "bytes") {
		return "", nil
	}
	var spans []span
	count := 0
	for _, part := range strings.Split(set, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		count++
		if count > maxRanges {
			return "", notSatisfiable(fmt.Sprintf("at most %d ranges may be requested", maxRanges)).
				With("max_ranges", maxRanges)
		}
		sp, inside, err := parseRangeSpec(part, size)
		if err != nil {
			return "", notSatisfiable("the Range header is not valid: use bytes=first-last, bytes=first- or bytes=-count")
		}
		if inside {
			spans = append(spans, sp)
		}
	}
	if count == 0 {
		return "", notSatisfiable("the Range header names no range")
	}
	if len(spans) == 0 {
		return "", notSatisfiable("no requested range lies inside the file")
	}
	slices.SortFunc(spans, func(a, b span) int {
		switch {
		case a.first < b.first:
			return -1
		case a.first > b.first:
			return 1
		}
		return 0
	})
	merged := []span{spans[0]}
	for _, sp := range spans[1:] {
		last := &merged[len(merged)-1]
		if sp.first <= last.last+1 {
			last.last = max(last.last, sp.last)
			continue
		}
		merged = append(merged, sp)
	}
	var b strings.Builder
	b.WriteString("bytes=")
	for i, sp := range merged {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d-%d", sp.first, sp.last)
	}
	return b.String(), nil
}

// parseRangeSpec reads one range of a Range header against a file of size bytes. inside is false
// for a well-formed range the file does not reach; err is set for a malformed one.
func parseRangeSpec(part string, size int64) (sp span, inside bool, err error) {
	if suffix, ok := strings.CutPrefix(part, "-"); ok {
		n, err := parseRangeNumber(suffix)
		if err != nil {
			return span{}, false, err
		}
		if n == 0 || size == 0 {
			return span{}, false, nil
		}
		return span{first: max(size-n, 0), last: size - 1}, true, nil
	}
	firstS, lastS, _ := strings.Cut(part, "-")
	first, err := parseRangeNumber(firstS)
	if err != nil {
		return span{}, false, err
	}
	last := size - 1
	if lastS != "" {
		if last, err = parseRangeNumber(lastS); err != nil {
			return span{}, false, err
		}
		if last < first {
			return span{}, false, fmt.Errorf("range ends before it starts")
		}
	}
	if first >= size {
		return span{}, false, nil
	}
	return span{first: first, last: min(last, size-1)}, true, nil
}

// parseRangeNumber reads a range's digits. Only ASCII digits are accepted, so no sign or space
// gets through.
func parseRangeNumber(s string) (int64, error) {
	if s == "" || len(s) > 19 {
		return 0, fmt.Errorf("not a number")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("not a number")
		}
	}
	return strconv.ParseInt(s, 10, 64)
}

// serveFile sends a file's contents. wantInline is what the caller asked for; the response is
// inline only when the policy allows it. corp is the Cross-Origin-Resource-Policy to send. Every
// response carries the same safety headers, so a file is never sent without them.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, n *store.Node, wantInline bool, corp string) error {
	ctype, disp := n.MimeType, dispAttachment
	if wantInline {
		if t, ok := s.inlinePolicy(r, n.MimeType); ok {
			ctype, disp = t, dispInline
		}
	}
	mod := n.ModifiedAt.UTC().Truncate(time.Second)

	// Set before any answer. The validators, the type and the disposition are set only on the
	// answers that carry the file: a JSON error must not carry the file's ETag, Last-Modified or
	// name.
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", contentCSP)
	h.Set("Cross-Origin-Resource-Policy", corp)

	status, err := preconditions(r, n.ETag, mod)
	if err != nil {
		return err
	}
	if status == http.StatusNotModified {
		setValidators(h, n.ETag, mod)
		w.WriteHeader(status)
		return nil
	}

	f, err := s.blobs.Open(n.AccountID, n.BlobID)
	if err != nil {
		return apierr.Wrap(apierr.Internal, "the file's contents are missing", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return apierr.Wrap(apierr.Internal, "the file's contents cannot be read", err)
	}
	if fi.Size() != n.Size {
		return apierr.Wrap(apierr.Internal, "the file's contents do not match its size",
			fmt.Errorf("blob %s holds %d bytes, the node says %d", n.BlobID, fi.Size(), n.Size))
	}

	if r.Header.Get("Range") != "" {
		if !ifRangeHolds(r, n.ETag, mod) {
			r.Header.Del("Range")
		} else {
			norm, err := normalizeRanges(r.Header.Get("Range"), n.Size)
			if err != nil {
				h.Set("Content-Range", fmt.Sprintf("bytes */%d", n.Size))
				return err
			}
			if norm == "" {
				r.Header.Del("Range")
			} else {
				r.Header.Set("Range", norm)
			}
		}
	}
	setValidators(h, n.ETag, mod)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", contentDisposition(disp, n.Name))
	// The preconditions were checked above, so ServeContent's own pass gives the same answer, and
	// the ranges it sees are valid. Its one plain-text error, 416, cannot happen here.
	http.ServeContent(w, r, n.Name, mod, f)
	return nil
}

// handleGetContent sends a file's contents to a caller who may read the file. The body is an
// attachment unless ?disposition=inline is asked for and the policy allows it.
func (s *Server) handleGetContent(w http.ResponseWriter, r *http.Request) error {
	n, err := s.nodeOf(r, r.PathValue("nod"), store.RoleViewer)
	if err != nil {
		return err
	}
	if n.Kind != store.KindFile {
		return apierr.New(apierr.InvalidRequest, "a folder has no contents; list its children instead")
	}
	inline := false
	switch d := r.URL.Query().Get("disposition"); d {
	case "", dispAttachment:
	case dispInline:
		inline = true
	default:
		return apierr.New(apierr.InvalidRequest, "disposition must be attachment or inline")
	}
	return s.serveFile(w, r, n, inline, corpSameOrigin)
}
