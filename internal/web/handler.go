// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web serves the web interface: the built single-page app at /, with its strict security
// headers, and the installation's logo.
package web

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// notBuiltPage is served when the build has no index.html: the interface was not built, so the
// server says so.
const notBuiltPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__SELFCLOUD_BRAND__</title>
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
	// BrandName and BrandAccent are ui.brand.name and ui.brand.accent. They name the installed app and colour its icons
	// and manifest (decision 65).
	BrandName   string
	BrandAccent string
	// BrandArtwork says the installation shows the SelfCloud artwork: it has no logo file, whatever its name (decision 126).
	BrandArtwork bool
	// IconFile is the icon, LogoFile the logo, or nil: the config file's (read with LoadIconFile and LoadLogo) or an
	// upload's. The icons and the favicon are drawn from them (decision 119).
	IconFile *Logo
	LogoFile *Logo
}

// Handler serves the built files of the web interface.
type Handler struct {
	files fs.FS
	// origins are the extra sources of the policy (the content origin), or empty.
	origins string
	// officeOrigin is the origin of the office editor, with a leading space, or empty when office editing is off.
	officeOrigin string
	// page is index.html split at the nonce placeholder, with the name's placeholder still in it, or nil when the build has
	// none. Each brand holds its own copy with the name in it.
	page []string
	// brand is the brand in use. A change builds a new one whole and swaps it in (decision 141), and a request reads it once,
	// so no answer mixes two brands.
	brand atomic.Pointer[Brand]
}

// Brand is one build of the installation's look in the web interface: the name the pages and the manifest carry, the drawn
// icons, the brand's SVG files and the notes about them. It is never changed.
type Brand struct {
	// info names the manifest and colours the icons; icons are the drawn icons by path (pwa.go).
	info  brandInfo
	icons map[string]icon
	// svgs are the brand's SVG files by path: the favicon, the rail's mark and the wordmark (decisions 109 and 119).
	svgs map[string]brandSVG
	// index is the handler's page with the name in it, or nil when the build has none. Joining the parts with a nonce gives
	// the page for that response.
	index []string
	// notes are the lines the server logs about the icons (decision 119).
	notes []string
}

// BrandOptions are what a brand is built from (decision 141): the name, the accent, whether the SelfCloud artwork shows (no
// logo at all, decision 126), and the icon and the logo, from the config file or an upload, or nil.
type BrandOptions struct {
	Name    string
	Accent  string
	Artwork bool
	Icon    *Logo
	Logo    *Logo
}

// BuildBrand builds a brand whole: the icons, the favicon, the manifest's name and the page. It changes nothing the handler
// serves until UseBrand swaps it in, so a brand that cannot be built leaves the one in use.
func (h *Handler) BuildBrand(o BrandOptions) (*Brand, error) {
	info := newBrandInfo(o.Name, o.Accent, o.Artwork)
	pick, err := pickIcons(o.Icon, o.Logo, info)
	if err != nil {
		return nil, err
	}
	icons, err := buildIcons(info, pick.art)
	if err != nil {
		return nil, fmt.Errorf("web: cannot draw the app icons: %w", err)
	}
	b := &Brand{info: info, icons: icons, svgs: brandSVGs(info, pick.favicon), notes: pick.notes}
	if h.page != nil {
		// The title and the noscript text carry the installation's name before any script runs (decision 109).
		b.index = make([]string, len(h.page))
		for i, part := range h.page {
			b.index[i] = strings.ReplaceAll(part, brandPlaceholder, brandHTML(info.name))
		}
	}
	return b, nil
}

// UseBrand swaps in a brand BuildBrand built: the next request is served from it.
func (h *Handler) UseBrand(b *Brand) {
	h.brand.Store(b)
}

// Notes are the lines about the brand's app icons and favicon: why they show the mark, or that a logo is refused for them.
// None is the usual case.
func (b *Brand) Notes() []string {
	return b.notes
}

