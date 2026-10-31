// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
)

// finishRequest runs the error path of one request as observe does: the handler's error is written, then the request
// is logged and counted. reqCtx is the request's own context, which the caller may have canceled, and cause is the
// error the handler returned. It returns the log, the server (for its scrape), and the status the request was recorded with.
func finishRequest(t *testing.T, reqCtx context.Context, cause error) (string, *Server, int) {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{})
	buf := &bytes.Buffer{}
	s.log = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}
	info := &reqInfo{id: id.New("req"), route: "GET /api/v1/nodes/{nod}/children", sw: sw}
	ctx := context.WithValue(reqCtx, ctxKey{}, info)
	r := httptest.NewRequest("GET", "/api/v1/nodes/nod_x/children", nil).WithContext(ctx)

	s.writeError(sw, r, cause)
	s.finish(r, sw, info, 3*time.Millisecond)

	status := sw.status
	return buf.String(), s, status
}

// scrapeCount returns the value of the request counter for one route and status, read from the metrics scrape, as an
// operator's metrics show it.
func scrapeCount(t *testing.T, s *Server, route string, status int) int {
	t.Helper()
	rec := httptest.NewRecorder()
	s.metrics.handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	prefix := `selfcloud_http_requests_total{method="GET",route="` + route + `",status="` + strconv.Itoa(status) + `"} `
	for _, l := range strings.Split(rec.Body.String(), "\n") {
		if v, ok := strings.CutPrefix(l, prefix); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				t.Fatalf("scrape value %q: %v", v, err)
			}
			return n
		}
	}
	return 0
}

// serverFailures counts the 5xx requests in a scrape: the failures an alert on server errors reads.
func serverFailures(t *testing.T, s *Server) int {
	t.Helper()
	rec := httptest.NewRecorder()
	s.metrics.handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	total := 0
	for _, l := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(l, "selfcloud_http_requests_total{") || !strings.Contains(l, `status="5`) {
			continue
		}
		if i := strings.LastIndex(l, " "); i >= 0 {
			n, _ := strconv.Atoi(l[i+1:])
			total += n
		}
	}
	return total
}

// TestClientGoneIsNotAServerError: when the request's own context is canceled because the client went away, the
// failure is a client disconnect. It is logged below ERROR and is not counted as a server failure (5xx).
func TestClientGoneIsNotAServerError(t *testing.T) {
	t.Parallel()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cause := apierr.Wrap(apierr.Internal, "internal error", context.Canceled)

	log, s, status := finishRequest(t, canceled, cause)
	if strings.Contains(log, "level=ERROR") {
		t.Errorf("a client that went away logged at ERROR: %s", log)
	}
	if !strings.Contains(log, "client disconnected") {
		t.Errorf("the disconnect is not logged as one: %s", log)
	}
	if status >= 500 {
		t.Errorf("a client that went away was recorded with status %d", status)
	}
	if n := serverFailures(t, s); n != 0 {
		t.Errorf("a client that went away counted as %d server failure(s) in metrics", n)
	}
	if n := scrapeCount(t, s, "GET /api/v1/nodes/{nod}/children", status); n != 1 {
		t.Errorf("the request is not counted under its status %d: count %d", status, n)
	}
}

// TestServerFailuresStayErrors: a failure that the client did not cause is still a 500 at ERROR. That covers a
// server-side deadline, a cancel the request did not see, and a cancel that is not the request's own.
func TestServerFailuresStayErrors(t *testing.T) {
	t.Parallel()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	live := context.Background()

	cases := []struct {
		name   string
		reqCtx context.Context
		cause  error
	}{
		{"server-side deadline", expired, context.DeadlineExceeded},
		{"deadline the client did not cause, under a canceled request", canceled, context.DeadlineExceeded},
		{"cancel with a live request", live, context.Canceled},
		{"other failure with a canceled client", canceled, errors.New("disk read failed")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cause := apierr.Wrap(apierr.Internal, "internal error", tc.cause)
			log, s, status := finishRequest(t, tc.reqCtx, cause)
			if status != 500 {
				t.Errorf("status %d, want 500", status)
			}
			if !strings.Contains(log, "level=ERROR") {
				t.Errorf("not logged at ERROR: %s", log)
			}
			if n := serverFailures(t, s); n != 1 {
				t.Errorf("counted %d server failures, want 1", n)
			}
		})
	}
}
