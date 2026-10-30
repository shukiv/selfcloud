// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apispec "github.com/shukiv/selfcloud/api"
	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

func newTestServer(t *testing.T, mutate ...func(*config.Config)) *Server {
	t.Helper()
	return newTestServerWith(t, nil, mutate...)
}

// newTestServerWith is newTestServer with a mail sender, which nil leaves unconfigured.
func newTestServerWith(t *testing.T, mailer mail.Sender, mutate ...func(*config.Config)) *Server {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.Storage.MinFree = 0
	for _, f := range mutate {
		f(cfg)
	}
	cfg.Store.Path = filepath.Join(cfg.DataDir, "selfcloud.db")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), store.Options{Path: cfg.Store.Path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := New(Options{Config: cfg, Store: st, Mailer: mailer})
	if err != nil {
		t.Fatal(err)
	}
	s.limitClock = frozenLimitClock
	return s
}

// frozenLimitInstant is the time every test server's rate limiters read: a fixed instant, so no bucket
// refills while a test runs.
var frozenLimitInstant = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func frozenLimitClock() time.Time { return frozenLimitInstant }

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorDetail {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("error Content-Type = %q", ct)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body %q: %v", rec.Body.String(), err)
	}
	return body.Error
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	rec := do(t, s.Handler(), "GET", "/healthz")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("GET /healthz = %d %s", rec.Code, rec.Body)
	}
}

func TestReadyz(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	rec := do(t, s.Handler(), "GET", "/readyz")
	if rec.Code != 200 {
		t.Fatalf("GET /readyz = %d %s", rec.Code, rec.Body)
	}
	var got readiness
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Status != "ready" || got.Checks["database"] != "ok" || got.Checks["data_dir"] != "ok" {
		t.Errorf("readiness = %+v", got)
	}

	full := newTestServer(t, func(c *config.Config) { c.Storage.MinFree = math.MaxInt64 })
	rec = do(t, full.Handler(), "GET", "/readyz")
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 503 || got.Status != "not_ready" || got.Checks["disk_free"] != "failed" {
		t.Errorf("with min_free unreachable: %d %s", rec.Code, rec.Body)
	}
}

func TestDraining(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	s.draining.Store(true)
	rec := do(t, s.Handler(), "GET", "/api/v1/info")
	if rec.Code != 503 || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("while draining: %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if e := decodeError(t, rec); e.Code != apierr.Unavailable {
		t.Errorf("code = %s", e.Code)
	}
	if rec := do(t, s.Handler(), "GET", "/healthz"); rec.Code != 200 {
		t.Errorf("liveness while draining = %d", rec.Code)
	}
}

func TestInfo(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) {
		c.PublicURL = "https://drive.example.com"
		c.Upload.MaxChunkSize = 90 * config.MiB
	})
	rec := do(t, s.Handler(), "GET", "/api/v1/info")
	var got infoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("GET /api/v1/info = %d %s", rec.Code, rec.Body)
	}
	if got.PublicURL != "https://drive.example.com" || got.Limits.MaxChunkSize != 90<<20 ||
		got.Limits.MaxFileSize != 100<<30 {
		t.Errorf("info = %+v", got)
	}
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	rec := do(t, s.Handler(), "GET", "/api/v1/nothing-here")
	if rec.Code != 404 {
		t.Fatalf("status = %d", rec.Code)
	}
	e := decodeError(t, rec)
	if e.Code != apierr.NotFound {
		t.Errorf("code = %s", e.Code)
	}
	if e.RequestID == "" || e.RequestID != rec.Header().Get("X-Request-Id") {
		t.Errorf("request_id %q does not match header %q", e.RequestID, rec.Header().Get("X-Request-Id"))
	}
}

func TestSpecServed(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	rec := do(t, s.Handler(), "GET", "/api/v1/openapi.yaml")
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), apispec.Spec) {
		t.Fatalf("GET /api/v1/openapi.yaml = %d", rec.Code)
	}
}

func TestMetrics(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	do(t, s.Handler(), "GET", "/api/v1/info")
	rec := do(t, s.Handler(), "GET", "/metrics")
	want := `selfcloud_http_requests_total{method="GET",route="GET /api/v1/info",status="200"} 1`
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("metrics lack %s", want)
	}

	sep := newTestServer(t, func(c *config.Config) { c.MetricsListen = "127.0.0.1:0" })
	if rec := do(t, sep.Handler(), "GET", "/metrics"); rec.Code != 404 {
		t.Errorf("with metrics_listen set, the API listener still serves /metrics: %d", rec.Code)
	}
}

func TestHandlerErrors(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	for name, tc := range map[string]struct {
		handle  handlerFunc
		status  int
		code    apierr.Code
		message string
	}{
		"coded": {
			handle: func(w http.ResponseWriter, r *http.Request) error {
				return apierr.New(apierr.QuotaExceeded, "full").With("required_bytes", 5)
			},
			status: 507, code: apierr.QuotaExceeded, message: "full",
		},
		"plain error stays private": {
			handle: func(w http.ResponseWriter, r *http.Request) error {
				return errors.New("disk exploded at /secret/path")
			},
			status: 500, code: apierr.Internal, message: "internal error",
		},
		"panic": {
			handle: func(w http.ResponseWriter, r *http.Request) error { panic("boom") },
			status: 500, code: apierr.Internal, message: "internal error",
		},
	} {
		h := s.observe(s.wrap(route{method: "GET", path: "/x", handle: tc.handle}))
		rec := do(t, h, "GET", "/x")
		e := decodeError(t, rec)
		if rec.Code != tc.status || e.Code != tc.code || e.Message != tc.message {
			t.Errorf("%s: %d %+v", name, rec.Code, e)
		}
	}
}

// TestShutdownCancelsStragglers checks the contract long uploads rely on: at the shutdown
// deadline, the request context is cancelled and the handler still gets to finish.
func TestShutdownCancelsStragglers(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) { c.ShutdownTimeout = config.Duration(100 * time.Millisecond) })
	started := make(chan struct{})
	finished := make(chan struct{})
	s.handler = s.observe(s.drain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(finished)
	})))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, ln, nil) }()

	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	<-started
	cancel()

	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("the in-flight request's context was not cancelled at the deadline")
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(shutdownGrace + time.Second):
		t.Fatal("Serve did not return")
	}
}
