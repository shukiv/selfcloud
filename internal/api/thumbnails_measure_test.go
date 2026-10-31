// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/mail"
)

// TestMeasureTilePage times a page of 100 thumbnails, cold and warm, on this machine. It runs only when
// SELFCLOUD_MEASURE is set, because it takes minutes of CPU and is a measurement, not a check:
//
//	SELFCLOUD_MEASURE=1 go test ./internal/api/ -run TestMeasureTilePage -v
func TestMeasureTilePage(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE") == "" {
		t.Skip("set SELFCLOUD_MEASURE=1 to measure")
	}
	s := newAuthServer(t, &mail.Recorder{})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Photos")

	// 100 distinct JPEGs of 2000 by 1500, the size of a phone photo scaled down.
	var ids []string
	for i := 0; i < 100; i++ {
		img := image.NewNRGBA(image.Rect(0, 0, 2000, 1500))
		for y := 0; y < 1500; y += 3 {
			for x := 0; x < 2000; x += 3 {
				img.Set(x, y, color.NRGBA{uint8(x + i), uint8(y), uint8(i * 2), 255})
			}
		}
		var b bytes.Buffer
		if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 85}); err != nil {
			t.Fatal(err)
		}
		n := putImage(t, s, key, fld.ID, fmt.Sprintf("/p%03d.jpg", i), "image/jpeg", b.Bytes())
		ids = append(ids, n.ID)
	}

	page := func() (time.Duration, int) {
		start := time.Now()
		ready := 0
		for _, id := range ids {
			rec := thumbAs(t, s, key, nil, id, 0)
			if rec.Code == http.StatusOK {
				ready++
			}
		}
		return time.Since(start), ready
	}

	// Cold: the first pass queues 100 files and the workers make them. Poll until all are ready.
	start := time.Now()
	for {
		_, ready := page()
		if ready == len(ids) {
			break
		}
		if time.Since(start) > 10*time.Minute {
			t.Fatalf("only %d of 100 thumbnails were ready after 10 minutes", ready)
		}
		time.Sleep(100 * time.Millisecond)
	}
	cold := time.Since(start)

	// Warm: every thumbnail is ready, so the page is served from the store and the files alone.
	warm, ready := page()
	if ready != len(ids) {
		t.Fatalf("warm page: %d ready", ready)
	}
	warm2, _ := page()

	// The listing that the grid reads, with has_thumbnail, for the same 100 nodes.
	listStart := time.Now()
	list := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/folders/"+fld.ID+"/children?limit=200", key, nil))
	listed := time.Since(listStart)
	withThumb := 0
	for _, it := range list.Items {
		if it.HasThumbnail != nil && *it.HasThumbnail {
			withThumb++
		}
	}

	t.Logf("cold: 100 thumbnails ready in %v (%.1f ms each, two workers)", cold, float64(cold.Milliseconds())/100)
	t.Logf("warm: 100 GETs of ready thumbnails in %v and %v (%.2f ms each)", warm, warm2,
		float64(warm.Microseconds())/1000/100)
	t.Logf("listing of 100 with has_thumbnail: %v, %d of 100 flagged", listed, withThumb)
}
