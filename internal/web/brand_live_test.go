// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// The brand changes while the server runs (decision 141): a new one is built whole, then swapped in at once.

// liveFiles is a build whose index and offline page carry the name's placeholder.
func liveFiles() fstest.MapFS {
	files := testFiles()
	files["index.html"] = &fstest.MapFile{Data: []byte(`<!doctype html><meta property="csp-nonce" nonce="` + cspNoncePlaceholder +
		`"><title>` + brandPlaceholder + `</title><noscript>` + brandPlaceholder + ` needs JavaScript.</noscript>`)}
	files["offline.html"] = &fstest.MapFile{Data: []byte("<p>" + brandPlaceholder + "</p>")}
	return files
}

// manifestOf reads the manifest the handler serves.
func manifestOf(t *testing.T, h *Handler) webManifest {
	t.Helper()
	var m webManifest
	if err := json.Unmarshal(get(t, h, http.MethodGet, manifestPath).Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	return m
}

// TestBrandRebuildSwapsEverything: a built brand changes nothing until it is used; once used, the next page carries the new
// name, and the manifest, the favicon, the icons, the offline page and the wordmark follow it.
func TestBrandRebuildSwapsEverything(t *testing.T) {
	h := newTestHandler(t, liveFiles(), Options{BrandName: "SelfCloud", BrandAccent: "#053D8E", BrandArtwork: true})
	oldIcon := get(t, h, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag")
	oldFavicon := favicon(t, h)
	icon, err := NewIcon(quadrantPNG(t, 512), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	logo, err := NewLogo([]byte(logoSVG), "image/svg+xml")
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.BuildBrand(BrandOptions{Name: "Acme <Drive>", Accent: "#0F766E", Artwork: false, Icon: icon, Logo: logo})
	if err != nil {
		t.Fatal(err)
	}
	if body := get(t, h, http.MethodGet, "/").Body.String(); !strings.Contains(body, "<title>SelfCloud</title>") {
		t.Errorf("a brand that is built but not used changed the page: %.200s", body)
	}
	h.UseBrand(b)

	body := get(t, h, http.MethodGet, "/").Body.String()
	if !strings.Contains(body, "<title>Acme &lt;Drive&gt;</title>") || !strings.Contains(body, "Acme &lt;Drive&gt; needs JavaScript.") ||
		strings.Contains(body, "SelfCloud") {
		t.Errorf("the page after the swap: %.300s", body)
	}
	if m := manifestOf(t, h); m.Name != "Acme <Drive>" || m.ThemeColor != "#0F766E" {
		t.Errorf("manifest = %s %s", m.Name, m.ThemeColor)
	}
	if f := favicon(t, h); f == oldFavicon || !strings.Contains(f, "data:image/png") {
		t.Errorf("the favicon is not the new icon's: %.120s", f)
	}
	if e := get(t, h, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag"); e == oldIcon || e == "" {
		t.Errorf("the icon's ETag did not change: %q", e)
	}
	quadrantsOK(t, "the swapped icon", iconImage(t, h, "/brand/icon-512.png"))
	if p := get(t, h, http.MethodGet, "/offline.html").Body.String(); !strings.Contains(p, "Acme &lt;Drive&gt;") {
		t.Errorf("offline page = %q", p)
	}
	// The installation has its own logo now, so it has no wordmark: the path is not served, and a request that reaches the
	// handler anyway gets 404, not the app page.
	if h.Serves(httptest.NewRequest(http.MethodGet, wordmarkPath, nil)) {
		t.Error("the wordmark is still served")
	}
	if rec := get(t, h, http.MethodGet, wordmarkPath); rec.Code != http.StatusNotFound {
		t.Errorf("the wordmark path answers %d", rec.Code)
	}
	if len(b.Notes()) != 0 || len(h.IconNotes()) != 0 {
		t.Errorf("notes = %q, %q", b.Notes(), h.IconNotes())
	}

	// Back to the default brand: the artwork again, and the page's old name.
	def, err := h.BuildBrand(BrandOptions{Name: "SelfCloud", Accent: "#053D8E", Artwork: true})
	if err != nil {
		t.Fatal(err)
	}
	h.UseBrand(def)
	if e := get(t, h, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag"); e != oldIcon {
		t.Errorf("the default icon's ETag = %q, want %q", e, oldIcon)
	}
	if !h.Serves(httptest.NewRequest(http.MethodGet, wordmarkPath, nil)) || get(t, h, http.MethodGet, wordmarkPath).Code != http.StatusOK {
		t.Error("the wordmark is not served with the artwork back")
	}
}

// TestBrandRebuildOfTheNotBuiltPage: the page that says the interface is not built names the brand in use.
func TestBrandRebuildOfTheNotBuiltPage(t *testing.T) {
	h := newTestHandler(t, unbuiltFiles(), Options{BrandName: "SelfCloud", BrandAccent: "#053D8E", BrandArtwork: true})
	b, err := h.BuildBrand(BrandOptions{Name: "Acme Drive", Accent: "#053D8E", Artwork: true})
	if err != nil {
		t.Fatal(err)
	}
	h.UseBrand(b)
	if body := get(t, h, http.MethodGet, "/").Body.String(); !strings.Contains(body, "<title>Acme Drive</title>") {
		t.Errorf("not-built page: %.200s", body)
	}
}

// TestBrandSwapWhileServing: pages, manifests and icons served while the brand is swapped again and again each come from one
// brand whole (run with -race).
func TestBrandSwapWhileServing(t *testing.T) {
	h := newTestHandler(t, liveFiles(), Options{BrandName: "One", BrandAccent: "#053D8E", BrandArtwork: true})
	brands := map[string]*Brand{}
	for _, name := range []string{"One", "Two"} {
		b, err := h.BuildBrand(BrandOptions{Name: name, Accent: "#053D8E", Artwork: true})
		if err != nil {
			t.Fatal(err)
		}
		brands[name] = b
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				h.UseBrand(brands["Two"])
			} else {
				h.UseBrand(brands["One"])
			}
		}
	}()
	for i := 0; i < 200; i++ {
		body := get(t, h, http.MethodGet, "/").Body.String()
		one, two := strings.Count(body, "One"), strings.Count(body, "Two")
		if one+two != 2 || (one != 0 && two != 0) {
			t.Fatalf("a page with two brands: %.200s", body)
		}
		if m := manifestOf(t, h); m.Name != "One" && m.Name != "Two" {
			t.Fatalf("manifest name %q", m.Name)
		}
	}
	close(stop)
	wg.Wait()
}
