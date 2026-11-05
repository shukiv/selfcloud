// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// sec1 item 9 (decision 84): a refused /wopi request is logged at warn once per client address per window, and at debug
// after that, so unauthenticated requests cannot fill the log with one warning each. Here the office is configured, so
// a request without a valid proof is refused as a forgery, which is the warned case.
func TestWOPIRefusalsWarnOncePerAddressPerWindow(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	var buf bytes.Buffer
	fx.s.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	for i := 0; i < 5; i++ {
		rec := remoteCall(t, fx.s, "GET", nodeRoute(id.New("nod"))+"?access_token=x", "192.0.2.50:4000", nil)
		if rec.Code < 400 {
			t.Fatalf("forged WOPI request %d: status %d, want a refusal", i+1, rec.Code)
		}
	}
	warns := 0
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "level=WARN") && strings.Contains(line, "wopi request refused") {
			warns++
		}
	}
	if warns != 1 {
		t.Errorf("warn lines for five refusals from one address = %d, want 1", warns)
	}
}
