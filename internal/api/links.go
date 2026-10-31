// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Signed download links (design sections 9 and 13).
//
// A link is one token, base64url(payload) "." base64url(MAC), served at /api/v1/links/{token}:
//
//	payload  "nod|acc|disposition|exp", exp in Unix seconds
//	MAC      HMAC-SHA256 over a label, the node's content tag and the payload
//
// The content tag is a random value that the store changes on every content write and never on a rename or
// move. It is not in the token. So a link stops working as soon as the contents change, and it stays dead
// when the same bytes come back (a re-upload or a version restore gets a new tag). A rename or move keeps
// the tag, so a link survives it. The link names one node, one account and one disposition. Two keys verify:
// the current signing key and, after a rotation, the previous one.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// linkPrefix is the path of a signed link. The content origin serves nothing else.
const linkPrefix = "/api/v1/links/"

const (
	// linkTokenMax bounds a token before anything is decoded.
	linkTokenMax = 512
	// linkPayloadMax bounds the decoded payload.
	linkPayloadMax = 256
	linkKeyLen     = 32
	linkMACLen     = sha256.Size
	linkMACLabel   = "selfcloud link v1"
	keyFileName    = "signing.key"
	prevKeyName    = "signing.prev.key"
	dispAttachment = "attachment"
	dispInline     = "inline"
)

// linkKeys holds the signing keys. current signs and verifies; previous only verifies.
type linkKeys struct {
	current  []byte
	previous []byte // nil when there is no previous key
}

// loadLinkKeys reads the signing keys, making the current one on first start unless the
// configuration names it. data_dir/keys is created with mode 0700.
func loadLinkKeys(dataDir string, cfg config.LinksConfig) (*linkKeys, error) {
	dir := filepath.Join(dataDir, "keys")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	// MkdirAll leaves an existing directory alone, so a looser mode is tightened here.
	if fi, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	} else if fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, fmt.Errorf("keys: %w", err)
		}
	}
	cur := cfg.SigningKeyFile
	if cur == "" {
		cur = filepath.Join(dir, keyFileName)
		if err := createKeyFile(cur, dir); err != nil {
			return nil, err
		}
	}
	current, err := readKeyFile(cur)
	if err != nil {
		return nil, err
	}
	k := &linkKeys{current: current}
	prev := cfg.PreviousKeyFile
	configured := prev != ""
	if !configured {
		prev = filepath.Join(dir, prevKeyName)
	}
	switch b, err := readKeyFile(prev); {
	case err == nil:
		k.previous = b
	case errors.Is(err, fs.ErrNotExist) && !configured:
	default:
		return nil, err
	}
	return k, nil
}

// refuseFreshLinkKey refuses a start that would make the default signing key while the database holds webhook endpoints.
// Their secrets were sealed under a key that is no longer there, so none of them opens. It runs before loadLinkKeys, so a
// refused start creates no key and changes nothing, and every start refuses until the operator acts: either the earlier
// key file goes back into the keys directory, or, when the key is lost for good, `selfcloud admin new-signing-key` makes a
// new one. An existing key, or a configured signing key file, is never refused here.
func refuseFreshLinkKey(ctx context.Context, st *store.Store, dataDir string, cfg config.LinksConfig) error {
	if cfg.SigningKeyFile != "" {
		return nil
	}
	dir := filepath.Join(dataDir, "keys")
	_, err := os.Stat(filepath.Join(dir, keyFileName))
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("keys: %w", err)
	}
	n, err := st.WebhookEndpointCount(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	return fmt.Errorf("the signing key %s is missing from %s, but the database holds %d webhook endpoint(s) sealed under "+
		"it; nothing was created or changed. Either put the earlier key file back into that directory and start again, "+
		"or, if the key is lost for good, run `selfcloud admin new-signing-key -i-lost-the-old-key`, which makes a new "+
		"key and invalidates the endpoints' secrets, signed links, delegated tokens and visitor credentials",
		keyFileName, dir, n)
}

