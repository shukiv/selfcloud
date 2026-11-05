// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/derived"
	"github.com/shukiv/selfcloud/internal/fsutil"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/office"
	"github.com/shukiv/selfcloud/internal/search"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/sweep"
	"github.com/shukiv/selfcloud/internal/web"
	"github.com/shukiv/selfcloud/internal/webhook"
	"github.com/shukiv/selfcloud/internal/wopi"
	webassets "github.com/shukiv/selfcloud/web"
)

// sweepInterval is how often housekeeping runs: blob removal, expired sessions and tokens.
const sweepInterval = 5 * time.Minute

// errServerShutdown is the cause the server gives when it cancels requests at its shutdown deadline. A request
// cancelled with it is a server-side failure, not a client that went away (respond.go clientGone).
var errServerShutdown = errors.New("server shutdown: requests cancelled at the deadline")

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
	cfg   *config.Config
	ui    *web.Handler // nil when ui.enabled is false
	logo  *web.Logo    // nil when no ui.brand.logo_file is set
	log   *slog.Logger
	store *store.Store
	blobs blob.Store
	// derivedPool makes thumbnails in child processes, and keeps their rows and files; see thumbnails.go.
	derivedPool *derived.Pool
	// conv is the Collabora conversion client and convertPool the pool that runs conversions; both are nil while
	// office.enabled is off. See exports.go (decision 37).
	conv        *office.Client
	convertPool *derived.ConvertPool
	// textWorker extracts the text of files for content search (slice q3, decision 46). It always runs; its converter is
	// the conversion client while office.enabled is on.
	textWorker *search.Worker
	trusted    []netip.Prefix
	metrics    *metrics
	jobs       *jobs.Runner
	mailer     mail.Sender // nil when mail is not configured
	templates  *mail.Templates
	limits     map[string]*limiter // rate limiters by "endpoint.dimension"
	// limitClock is the time the rate limiters read. Production keeps time.Now; tests freeze it.
	limitClock func() time.Time
	// sessionClock is the time the web sessions read. Production keeps time.Now; tests move it.
	sessionClock func() time.Time
	// dropFlight counts the bytes of drop pieces whose bodies are still arriving (round 2, item 1).
	dropFlight dropFlight
	pwFails    passwordFails // wrong current passwords, per session, for POST /me/password
	// origin is public_url's scheme and host. Browsers send it as Origin, and unsafe requests
	// made with a session must carry it.
	origin string
	// contentHost is the host of content_url, or "" when it is not set. Requests on that host
	// get signed links and nothing else; see contentGate.
	contentHost   string
	contentScheme string
	// links signs and verifies signed download links; see links.go.
	links *linkKeys
	// searchFailures counts the failed search batches in a row, for the retry wait; see runSearchIndex.
	searchFailures atomic.Int64
	// tokens signs and verifies delegated tokens; see tokens.go.
	tokens *tokenKeys
	// wopiTokens signs and verifies WOPI access tokens under a key of their own; office holds Collabora's discovery
	// document and proof keys, and is nil while office.enabled is off. See office.go.
	wopiTokens *tokenKeys
	office     *wopi.Discovery
	// webhookSeal encrypts webhook secrets at rest; see internal/webhook.
	webhookSeal *webhook.Sealer
	// webhookClient is the one client every webhook connection goes through (internal/webhook/client.go). It checks
	// each address it dials. Tests replace it with one that has a stub resolver.
	webhookClient *webhook.Client
	// webhookClock and webhookJitter drive the worker's schedule. Tests replace them with a fake clock.
	webhookClock  func() time.Time
	webhookJitter func() float64
	// shares signs and verifies the visitor credentials of links that anyone can open; see
	// share_credentials.go. guard counts the wrong passwords those links take; see share_open.go.
	shares *shareKeys
	guard  *shareGuard
	// corsAllowed holds the origins whose pages may call the API; see cors.go. Empty when CORS is off.
	corsAllowed map[string]bool
	// views records the views of the Recent list off the request path; see views.go.
	views *viewRecorder
	// starLimit is how many stars one user may hold; see handleStarNode. A test lowers it.
	starLimit int
	// sessionCookie is the cookie's name; the "__Host-" form is used when public_url is https.
	sessionCookie string
	// bg holds mail sent after a response has gone out, so Serve can wait for it.
	bg       sync.WaitGroup
	draining atomic.Bool
	handler  http.Handler
	// copySyncNodes is the largest copy made in the request; see syncCopyNodes.
	copySyncNodes int
	// copyYield is how long a chunked copy runs before it yields its turn to the queue (copyBatches). A test lowers it.
	copyYield time.Duration
	// zipYield is how long a zip job runs in one turn before it yields its turn to the queue (runZipJob). A test lowers it.
	zipYield time.Duration
	// zipFault, when set, is called at each stage of a zip job and fails it when it returns an error (tests only).
	zipFault func(stage string) error
	// zipWalkFault, when set, is asked about each node a zip's walk reaches; an error it returns is the node's access
	// error (tests only).
	zipWalkFault func(nodeID string) error
	// zipFiles holds the stored zips (decision 35); zips holds the ones being made, between the turns of their jobs.
	zipFiles *derived.ZipFiles
	zips     zipRegistry
	// copyFault, when set, is called at each stage of a chunked copy and fails it when it returns an error (tests
	// only). copyHold, when set, receives the writer hold of each write a chunked copy makes (measurement only).
	copyFault func(stage string) error
	copyHold  func(phase string, hold time.Duration)
	// activityCeiling, when set, replaces the most audit rows one activity read looks at (tests only).
	activityCeiling int
	// activityWalks, when set, replaces the most distinct people one activity read walks for reach (tests only).
	activityWalks int
	// copyBatchRows, when above zero, fixes the rows per copy batch instead of sizing them from the measured hold (tests).
	copyBatchRows int
	// emptyTrashLimit is the most roots one empty-trash call purges; see handleEmptyTrash. A test lowers it.
	emptyTrashLimit int
	// trashScanLimit is the most trash roots one listing or empty-trash call examines; see trashScanMax.
	trashScanLimit int
	// beforeWrite is a test hook, run before a mutating request takes the write lock. Nil in
	// production.
	beforeWrite func()

	// afterPieceWrite is a test hook, run once a piece's bytes are written to its file and before they are recorded.
	// Nil in production.
	afterPieceWrite func(uploadID string)
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
	// link admits the credential of a visitor to an access link (a link that anyone can open). drop admits
	// the credential of a drop link's visitor. A route without the flag that matches the credential refuses
	// it, so a route added later fails closed. Each kind reaches only its own routes.
	link bool
	drop bool
	// imp says whether the route may be called while the browser is signed in as a tenant user by an
	// installation admin (decision 41). The zero value is unclassified and is refused, so a route added
	// without it fails closed; TestEveryRouteIsClassified makes that a test failure too.
	imp impClass
	// mustChange admits a user whose password an admin typed (decision 41). Every other route refuses that
	// user with password_change_required.
	mustChange bool
}

