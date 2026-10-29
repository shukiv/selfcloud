// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Visitor credentials for links that anyone can open (design section 8, Share links).
//
// A visitor who opens such a link gets a credential that acts as a bearer for the link's node and
// everything below it. The credential is an HMAC-signed payload, the same form as a delegated token:
//
//	scl_ base64url(payload JSON) "." base64url(HMAC-SHA256 of the payload bytes)
//
// The MAC key is derived from the signing key with HKDF-SHA256 under a label of its own, so a credential
// is never mistaken for a token or a signed link. It lives shareCredentialTTL, and POST /share/renew swaps a
// live one for a fresh one. A credential names its link, account and node, but its role and its link's state
// are read from the database on every request: a credential whose link is reset, deleted, paused, trashed,
// expired or whose account is suspended is refused at once.

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
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

const (
	// shareCredentialTTL is how long one credential lives. A visitor's page renews it before then.
	shareCredentialTTL = 15 * time.Minute
	// shareCredentialMax bounds a credential before anything is decoded.
	shareCredentialMax = 1024
	shareCredentialVer = 1
	shareKeyLabel      = "selfcloud share credential v1"
)

// errBadCredential is the answer to a visitor credential that is malformed, forged or of another key. One
// answer for all of them, as for tokens.
var errBadCredential = apierr.New(apierr.Unauthenticated, "the credential is not valid")

// shareKeys holds the keys that sign visitor credentials. current signs and verifies; previous only verifies.
type shareKeys struct {
	current  tokenKey
	previous *tokenKey
}

// deriveShareKeys derives the credential keys from the signing keys the links use.
func deriveShareKeys(lk *linkKeys) (*shareKeys, error) {
	cur, err := deriveShareKey(lk.current)
	if err != nil {
		return nil, err
	}
	sk := &shareKeys{current: cur}
	if lk.previous != nil {
		prev, err := deriveShareKey(lk.previous)
		if err != nil {
			return nil, err
		}
		sk.previous = &prev
	}
	return sk, nil
}

func deriveShareKey(secret []byte) (tokenKey, error) {
	mac, err := hkdf.Key(sha256.New, secret, nil, shareKeyLabel, tokenMACLen)
	if err != nil {
		return tokenKey{}, fmt.Errorf("share credentials: %w", err)
	}
	sum := sha256.Sum256(mac)
	return tokenKey{mac: mac, kid: base64.RawURLEncoding.EncodeToString(sum[:8])}, nil
}

