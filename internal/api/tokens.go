// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Delegated tokens (design section 7).
//
// An account key mints a token for a browser. The token names the account, optionally one of its
// users to act as, a list of folders, a permission and a lifetime:
//
//	sct_ base64url(payload JSON) "." base64url(HMAC-SHA256 of the payload bytes)
//
// The MAC key is derived from the signing key with HKDF-SHA256 under a label of its own, so a token
// can never be mistaken for a signed download link, although both come from keys/signing.key. A
// token's signature needs no database read. Revoking a token does need one: the account, the
// minting key and the user are read on every use, because revocation changes those rows and the
// token itself never changes.

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

const (
	// tokenMax bounds a token before anything is decoded.
	tokenMax = 4096
	// tokenMaxFolders is the most folders one token may reach.
	tokenMaxFolders = 32
	tokenDefaultTTL = 15 * time.Minute
	tokenMinTTL     = time.Minute
	tokenVersion    = 1
	tokenKeyLabel   = "selfcloud token v1"
	tokenAudience   = "api"
	tokenMACLen     = sha256.Size
	permRead        = "read"
	permWrite       = "write"
)

// errBadToken is the answer to every token that is malformed, forged, unknown, or void. One answer
// for all of them means a probe learns nothing from the difference.
var errBadToken = apierr.New(apierr.Unauthenticated, "the token is not valid")

// tokenKey is a derived MAC key and its ID, which is the first 8 bytes of SHA-256 of the key.
type tokenKey struct {
	mac []byte
	kid string
}

// tokenKeys holds the derived keys. current signs and verifies; previous only verifies, so tokens
// survive a rotation of the signing key until the previous key file is removed.
type tokenKeys struct {
	current  tokenKey
	previous *tokenKey // nil when there is no previous key
}

// deriveTokenKeys derives the token keys from the signing keys the links use.
func deriveTokenKeys(lk *linkKeys) (*tokenKeys, error) {
	cur, err := deriveTokenKey(lk.current)
	if err != nil {
		return nil, err
	}
	tk := &tokenKeys{current: cur}
	if lk.previous != nil {
		prev, err := deriveTokenKey(lk.previous)
		if err != nil {
			return nil, err
		}
		tk.previous = &prev
	}
	return tk, nil
}

func deriveTokenKey(secret []byte) (tokenKey, error) {
	mac, err := hkdf.Key(sha256.New, secret, nil, tokenKeyLabel, tokenMACLen)
	if err != nil {
		return tokenKey{}, fmt.Errorf("tokens: %w", err)
	}
	sum := sha256.Sum256(mac)
	return tokenKey{mac: mac, kid: base64.RawURLEncoding.EncodeToString(sum[:8])}, nil
}

// tokenPayload is what a token says. Its fields are short, as the token travels in a header.
type tokenPayload struct {
	Version    int      `json:"v"`
	KeyID      string   `json:"kid"` // the signing key that made the MAC
	AccountID  string   `json:"acc"`
	UserID     string   `json:"usr"` // "" when the token acts as the minting key
	MintedBy   string   `json:"key"` // the account key that minted the token
	Folders    []string `json:"fld"`
	Node       string   `json:"nod,omitempty"` // a directory the token is confined to; "" for whole folders
	Permission string   `json:"perm"`          // read or write
	Audience   string   `json:"aud"`
	IssuedAt   int64    `json:"iat"` // Unix milliseconds
	ExpiresAt  int64    `json:"exp"` // Unix milliseconds
}

// valid reports whether the fields have the shape a token minted here has. It is checked after the
// MAC, so only a token this server signed gets this far.
func (pl tokenPayload) valid() bool {
	if pl.Version != tokenVersion || !id.Valid("acc", pl.AccountID) || !id.Valid("key", pl.MintedBy) {
		return false
	}
	if pl.UserID != "" && !id.Valid("usr", pl.UserID) {
		return false
	}
	if len(pl.Folders) == 0 || len(pl.Folders) > tokenMaxFolders {
		return false
	}
	for _, f := range pl.Folders {
		if !id.Valid("fld", f) {
			return false
		}
	}
	if pl.Node != "" && !id.Valid("nod", pl.Node) {
		return false
	}
	return (pl.Permission == permRead || pl.Permission == permWrite) && pl.Audience == tokenAudience &&
		pl.IssuedAt > 0 && pl.ExpiresAt > pl.IssuedAt
}

