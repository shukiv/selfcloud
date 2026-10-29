// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web serves the web interface: the built single-page app at /, with its strict security
// headers, and the installation's logo.
package web

import (
	"bytes"
	"fmt"
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

// Options configure a Handler.
type Options struct {
	// ContentURL is content_url, or empty. Its origin is allowed for images and frames, because
	// file previews load from it.
	ContentURL string
}

// Handler serves the built files of the web interface.
type Handler struct {
	files fs.FS
	csp   string
}

// New returns a handler for files. The build's index.html is served at "/" and for client routes.
// A build without one (dist/ holds only its .gitignore) serves the not-built page instead.
func New(files fs.FS, opts Options) (*Handler, error) {
	imgSrc, frameSrc := "", ""
	if opts.ContentURL != "" {
		u, err := url.Parse(opts.ContentURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("web: content URL %q has no origin", opts.ContentURL)
		}
		origin := u.Scheme + "://" + u.Host
		imgSrc, frameSrc = " "+origin, " "+origin
	}
	csp := "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:" + imgSrc +
		"; font-src 'self'; connect-src 'self'; frame-src 'self'" + frameSrc +
		"; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
	return &Handler{files: files, csp: csp}, nil
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
	hd.Set("Content-Security-Policy", h.csp)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "same-origin")
	hd.Set("X-Frame-Options", "DENY")
	if missingAsset {
		// A build asset that is not there is a stale or mistyped reference; answering with the app
		// page would hide that behind a script or style that fails to parse.
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
	hd.Set("Content-Type", contentType(name))
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
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