// impClass classifies a route for a session that is an installation admin's sign-in-as (decision 41).
type impClass int

const (
	impUnclassified impClass = iota // the zero value: refused
	impAllow                        // the route acts for the person the browser is signed in as
	impRefuse                       // the route changes what outlives the session, or the person's own sign-in
)

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
	dpool, err := newDerivedPool(opts.Config, opts.Store, blobs, log)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	zdir := opts.Config.Derived.Dir
	if zdir == "" {
		zdir = filepath.Join(opts.Config.DataDir, "derived")
	}
	zipFiles, err := derived.NewZipFiles(zdir, opts.Store, opts.Config.Zip.TTL.D())
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	// Pending thumbnails are work a previous process never finished. Clearing them here, before any request is
	// served, means no queued job can be lost to the clearing.
	if _, err := dpool.Recover(context.Background()); err != nil {
		return nil, fmt.Errorf("api: derived files: %w", err)
	}
	pub, err := url.Parse(opts.Config.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("api: public_url: %w", err)
	}
	cfg := opts.Config
	if err := refuseFreshLinkKey(context.Background(), opts.Store, cfg.DataDir, cfg.Links); err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	links, err := loadLinkKeys(cfg.DataDir, cfg.Links)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	webhookSeal, err := webhook.NewSealer(links.current, links.previous)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	allowNetworks, err := config.ParsePrefixes(cfg.Webhooks.AllowNetworks)
	if err != nil {
		return nil, fmt.Errorf("api: webhooks.allow_networks: %w", err)
	}
	webhookClient := webhook.NewClient(webhook.ClientOptions{Resolver: net.DefaultResolver, Allow: allowNetworks,
		AllowHTTP: cfg.Webhooks.AllowHTTP})
	tokens, err := deriveTokenKeys(links)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	wopiTokens, err := deriveWOPIKeys(links)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	shares, err := deriveShareKeys(links)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	corsAllowed := map[string]bool{}
	for _, o := range cfg.CORS.AllowedOrigins {
		corsAllowed[strings.ToLower(o)] = true
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
		limitClock:    time.Now,
		sessionClock:  time.Now,
		cfg:           cfg,
		log:           log,
		store:         opts.Store,
		blobs:         blobs,
		derivedPool:   dpool,
		zipFiles:      zipFiles,
		trusted:       trusted,
		metrics:       newMetrics(),
		jobs:          jobs.New(opts.Store, log),
		mailer:        opts.Mailer,
		templates:     templates,
		origin:        pub.Scheme + "://" + pub.Host,
		contentHost:   contentHost,
		contentScheme: contentScheme,
		links:         links,
		tokens:        tokens,
		wopiTokens:    wopiTokens,
		webhookSeal:   webhookSeal,
		webhookClient: webhookClient,
		webhookClock:  time.Now,
		webhookJitter: webhook.Jitter,
		shares:        shares,
		guard:         newShareGuard(),
		corsAllowed:   corsAllowed,
		sessionCookie: "selfcloud_session",
		limits: map[string]*limiter{
			"login.ip":    newLimiter(cfg.RateLimit.PerIPPerMinute),
			"login.email": newLimiter(cfg.RateLimit.PerEmailPerMinute),
			// Failed sign-ins to one email from any address, an hour wide (sec1, decision 84). Fixed: it bounds
			// guessing across addresses, which the per-address limit above does not.
			"login.failed": newLimiterRate(30, time.Hour),
			// Failed sign-ins from one device cookie, an hour wide: the budget a trusted device has of its own
			// (sec1, decision 84). Keyed on the cookie's nonce.
			"login.device": newLimiterRate(10, time.Hour),
			// One-request writes (a new small file, a replaced small file, a WOPI save), per caller: a body can be large,
			// so each caller has a minute's worth of them (sec1, decision 84).
			oneRequestWriteRate: newLimiter(60),
			"forgot.ip":         newLimiter(cfg.RateLimit.PerIPPerMinute),
			"forgot.email":      newLimiter(cfg.RateLimit.PerEmailPerMinute),
			"verify.ip":         newLimiter(cfg.RateLimit.PerIPPerMinute),
			"reset.ip":          newLimiter(cfg.RateLimit.PerIPPerMinute),
			// Changing a password checks the current one, so a stolen session could guess it.
			// These limits are fixed: they guard a password, not a configurable flow.
			"changepw.user": newLimiter(5),
			"changepw.ip":   newLimiter(10),
			// Sign-in links: made by an account key, per account; redeemed per client address, as login is.
			"signlink.create": newLimiter(60),
			"signlink.ip":     newLimiter(cfg.RateLimit.PerIPPerMinute),
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
			// Asking for access, per user: each request mails the managers of the item, so a flood of them
			// is refused.
			"access.request": newLimiterRate(20, time.Hour),
			// Listing the pending requests, per caller: the scan reads up to 5,000 rows a call.
			"access.list": newLimiter(60),
			// Searches, per caller: each one reads up to the scan budget and checks access to every candidate.
			"search": newLimiter(cfg.Search.RatePerMinute),
			// Searches, per account: every user and token of an account has its own limit above, so a tenant with many users
			// would multiply it. The account's total is ten times one caller's (review S5).
			"search.account": newLimiter(cfg.Search.RatePerMinute * 10),
			// Directory searches are typed, so one caller searching while it types stays well inside this.
			"directory.search": newLimiter(60),
			// Access-request mail has buckets of its own, so requests cannot spend the mail a user needs to
			// sign in: per account, and per address the mail goes to.
			"mail.requests.account": newLimiterRate(20, time.Hour),
			"mail.requests.target":  newLimiterRate(5, time.Hour),
			// Each requesting user has its own allowance of request mail, so one user cannot use up the account's.
			"mail.requests.requester": newLimiterRate(3, time.Hour),
			// Share mail has buckets of its own too, so sharing cannot spend the mail a user needs to sign in.
			"mail.share.account": newLimiterRate(60, time.Hour),
			"mail.share.target":  newLimiterRate(10, time.Hour),
			// The mail a drop link sends its creator about arriving files has buckets of its own, as share mail does.
			"mail.drop.account": newLimiterRate(60, time.Hour),
			"mail.drop.target":  newLimiterRate(10, time.Hour),
			// Starting uploads, per caller: each one reserves disk, so a flood of them is refused.
			"upload.create": newLimiter(60),
			// Minting delegated tokens, per account key: each is a credential, so a flood is refused.
			"tokens.mint": newLimiter(60),
			// Opening an editor, per person: each one makes a credential and a session row.
			"office.open": newLimiter(60),
			// Comment writes, per user (a token counts as its user): 30 a minute (decision 39).
			"comment.write": newLimiter(30),
			// A place in a media file, per user. A client writes one at most every 15 seconds while a file plays, and one on
			// each pause that moved it: about 4 a minute of playing, so 60 a minute leaves room for seeks and file changes.
			"resume.write": newLimiter(60),
			// Saved playlists, per user (decision 58). A read walks the reach of every entry, up to 500, so reads are
			// charged like tags.batch; writes like the resume's.
			"playlist.read":  newLimiter(120),
			"playlist.write": newLimiter(60),
			// People suggestions for a mention, per user, as directory searches are (decision 39).
			"people.search": newLimiter(60),
			// The activity of a node: a read costs a scan and a reach walk per person, so it is limited per caller and per
			// account (decision 50).
			"activity":         newLimiter(120),
			"activity.account": newLimiter(1200),
			// Who viewed a file: the same reach walks as the activity, so the same two limits (decision 51).
			"viewers":         newLimiter(120),
			"viewers.account": newLimiter(1200),
			// Opening a share link is a secret lookup, so it is limited per client address.
			"share.open.ip":   newLimiter(30),
			"share.open.user": newLimiter(60),
			// Uploads through a drop link: 30 an hour per client address, and 300 an hour per link across its visitors.
			"drop.ip":   newLimiterRate(30, time.Hour),
			"drop.link": newLimiterRate(300, time.Hour),
			// Signed links: creation per principal; serving per client address, generous enough
			// for a player seeking through a video.
			"links.create": newLimiter(120),
			// Thumbnail requests that start new work, per caller. A read of a thumbnail already made is not charged.
			"thumb.enqueue": newLimiter(300),
			// Audio tags read that start new work, per caller (decision 56). The batch route names up to 100 files at once.
			"tags.enqueue": newLimiter(300),
			// tags.batch counts the batch requests of a caller, each of which walks up to 100 nodes for access (decision 56).
			"tags.batch": newLimiter(120),
			// Zips asked for, per caller: each one is a job that reads a whole subtree.
			"zip.create": newLimiter(60),
			// Conversions (previews and exports) that start new work, per caller. A read of one already made is not charged.
			"convert.enqueue": newLimiter(120),
			"links.serve":     newLimiter(600),
		},
	}
	s.views = newViewRecorder(opts.Store, log)
	s.starLimit = maxStarsPerUser
	if pub.Scheme == "https" {
		s.sessionCookie = "__Host-selfcloud_session"
	}
	s.jobs.Handle(jobAccountDelete, s.runAccountDelete)
	s.jobs.Handle(jobCopyNode, s.runCopyJob)
	s.jobs.Handle(jobZip, s.runZipJob)
	s.jobs.Handle(store.SearchJobKind, s.runSearchIndex)
	s.copySyncNodes = syncCopyNodes
	s.copyYield = copyYieldAfter
	s.zipYield = zipYieldAfter
	s.emptyTrashLimit = emptyTrashMax
	s.trashScanLimit = trashScanMax
	s.freeBytes = fsutil.FreeBytes
	s.uploadSlots = map[string]*uploadSlot{}
	s.uploadSlotWait = 5 * time.Second
	s.metrics.reg.MustRegister(newTotalsCollector(opts.Store, log), newWebhookCollector(opts.Store, log))

	if cfg.UI.Brand.LogoFile != "" {
		if s.logo, err = web.LoadLogo(cfg.UI.Brand.LogoFile); err != nil {
			return nil, fmt.Errorf("api: ui.brand.logo_file: %w", err)
		}
	}
	if cfg.Office.Enabled {
		s.office = wopi.NewDiscovery(cfg.Office.DiscoveryURL, nil, nil)
		d := cfg.Derived
		conv, err := office.New(cfg.Office.DiscoveryURL, nil, office.Options{Concurrent: d.ConvertWorkers,
			Timeout: d.Timeout.D(), MaxOutput: int64(d.MaxConvertBytes)})
		if err != nil {
			return nil, fmt.Errorf("api: office: %w", err)
		}
		s.conv = conv
		s.convertPool = derived.NewConvertPool(derived.ConvertOptions{Store: opts.Store, Blobs: blobs,
			Cache: s.derivedPool.Cache(), Conv: convertAdapter{s: s}, Workers: d.ConvertWorkers, Queue: d.ConvertQueue, PerAccount: d.ConvertAccountQueue,
			Timeout: d.Timeout.D(), MaxSourceBytes: int64(d.MaxSourceBytes), MaxOutputBytes: int64(d.MaxConvertBytes),
			MinFree: uint64(cfg.Storage.MinFree), Log: log})
	}
	// The content worker's converter is nil unless office is on and search.office_text is set (review S4): a document then
	// gets a skipped row, and no request is made. Plain text still works.
	var textConv search.Converter
	if cfg.Office.Enabled && cfg.Search.OfficeText {
		textConv = s.conv
	}
	s.textWorker = search.NewWorker(search.WorkerOptions{Store: opts.Store, Blobs: blobs, Conv: textConv,
		OfficeText: cfg.Search.OfficeText,
		TextMax:    int64(cfg.Search.TextMaxBytes), FullTextMax: int64(cfg.Search.FullTextMaxBytes),
		CapAccount: int64(cfg.Search.MaxTextPerAccount), CapTotal: int64(cfg.Search.MaxTextTotal), Log: log})
	if cfg.UI.Enabled {
		var officeURL string
		if cfg.Office.Enabled {
			officeURL = cfg.Office.DiscoveryURL
		}
		if s.ui, err = web.New(webassets.Assets(), web.Options{ContentURL: cfg.ContentURL, OfficeURL: officeURL,
			BrandName: cfg.UI.Brand.Name, BrandAccent: cfg.UI.Brand.Accent}); err != nil {
			return nil, fmt.Errorf("api: %w", err)
		}
	}

	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		mux.Handle(rt.method+" "+rt.path, s.wrap(rt))
	}
	// Unmatched requests go to the web interface when it serves them; anything else, the JSON 404.
	// Its answer is the web interface's files or the JSON 404. It reads no credential and changes nothing, so an
	// impersonated browser loads the app (decision 41).
	mux.Handle("/", s.wrap(route{path: "unmatched", handle: s.handleUnmatched, imp: impAllow}))
	s.handler = unsafeAssetGuard(s.observe(s.drain(s.cors(s.contentGate(mux)))))
	for _, w := range cfg.UI.Brand.ContrastWarnings() {
		log.Warn("brand colours fall short of the contrast the text needs; the config is kept", "detail", w)
	}
	return s, nil
}