func tokenMAC(key, body []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(body)
	return h.Sum(nil)
}

// sign returns the token for pl, signed with the current key.
func (k *tokenKeys) sign(pl tokenPayload) (string, error) {
	body, err := json.Marshal(pl)
	if err != nil {
		return "", fmt.Errorf("tokens: %w", err)
	}
	return auth.TokenPrefix + base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(tokenMAC(k.current.mac, body)), nil
}

// match returns the key that made mac over body, under either key, or nil. Each comparison is
// constant-time; the only timing difference is whether the current key matched, which tells a
// caller nothing it does not already know about its own token.
func (k *tokenKeys) match(body, mac []byte) *tokenKey {
	if hmac.Equal(tokenMAC(k.current.mac, body), mac) {
		return &k.current
	}
	if k.previous != nil && hmac.Equal(tokenMAC(k.previous.mac, body), mac) {
		return k.previous
	}
	return nil
}

// splitToken checks a token's outer shape and decodes its two parts. Its length is checked before
// anything is decoded.
func splitToken(tok string) (body, mac []byte, err error) {
	if len(tok) > tokenMax || !strings.HasPrefix(tok, auth.TokenPrefix) {
		return nil, nil, errBadToken
	}
	b64, m64, ok := strings.Cut(tok[len(auth.TokenPrefix):], ".")
	if !ok || b64 == "" || strings.Contains(m64, ".") {
		return nil, nil, errBadToken
	}
	if body, err = base64.RawURLEncoding.DecodeString(b64); err != nil {
		return nil, nil, errBadToken
	}
	if mac, err = base64.RawURLEncoding.DecodeString(m64); err != nil || len(mac) != tokenMACLen {
		return nil, nil, errBadToken
	}
	return body, mac, nil
}

// parsePayload reads the payload strictly: an unknown field, or anything after the object, is
// refused.
func parsePayload(body []byte) (tokenPayload, error) {
	var pl tokenPayload
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pl); err != nil {
		return pl, errBadToken
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return pl, errBadToken
	}
	return pl, nil
}

// authenticateToken identifies the caller from a delegated token. The checks run from the cheapest
// to the most expensive: the shape, the MAC, the payload, the expiry and then one read of the
// database. Every failure except expiry gets errBadToken. An expired token gets its own answer, so
// a client refreshes it silently and asks the person to sign in only when the token is refused.
func (s *Server) authenticateToken(ctx context.Context, tok string) (*auth.Principal, error) {
	body, mac, err := splitToken(tok)
	if err != nil {
		return nil, err
	}
	key := s.tokens.match(body, mac)
	if key == nil {
		return nil, errBadToken
	}
	pl, err := parsePayload(body)
	if err != nil || !pl.valid() || pl.KeyID != key.kid {
		return nil, errBadToken
	}
	now := time.Now()
	if now.UnixMilli() >= pl.ExpiresAt {
		return nil, apierr.New(apierr.TokenExpired, "the token has expired; get a new one")
	}
	d, err := s.store.Delegation(ctx, pl.AccountID, pl.MintedBy, pl.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errBadToken
	}
	if err != nil {
		return nil, err
	}
	p := principalOfToken(pl, d)
	if !callerLive(p, d, now) {
		return nil, errBadToken
	}
	return p, nil
}

