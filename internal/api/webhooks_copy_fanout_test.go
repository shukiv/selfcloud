// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
)

// A copy of 1,000 files reports each file to five subscribed endpoints, and the folder once to six: the files times
// the endpoints is the cost, and it is capped (decision 27).
func TestWebhookCopyFanoutLimit(t *testing.T) {
	for _, c := range []struct {
		endpoints int
		perFile   bool
	}{{5, true}, {6, false}} {
		fx := newWebhookFixture(t)
		for i := 0; i < c.endpoints; i++ {
			fx.create(t, map[string]any{"url": fmt.Sprintf("https://hooks%d.example.com/in", i), "events": []string{"file.created"}})
		}
		one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
		dir := bulkDir(t, fx.s, fx.acc.ID, one.ID, store.WebhookFolderThreshold)

		seen := len(outboxOf(t, fx.s, fx.acc.ID))
		wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+dir+"/copy", fx.key, map[string]any{"name": "copied"}), 201)
		evs := outboxOf(t, fx.s, fx.acc.ID)[seen:]
		if c.perFile {
			checkBulkEvents(t, evs, store.WebhookFolderThreshold, "file.created")
			continue
		}
		if len(evs) != 1 || evs[0].Type != "file.created" || !strings.Contains(evs[0].Payload, `"subtree":true`) {
			t.Fatalf("%d endpoints: %d events, want one file.created folder summary", c.endpoints, len(evs))
		}
	}
}