// credentialPayload is what a visitor credential says. Times are Unix milliseconds.
type credentialPayload struct {
	Version   int    `json:"v"`
	KeyID     string `json:"kid"`
	AccountID string `json:"acc"`
	LinkID    string `json:"lnk"`
	NodeID    string `json:"nod"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// valid reports whether the fields have the shape a credential made here has. It runs after the MAC.
func (pl credentialPayload) valid() bool {
	if pl.Version != shareCredentialVer || !id.Valid("acc", pl.AccountID) || !id.Valid("shl", pl.LinkID) ||
		!id.Valid("nod", pl.NodeID) {
		return false
	}
	ttl := shareCredentialTTL.Milliseconds()
	return pl.IssuedAt > 0 && pl.ExpiresAt > pl.IssuedAt && pl.ExpiresAt-pl.IssuedAt <= ttl
}

// sign returns the credential for pl, signed with the current key.
func (k *shareKeys) sign(pl credentialPayload) (string, error) {
	body, err := json.Marshal(pl)
	if err != nil {
		return "", fmt.Errorf("share credentials: %w", err)
	}
	return auth.SharePrefix + base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(tokenMAC(k.current.mac, body)), nil
}

// match returns the key that made mac over body, under either key, or nil.
func (k *shareKeys) match(body, mac []byte) *tokenKey {
	if hmac.Equal(tokenMAC(k.current.mac, body), mac) {
		return &k.current
	}
	if k.previous != nil && hmac.Equal(tokenMAC(k.previous.mac, body), mac) {
		return k.previous
	}
	return nil
}

// splitShareCredential checks a credential's outer shape and decodes its two parts.
func splitShareCredential(cred string) (body, mac []byte, err error) {
	if len(cred) > shareCredentialMax || !strings.HasPrefix(cred, auth.SharePrefix) {
		return nil, nil, errBadCredential
	}
	b64, m64, ok := strings.Cut(cred[len(auth.SharePrefix):], ".")
	if !ok || b64 == "" || strings.Contains(m64, ".") {
		return nil, nil, errBadCredential
	}
	if body, err = base64.RawURLEncoding.DecodeString(b64); err != nil {
		return nil, nil, errBadCredential
	}
	if mac, err = base64.RawURLEncoding.DecodeString(m64); err != nil || len(mac) != tokenMACLen {
		return nil, nil, errBadCredential
	}
	return body, mac, nil
}

// parseCredentialPayload reads the payload strictly: an unknown field, or anything after the object, is refused.
func parseCredentialPayload(body []byte) (credentialPayload, error) {
	var pl credentialPayload
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pl); err != nil {
		return pl, errBadCredential
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return pl, errBadCredential
	}
	return pl, nil
}

// shareOpenable reports whether an anyone link gives access now, judged by the state st read: it must be an
// anyone link that has not expired, on an active account whose switch is on, with its node not trashed, and
// whose team space allows it (members_only admitting only the root). It is the one rule every use applies.
func shareOpenable(st *store.ShareLinkState, now time.Time) bool {
	l := &st.Link
	switch {
	case l.Audience != store.AudienceAnyone:
		return false
	case l.ExpiresAt == nil || !now.Before(*l.ExpiresAt):
		return false
	case st.AccountStatus != store.AccountActive || !st.PublicLinks:
		return false
	case st.NodeTrashed:
		return false
	case st.FolderKind != store.KindPersonal && !st.AllowLinks:
		return false
	case st.MembersOnly && !st.NodeIsRoot:
		return false
	}
	return true
}

// credentialLive reports whether a credential made from pl still stands against the link's state st: the node
// is the link's, the credential was issued after the link's current secret (so a reset ends it), and the link is
// openable now.
func credentialLive(st *store.ShareLinkState, pl credentialPayload, now time.Time) bool {
	return st.Link.NodeID == pl.NodeID && time.UnixMilli(pl.IssuedAt).After(st.Link.CredentialsAt) &&
		shareOpenable(st, now)
}

// authenticateShare identifies the caller from a visitor credential. The checks run from the cheapest to the
// most expensive: the shape, the MAC, the payload, the expiry, and then one read of the link's state. A bad
// credential is unauthenticated; a credential whose link is dead is not_found, the one answer every dead link
// gets.
func (s *Server) authenticateShare(ctx context.Context, cred string) (*auth.Principal, error) {
	body, mac, err := splitShareCredential(cred)
	if err != nil {
		return nil, err
	}
	key := s.shares.match(body, mac)
	if key == nil {
		return nil, errBadCredential
	}
	pl, err := parseCredentialPayload(body)
	if err != nil || !pl.valid() || pl.KeyID != key.kid {
		return nil, errBadCredential
	}
	now := time.Now()
	if now.UnixMilli() >= pl.ExpiresAt {
		return nil, apierr.New(apierr.Unauthenticated, "the credential has expired; open the link again")
	}
	st, err := s.store.ShareLinkStateByID(ctx, pl.AccountID, pl.LinkID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("link")
	}
	if err != nil {
		return nil, err
	}
	if !credentialLive(st, pl, now) {
		return nil, notFound("link")
	}
	// An anyone link gives the viewer role, whatever its row says: the row is read here for the link's state only.
	return &auth.Principal{Kind: auth.KindShareLink, ID: st.Link.ID, AccountID: st.Link.AccountID,
		Link: &auth.LinkScope{Node: st.Link.NodeID, Role: store.RoleViewer, ExpiresAt: time.UnixMilli(pl.ExpiresAt)}}, nil
}

// issueShareCredential signs a fresh credential for the link st describes. It is issued at now, or one
// millisecond after the link's credentials_at when that is later, so a credential made in the very millisecond
// of a reset is not already void. It returns the credential and when it ends.
func (s *Server) issueShareCredential(st *store.ShareLinkState, now time.Time) (string, time.Time, error) {
	iat := time.UnixMilli(now.UnixMilli())
	if !iat.After(st.Link.CredentialsAt) {
		iat = st.Link.CredentialsAt.Add(time.Millisecond)
	}
	exp := iat.Add(shareCredentialTTL)
	tok, err := s.shares.sign(credentialPayload{Version: shareCredentialVer, KeyID: s.shares.current.kid,
		AccountID: st.Link.AccountID, LinkID: st.Link.ID, NodeID: st.Link.NodeID,
		IssuedAt: iat.UnixMilli(), ExpiresAt: exp.UnixMilli()})
	return tok, exp, err
}
