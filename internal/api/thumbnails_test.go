// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// runThumbnails runs the server's derived workers for the test, and stops them when it ends. The decoder runs as this
// test binary's thumb subcommand (TestMain).
func runThumbnails(t *testing.T, s *Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.derivedPool.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 160, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// bombPNG claims 30000 by 30000 pixels with no pixel data: a valid header, far over the pixel cap.
func bombPNG() []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 30000)
	binary.BigEndian.PutUint32(ihdr[4:], 30000)
	ihdr[8], ihdr[9] = 8, 6
	chunk := func(typ string, data []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(data)))
		b.Write(n[:])
		b.WriteString(typ)
		b.Write(data)
		var c [4]byte
		binary.BigEndian.PutUint32(c[:], crc32.ChecksumIEEE(append([]byte(typ), data...)))
		b.Write(c[:])
	}
	chunk("IHDR", ihdr)
	chunk("IEND", nil)
	return b.Bytes()
}

// putImage writes a file of the given stored type, which the thumbnail route decides on.
func putImage(t *testing.T, s *Server, cred, fld, p, mimeType string, data []byte) nodeJSON {
	t.Helper()
	rec := rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/files?"+q(p), cred, string(data),
		map[string]string{"Content-Type": mimeType})
	wantStatus(t, rec, 201)
	return decode[nodeJSON](t, rec)
}

func thumbPath(nodeID string, size int) string {
	if size == 0 {
		return "/api/v1/nodes/" + nodeID + "/thumbnail"
	}
	return fmt.Sprintf("/api/v1/nodes/%s/thumbnail?size=%d", nodeID, size)
}

// thumbAs asks for a thumbnail with a credential (a key or a session cookie).
func thumbAs(t *testing.T, s *Server, cred string, sess *testSession, nodeID string, size int) *httptest.ResponseRecorder {
	t.Helper()
	if sess != nil {
		return rawSession(t, s, *sess, "GET", thumbPath(nodeID, size), "", nil)
	}
	return rawCall(t, s, "GET", thumbPath(nodeID, size), cred, "", nil)
}

// waitThumb asks until the thumbnail is no longer being made, and returns that answer. A 202 is waited for; any other
// status is returned as it is. It fails the test when no answer comes in time.
func waitThumb(t *testing.T, s *Server, cred string, sess *testSession, nodeID string, size int) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		rec := thumbAs(t, s, cred, sess, nodeID, size)
		if rec.Code != http.StatusAccepted {
			return rec
		}
		if time.Now().After(deadline) {
			t.Fatalf("the thumbnail of %s is still pending after 60 seconds", nodeID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func imageSize(t *testing.T, body []byte) image.Point {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("the thumbnail is not a JPEG: %v", err)
	}
	return image.Pt(img.Bounds().Dx(), img.Bounds().Dy())
}

func TestThumbnailIsMadeOnceAndServed(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Pictures")
	pic := putImage(t, s, key, fld.ID, "/pic.png", "image/png", pngBytes(t, 800, 600))

	// Before any request nothing is made, and the node says so.
	if rec := thumbAs(t, s, key, nil, pic.ID, 0); rec.Code != http.StatusAccepted || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("first request: %d %s, want 202 with Retry-After", rec.Code, rec.Body)
	}
	stat := decode[nodeJSON](t, call(t, s, "GET", "/api/v1/nodes/"+pic.ID, key, nil))
	if stat.HasThumbnail == nil || *stat.HasThumbnail {
		t.Fatalf("has_thumbnail before the thumbnail is ready: %v", stat.HasThumbnail)
	}

	rec := waitThumb(t, s, key, nil, pic.ID, 0)
	wantStatus(t, rec, http.StatusOK)
	h := rec.Header()
	if h.Get("Content-Type") != "image/jpeg" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != "sandbox; default-src 'none'" || h.Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Errorf("headers: %v", h)
	}
	etag := h.Get("ETag")
	if len(etag) != 66 || !strings.HasPrefix(etag, `"`) {
		t.Errorf("ETag %q is not a quoted SHA-256", etag)
	}
	if got := imageSize(t, rec.Body.Bytes()); got != image.Pt(256, 192) {
		t.Errorf("256 thumbnail is %v, want 256x192", got)
	}

	// A conditional request for the same version is a 304, and HEAD has no body.
	cond := rawCall(t, s, "GET", thumbPath(pic.ID, 0), key, "", map[string]string{"If-None-Match": etag})
	wantStatus(t, cond, http.StatusNotModified)
	head := rawCall(t, s, "HEAD", thumbPath(pic.ID, 0), key, "", nil)
	wantStatus(t, head, http.StatusOK)
	if head.Body.Len() != 0 {
		t.Errorf("HEAD carried %d bytes", head.Body.Len())
	}

	// The stat now says the thumbnail is ready, and a listing says so too.
	stat = decode[nodeJSON](t, call(t, s, "GET", "/api/v1/nodes/"+pic.ID, key, nil))
	if stat.HasThumbnail == nil || !*stat.HasThumbnail {
		t.Errorf("has_thumbnail after ready: %v", stat.HasThumbnail)
	}
	list := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/folders/"+fld.ID+"/children", key, nil))
	if len(list.Items) != 1 || list.Items[0].HasThumbnail == nil || !*list.Items[0].HasThumbnail {
		t.Errorf("listing has_thumbnail: %+v", list.Items)
	}

	// Another size is made on its own.
	big := waitThumb(t, s, key, nil, pic.ID, 1024)
	wantStatus(t, big, http.StatusOK)
	// The source is 800 by 600, smaller than 1024, so it is not enlarged.
	if got := imageSize(t, big.Body.Bytes()); got != image.Pt(800, 600) {
		t.Errorf("1024 thumbnail is %v, want 800x600 (no enlargement)", got)
	}
	wantStatus(t, thumbAs(t, s, key, nil, pic.ID, 77), http.StatusBadRequest)
}

