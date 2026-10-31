// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web serves the web interface: the built single-page app at /, with its strict security
// headers, and the installation's logo.
package web

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// notBuiltPage is served when the build has no index.html: the interface was not built, so the
// server says so.
const notBuiltPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Selfcloud</title>
</head>
<body>
<p>The web interface is not built. Run make build.</p>
</body>
</html>
`

// reserved are the paths the server owns. The web interface never answers under them, so an
// unknown API path still gets the JSON 404.
var reserved = []string{"/api", "/wopi", "/brand", "/metrics", "/healthz", "/readyz"}

// cspNoncePlaceholder is the text the build writes where the nonce goes: the Vite option
// html.cspNonce in the web interface's vite.config.ts sets it, in the page's meta tag and on its
// script and link tags. The server replaces every occurrence with a fresh nonce per response.
const cspNoncePlaceholder = "__SELFCLOUD_CSP_NONCE__"

// Options configure a Handler.
type Options struct {
	// ContentURL is content_url, or empty. Its origin is allowed for images, video, audio and frames,
	// because file previews load from it.
	ContentURL string
	// OfficeURL is office.discovery_url, or empty when office editing is off. Its origin is allowed for the editor's
	// frame, and as the target of the editor's form post (form-action), because the editor is Collabora's page.
	OfficeURL string
}

// Handler serves the built files of the web interface.
type Handler struct {
	files fs.FS
	// origins are the extra sources of the policy (the content origin), or empty.
	origins string
	// officeOrigin is the origin of the office editor, with a leading space, or empty when office editing is off.
	officeOrigin string
	// index is index.html split at the nonce placeholder, or nil when the build has none. Joining
	// the parts with a nonce gives the page for that response.
	index []string
}

// New returns a handler for files. The build's index.html is served at "/" and for client routes.
// A build without one (dist/ holds only its .gitignore) serves the not-built page instead. An index
// without the nonce placeholder comes from a stale build, and is refused.
func New(files fs.FS, opts Options) (*Handler, error) {
	origins := ""
	if opts.ContentURL != "" {
		u, err := url.Parse(opts.ContentURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("web: content URL %q has no origin", opts.ContentURL)
		}
		origins = " " + u.Scheme + "://" + u.Host
	}
	officeOrigin := ""
	if opts.OfficeURL != "" {
		u, err := url.Parse(opts.OfficeURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("web: office URL %q has no origin", opts.OfficeURL)
		}
		officeOrigin = " " + u.Scheme + "://" + u.Host
	}
	h := &Handler{files: files, origins: origins, officeOrigin: officeOrigin}
	data, err := fs.ReadFile(files, "index.html")
	if err == nil {
		parts := strings.Split(string(data), cspNoncePlaceholder)
		if len(parts) == 1 {
			return nil, errors.New("web: index.html has no CSP nonce placeholder; rebuild the web interface")
		}
		h.index = parts
	}
	return h, nil
}

// policy returns the content policy. With no nonce it is the policy for assets and the not-built
// page; with one, style-src also allows the inline styles that carry that nonce.
func (h *Handler) policy(nonce string) string {
	style := "'self'"
	if nonce != "" {
		style += " 'nonce-" + nonce + "'"
	}
	return "default-src 'self'; script-src 'self'; style-src " + style + "; img-src 'self' data: blob:" + h.origins +
		"; media-src 'self'" + h.origins +
		"; font-src 'self'; connect-src 'self'; frame-src 'self'" + h.origins + h.officeOrigin +
		"; frame-ancestors 'none'; base-uri 'none'; form-action 'self'" + h.officeOrigin
}

// robotsTxt keeps crawlers away from the pages that open share links. The pages also answer with
// X-Robots-Tag, which binds the crawlers that ignore this file. Its path is "/s" and the paths under it, and
// nothing else: a longer path such as /sign-in is not a share link.
const robotsTxt = `User-agent: *
Disallow: /s$
Disallow: /s/
`

// sharePagePath reports whether p is the page that opens a share link, or a path under it.
func sharePagePath(p string) bool {
	return p == "/s" || strings.HasPrefix(p, "/s/")
}

// Serves reports whether r belongs to the web interface: a GET or HEAD outside the reserved
// paths, and with no name that starts with a dot.
func (h *Handler) Serves(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	for _, prefix := range reserved {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return false
		}
	}
	return !hidden(p)
}

// hidden reports whether p has a name that starts with a dot, which is never served.
func hidden(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// ServeHTTP serves the file at the request path. A path with no file gets index.html, so the
// client-side router can answer it.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/robots.txt" {
		hd := w.Header()
		hd.Set("Content-Type", "text/plain; charset=utf-8")
		hd.Set("Cache-Control", "no-cache")
		hd.Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "robots.txt", time.Time{}, strings.NewReader(robotsTxt))
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	fallback := false
	data, err := fs.ReadFile(h.files, name)
	missingAsset := err != nil && strings.HasPrefix(name, "assets/")
	if hidden(r.URL.Path) || !fs.ValidPath(name) || err != nil {
		name, fallback = "index.html", true
		data, err = fs.ReadFile(h.files, name)
		if err != nil {
			data = []byte(notBuiltPage)
		}
	}
	hd := w.Header()
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "same-origin")
	hd.Set("X-Frame-Options", "DENY")
	if sharePagePath(r.URL.Path) {
		// The page that opens a share link holds the link's secret in its address. Nothing it links to may see
		// that address in a Referer, and no search engine may index it.
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Robots-Tag", "noindex, nofollow")
	}
	if missingAsset {
		// A build asset that is not there is a stale or mistyped reference; answering with the app
		// page would hide that behind a script or style that fails to parse.
		hd.Set("Content-Security-Policy", h.policy(""))
		hd.Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	switch {
	case fallback || name == "index.html":
		hd.Set("Cache-Control", "no-store")
	case strings.HasPrefix(name, "assets/"):
		// Build output names carry a content hash, so a changed file gets a new name.
		hd.Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		hd.Set("Cache-Control", "no-cache")
	}
	if name == "index.html" && h.index != nil && err == nil {
		// The page is built per response: a fresh nonce, drawn from the system's random source.
		nonce, nerr := newNonce()
		if nerr != nil {
			http.Error(w, "web: cannot draw a CSP nonce", http.StatusInternalServerError)
			return
		}
		data = []byte(strings.Join(h.index, nonce))
		hd.Set("Content-Security-Policy", h.policy(nonce))
	} else {
		// Any other file and the not-built page: the policy with no nonce.
		hd.Set("Content-Security-Policy", h.policy(""))
	}
	hd.Set("Content-Type", contentType(name))
	if name == "index.html" {
		// The page is always sent whole: a partial answer would carry a nonce that the policy never saw.
		r.Header.Del("Range")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// nonceReader is the random source of nonces. Tests replace it to make the source fail.
var nonceReader io.Reader = rand.Reader

// newNonce draws 16 random bytes and returns them base64-encoded, as a CSP nonce.
func newNonce() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(nonceReader, b[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}

var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".woff2":       "font/woff2",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".json":        "application/json",
	".txt":         "text/plain; charset=utf-8",
	".webmanifest": "application/manifest+json",
	".ico":         "image/x-icon",
}

func contentType(name string) string {
	if t, ok := contentTypes[strings.ToLower(path.Ext(name))]; ok {
		return t
	}
	return "application/octet-stream"
}