// IconNotes are the notes of the brand in use.
func (h *Handler) IconNotes() []string {
	return h.brand.Load().notes
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
		h.page = parts
	}
	b, err := h.BuildBrand(BrandOptions{Name: opts.BrandName, Accent: opts.BrandAccent, Artwork: opts.BrandArtwork,
		Icon: opts.IconFile, Logo: opts.LogoFile})
	if err != nil {
		return nil, err
	}
	h.UseBrand(b)
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
		"; font-src 'self'; connect-src 'self'; manifest-src 'self'; worker-src 'self'; frame-src 'self'" + h.origins + h.officeOrigin +
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
	if r.URL.Path == shareTargetPath && (r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodPost) {
		// The share target answers 303 whatever the method (decision 82); it is checked first, as its path is not reserved.
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	if p == wordmarkPath && !h.brand.Load().info.artwork {
		return false
	}
	if isPWAPath(p) {
		// The manifest and the icons sit under /brand, which is reserved below, so they are checked first.
		return true
	}
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
// client-side router can answer it. The brand is read once, so the answer comes from one brand.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b := h.brand.Load()
	if r.URL.Path == shareTargetPath {
		// The share target (decision 82). The body is never read: with no service worker in control, nothing the person
		// shared reaches the server, and the page at /share says so. The answer is the same for every method.
		hd := w.Header()
		hd.Set("Location", "/share")
		hd.Set("Cache-Control", "no-store")
		hd.Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusSeeOther)
		return
	}
	if UnsafeAssetRequest(r) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", h.policy(""))
		hd.Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/robots.txt" {
		hd := w.Header()
		hd.Set("Content-Type", "text/plain; charset=utf-8")
		hd.Set("Cache-Control", "no-cache")
		hd.Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "robots.txt", time.Time{}, strings.NewReader(robotsTxt))
		return
	}
	if h.servePWA(w, r, b) {
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	fallback := false
	data, err := fs.ReadFile(h.files, name)
	if err == nil && isVariantName(name) {
		// A precompressed variant is served only as the encoding of its file (decision 131): asked for by name, it is missing.
		err = fs.ErrNotExist
	}
	missingAsset := err != nil && strings.HasPrefix(name, "assets/")
	if hidden(r.URL.Path) || !fs.ValidPath(name) || err != nil {
		name, fallback = "index.html", true
		data, err = fs.ReadFile(h.files, name)
		if err != nil {
			data = []byte(strings.ReplaceAll(notBuiltPage, brandPlaceholder, brandHTML(b.info.name)))
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
	if !fallback && err == nil && strings.HasPrefix(name, "assets/") {
		// An asset with precompressed variants (decision 131) is answered in the encoding the client takes, brotli first.
		// Vary says so on every answer of such a file, encoded or not. A request with a Range gets the file as it is.
		if br, gz := h.variants(name); br || gz {
			hd.Add("Vary", "Accept-Encoding")
			if r.Header.Get("Range") == "" {
				accept := strings.Join(r.Header.Values("Accept-Encoding"), ",")
				if enc := pickEncoding(accept, br, gz); enc != "" {
					if variant, verr := fs.ReadFile(h.files, name+variantSuffix[enc]); verr == nil {
						data = variant
						hd.Set("Content-Encoding", enc)
						// ServeContent sets no length for an encoded answer, so the length of the bytes sent goes here.
						hd.Set("Content-Length", strconv.Itoa(len(data)))
					}
				}
			}
		}
	}
	if name == "index.html" && b.index != nil && err == nil {
		// The page is built per response: a fresh nonce, drawn from the system's random source.
		nonce, nerr := newNonce()
		if nerr != nil {
			http.Error(w, "web: cannot draw a CSP nonce", http.StatusInternalServerError)
			return
		}
		data = []byte(strings.Join(b.index, nonce))
		hd.Set("Content-Security-Policy", h.policy(nonce))
	} else {
		// Any other file and the not-built page: the policy with no nonce.
		hd.Set("Content-Security-Policy", h.policy(""))
	}
	hd.Set("Content-Type", contentType(name))
	if name == "offline.html" && err == nil {
		// The offline page names the installation, so the worker's cached copy shows the installation's name.
		data = bytes.ReplaceAll(data, []byte(brandPlaceholder), []byte(brandHTML(b.info.name)))
		// Its validator covers the name it carries, so a renamed installation gives the worker a new copy to fetch (decision 65).
		sum := sha256.Sum256(data)
		hd.Set("ETag", `"`+hex.EncodeToString(sum[:16])+`"`)
	}
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

// UnsafeAssetRequest reports whether r names a file under /assets/ by a path that is not its own clean form: a path with a
// dot segment, a double slash, a backslash or an escape left in it, decoded or as sent. Such a request never gets an answer
// from the build, not a redirect to the cleaned path and not the app page (the service worker relies on this).
func UnsafeAssetRequest(r *http.Request) bool {
	p := r.URL.Path
	if !strings.HasPrefix(p, "/assets/") {
		return false
	}
	if path.Clean(p) != p || strings.ContainsAny(p, `\%`) || strings.Contains(p, "//") {
		return true
	}
	raw := r.URL.EscapedPath()
	return path.Clean(raw) != raw
}