// handleUnmatched answers a request no API route matched: the web interface's file when it is
// for the web interface, else the JSON 404.
func (s *Server) handleUnmatched(w http.ResponseWriter, r *http.Request) error {
	if s.ui != nil && s.ui.Serves(r) {
		s.ui.ServeHTTP(w, r)
		return nil
	}
	return s.handleNotFound(w, r)
}

func (s *Server) routes() []route {
	rs := []route{
		{method: http.MethodGet, path: "/healthz", handle: s.handleHealthz, imp: impAllow},
		{method: http.MethodGet, path: "/readyz", handle: s.handleReadyz, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/info", handle: s.handleInfo, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/openapi.yaml", handle: s.handleSpec, imp: impAllow},

		{method: http.MethodPost, path: "/api/v1/accounts", handle: s.handleCreateAccount, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts", handle: s.handleListAccounts, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}", handle: s.handleGetAccount, auth: true, imp: impAllow},
		{method: http.MethodPatch, path: "/api/v1/accounts/{acc}", handle: s.handleUpdateAccount, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}", handle: s.handleDeleteAccount, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/usage", handle: s.handleUsage, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/keys", handle: s.handleCreateKey, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/keys", handle: s.handleListKeys, auth: true, imp: impAllow},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/keys/{key}", handle: s.handleRevokeKey, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/tokens", handle: s.handleMintToken, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/tokens/revoke", handle: s.handleRevokeTokens, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/audit", handle: s.handleAuditLog, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/webhooks", handle: s.handleCreateWebhook, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/webhooks", handle: s.handleListWebhooks, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/webhooks/{hook}", handle: s.handleGetWebhook, auth: true, imp: impAllow},
		{method: http.MethodPatch, path: "/api/v1/accounts/{acc}/webhooks/{hook}", handle: s.handleUpdateWebhook, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/webhooks/{hook}", handle: s.handleDeleteWebhook, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/webhooks/{hook}/secret", handle: s.handleRotateWebhookSecret, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/webhooks/{hook}/test", handle: s.handleTestWebhook, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/webhooks/{hook}/deliveries", handle: s.handleListWebhookDeliveries, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/webhooks/{hook}/deliveries/{dlv}/retry", handle: s.handleRetryWebhookDelivery, auth: true, imp: impRefuse},
		{method: http.MethodPut, path: "/api/v1/accounts/{acc}/quota-warning", handle: s.handlePutQuotaWarning, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/quota-warning", handle: s.handleGetQuotaWarning, auth: true, imp: impAllow},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/quota-warning", handle: s.handleDeleteQuotaWarning, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/jobs/{job}", handle: s.handleGetJob, auth: true, imp: impAllow},

		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/folders", handle: s.handleCreateFolder, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/folders", handle: s.handleListFolders, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/folders/{fld}", handle: s.handleGetFolder, auth: true, imp: impAllow},
		{method: http.MethodPatch, path: "/api/v1/accounts/{acc}/folders/{fld}", handle: s.handleUpdateFolder, auth: true, imp: impRefuse},

		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/groups", handle: s.handleCreateGroup, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/groups", handle: s.handleListGroups, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/groups/{grp}", handle: s.handleGetGroup, auth: true, imp: impAllow},
		{method: http.MethodPatch, path: "/api/v1/accounts/{acc}/groups/{grp}", handle: s.handleUpdateGroup, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/groups/{grp}", handle: s.handleDeleteGroup, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/groups/{grp}/members", handle: s.handleListGroupMembers, auth: true, imp: impAllow},
		{method: http.MethodPut, path: "/api/v1/accounts/{acc}/groups/{grp}/members/{usr}", handle: s.handleAddGroupMember, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/groups/{grp}/members/{usr}", handle: s.handleRemoveGroupMember, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/folders/{fld}", handle: s.handleDeleteFolder, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/folders/{fld}/grants", handle: s.handleListFolderGrants, auth: true, imp: impAllow},
		{method: http.MethodPut, path: "/api/v1/accounts/{acc}/folders/{fld}/grants/{usr}", handle: s.handleSetGrant, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/folders/{fld}/grants/{usr}", handle: s.handleRemoveGrant, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/users/{usr}/grants", handle: s.handleListUserGrants, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/folders", handle: s.handleMyFolders, auth: true, imp: impAllow},
		{method: http.MethodPatch, path: "/api/v1/folders/{fld}/settings", handle: s.handleUpdateFolderSettings, auth: true, imp: impRefuse},
		{method: http.MethodPut, path: "/api/v1/folders/{fld}/hidden", handle: s.handleHideFolder, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/folders/{fld}/hidden", handle: s.handleShowFolder, auth: true, imp: impRefuse},

		{method: http.MethodPost, path: "/api/v1/admins", handle: s.handleCreateAdmin, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/admins", handle: s.handleListAdmins, auth: true, imp: impAllow},
		{method: http.MethodDelete, path: "/api/v1/admins/{usr}", handle: s.handleDeleteAdmin, auth: true, imp: impRefuse},

		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/users", handle: s.handleCreateUser, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/users", handle: s.handleListUsers, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/users/{usr}", handle: s.handleGetUser, auth: true, imp: impAllow},
		{method: http.MethodPatch, path: "/api/v1/accounts/{acc}/users/{usr}", handle: s.handleUpdateUser, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/users/{usr}", handle: s.handleDeleteUser, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/users/{usr}/verification", handle: s.handleSendVerification, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/users/{usr}/password-reset", handle: s.handlePasswordReset, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/users/{usr}/sign-in-links", handle: s.handleCreateSignInLink, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/users/{usr}/tokens/revoke", handle: s.handleRevokeUserTokens, auth: true, imp: impRefuse},
		// An installation admin's typed password and sign-in-as (decision 41). Both need the admin's own session, not a key,
		// and the admin's password again; both are off unless admin.tenant_access is on.
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/users/{usr}/password", handle: s.handleSetUserPassword, session: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/users/{usr}/sign-in", handle: s.handleSignInAs, session: true, imp: impRefuse},

		{method: http.MethodPost, path: "/api/v1/auth/verify", handle: s.handleVerify, sameSite: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/auth/login", handle: s.handleLogin, sameSite: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/auth/logout", handle: s.handleLogout, session: true, imp: impRefuse, mustChange: true},
		{method: http.MethodPost, path: "/api/v1/auth/password/forgot", handle: s.handleForgotPassword, sameSite: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/auth/password/reset", handle: s.handleResetPassword, sameSite: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/auth/sign-in-link", handle: s.handleRedeemSignInLink, sameSite: true, imp: impRefuse},
		// Go back ends an impersonation and restores the admin's session. It checks its own CSRF token, because the
		// impersonation may be past its hour, and a route that required a live session could not run then.
		{method: http.MethodPost, path: "/api/v1/auth/impersonation/end", handle: s.handleEndImpersonation, sameSite: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/me", handle: s.handleMe, session: true, imp: impAllow, mustChange: true},
		{method: http.MethodPatch, path: "/api/v1/me", handle: s.handleUpdateMe, session: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/me/password", handle: s.handleChangePassword, session: true, imp: impRefuse, mustChange: true},

		{method: http.MethodGet, path: "/api/v1/folders/{fld}/children", handle: s.handleListPathChildren, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/folders/{fld}/directories", handle: s.handleMkdir, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/folders/{fld}/files", handle: s.handleCreateFile, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}", handle: s.handleStatNode, auth: true, link: true, imp: impAllow},
		{method: http.MethodPatch, path: "/api/v1/nodes/{nod}", handle: s.handleRenameNode, auth: true, imp: impAllow},
		{method: http.MethodDelete, path: "/api/v1/nodes/{nod}", handle: s.handleDeleteNode, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/children", handle: s.handleListNodeChildren, auth: true, link: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/move", handle: s.handleMoveNode, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/copy", handle: s.handleCopyNode, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/shortcuts", handle: s.handleCreateShortcut, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/content", handle: s.handleGetContent, auth: true, link: true, imp: impAllow},
		{method: http.MethodHead, path: "/api/v1/nodes/{nod}/content", handle: s.handleGetContent, auth: true, link: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/thumbnail", handle: s.handleGetThumbnail, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/preview", handle: s.handleGetPreview, auth: true, imp: impAllow},
		{method: http.MethodHead, path: "/api/v1/nodes/{nod}/preview", handle: s.handleGetPreview, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/exports", handle: s.handleCreateExport, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/exports/{drv}", handle: s.handleGetExport, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/zips", handle: s.handleCreateZip, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/zips/{zip}", handle: s.handleGetZip, auth: true, imp: impAllow},
		{method: http.MethodDelete, path: "/api/v1/zips/{zip}", handle: s.handleDeleteZip, auth: true, imp: impAllow},
		{method: http.MethodHead, path: "/api/v1/nodes/{nod}/thumbnail", handle: s.handleGetThumbnail, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/tags", handle: s.handleGetTags, auth: true, imp: impAllow},
		{method: http.MethodHead, path: "/api/v1/nodes/{nod}/tags", handle: s.handleGetTags, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/media/tags", handle: s.handleTagsBatch, auth: true, imp: impAllow},
		{method: http.MethodPut, path: "/api/v1/nodes/{nod}/content", handle: s.handlePutContent, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/links", handle: s.handleCreateLink, auth: true, link: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/permissions", handle: s.handleListNodePermissions, auth: true, imp: impAllow},
		{method: http.MethodPut, path: "/api/v1/nodes/{nod}/permissions/{subject}", handle: s.handleSetNodePermission, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/nodes/{nod}/permissions/{subject}", handle: s.handleRemoveNodePermission, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/shared", handle: s.handleSharedWithMe, auth: true, imp: impAllow},
		{method: http.MethodPut, path: "/api/v1/nodes/{nod}/star", handle: s.handleStarNode, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/nodes/{nod}/star", handle: s.handleUnstarNode, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/starred", handle: s.handleListStarred, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/resume", handle: s.handleGetMediaResume, auth: true, imp: impRefuse},
		{method: http.MethodPut, path: "/api/v1/nodes/{nod}/resume", handle: s.handlePutMediaResume, auth: true, imp: impRefuse},
		// Saved playlists (decision 58): the signed-in user's own, with no token and no impersonation.
		{method: http.MethodGet, path: "/api/v1/playlists", handle: s.handleListPlaylists, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/playlists", handle: s.handleCreatePlaylist, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/playlists/{pls}", handle: s.handleGetPlaylist, auth: true, imp: impRefuse},
		{method: http.MethodPatch, path: "/api/v1/playlists/{pls}", handle: s.handleRenamePlaylist, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/playlists/{pls}", handle: s.handleDeletePlaylist, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/playlists/{pls}/entries", handle: s.handleAddPlaylistEntry, auth: true, imp: impRefuse},
		{method: http.MethodPut, path: "/api/v1/playlists/{pls}/entries", handle: s.handleReplacePlaylistEntries, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/playlists/{pls}/entries/{ple}", handle: s.handleRemovePlaylistEntry, auth: true, imp: impRefuse},
		{method: http.MethodPut, path: "/api/v1/playlists/{pls}/order", handle: s.handleReorderPlaylistEntries, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/subtitles", handle: s.handleListSubtitles, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/comments", handle: s.handleListComments, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/comments", handle: s.handleCreateComment, auth: true, imp: impRefuse},
		{method: http.MethodPatch, path: "/api/v1/comments/{cmt}", handle: s.handleEditComment, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/comments/{cmt}", handle: s.handleDeleteComment, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/comments/{cmt}/resolve", handle: s.handleResolveComment, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/people", handle: s.handleListPeople, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/activity", handle: s.handleNodeActivity, auth: true, imp: impAllow},
		// Who viewed a file (slice r5, decision 51): managers and the account key only, while the account's switch is on.
		// A token never reads it, and a sign-in-as session does not either.
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/viewers", handle: s.handleNodeViewers, auth: true, imp: impRefuse},
		// Notifications (slice r, decision 43): the signed-in user's own inbox and email switch. A token never reads them.
		{method: http.MethodGet, path: "/api/v1/notifications", handle: s.handleListNotifications, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/notifications/unread", handle: s.handleUnreadNotifications, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/notifications/{ntf}/read", handle: s.handleReadNotification, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/notifications/settings", handle: s.handleGetNotifySettings, auth: true, imp: impRefuse},
		{method: http.MethodPatch, path: "/api/v1/notifications/settings", handle: s.handlePatchNotifySettings, auth: true, imp: impRefuse},
		// The unsubscribe link of a mail: no session and no cookie, and the token in the query is the only authority. GET
		// only shows the page; the POST, from the page or from a mail client (RFC 8058), switches the email off.
		{method: http.MethodGet, path: notifyUnsubPath, handle: s.handleUnsubscribeForm, secretPath: true, imp: impAllow},
		{method: http.MethodPost, path: notifyUnsubPath, handle: s.handleUnsubscribe, secretPath: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/recent", handle: s.handleListRecent, auth: true, imp: impAllow},
		{method: http.MethodDelete, path: "/api/v1/recent", handle: s.handleClearRecent, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/recent/{nod}", handle: s.handleRemoveRecent, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/share-links", handle: s.handleCreateShareLink, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/share-links", handle: s.handleListShareLinks, auth: true, imp: impAllow},
		{method: http.MethodPatch, path: "/api/v1/share-links/{shl}", handle: s.handleUpdateShareLink, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/share-links/{shl}/reset", handle: s.handleResetShareLink, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/share-links/{shl}", handle: s.handleDeleteShareLink, auth: true, imp: impRefuse},
		// Opening a link checks its own credential: the handler answers a dead link the same way for every caller.
		{method: http.MethodPost, path: "/api/v1/share/open", handle: s.handleOpenShareLink, imp: impAllow},
		// Renewing a visitor credential: the credential itself is the caller.
		{method: http.MethodPost, path: "/api/v1/share/renew", handle: s.handleRenewShareCredential, auth: true, link: true, drop: true, imp: impAllow},
		// What a drop link's visitor may upload, and what the drop page shows: the credential is the caller.
		{method: http.MethodGet, path: "/api/v1/share/info", handle: s.handleShareInfo, auth: true, drop: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/storage-policy", handle: s.handleGetStoragePolicy, auth: true, imp: impAllow},
		{method: http.MethodPatch, path: "/api/v1/accounts/{acc}/storage-policy", handle: s.handleUpdateStoragePolicy, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/storage", handle: s.handleAccountStorage, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/storage/largest", handle: s.handleAccountLargest, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/me/storage/largest", handle: s.handleMyLargest, session: true, imp: impAllow},
		// Search by name and label values. A share-link visitor is refused (no link flag); a token may search within its scope.
		{method: http.MethodGet, path: "/api/v1/search", handle: s.handleSearch, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/label-fields", handle: s.handleListLabelFields, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/label-fields", handle: s.handleCreateLabelField, auth: true, imp: impRefuse},
		{method: http.MethodPatch, path: "/api/v1/accounts/{acc}/label-fields/{lbf}", handle: s.handleUpdateLabelField, auth: true, imp: impRefuse},
		{method: http.MethodDelete, path: "/api/v1/accounts/{acc}/label-fields/{lbf}", handle: s.handleDeleteLabelField, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/labels", handle: s.handleListNodeLabels, auth: true, imp: impAllow},
		{method: http.MethodPut, path: "/api/v1/nodes/{nod}/labels/{lbf}", handle: s.handleSetNodeLabel, auth: true, imp: impAllow},
		{method: http.MethodDelete, path: "/api/v1/nodes/{nod}/labels/{lbf}", handle: s.handleClearNodeLabel, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/sharing-policy", handle: s.handleGetSharingPolicy, auth: true, imp: impAllow},
		{method: http.MethodPatch, path: "/api/v1/accounts/{acc}/sharing-policy", handle: s.handleUpdateSharingPolicy, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/accounts/{acc}/share-links", handle: s.handleListAccountShareLinks, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/accounts/{acc}/share-links/delete-public", handle: s.handleDeletePublicShareLinks, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/directory", handle: s.handleDirectory, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/transfer-ownership", handle: s.handleTransferOwnership, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/access-requests", handle: s.handleCreateAccessRequest, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/access-requests", handle: s.handleListNodeAccessRequests, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/access-requests", handle: s.handleListPendingAccessRequests, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/access-requests/{arq}/approve", handle: s.handleApproveAccessRequest, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/access-requests/{arq}/deny", handle: s.handleDenyAccessRequest, auth: true, imp: impRefuse},

		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/restore", handle: s.handleRestoreNode, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/trash", handle: s.handleListTrash, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/trash/empty", handle: s.handleEmptyTrash, auth: true, imp: impAllow},
		{method: http.MethodDelete, path: "/api/v1/trash/{nod}", handle: s.handlePurgeTrashed, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/versions", handle: s.handleListVersions, auth: true, imp: impAllow},
		{method: http.MethodGet, path: "/api/v1/nodes/{nod}/versions/{ver}/content", handle: s.handleGetVersionContent, auth: true, imp: impAllow},
		{method: http.MethodHead, path: "/api/v1/nodes/{nod}/versions/{ver}/content", handle: s.handleGetVersionContent, auth: true, imp: impAllow},
		{method: http.MethodPatch, path: "/api/v1/nodes/{nod}/versions/{ver}", handle: s.handleUpdateVersion, auth: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/versions/{ver}/restore", handle: s.handleRestoreVersion, auth: true, imp: impAllow},
		{method: http.MethodDelete, path: "/api/v1/nodes/{nod}/versions/{ver}", handle: s.handleDeleteVersion, auth: true, imp: impAllow},

		// A signed link carries its own authority: no credential, and no cookie is read.
		{method: http.MethodGet, path: "/api/v1/links/{token}", handle: s.handleGetLink, secretPath: true, imp: impAllow},
		{method: http.MethodHead, path: "/api/v1/links/{token}", handle: s.handleGetLink, secretPath: true, imp: impAllow},

		// Office editing (decision 31). The editor route is for a person; the /wopi routes are called by Collabora
		// and carry their token in the query, so they are secretPath. All of them answer not_found while office.enabled
		// is off, and the /wopi routes check their proof and token before anything else.
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/office", handle: s.handleOpenOffice, auth: true, imp: impRefuse},
		{method: http.MethodPost, path: "/api/v1/nodes/{nod}/unlock", handle: s.handleBreakNodeLock, auth: true, imp: impRefuse},
		{method: http.MethodGet, path: "/wopi/files/{nod}", handle: s.handleWOPICheckFileInfo, secretPath: true, imp: impAllow},
		{method: http.MethodGet, path: "/wopi/files/{nod}/contents", handle: s.handleWOPIGetFile, secretPath: true, imp: impAllow},
		{method: http.MethodPost, path: "/wopi/files/{nod}/contents", handle: s.handleWOPIPutFile, secretPath: true, imp: impAllow},
		{method: http.MethodPost, path: "/wopi/files/{nod}", handle: s.handleWOPIOverride, secretPath: true, imp: impAllow},

		{method: http.MethodOptions, path: "/api/v1/uploads", handle: s.handleUploadOptions, imp: impAllow},
		// Uploads admit a drop link's visitor. The access link's visitor is refused on each of them. A drop link
		// has no commit: its uploads take a free name, so none waits for one.
		{method: http.MethodPost, path: "/api/v1/uploads", handle: s.handleCreateUpload, auth: true, drop: true, imp: impAllow},
		{method: http.MethodHead, path: "/api/v1/uploads/{upl}", handle: s.handleHeadUpload, auth: true, drop: true, imp: impAllow},
		{method: http.MethodPatch, path: "/api/v1/uploads/{upl}", handle: s.handlePatchUpload, auth: true, drop: true, imp: impAllow},
		{method: http.MethodDelete, path: "/api/v1/uploads/{upl}", handle: s.handleDeleteUpload, auth: true, drop: true, imp: impAllow},
		{method: http.MethodPost, path: "/api/v1/uploads/{upl}/commit", handle: s.handleCommitUpload, auth: true, imp: impAllow},
	}
	rs = append(rs, route{method: http.MethodGet, path: logoPath, handle: s.handleLogo, imp: impAllow})
	if s.cfg.MetricsListen == "" {
		rs = append(rs, route{method: http.MethodGet, path: "/metrics", handle: s.handleMetrics, imp: impAllow})
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
				if rt.drop && apierr.From(err).Code == apierr.NotFound {
					s.endDroppedUploadOnDeadLink(r)
				}
				s.writeError(w, r, err)
				return
			}
			// A visitor's credential reaches only the routes that admit its kind. An access link's visitor needs
			// a route with link, a drop link's visitor one with drop. Any other route refuses it, so a route added
			// later is closed to visitors unless it says otherwise.
			if p != nil && p.Kind == auth.KindShareLink && !admitsVisitor(rt, p.Link) {
				s.writeError(w, r, apierr.New(apierr.Forbidden,
					"a link that anyone can open does not reach this").With("reason", "link_route"))
				return
			}
			if info != nil {
				info.principal = p
				info.session = sess
			}
			if err := s.checkImpersonation(rt, p); err != nil {
				s.writeError(w, r, err)
				return
			}
		} else if rt.imp != impAllow && s.signedInAsOther(r) {
			// A route that sets a new session would replace the impersonation without ending it.
			s.writeError(w, r, impersonationRefused())
			return
		}
		if err := rt.handle(w, r); err != nil {
			s.writeError(w, r, err)
		}
	})
}

// admitsVisitor reports whether route rt admits the visitor credential described by l: the drop route flag for a
// drop link's visitor, the link flag for any other visitor. A visitor without a scope is refused.
func admitsVisitor(rt route, l *auth.LinkScope) bool {
	switch {
	case l == nil:
		return false
	case l.UploadOnly:
		return rt.drop
	default:
		return rt.link
	}
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
	baseCtx, cancelBase := context.WithCancelCause(context.WithoutCancel(ctx))
	defer cancelBase(nil)
	// The view recorder outlives the HTTP shutdown, so views recorded while requests finish are written. Its last
	// batch is written before Serve returns, while the store is still open.
	viewsCtx, stopViews := context.WithCancel(baseCtx)
	viewsDone := make(chan struct{})
	go func() { defer close(viewsDone); s.views.run(viewsCtx) }()
	defer func() { stopViews(); <-viewsDone }()
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
		TextMax: int64(s.cfg.Search.TextMaxBytes), CapAccount: int64(s.cfg.Search.MaxTextPerAccount),
		CapTotal: int64(s.cfg.Search.MaxTextTotal),
		Interval: sweepInterval, UploadExpiry: s.cfg.Upload.Expiry.D(),
		UploadMaxLifetime: s.cfg.Upload.MaxLifetime.D(), TrashRetention: s.cfg.Trash.Retention.D(),
		VersionsMaxCount: s.cfg.Versions.MaxCount, VersionsMaxAge: s.cfg.Versions.MaxAge.D(),
		AuditRetention: time.Duration(s.cfg.Audit.RetentionDays) * 24 * time.Hour,
		Derived:        s.derivedPool.Cache(), Zips: s.zipFiles, Logger: s.log})
	go func() { defer close(sweepDone); sw.Run(jobsCtx) }()
	derivedDone := make(chan struct{})
	go func() { defer close(derivedDone); s.derivedPool.Run(jobsCtx) }()
	convertDone := make(chan struct{})
	go func() {
		defer close(convertDone)
		if s.convertPool != nil {
			s.convertPool.Run(jobsCtx)
		}
	}()
	textDone := make(chan struct{})
	go func() {
		defer close(textDone)
		if s.cfg.Office.Enabled && s.cfg.Search.OfficeText {
			// Documents skipped while office or office_text was off are converted now (decision 46).
			if _, err := s.store.ReleaseTextSkipped(jobsCtx); err != nil && jobsCtx.Err() == nil {
				s.log.Error("content search: cannot release the skipped documents", "error", err)
			}
		}
		s.textWorker.Run(jobsCtx)
	}()
	noticeDone := make(chan struct{})
	go func() { defer close(noticeDone); s.runDropNotices(jobsCtx) }()
	webhooksDone := make(chan struct{})
	go func() { defer close(webhooksDone); s.runWebhooks(jobsCtx) }()
	notifyDone := make(chan struct{})
	go func() { defer close(notifyDone); s.runNotifications(jobsCtx) }()

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errc:
	}
	s.draining.Store(true)
	stopJobs() // an interrupted job is run again on the next start
	defer func() {
		<-jobsDone
		<-sweepDone
		<-noticeDone
		<-webhooksDone
		<-notifyDone
		<-derivedDone
		<-convertDone
		<-textDone
	}()
	s.log.Info("shutting down", "timeout", s.cfg.ShutdownTimeout.String())

	shCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout.D())
	defer cancel()
	defer s.bg.Wait()
	if err := hs.Shutdown(shCtx); errors.Is(err, context.DeadlineExceeded) {
		s.log.Warn("requests still running at the shutdown deadline; cancelling them")
		cancelBase(errServerShutdown)
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

// unsafeAssetGuard answers a request for a file under /assets/ by a path that is not clean with 404, before the mux can
// redirect it to the cleaned path (web.UnsafeAssetRequest).
func unsafeAssetGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if web.UnsafeAssetRequest(r) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
