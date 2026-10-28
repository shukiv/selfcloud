// Package api is Selfcloud's HTTP layer: routing, middleware and handlers.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync/atomic"
	"time"

	"selfcloud/internal/config"
	"selfcloud/internal/store"
)

// shutdownGrace is how long requests get to wrap up after their contexts are cancelled at the
// shutdown deadline, before their connections are closed.
const shutdownGrace = 5 * time.Second

// Options configure a Server.
type Options struct {
	Config *config.Config
	Logger *slog.Logger
	Store  *store.Store
}

// Server serves the HTTP API.
type Server struct {
	cfg      *config.Config
	log      *slog.Logger
	store    *store.Store
	trusted  []netip.Prefix
	metrics  *metrics
	draining atomic.Bool
	handler  http.Handler
}

// handlerFunc is a handler that returns its error instead of writing it; the route wrapper
// renders the error as JSON.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// route is one entry in the route table. The table both registers the handlers and lets a
// test check that every route is documented in api/openapi.yaml, and the reverse.
type route struct {
	method string
	path   string
	handle handlerFunc
	// secretPath marks a path that carries a credential, such as a signed link. The access log
	// records the route pattern instead of the path.
	secretPath bool
}

// New builds a Server.
func New(opts Options) (*Server, error) {
	if opts.Config == nil || opts.Store == nil {
		return nil, errors.New("api: Config and Store are required")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	trusted, err := config.ParsePrefixes(opts.Config.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("api: trusted_proxies: %w", err)
	}
	s := &Server{
		cfg:     opts.Config,
		log:     log,
		store:   opts.Store,
		trusted: trusted,
		metrics: newMetrics(),
	}
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		mux.Handle(rt.method+" "+rt.path, s.wrap(rt))
	}
	mux.Handle("/", s.wrap(route{path: "unmatched", handle: s.handleNotFound}))
	s.handler = s.observe(s.drain(mux))
	return s, nil
}

func (s *Server) routes() []route {
	rs := []route{
		{method: http.MethodGet, path: "/healthz", handle: s.handleHealthz},
		{method: http.MethodGet, path: "/readyz", handle: s.handleReadyz},
		{method: http.MethodGet, path: "/api/v1/info", handle: s.handleInfo},
		{method: http.MethodGet, path: "/api/v1/openapi.yaml", handle: s.handleSpec},
	}
	if s.cfg.MetricsListen == "" {
		rs = append(rs, route{method: http.MethodGet, path: "/metrics", handle: s.handleMetrics})
	}
	return rs
}

// wrap adapts a route's handler: it labels the request with the route for logs and metrics,
// and renders a returned error.
func (s *Server) wrap(rt route) http.Handler {
	label := rt.path
	if rt.method != "" {
		label = rt.method + " " + rt.path
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if info := infoFrom(r.Context()); info != nil {
			info.route = label
			info.secretPath = rt.secretPath
		}
		if err := rt.handle(w, r); err != nil {
			s.writeError(w, r, err)
		}
	})
}

// Handler returns the server's HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// Run listens on the configured addresses and serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	var mln net.Listener
	if s.cfg.MetricsListen != "" {
		if mln, err = net.Listen("tcp", s.cfg.MetricsListen); err != nil {
			ln.Close()
			return fmt.Errorf("api: metrics: %w", err)
		}
	}
	return s.Serve(ctx, ln, mln)
}

// Serve serves the API on ln, and metrics on mln when it is not nil, until ctx is cancelled.
//
// On shutdown, readiness fails and new requests get 503 at once, while requests in flight get
// shutdown_timeout to finish. If some are still running then, their contexts are cancelled so
// long transfers can save their progress, and after shutdownGrace their connections are
// closed.
func (s *Server) Serve(ctx context.Context, ln, mln net.Listener) error {
	baseCtx, cancelBase := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelBase()
	errLog := slog.NewLogLogger(s.log.Handler(), slog.LevelWarn)
	hs := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
		ErrorLog:          errLog,
	}
	errc := make(chan error, 2)
	go func() { errc <- hs.Serve(ln) }()
	s.log.Info("listening", "addr", ln.Addr().String(), "public_url", s.cfg.PublicURL,
		"web_interface", s.cfg.UI.Enabled)

	var ms *http.Server
	if mln != nil {
		ms = &http.Server{Handler: s.metrics.handler(), ReadHeaderTimeout: 10 * time.Second, ErrorLog: errLog}
		go func() { errc <- ms.Serve(mln) }()
		s.log.Info("metrics listening", "addr", mln.Addr().String())
	}

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errc:
	}
	s.draining.Store(true)
	s.log.Info("shutting down", "timeout", s.cfg.ShutdownTimeout.String())

	shCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout.D())
	defer cancel()
	if err := hs.Shutdown(shCtx); errors.Is(err, context.DeadlineExceeded) {
		s.log.Warn("requests still running at the shutdown deadline; cancelling them")
		cancelBase()
		grace, cancelGrace := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancelGrace()
		if err := hs.Shutdown(grace); err != nil {
			hs.Close()
		}
	}
	if ms != nil {
		ms.Close()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}
