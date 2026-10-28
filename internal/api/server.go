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
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"selfcloud/internal/apierr"
	"selfcloud/internal/auth"
	"selfcloud/internal/blob"
	"selfcloud/internal/config"
	"selfcloud/internal/fsutil"
	"selfcloud/internal/jobs"
	"selfcloud/internal/mail"
	"selfcloud/internal/store"
	"selfcloud/internal/sweep"
)

// sweepInterval is how often housekeeping runs: blob removal, expired sessions and tokens.
const sweepInterval = 5 * time.Minute

// shutdownGrace is how long requests get to wrap up after their contexts are cancelled at the
// shutdown deadline, before their connections are closed.
const shutdownGrace = 5 * time.Second

// Options configure a Server.
type Options struct {
	Config *config.Config
	Logger *slog.Logger
	Store  *store.Store
	// Mailer sends verification and reset mail. Nil means mail is not configured: the server
	// then refuses the "email" verification method instead of failing at send time.
	Mailer mail.Sender
}

// Server serves the HTTP API.
type Server struct {
	cfg       *config.Config
	log       *slog.Logger
	store     *store.Store
	blobs     blob.Store
	trusted   []netip.Prefix
	metrics   *metrics
	jobs      *jobs.Runner
	mailer    mail.Sender // nil when mail is not configured
	templates *mail.Templates
	limits    map[string]*limiter // rate limiters by "endpoint.dimension"
	pwFails   passwordFails       // wrong current passwords, per session, for POST /me/password
	// origin is public_url's scheme and host. Browsers send it as Origin, and unsafe requests
	// made with a session must carry it.
	origin string
	// contentHost is the host of content_url, or "" when it is not set. Requests on that host
	// get signed links and nothing else; see contentGate.
	contentHost   string
	contentScheme string
	// links signs and verifies signed download links; see links.go.
	links *linkKeys
	// sessionCookie is the cookie's name; the "__Host-" form is used when public_url is https.
	sessionCookie string
	// bg holds mail sent after a response has gone out, so Serve can wait for it.
	bg       sync.WaitGroup
	draining atomic.Bool
	handler  http.Handler
	// copySyncNodes is the largest copy made in the request; see syncCopyNodes.
	copySyncNodes int
	// beforeWrite is a test hook, run before a mutating request takes the write lock. Nil in
	// production.
	beforeWrite func()
	// uploadSlots holds the PATCH slot of each upload with a piece or a delete in progress; see
	// takeUploadSlot. uploadSlotWait is how long a request waits for a slot before it gives up.
	uploadSlotMu   sync.Mutex
	uploadSlots    map[string]*uploadSlot
	uploadSlotWait time.Duration
	// freeBytes reports the free space of the file system holding a path; a test replaces it.
	freeBytes func(dir string) (uint64, error)
	// beforeCommitUpload is a test hook, run inside a completing upload's transaction after the file
	// has moved, to simulate a crash before the commit. Nil in production.
	beforeCommitUpload func() error
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
	// auth requires a credential; the handler then finds the caller with principalOf. A
	// credential is a Bearer key, or a session cookie when there is no Authorization header.
	auth bool
	// session requires a session cookie and ignores any Authorization header.
	session bool
	// sameSite refuses requests whose Origin header names another site. It guards sign-in
	// endpoints that take no credential, so another site cannot sign a browser in to an
	// account of its choosing. Requests without an Origin header, from servers, pass.
	sameSite bool
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
	templates, err := mail.NewTemplates(opts.Config.Mail.TemplatesDir)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	blobs, err := blob.New(opts.Config.DataDir)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	pub, err := url.Parse(opts.Config.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("api: public_url: %w", err)
	}
	cfg := opts.Config
	links, err := loadLinkKeys(cfg.DataDir, cfg.Links)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	var contentHost, contentScheme string
	if cfg.ContentURL != "" {
		cu, err := url.Parse(cfg.ContentURL)
		if err != nil {
			return nil, fmt.Errorf("api: content_url: %w", err)
		}
		contentScheme = cu.Scheme
		contentHost = canonicalHost(cu.Host, cu.Scheme)
	}
	s := &Server{
		cfg:           cfg,
		log:           log,
		store:         opts.Store,
		blobs:         blobs,
		trusted:       trusted,
		metrics:       newMetrics(),
		jobs:          jobs.New(opts.Store, log),
		mailer:        opts.Mailer,
		templates:     templates,
		origin:        pub.Scheme + "://" + pub.Host,
		contentHost:   contentHost,
		contentScheme: contentScheme,
		links:         links,
		sessionCookie: "selfcloud_session",
		limits: map[string]*limiter{
			"login.ip":     newLimiter(cfg.RateLimit.PerIPPerMinute),
			"login.email":  newLimiter(cfg.RateLimit.PerEmailPerMinute),
			"forgot.ip":    newLimiter(cfg.RateLimit.PerIPPerMinute),
			"forgot.email": newLimiter(cfg.RateLimit.PerEmailPerMinute),
			"verify.ip":    newLimiter(cfg.RateLimit.PerIPPerMinute),
			"reset.ip":     newLimiter(cfg.RateLimit.PerIPPerMinute),
			// Changing a password checks the current one, so a stolen session could guess it.
			// These limits are fixed: they guard a password, not a configurable flow.
			"changepw.user": newLimiter(5),
			"changepw.ip":   newLimiter(10),
			// Verification and reset mail: per account, and per address it goes to. Links returned
			// to a key are not mail and are not counted.
			"mail.account": newLimiterRate(30, time.Hour),
			"mail.target":  newLimiterRate(3, time.Hour),
			// Forgot-password mail is requested by anyone, so it has buckets of its own. Invitations
			// and resends never draw on these, and these never draw on the buckets above.
			"mail.forgot.target":  newLimiterRate(3, time.Hour),
			"mail.forgot.ip":      newLimiterRate(20, time.Hour),
			"mail.forgot.account": newLimiterRate(30, time.Hour),
			// Each address gets one reset an hour that the account cap does not count, so one
			// caller who uses up an account's cap on repeats cannot stop its users' first resets.
			"mail.forgot.first": newLimiterRate(1, time.Hour),
			// Starting uploads, per caller: each one reserves disk, so a flood of them is refused.
			"upload.create": newLimiter(60),
			// Signed links: creation per principal; serving per client address, generous enough
			// for a player seeking through a video.
			"links.create": newLimiter(120),
			"links.serve":  newLimiter(600),
		},
	}
	if pub.Scheme == "https" {
		s.sessionCookie = "__Host-selfcloud_session"
	}
	s.jobs.Handle(jobAccountDelete, s.runAccountDelete)
	s.jobs.Handle(jobCopyNode, s.runCopyJob)
	s.copySyncNodes = syncCopyNodes
	s.freeBytes = fsutil.FreeBytes
	s.uploadSlots = map[string]*uploadSlot{}
	s.uploadSlotWait = 5 * time.Second
	s.metrics.reg.MustRegister(newTotalsCollector(opts.Store, log))

	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		mux.Handle(rt.method+" "+rt.path, s.wrap(rt))
	}
	mux.Handle("/", s.wrap(route{path: "unmatched", handle: s.handleNotFound}))
	s.handler = s.observe(s.drain(s.contentGate(mux)))
	return s, nil
}

