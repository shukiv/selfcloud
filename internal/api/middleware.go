// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

type ctxKey struct{}

// reqInfo follows a request through the middleware and its handler.
type reqInfo struct {
	id         string
	ip         netip.Addr
	route      string // route label: "GET /api/v1/info", or "unmatched"
	secretPath bool
	principal  *auth.Principal // set once the route has authenticated the caller
	session    *store.Session  // set when the caller signed in with a session cookie
	err        *apierr.Error
	sw         *statusWriter
}

func infoFrom(ctx context.Context) *reqInfo {
	v, _ := ctx.Value(ctxKey{}).(*reqInfo)
	return v
}

// sessionOf returns the session the request was made with, or nil.
func sessionOf(r *http.Request) *store.Session {
	if info := infoFrom(r.Context()); info != nil {
		return info.session
	}
	return nil
}

func (i *reqInfo) wroteHeader() bool { return i.sw != nil && i.sw.status != 0 }

// statusWriter records the status and body size of a response. It passes ReadFrom through, so
// a file copied to the response can still go out by sendfile(2).
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 && code >= 200 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	var n int64
	var err error
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err = rf.ReadFrom(r)
	} else {
		n, err = io.Copy(writerOnly{w.ResponseWriter}, r)
	}
	w.bytes += n
	return n, err
}

func (w *statusWriter) Flush() { _ = http.NewResponseController(w.ResponseWriter).Flush() }

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// writerOnly hides any ReadFrom method, so io.Copy does not loop back into it.
type writerOnly struct{ io.Writer }

// observe is the outermost middleware. It gives each request an ID and its client address,
// turns panics into internal errors, and records the access log line and metrics.
// logPath is the request path as the access log shows it. A signed link's token is a
// credential, so any path that mentions links in any spelling is replaced by the route label.
// The check fails closed on the raw path rather than parsing it: the mux answers a path with
// doubled slashes or dot segments by a redirect that matches no route, and a cleaned form such as
// that of /links/../TOKEN no longer names links while the raw path still holds the token. Routes
// marked secretPath are never logged either.
func logPath(info *reqInfo, p string) string {
	if info.secretPath || strings.Contains(strings.ToLower(p), "links") {
		return info.route
	}
	return p
}

func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		info := &reqInfo{
			id:    id.New("req"),
			ip:    clientIP(r, s.trusted, s.cfg.ClientIPHeader),
			route: "unmatched",
			sw:    sw,
		}
		sw.Header().Set("X-Request-Id", info.id)
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, info))

		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				s.log.Error("panic", "request_id", info.id, "panic", p, "stack", string(debug.Stack()))
				s.writeError(sw, r, apierr.New(apierr.Internal, "internal error"))
			}
			s.finish(r, sw, info, time.Since(start))
		}()
		next.ServeHTTP(sw, r)
	})
}

// quietRoutes are logged at debug level; probes and scrapes would drown everything else.
var quietRoutes = map[string]bool{
	"GET /healthz": true,
	"GET /readyz":  true,
	"GET /metrics": true,
}

func (s *Server) finish(r *http.Request, sw *statusWriter, info *reqInfo, d time.Duration) {
	status := sw.status
	if status == 0 {
		status = http.StatusOK
	}
	s.metrics.observe(r.Method, info.route, status, d, sw.bytes)

	level := slog.LevelInfo
	switch {
	case quietRoutes[info.route] && status < 400:
		level = slog.LevelDebug
	case quietRoutes[info.route]:
		level = slog.LevelWarn // a failing probe repeats every few seconds; the cause is logged once each time
	case status >= 500:
		level = slog.LevelError
	}
	if !s.log.Enabled(r.Context(), level) {
		return
	}
	attrs := []slog.Attr{
		slog.String("request_id", info.id),
		slog.String("method", r.Method),
		slog.String("route", info.route),
		slog.String("path", logPath(info, r.URL.Path)),
		slog.Int("status", status),
		slog.Int64("bytes", sw.bytes),
		slog.Float64("duration_ms", float64(d.Microseconds())/1000),
		slog.String("ip", info.ip.String()),
	}
	if info.err != nil {
		attrs = append(attrs, slog.String("code", string(info.err.Code)))
		if info.err.Err != nil {
			attrs = append(attrs, slog.String("cause", info.err.Err.Error()))
		}
	}
	s.log.LogAttrs(r.Context(), level, "request", attrs...)
}

// drain answers 503 to everything but the liveness probe once shutdown has begun.
func (s *Server) drain(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.draining.Load() && r.URL.Path != "/healthz" {
			w.Header().Set("Connection", "close")
			s.writeError(w, r, apierr.New(apierr.Unavailable, "the server is shutting down").Retry(5*time.Second))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestID returns the request's ID, for logs.
func requestID(r *http.Request) string {
	if info := infoFrom(r.Context()); info != nil {
		return info.id
	}
	return ""
}