// principalOfToken is the caller a token stands for: its user, when it names one, else the account
// key that minted it.
func principalOfToken(pl tokenPayload, d *store.Delegation) *auth.Principal {
	scope := &auth.TokenScope{
		Folders:   pl.Folders,
		Node:      pl.Node,
		Write:     pl.Permission == permWrite,
		Audience:  pl.Audience,
		MintedBy:  pl.MintedBy,
		IssuedAt:  time.UnixMilli(pl.IssuedAt).UTC(),
		ExpiresAt: time.UnixMilli(pl.ExpiresAt).UTC(),
	}
	if pl.UserID != "" {
		return &auth.Principal{Kind: auth.KindUser, ID: pl.UserID, AccountID: pl.AccountID, UserID: pl.UserID,
			Role: d.UserRole, Token: scope}
	}
	return &auth.Principal{Kind: auth.KindAccountKey, ID: pl.MintedBy, AccountID: pl.AccountID, Token: scope}
}

// callerKeyID is the account key a caller depends on: its own key, or the key that minted its token.
// It is "" for a signed-in user, which depends on no key.
func callerKeyID(p *auth.Principal) string {
	if p.Token != nil {
		return p.Token.MintedBy
	}
	if p.Kind == auth.KindAccountKey {
		return p.ID
	}
	return ""
}

// callerLive reports whether the caller p stands for may still act, judged by the state d read from
// the database now. Its lifetime is not judged here: a token that expires after its job was queued
// still lets the job run, because the action was allowed when it was asked for. Revocation is
// what ends it: the account is suspended or deleting; the minting key is revoked, expired or
// gone; the user is disabled, pending or gone; or the token was issued at or before the account's
// cut-off.
func callerLive(p *auth.Principal, d *store.Delegation, now time.Time) bool {
	if d.AccountStatus != store.AccountActive {
		return false
	}
	if p.Token != nil && !p.Token.IssuedAt.After(d.TokensNotBefore) {
		return false
	}
	if callerKeyID(p) != "" {
		if !d.KeyFound || d.KeyRevokedAt != nil || (d.KeyExpiresAt != nil && !now.Before(*d.KeyExpiresAt)) {
			return false
		}
	}
	if p.Kind == auth.KindUser && (!d.UserFound || d.UserStatus != store.UserActive) {
		return false
	}
	return true
}

// tokenRequest is the body of POST /accounts/{acc}/tokens. Unknown fields are refused.
type tokenRequest struct {
	User        string   `json:"user"`        // optional: act as this user of the account
	Folders     []string `json:"folders"`     // 1 to 32 folders of the account
	Permissions string   `json:"permissions"` // read (the default) or write
	TTL         *int64   `json:"ttl"`         // seconds; the default is 15 minutes
	Audience    string   `json:"audience"`    // api (the default)
	// Path or Node confines the token to one directory and everything below it. Path names a
	// directory inside the one folder given, and Node is a directory ID in one of the folders. Give
	// at most one of them.
	Path *string `json:"path"`
	Node string  `json:"node"`
}

