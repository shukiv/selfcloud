// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
)

// serveCancelled runs the error path of one request whose context is cancelled with cause, and returns the status
// the request was recorded with, the body the client was sent, and the log.
func serveCancelled(t *testing.T, ctx context.Context, cause error) (int, string, string) {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{})
	buf := &bytes.Buffer{}
	s.log = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}
	info := &reqInfo{id: id.New("req"), route: "GET /api/v1/nodes/{nod}/children", sw: sw}
	r := httptest.NewRequest("GET", "/api/v1/nodes/nod_x/children", nil).WithContext(context.WithValue(ctx, ctxKey{}, info))

	s.writeError(sw, r, cause)
	s.finish(r, sw, info, 3*time.Millisecond)
	return sw.status, rec.Body.String(), buf.String()
}

// TestServerShutdownCancelIsAServerFailure: when the server cancels a request at its shutdown deadline, the request
// is not a client that went away. It stays a 500 at ERROR, and the client is sent the JSON error.
func TestServerShutdownCancelIsAServerFailure(t *testing.T) {
	t.Parallel()
	base, cancel := context.WithCancelCause(context.Background())
	cancel(errServerShutdown)
	cause := apierr.Wrap(apierr.Internal, "internal error", context.Canceled)

	status, body, log := serveCancelled(t, base, cause)
	if status != 500 {
		t.Errorf("status %d, want 500 for a request the server cancelled", status)
	}
	if !strings.Contains(log, "level=ERROR") {
		t.Errorf("a request the server cancelled is not logged at ERROR: %s", log)
	}
	if strings.Contains(log, "client disconnected") {
		t.Errorf("a request the server cancelled is logged as a client disconnect: %s", log)
	}
	if !strings.Contains(body, `"code":"internal"`) {
		t.Errorf("the client of a request the server cancelled was not sent the JSON error: %q", body)
	}
}

// TestClientCancelWithCauseStillDisconnects: a client that went away is still a disconnect when the cancel carries a
// cause of its own, so only the server's shutdown cause changes the answer.
func TestClientCancelWithCauseStillDisconnects(t *testing.T) {
	t.Parallel()
	base, cancel := context.WithCancelCause(context.Background())
	cancel(context.Canceled)
	status, _, log := serveCancelled(t, base, apierr.Wrap(apierr.Internal, "internal error", context.Canceled))
	if status != statusClientClosed || strings.Contains(log, "level=ERROR") {
		t.Errorf("a client that went away: status %d, log %s", status, log)
	}
}
