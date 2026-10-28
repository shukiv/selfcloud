// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Cross-origin requests (design section 7).
//
// A browser application on another origin calls the API with a delegated token in its
// Authorization header, and CORS lets its page read the answers. Cookies take no part: a session
// is refused on any request from another origin (see authenticateSession), so CORS never lets a
// browser send an ambient credential across sites.

import (
	"net/http"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// corsMethods are the methods an allowed page may use.
const corsMethods = "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS"

// corsMaxAge is how long, in seconds, a browser may cache a preflight answer.
const corsMaxAge = "600"

// corsRequestHeaders are the request headers an allowed page may send. The Upload-* headers are
// the ones the upload handlers read.
const corsRequestHeaders = "Authorization, Content-Type, If-Match, If-None-Match, If-Range, Range, " +
	"Tus-Resumable, Upload-Length, Upload-Offset, Upload-Metadata, Upload-Checksum"

// corsExposedHeaders are the response headers an allowed page may read. Browsers hide every other
// header from cross-origin code.
var corsExposedHeaders = strings.Join([]string{
	"Location", "Upload-Offset", "Upload-Length", "Upload-Expires", "Upload-Metadata",
	"Tus-Resumable", "Tus-Max-Size", "Tus-Version", "Tus-Extension", "Tus-Checksum-Algorithm",
	"Selfcloud-Max-Chunk-Size", "Selfcloud-Node-ID", "Selfcloud-Upload-State",
	"ETag", "Content-Range", "Accept-Ranges", "Content-Disposition", "Retry-After", "X-Request-Id",
}, ", ")

// cors answers cross-origin requests from the origins in cors.allowed_origins. It runs before
// routing and authentication, so a preflight is never refused for want of a credential or a route.
// A preflight from an origin that is not allowed is refused with 403 and no CORS headers, so the
// browser shows the request as blocked. Any other request from such an origin is served as usual
// and gets no CORS headers; the browser then hides the answer from the page.
//
// Signed links and the content origin are left alone: they carry their own authority, and nothing
// in them needs a cross-origin page.
func (s *Server) cors(next http.Handler) http.Handler {
	if len(s.corsAllowed) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.onContentHost(r) || isLinkPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		// Every answer depends on the Origin it was made for, so a cache must key on it.
		h.Add("Vary", "Origin")
		origin := strings.ToLower(r.Header.Get("Origin"))
		allowed := origin != "" && !strings.EqualFold(origin, s.origin) && s.corsAllowed[origin]
		// A preflight is an OPTIONS with Access-Control-Request-Method. Any other OPTIONS, such as
		// the tus capability probe, is an ordinary request and reaches its handler.
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" && origin != "" {
			if !allowed {
				s.writeError(w, r, apierr.New(apierr.Forbidden, "the origin is not allowed"))
				return
			}
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", corsMethods)
			h.Set("Access-Control-Allow-Headers", corsRequestHeaders)
			h.Set("Access-Control-Max-Age", corsMaxAge)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if allowed {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Expose-Headers", corsExposedHeaders)
		}
		next.ServeHTTP(w, r)
	})
}