func TestThumbnailRefusals(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Mixed")
	dir := mkdirs(t, s, key, fld.ID, "/dir")
	note := putNode(t, s, key, fld.ID, "/note.txt", "text")
	svg := putImage(t, s, key, fld.ID, "/drawing.svg", "image/svg+xml",
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	corrupt := putImage(t, s, key, fld.ID, "/broken.png", "image/png", []byte("\x89PNG\r\n\x1a\nnot a png"))
	bomb := putImage(t, s, key, fld.ID, "/bomb.png", "image/png", bombPNG())
	pic := putImage(t, s, key, fld.ID, "/pic.jpg", "image/jpeg", pngBytes(t, 40, 40))

	// Things that are not images get no thumbnail, and no work is queued for them.
	for name, id := range map[string]string{"folder": dir.ID, "text": note.ID, "svg": svg.ID} {
		if rec := thumbAs(t, s, key, nil, id, 0); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, rec.Code)
		}
	}
	// A shortcut to a picture has no thumbnail of its own.
	sc := decode[nodeJSON](t, call(t, s, "POST", "/api/v1/nodes/"+dir.ID+"/shortcuts", key, map[string]any{"target": pic.ID}))
	if rec := thumbAs(t, s, key, nil, sc.ID, 0); rec.Code != http.StatusNotFound {
		t.Errorf("shortcut: %d, want 404", rec.Code)
	}
	// Sources that cannot be decoded fail once and answer 404 from then on.
	for name, id := range map[string]string{"corrupt": corrupt.ID, "bomb": bomb.ID} {
		if rec := waitThumb(t, s, key, nil, id, 0); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s, want 404", name, rec.Code, rec.Body)
		}
		if rec := thumbAs(t, s, key, nil, id, 0); rec.Code != http.StatusNotFound {
			t.Errorf("%s again: %d, want 404 without a retry", name, rec.Code)
		}
	}
	// A small JPEG declared as image/jpeg is decoded by its bytes, so it is thumbnailed.
	wantStatus(t, waitThumb(t, s, key, nil, pic.ID, 0), http.StatusOK)
}

func TestThumbnailFollowsAccessAndTheFile(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	runThumbnails(t, fx.s)
	s := fx.s
	pic := putImage(t, s, fx.key, fx.fld.ID, "/pic.png", "image/png", pngBytes(t, 300, 200))
	other := newFolder(t, s, fx.key, fx.acc.ID, "Elsewhere")
	ann := fx.annSess

	// Ann has no role on the picture: it is not found, as for a file that does not exist.
	if rec := thumbAs(t, s, "", &ann, pic.ID, 0); rec.Code != http.StatusNotFound {
		t.Fatalf("a user with no role: %d, want 404", rec.Code)
	}
	// With a viewer role she reads it, and viewing is not refused by the no-download rule.
	fx.setPerm(t, pic.ID, fx.ann.ID, "viewer")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/nodes/"+pic.ID, fx.key, map[string]any{"no_download": true}), 200)
	wantStatus(t, waitThumb(t, s, "", &ann, pic.ID, 0), http.StatusOK)

	// The thumbnail is of the file, so a move keeps it: the same answer, with no new work.
	moved := call(t, s, "POST", "/api/v1/nodes/"+pic.ID+"/move", fx.key, map[string]any{"folder": other.ID})
	wantStatus(t, moved, http.StatusOK)
	wantStatus(t, thumbAs(t, s, "", &ann, pic.ID, 0), http.StatusOK)

	// Revoked access stops the thumbnail at once, on the next call.
	wantStatus(t, call(t, s, "DELETE", "/api/v1/nodes/"+pic.ID+"/permissions/"+fx.ann.ID, fx.key, nil), 204)
	if rec := thumbAs(t, s, "", &ann, pic.ID, 0); rec.Code != http.StatusNotFound {
		t.Errorf("after revoking: %d, want 404", rec.Code)
	}

	// A trashed picture is not found, and comes back when restored.
	wantStatus(t, call(t, s, "DELETE", "/api/v1/nodes/"+pic.ID, fx.key, nil), 204)
	if rec := thumbAs(t, s, fx.key, nil, pic.ID, 0); rec.Code != http.StatusNotFound {
		t.Errorf("a trashed picture: %d, want 404", rec.Code)
	}

	// Another account holds no path to the picture.
	admin := fx.admin
	_, otherKey := newAccount(t, s, admin, "Other")
	if rec := thumbAs(t, s, otherKey, nil, pic.ID, 0); rec.Code != http.StatusNotFound {
		t.Errorf("another account: %d, want 404", rec.Code)
	}
}