// tokenJSON is the answer to a mint. The token is shown here and nowhere else.
type tokenJSON struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// handleMintToken makes a delegated token. Only an account key of the account may mint one. A
// token cannot mint another, and neither can a session or an installation admin, so a token never
// widens itself. The reply carries the token once; the audit log records the mint, never the token.
func (s *Server) handleMintToken(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if p == nil || p.Kind != auth.KindAccountKey || p.Token != nil {
		return apierr.New(apierr.Forbidden, "only an account key may mint a delegated token")
	}
	accID := r.PathValue("acc")
	if accID != p.AccountID {
		return apierr.New(apierr.NotFound, "no such account")
	}
	if err := s.rateLimit("tokens.mint", string(p.Kind)+":"+p.ID); err != nil {
		return err
	}
	var in tokenRequest
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	folders, write, ttl, err := s.checkTokenRequest(in)
	if err != nil {
		return err
	}
	now := time.Now()
	pl := tokenPayload{
		Version:    tokenVersion,
		KeyID:      s.tokens.current.kid,
		AccountID:  accID,
		UserID:     in.User,
		MintedBy:   p.ID,
		Folders:    folders,
		Permission: permRead,
		Audience:   tokenAudience,
		IssuedAt:   now.UnixMilli(),
		ExpiresAt:  now.Add(ttl).UnixMilli(),
	}
	if write {
		pl.Permission = permWrite
	}
	if err := checkTokenScope(in, folders); err != nil {
		return err
	}
	var out tokenJSON
	ctx := r.Context()
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		acc, err := tx.Account(ctx, accID)
		if errors.Is(err, store.ErrNotFound) {
			return apierr.New(apierr.NotFound, "no such account")
		}
		if err != nil {
			return err
		}
		switch acc.Status {
		case store.AccountSuspended:
			return apierr.New(apierr.AccountSuspended, "the account is suspended")
		case store.AccountActive:
		default:
			return apierr.New(apierr.NotFound, "no such account")
		}
		target := accID
		if in.User != "" {
			target = in.User
			u, err := tx.User(ctx, accID, in.User)
			if errors.Is(err, store.ErrNotFound) {
				return notFound("user")
			}
			if err != nil {
				return err
			}
			switch u.Status {
			case store.UserPending:
				return apierr.New(apierr.UserNotVerified, "the user is not verified yet")
			case store.UserDisabled:
				return apierr.New(apierr.UserDisabled, "the user is disabled")
			}
		}
		for _, f := range folders {
			if _, err := tx.Folder(ctx, accID, f); errors.Is(err, store.ErrNotFound) {
				return notFound("folder")
			} else if err != nil {
				return err
			}
		}
		// The directory is resolved here, inside the transaction, so the node the token is
		// confined to is the one that exists when the token is signed.
		switch {
		case in.Path != nil:
			dir, err := s.tokenDirByPath(ctx, tx, accID, folders[0], *in.Path)
			if err != nil {
				return err
			}
			pl.Node = dir.ID
		case in.Node != "":
			dir, err := tx.Node(ctx, accID, in.Node)
			if errors.Is(err, store.ErrNotFound) || (err == nil && !slices.Contains(folders, dir.FolderID)) {
				return notFound("node")
			}
			if err != nil {
				return err
			}
			// A directory in the trash cannot scope a token: the token would reach nothing there.
			if self, above, err := tx.TrashState(ctx, accID, dir.ID); err != nil {
				return err
			} else if self || above {
				return notFound("node")
			}
			if dir.Kind != store.KindDir {
				return apierr.New(apierr.InvalidRequest, "node must be a directory")
			}
			pl.Node = dir.ID
		}
		token, err := s.tokens.sign(pl)
		if err != nil {
			return err
		}
		out = tokenJSON{Token: token, ExpiresAt: time.UnixMilli(pl.ExpiresAt).UTC()}
		return tx.InsertAudit(ctx, s.auditEntry(r, accID, "token.mint", target, map[string]any{
			"user": in.User, "folders": folders, "node": pl.Node, "permissions": pl.Permission,
			"audience": pl.Audience, "expires_at": out.ExpiresAt.Format(time.RFC3339),
		}))
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, out)
	return nil
}

// checkTokenScope checks the shape of the directory a mint names. Path needs exactly one folder,
// since a path has no meaning across several; Node needs a valid directory ID; and Path and Node
// are exclusive.
func checkTokenScope(in tokenRequest, folders []string) error {
	switch {
	case in.Path != nil && in.Node != "":
		return apierr.New(apierr.InvalidRequest, "give path or node, not both")
	case in.Path != nil && len(folders) != 1:
		return apierr.New(apierr.InvalidRequest, "path needs exactly one folder")
	case in.Node != "":
		return validID("nod", in.Node)
	}
	return nil
}