// createKeyFile makes a new random key at path if nothing is there. The key is written to a
// temporary file and linked into place, so a crash leaves either no key or a whole one, and an
// existing key is never replaced.
func createKeyFile(path, dir string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	tmp, err := os.CreateTemp(dir, "tmp-key-*")
	if err != nil {
		return fmt.Errorf("keys: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // the temporary name is gone after the link
	var b [linkKeyLen]byte
	rand.Read(b[:]) // crypto/rand.Read never returns an error.
	if _, err := tmp.Write(b[:]); err != nil {
		tmp.Close()
		return fmt.Errorf("keys: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("keys: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("keys: %w", err)
	}
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("keys: %w", err)
	}
	return nil
}

// NewSigningKey makes the default signing key (or the configured one) for an operator whose key is lost for good. It
// never replaces a key: an existing key is an error. It returns the path it made. The key itself is never printed. The
// previous key file, if there is one, is left alone and still verifies what it signed.
func NewSigningKey(dataDir string, cfg config.LinksConfig) (string, error) {
	path := cfg.SigningKeyFile
	if path == "" {
		path = filepath.Join(dataDir, "keys", keyFileName)
	}
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return "", fmt.Errorf("a signing key already exists at %s; nothing was changed", path)
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("keys: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("keys: %w", err)
	}
	if err := createKeyFile(path, dir); err != nil {
		return "", err
	}
	return path, nil
}

// readKeyFile reads a signing key. It refuses a key that anyone but its owner can read, and one
// that is not 32 bytes.
func readKeyFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("keys: %s is readable by others; its mode must be 0600", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, linkKeyLen+1))
	if err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	if len(b) != linkKeyLen {
		return nil, fmt.Errorf("keys: %s must hold exactly %d bytes", path, linkKeyLen)
	}
	return b, nil
}

// linkClaims is what a link says: which node, of which account, in which disposition, until when.
type linkClaims struct {
	nod, acc, disp string
	exp            int64
}

// payload is the signed form of the claims.
func (c linkClaims) payload() []byte {
	return []byte(c.nod + "|" + c.acc + "|" + c.disp + "|" + strconv.FormatInt(c.exp, 10))
}

// linkMAC is the MAC of a payload for a file whose current content tag is tag.
func linkMAC(key []byte, tag string, payload []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(linkMACLabel))
	h.Write([]byte{0})
	h.Write([]byte(tag))
	h.Write([]byte{0})
	h.Write(payload)
	return h.Sum(nil)
}

// sign returns the token for the claims, signed with the current key. tag is the file's content tag
// now.
func (k *linkKeys) sign(c linkClaims, tag string) string {
	p := c.payload()
	return base64.RawURLEncoding.EncodeToString(p) + "." +
		base64.RawURLEncoding.EncodeToString(linkMAC(k.current, tag, p))
}

// verify reports whether mac is a valid MAC of payload for tag under either key. Both are
// computed and compared in constant time.
func (k *linkKeys) verify(payload, mac []byte, tag string) bool {
	ok := hmac.Equal(linkMAC(k.current, tag, payload), mac)
	if k.previous != nil {
		ok = hmac.Equal(linkMAC(k.previous, tag, payload), mac) || ok
	}
	return ok
}

// parseLinkToken splits and checks a token's shape. It returns the claims, the payload bytes and
// the MAC. Any malformed token is an error; the caller answers all of them alike.
func parseLinkToken(tok string) (linkClaims, []byte, []byte, error) {
	var c linkClaims
	if len(tok) == 0 || len(tok) > linkTokenMax || strings.Count(tok, ".") != 1 {
		return c, nil, nil, errLinkMalformed
	}
	p64, m64, _ := strings.Cut(tok, ".")
	payload, err := base64.RawURLEncoding.DecodeString(p64)
	if err != nil || len(payload) == 0 || len(payload) > linkPayloadMax {
		return c, nil, nil, errLinkMalformed
	}
	mac, err := base64.RawURLEncoding.DecodeString(m64)
	if err != nil || len(mac) != linkMACLen {
		return c, nil, nil, errLinkMalformed
	}
	parts := strings.Split(string(payload), "|")
	if len(parts) != 4 || !id.Valid("nod", parts[0]) || !id.Valid("acc", parts[1]) ||
		!validLinkDisposition(parts[2]) || !allDigits(parts[3]) {
		return c, nil, nil, errLinkMalformed
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return c, nil, nil, errLinkMalformed
	}
	c = linkClaims{nod: parts[0], acc: parts[1], disp: parts[2], exp: exp}
	return c, payload, mac, nil
}

