// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
)

// sec1 item 4 (decision 84): /metrics is not on the public listener by default. It shows storage totals, route counts
// and the webhook queue, and it was unauthenticated there.
func TestMetricsIsOffTheMainListenerByDefault(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	if rec := do(t, s.Handler(), "GET", "/metrics"); rec.Code != 404 {
		t.Errorf("GET /metrics on the main listener by default: status %d, want 404", rec.Code)
	}
}

// sec1 item 4 (decision 84): the main listener serves /metrics only with the explicit flag.
func TestMetricsOnTheMainListenerNeedsTheFlag(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) { c.MetricsOnMain = true })
	if rec := do(t, s.Handler(), "GET", "/metrics"); rec.Code != 200 {
		t.Errorf("GET /metrics with metrics_on_main: status %d, want 200", rec.Code)
	}
}

// sec1 item 4 (decision 84): with metrics_listen set, /metrics is on that address, and the main listener does not
// serve it even with the flag.
func TestMetricsListenWinsOverTheFlag(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) {
		c.MetricsListen = "127.0.0.1:0"
		c.MetricsOnMain = true
	})
	if rec := do(t, s.Handler(), "GET", "/metrics"); rec.Code != 404 {
		t.Errorf("with metrics_listen set, the main listener serves /metrics: %d", rec.Code)
	}
}