func TestThumbnailIsSharedByCopiesAndRenewedByNewVersions(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Pictures")
	pic := putImage(t, s, key, fld.ID, "/first.png", "image/png", pngBytes(t, 120, 90))
	first := waitThumb(t, s, key, nil, pic.ID, 0)
	wantStatus(t, first, http.StatusOK)

	// A copy shares the blob, so its thumbnail is ready at once: no second decode is queued.
	cp := decode[nodeJSON](t, call(t, s, "POST", "/api/v1/nodes/"+pic.ID+"/copy", key,
		map[string]any{"on_conflict": "rename"}))
	wantStatus(t, thumbAs(t, s, key, nil, cp.ID, 0), http.StatusOK)

	// A new version of the file is a new blob, so its thumbnail is made again, and differs.
	rec := rawCall(t, s, "PUT", "/api/v1/nodes/"+pic.ID+"/content", key, string(pngBytes(t, 200, 100)),
		map[string]string{"Content-Type": "image/png", "If-Match": pic.ETag})
	wantStatus(t, rec, http.StatusOK)
	renewed := waitThumb(t, s, key, nil, pic.ID, 0)
	wantStatus(t, renewed, http.StatusOK)
	if renewed.Header().Get("ETag") == first.Header().Get("ETag") {
		t.Errorf("a new version kept the old thumbnail")
	}
	// The new source is 200 by 100: smaller than 256, so it is not enlarged.
	if got := imageSize(t, renewed.Body.Bytes()); got != image.Pt(200, 100) {
		t.Errorf("new version thumbnail %v, want 200x100", got)
	}
}

func TestThumbnailOnTheContentOriginIsASignedLink(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) {
		c.ContentURL = "https://files.example.test"
	})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Pictures")
	pic := putImage(t, s, key, fld.ID, "/pic.png", "image/png", pngBytes(t, 64, 48))

	// With content_url set, the main origin does not serve the bytes: a ready thumbnail is a redirect to a link.
	var loc string
	deadline := time.Now().Add(60 * time.Second)
	for loc == "" {
		rec := thumbAs(t, s, key, nil, pic.ID, 0)
		switch rec.Code {
		case http.StatusSeeOther:
			loc = rec.Header().Get("Location")
		case http.StatusAccepted:
			if time.Now().After(deadline) {
				t.Fatal("the thumbnail never became ready")
			}
			time.Sleep(20 * time.Millisecond)
		default:
			t.Fatalf("on the main origin: %d %s", rec.Code, rec.Body)
		}
	}
	if !strings.HasPrefix(loc, "https://files.example.test/api/v1/links/") {
		t.Fatalf("Location %q is not a signed link on the content origin", loc)
	}
	// The link serves the image on the content origin, cross-origin.
	got := rawCall(t, s, "GET", loc, "", "", nil)
	wantStatus(t, got, http.StatusOK)
	if got.Header().Get("Content-Type") != "image/jpeg" || got.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Errorf("link answer headers: %v", got.Header())
	}
	imageSize(t, got.Body.Bytes())
	// A link is not a way to ask for a thumbnail of another node: a tampered token is not found.
	tampered := strings.Replace(loc, "/api/v1/links/", "/api/v1/links/x", 1)
	wantStatus(t, rawCall(t, s, "GET", tampered, "", "", nil), http.StatusNotFound)
	// The API refuses to sign a thumbnail disposition itself.
	bad := call(t, s, "POST", "/api/v1/nodes/"+pic.ID+"/links", key, map[string]any{"disposition": "thumb_256"})
	wantStatus(t, bad, http.StatusBadRequest)
}
