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

// serveError runs the error path of one request with a live context, and returns the status, the log, and the server
// whose metrics count it.
func serveError(t *testing.T, cause error) (int, string, *Server) {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{})
	buf := &bytes.Buffer{}
	s.log = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}
	info := &reqInfo{id: id.New("req"), route: "POST /api/v1/folders/{fld}/files", sw: sw}
	r := httptest.NewRequest("POST", "/api/v1/folders/fld_x/files", nil).WithContext(context.WithValue(context.Background(), ctxKey{}, info))
	s.writeError(sw, r, cause)
	s.finish(r, sw, info, 3*time.Millisecond)
	return sw.status, buf.String(), s
}

// TestQuotaRefusalIsNotAServerFailure: a refusal because the quota is full is the client's answer, not a fault. It is
// logged below ERROR and is not counted among the server failures.
func TestQuotaRefusalIsNotAServerFailure(t *testing.T) {
	t.Parallel()
	status, log, s := serveError(t, apierr.New(apierr.QuotaExceeded, "storage limit reached"))
	if status != apierr.QuotaExceeded.Status() {
		t.Errorf("status %d, want %d", status, apierr.QuotaExceeded.Status())
	}
	if strings.Contains(log, "level=ERROR") {
		t.Errorf("a quota refusal is logged at ERROR: %s", log)
	}
	if n := serverFailures(t, s); n != 0 {
		t.Errorf("a quota refusal counted as %d server failure(s)", n)
	}
}

// TestStorageFullStaysAServerFailure: the disk being full is a real storage failure. It keeps the ERROR and the count.
func TestStorageFullStaysAServerFailure(t *testing.T) {
	t.Parallel()
	_, log, s := serveError(t, apierr.New(apierr.StorageFull, "the disk is full"))
	if !strings.Contains(log, "level=ERROR") {
		t.Errorf("a storage failure is not logged at ERROR: %s", log)
	}
	if n := serverFailures(t, s); n != 1 {
		t.Errorf("a storage failure counted %d server failures, want 1", n)
	}
}