func (s *Server) routes() []route {
	rs := []route{
		{method: http.MethodGet, path: "/healthz", handle: s.handleHealthz},
		{method: http.MethodGet, path: "/readyz", handle: s.handleReadyz},
		{method: http.MethodGet, path: "/api/v1/info", handle: s.handleInfo},
		{method: http.MethodGet, path: "/api/v1/openapi.yaml", handle: s.handleSpec},

		{method: http.MethodPost, path: "/api/v1/accounts", handle: s.handleCreateAccount, auth: true},
		{method: http.MethodGet, path: "/api/v1/accounts", handle: s.handleListAccounts, auth: true},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}", handle: s.handleGetAccount, auth: true},
		{method: http.MethodPatch, path: "/api/v1/accounts/{acc}", handle: s.handleUpdateAccount, auth: true},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}", handle: s.handleDeleteAccount, auth: true},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/usage", handle: s.handleUsage, auth: true},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/keys", handle: s.handleCreateKey, auth: true},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/keys", handle: s.handleListKeys, auth: true},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/keys/{key}", handle: s.handleRevokeKey, auth: true},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/audit", handle: s.handleAuditLog, auth: true},
		{method: http.MethodGet, path: "/api/v1/jobs/{job}", handle: s.handleGetJob, auth: true},

		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/folders", handle: s.handleCreateFolder, auth: true},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/folders", handle: s.handleListFolders, auth: true},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/folders/{fld}", handle: s.handleGetFolder, auth: true},
		{method: http.MethodPatch, path: "/api/v1/accounts/{acc}/folders/{fld}", handle: s.handleUpdateFolder, auth: true},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/folders/{fld}", handle: s.handleDeleteFolder, auth: true},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/folders/{fld}/grants", handle: s.handleListFolderGrants, auth: true},
		{method: http.MethodPut, path: "/api/v1/accounts/{acc}/folders/{fld}/grants/{usr}", handle: s.handleSetGrant, auth: true},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/folders/{fld}/grants/{usr}", handle: s.handleRemoveGrant, auth: true},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/users/{usr}/grants", handle: s.handleListUserGrants, auth: true},
		{method: http.MethodGet, path: "/api/v1/folders", handle: s.handleMyFolders, auth: true},

		{method: http.MethodPost, path: "/api/v1/admins", handle: s.handleCreateAdmin, auth: true},
		{method: http.MethodGet, path: "/api/v1/admins", handle: s.handleListAdmins, auth: true},
		{method: http.MethodDelete, path: "/api/v1/admins/{usr}", handle: s.handleDeleteAdmin, auth: true},

		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/users", handle: s.handleCreateUser, auth: true},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/users", handle: s.handleListUsers, auth: true},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/users/{usr}", handle: s.handleGetUser, auth: true},
		{method: http.MethodPatch, path: "/api/v1/accounts/{acc}/users/{usr}", handle: s.handleUpdateUser, auth: true},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/users/{usr}", handle: s.handleDeleteUser, auth: true},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/users/{usr}/verification", handle: s.handleSendVerification, auth: true},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/users/{usr}/password-reset", handle: s.handlePasswordReset, auth: true},

		{method: http.MethodPost, path: "/api/v1/auth/verify", handle: s.handleVerify, sameSite: true},
		{method: http.MethodPost, path: "/api/v1/auth/login", handle: s.handleLogin, sameSite: true},
		{method: http.MethodPost, path: "/api/v1/auth/logout", handle: s.handleLogout, session: true},
		{method: http.MethodPost, path: "/api/v1/auth/password/forgot", handle: s.handleForgotPassword, sameSite: true},
		{method: http.MethodPost, path: "/api/v1/auth/password/reset", handle: s.handleResetPassword, sameSite: true},
		{method: http.MethodGet, path: "/api/v1/me", handle: s.handleMe, session: true},
		{method: http.MethodPatch, path: "/api/v1/me", handle: s.handleUpdateMe, session: true},
		{method: http.MethodPost, path: "/api/v1/me/password", handle: s.handleChangePassword, session: true},

		{method: http.MethodGet, path: "/api/v1/folders/{fld}/children", handle: s.handleListPathChildren, auth: true},
		{method: http.MethodPost, path: "/api/v1/folders/{fld}/directories", handle: s.handleMkdir, auth: true},
		{method: http.MethodPost, path: "/api/v1/folders/{fld}/files", handle: s.handleCreateFile, auth: true},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}", handle: s.handleStatNode, auth: true},
		{method: http.MethodPatch, path: "/api/v1/nodes/{nod}", handle: s.handleRenameNode, auth: true},
		{method: http.MethodDelete, path: "/api/v1/nodes/{nod}", handle: s.handleDeleteNode, auth: true},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/children", handle: s.handleListNodeChildren, auth: true},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/move", handle: s.handleMoveNode, auth: true},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/copy", handle: s.handleCopyNode, auth: true},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/content", handle: s.handleGetContent, auth: true},
		{method: http.MethodHead, path: "/api/v1/nodes/{nod}/content", handle: s.handleGetContent, auth: true},
		{method: http.MethodPut, path: "/api/v1/nodes/{nod}/content", handle: s.handlePutContent, auth: true},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/links", handle: s.handleCreateLink, auth: true},

		// A signed link carries its own authority: no credential, and no cookie is read.
		{method: http.MethodGet, path: "/api/v1/links/{token}", handle: s.handleGetLink, secretPath: true},
		{method: http.MethodHead, path: "/api/v1/links/{token}", handle: s.handleGetLink, secretPath: true},

		{method: http.MethodOptions, path: "/api/v1/uploads", handle: s.handleUploadOptions},
		{method: http.MethodPost, path: "/api/v1/uploads", handle: s.handleCreateUpload, auth: true},
		{method: http.MethodHead, path: "/api/v1/uploads/{upl}", handle: s.handleHeadUpload, auth: true},
		{method: http.MethodPatch, path: "/api/v1/uploads/{upl}", handle: s.handlePatchUpload, auth: true},
		{method: http.MethodDelete, path: "/api/v1/uploads/{upl}", handle: s.handleDeleteUpload, auth: true},
		{method: http.MethodPost, path: "/api/v1/uploads/{upl}/commit", handle: s.handleCommitUpload, auth: true},
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
		info := infoFrom(r.Context())
		if info != nil {
			info.route = label
			info.secretPath = rt.secretPath
		}
		if rt.sameSite {
			if o := r.Header.Get("Origin"); o != "" && o != s.origin {
				s.writeError(w, r, apierr.New(apierr.Forbidden, "the request must come from this site"))
				return
			}
		}
		if rt.auth || rt.session {
			var (
				p    *auth.Principal
				sess *store.Session
				err  error
			)
			if rt.session {
				p, sess, err = s.authenticateSession(r)
			} else {
				p, sess, err = s.authenticate(r)
			}
			if err != nil {
				s.writeError(w, r, err)
				return
			}
			if info != nil {
				info.principal = p
				info.session = sess
			}
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

	jobsCtx, stopJobs := context.WithCancel(baseCtx)
	jobsDone := make(chan struct{})
	go func() { defer close(jobsDone); s.jobs.Run(jobsCtx) }()
	sweepDone := make(chan struct{})
	sw := sweep.New(sweep.Options{Store: s.store, Blobs: s.blobs, DeleteGrace: s.cfg.Blobs.DeleteGrace.D(),
		Interval: sweepInterval, UploadExpiry: s.cfg.Upload.Expiry.D(),
		UploadMaxLifetime: s.cfg.Upload.MaxLifetime.D(), Logger: s.log})
	go func() { defer close(sweepDone); sw.Run(jobsCtx) }()

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errc:
	}
	s.draining.Store(true)
	stopJobs() // an interrupted job is run again on the next start
	defer func() { <-jobsDone; <-sweepDone }()
	s.log.Info("shutting down", "timeout", s.cfg.ShutdownTimeout.String())

	shCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout.D())
	defer cancel()
	defer s.bg.Wait()
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