// errLinkMalformed is a token that is malformed, forged or for content that changed. Callers answer it
// as not_found, so a probe learns nothing from the difference.
var errLinkMalformed = errors.New("not a valid link")

func validDisposition(d string) bool { return d == dispAttachment || d == dispInline }

// allDigits reports whether s is a non-empty run of ASCII digits, the only form an expiry takes.
func allDigits(s string) bool {
	if s == "" || len(s) > 19 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// linkNotFound is the answer to every link that is not valid.
func linkNotFound() error { return notFound("link") }

// linkRequest is the body of POST /nodes/{nod}/links. Both fields are optional.
type linkRequest struct {
	TTL         *int64 `json:"ttl"`         // seconds
	Disposition string `json:"disposition"` // attachment (default) or inline
}

// linkJSON is the answer to POST /nodes/{nod}/links.
type linkJSON struct {
	URL         string    `json:"url"`
	Disposition string    `json:"disposition"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// handleCreateLink makes a signed link to a file the caller can read. A ttl above links.max_ttl
// is refused, not shortened, so a caller never gets less than it asked for without knowing it.
func (s *Server) handleCreateLink(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if p == nil {
		return apierr.New(apierr.Unauthenticated, "credentials are required")
	}
	// A visitor's budget is its own: one visitor cannot use up the link's budget for everyone else on it.
	key := string(p.Kind) + ":" + p.ID
	if p.Kind == auth.KindShareLink {
		key += "|" + addressKey(r)
	}
	if err := s.rateLimit("links.create", key); err != nil {
		return err
	}
	var in linkRequest
	if r.ContentLength != 0 { // no body at all takes the defaults
		if err := decodeJSON(w, r, &in); err != nil {
			return err
		}
	}
	// A link is a capability that no revocation reaches, so one made with a delegated token may not
	// outlive the token: its longest life is what the token has left.
	ttl, maxTTL := s.cfg.Links.DefaultTTL.D(), s.cfg.Links.MaxTTL.D()
	if p.Token != nil {
		left := time.Until(p.Token.ExpiresAt).Truncate(time.Second)
		if left < time.Second {
			return apierr.New(apierr.TokenExpired, "the token has expired")
		}
		ttl, maxTTL = min(ttl, left), min(maxTTL, left)
	}
	// A visitor of a link that anyone can open gets the same cap: its signed links end with its credential.
	if p.Kind == auth.KindShareLink && p.Link != nil {
		left := time.Until(p.Link.ExpiresAt).Truncate(time.Second)
		if left < time.Second {
			return apierr.New(apierr.Unauthenticated, "the credential has expired; open the link again")
		}
		ttl, maxTTL = min(ttl, left), min(maxTTL, left)
	}
	if in.TTL != nil {
		if *in.TTL < 1 {
			return apierr.New(apierr.InvalidRequest, "ttl must be at least 1 second")
		}
		if *in.TTL > int64(maxTTL/time.Second) {
			return apierr.Newf(apierr.InvalidRequest, "ttl may be at most %d seconds", int64(maxTTL/time.Second)).
				With("max_ttl_seconds", int64(maxTTL/time.Second))
		}
		ttl = time.Duration(*in.TTL) * time.Second
	}
	disp := in.Disposition
	if disp == "" {
		disp = dispAttachment
	}
	if !validDisposition(disp) {
		return apierr.New(apierr.InvalidRequest, "disposition must be attachment or inline")
	}
	n, role, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	if n.Kind != store.KindFile {
		return apierr.New(apierr.InvalidRequest, "a folder cannot be linked; link a file inside it")
	}
	// A link that downloads is a download the no-download rule forbids; an inline link is allowed.
	restricted, err := s.downloadRestricted(r.Context(), n, role)
	if err != nil {
		return err
	}
	if restricted && !s.inlineAllowed(r, n, disp == dispInline) {
		return errDownloadRestricted()
	}
	s.noteView(p, n)
	expires := time.Now().Add(ttl).Truncate(time.Second)
	c := linkClaims{nod: n.ID, acc: n.AccountID, disp: disp, exp: expires.Unix()}
	base := s.cfg.PublicURL
	if s.cfg.ContentURL != "" {
		base = s.cfg.ContentURL
	}
	// A visitor's signed links are not logged one by one, so a busy link cannot flood the audit log.
	if p.Kind != auth.KindShareLink {
		ctx := r.Context()
		s.atWrite()
		err = s.store.Write(ctx, func(tx *store.Tx) error {
			return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "node.link", n.ID,
				map[string]any{"disposition": disp, "ttl_seconds": int64(ttl / time.Second),
					"expires_at": expires.Format(time.RFC3339)}, n.FolderID))
		})
		if err != nil {
			return err
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, linkJSON{
		URL:         base + linkPrefix + s.links.sign(c, n.ContentTag),
		Disposition: disp,
		ExpiresAt:   expires,
	})
	return nil
}

// handleGetLink serves the file a signed link names. It needs no credential and reads no cookie.
// The token must verify against the node's current content tag; the link must not have expired; the
// account must be active. The user's grants are not consulted: the link is the capability.
func (s *Server) handleGetLink(w http.ResponseWriter, r *http.Request) error {
	// Charged before the token is parsed or the database is read: a flood of guesses costs
	// little, and a player's seeks come from one address.
	if err := s.rateLimit("links.serve", clientKey(r)); err != nil {
		return err
	}
	ctx := r.Context()
	c, payload, mac, err := parseLinkToken(r.PathValue("token"))
	if err != nil {
		return linkNotFound()
	}
	n, err := s.store.Node(ctx, c.acc, c.nod)
	if errors.Is(err, store.ErrNotFound) || (err == nil && n.Kind != store.KindFile) {
		return linkNotFound()
	}
	if err != nil {
		return err
	}
	if !s.links.verify(payload, mac, n.ContentTag) {
		return linkNotFound()
	}
	// Trashing a file does not change its SHA-256, so the link's MAC still checks. A link to a file in the trash
	// is refused here, so the link stops working the moment the file is trashed.
	self, above, err := s.store.TrashState(ctx, n.AccountID, n.ID)
	if err != nil {
		return err
	}
	if self || above {
		return linkNotFound()
	}
	if !time.Now().Before(time.Unix(c.exp, 0)) {
		return apierr.New(apierr.LinkExpired, "the link has expired; ask for a new one")
	}
	acc, err := s.store.Account(ctx, c.acc)
	if errors.Is(err, store.ErrNotFound) {
		return linkNotFound()
	}
	if err != nil {
		return err
	}
	switch acc.Status {
	case store.AccountActive:
	case store.AccountSuspended:
		return apierr.New(apierr.AccountSuspended, "the account is suspended")
	default:
		return linkNotFound()
	}
	// A thumbnail link (thumb_<size>) names a derived file, not the contents.
	if strings.HasPrefix(c.disp, thumbDispPrefix) {
		return s.serveThumbLink(w, r, n, c.disp)
	}
	return s.serveFile(w, r, n, c.disp == dispInline, corpCrossOrigin)
}

// contentGate refuses, on the content origin, every request that is not for a signed link. That
// origin has no API, interface or session: a browser's cookies for the main origin are never sent
// to it, and nothing on it reads one. The path must be clean, so that no ".." can carry a request
// past the check.
func (s *Server) contentGate(next http.Handler) http.Handler {
	if s.contentHost == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.onContentHost(r) && !isLinkPath(r.URL.Path) {
			s.writeError(w, r, apierr.New(apierr.NotFound, "no such endpoint"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLinkPath reports whether p is the path of one signed link, with no dot segments.
func isLinkPath(p string) bool {
	return strings.HasPrefix(p, linkPrefix) && len(p) > len(linkPrefix) && !strings.Contains(p[len(linkPrefix):], "/") &&
		!strings.Contains(p, "..") && !strings.Contains(p, "//")
}