// tokenDirByPath resolves a path under a folder's root to the directory it names, inside the
// transaction. A path that names no directory is not_found.
func (s *Server) tokenDirByPath(ctx context.Context, tx *store.Tx, accID, folderID, path string) (*store.Node, error) {
	segs, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	root, err := tx.RootNode(ctx, accID, folderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("folder")
	}
	if err != nil {
		return nil, err
	}
	find := func(ctx context.Context, parentID, key string) (*store.Node, error) {
		return tx.Child(ctx, accID, parentID, key)
	}
	dir, err := walkDirs(ctx, find, root, segs)
	if errors.As(err, new(*notDirError)) {
		return nil, notFound("directory")
	}
	if err != nil {
		return nil, err
	}
	if dir == nil {
		return nil, notFound("directory")
	}
	return dir, nil
}

// checkTokenRequest validates a mint request. It returns the folders with duplicates removed, the
// permission, and the lifetime. A lifetime above tokens.max_ttl is refused, not shortened, as for
// signed links, so a client never gets less than it asked for without knowing it.
func (s *Server) checkTokenRequest(in tokenRequest) (folders []string, write bool, ttl time.Duration, err error) {
	if in.User != "" {
		if err := validID("usr", in.User); err != nil {
			return nil, false, 0, err
		}
	}
	switch {
	case len(in.Folders) == 0:
		return nil, false, 0, apierr.New(apierr.InvalidRequest, "folders must name at least one folder")
	case len(in.Folders) > tokenMaxFolders:
		return nil, false, 0, apierr.Newf(apierr.InvalidRequest, "a token may name at most %d folders", tokenMaxFolders).
			With("max_folders", tokenMaxFolders)
	}
	seen := map[string]bool{}
	for _, f := range in.Folders {
		if err := validID("fld", f); err != nil {
			return nil, false, 0, err
		}
		if !seen[f] {
			seen[f] = true
			folders = append(folders, f)
		}
	}
	switch in.Permissions {
	case "", permRead:
	case permWrite:
		write = true
	default:
		return nil, false, 0, apierr.New(apierr.InvalidRequest, "permissions must be read or write")
	}
	switch in.Audience {
	case "", tokenAudience:
	case "wopi":
		return nil, false, 0, apierr.New(apierr.InvalidRequest, "wopi tokens are not available until the office-editing slice")
	default:
		return nil, false, 0, apierr.New(apierr.InvalidRequest, "audience must be api")
	}
	ttl = tokenDefaultTTL
	if in.TTL != nil {
		maxTTL := s.cfg.Tokens.MaxTTL.D()
		if *in.TTL < int64(tokenMinTTL/time.Second) {
			return nil, false, 0, apierr.Newf(apierr.InvalidRequest, "ttl must be at least %d seconds", int64(tokenMinTTL/time.Second))
		}
		if *in.TTL > int64(maxTTL/time.Second) {
			return nil, false, 0, apierr.Newf(apierr.InvalidRequest, "ttl may be at most %d seconds", int64(maxTTL/time.Second)).
				With("max_ttl_seconds", int64(maxTTL/time.Second))
		}
		ttl = time.Duration(*in.TTL) * time.Second
	}
	return folders, write, ttl, nil
}

// handleRevokeTokens ends every token issued up to now, for the whole account. The account's cut-off
// moves to now. A token issued in the same millisecond is void too, which is the safe side of the
// boundary. An account key, or a person who manages the account, may do this.
func (s *Server) handleRevokeTokens(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	var cut time.Time
	ctx := r.Context()
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Account(ctx, acc.ID)
		if errors.Is(err, store.ErrNotFound) {
			return apierr.New(apierr.NotFound, "no such account")
		}
		if err != nil {
			return err
		}
		// The time is read inside the transaction, and the cut-off only moves forward, so a call that
		// waited for the writer cannot reopen tokens a later call has already ended.
		now := store.Now()
		cut = now
		if cur.TokensNotBefore.After(now) {
			cut = cur.TokensNotBefore
		}
		cur.TokensNotBefore = cut
		cur.UpdatedAt = now
		if err := tx.UpdateAccount(ctx, cur); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "tokens.revoke", acc.ID,
			map[string]any{"tokens_not_before": cut.Format(time.RFC3339Nano)}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]time.Time{"tokens_not_before": cut})
	return nil
}
