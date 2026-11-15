// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// Embedding lets a host application frame Selfcloud's pages (decision 185). The origins allowed for an
// account are the installation's embed.origins and the account's own list, unioned. A frame sign-in link
// names the account before the fragment; redeeming it starts a frame session whose cookie is partitioned,
// and the pages that session opens may be framed by those origins.

// embedOriginsJSON is the body of the embed routes: the account's allowed host origins.
type embedOriginsJSON struct {
	Origins []string `json:"origins"`
}

// handleGetEmbedOrigins answers the account's own allowed host origins, sorted.
func (s *Server) handleGetEmbedOrigins(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	origins, err := s.store.EmbedOrigins(r.Context(), acc.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, embedOriginsJSON{Origins: origins})
	return nil
}

// handlePutEmbedOrigins replaces the account's allowed host origins. A bad origin is named and writes
// nothing. The change is audited with the new list. Only the account's owner or key may make it.
func (s *Server) handlePutEmbedOrigins(w http.ResponseWriter, r *http.Request) error {
	var in embedOriginsJSON
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	origins, err := cleanEmbedOrigins(in.Origins)
	if err != nil {
		return err
	}
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.ReplaceEmbedOrigins(ctx, acc.ID, origins, store.Now()); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "account.embed_origins", acc.ID, map[string]any{"origins": origins}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, embedOriginsJSON{Origins: origins})
	return nil
}

// cleanEmbedOrigins checks an account's list: at most MaxEmbedOrigins, each a valid origin, deduplicated
// after normalisation and sorted. A refusal names the bad origin (decision 185).
func cleanEmbedOrigins(raw []string) ([]string, error) {
	if len(raw) > config.MaxEmbedOrigins {
		return nil, apierr.Newf(apierr.InvalidRequest, "an embed list holds at most %d origins", config.MaxEmbedOrigins)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, o := range raw {
		n, err := config.NormalizeOrigin(o)
		if err != nil {
			return nil, apierr.New(apierr.InvalidRequest, err.Error())
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out, nil
}

// allowedEmbedOrigins returns the origins that may frame an account's pages: the installation's list and
// the account's together, sorted, each once (decision 185).
func (s *Server) allowedEmbedOrigins(ctx context.Context, accountID string) ([]string, error) {
	acct, err := s.store.EmbedOrigins(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return s.unionEmbedOrigins(acct)
}

// unionEmbedOrigins merges an account's list with the installation's, normalising and deduplicating.
func (s *Server) unionEmbedOrigins(account []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	add := func(o string) error {
		n, err := config.NormalizeOrigin(o)
		if err != nil {
			return err
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
		return nil
	}
	for _, o := range s.settings().Embed.Origins {
		if err := add(o); err != nil {
			return nil, err
		}
	}
	for _, o := range account {
		if err := add(o); err != nil {
			return nil, err
		}
	}
	slices.Sort(out)
	return out, nil
}

// framePublicURLAllowed reports whether a frame sign-in link may be made: the public address is https, or
// http on localhost and 127.0.0.1 (decision 185). A partitioned cookie needs a secure context.
func (s *Server) framePublicURLAllowed() bool {
	u, err := url.Parse(s.cfg.PublicURL)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		h := u.Hostname()
		return h == "localhost" || h == "127.0.0.1"
	}
	return false
}

// frameLinkURL adds frame=<acc> to the part of a sign-in link before the fragment, so the page knows which
// account's origins to allow before the token is read. The token stays in the fragment (decision 185).
func frameLinkURL(link, accID string) string {
	base, frag := link, ""
	if i := strings.Index(link, "#"); i >= 0 {
		base, frag = link[:i], link[i:]
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "frame=" + url.QueryEscape(accID) + frag
}

// signInPageLocation works out, once, the path of the sign-in page from users.sign_in_url_template with
// public_url substituted, and whether the template's host is this installation's public URL. A template
// that points at another host never frames its sign-in page here, so local is false and the hook's first
// case does not apply (decision 185).
func signInPageLocation(tmpl, publicURL string) (path string, local bool) {
	link := strings.NewReplacer("{public_url}", publicURL, "{token}", "").Replace(tmpl)
	u, err := url.Parse(link)
	if err != nil {
		return "", false
	}
	pub, err := url.Parse(publicURL)
	if err != nil {
		return "", false
	}
	if !strings.EqualFold(u.Scheme, pub.Scheme) || !strings.EqualFold(u.Host, pub.Host) {
		return "", false
	}
	if u.Path == "" {
		return "/", true
	}
	return u.Path, true
}

// frameAncestors names the host origins that may frame the page r opens, or nil (decision 185). It is the
// web handler's hook, consulted only for the app page. The sign-in page a frame link names — the path of
// users.sign_in_url_template, on this installation's host — allows that account's origins; otherwise a live
// frame session's account does. It reads by hash, as the auth middleware does, and writes nothing.
func (s *Server) frameAncestors(r *http.Request) []string {
	ctx := r.Context()
	if s.signInPageLocal && r.URL.Path == s.signInPagePath {
		if accID := r.URL.Query().Get("frame"); id.Valid("acc", accID) {
			if acc, err := s.store.Account(ctx, accID); err == nil && acc.Status == store.AccountActive {
				if origins, err := s.allowedEmbedOrigins(ctx, accID); err == nil && len(origins) > 0 {
					return origins
				}
			}
		}
	}
	c, err := r.Cookie(s.sessionCookie)
	if err != nil || !auth.ValidToken(c.Value) {
		return nil
	}
	sess, err := s.store.SessionByHash(ctx, auth.HashToken(c.Value))
	if err != nil || !sess.Frame || !s.sessionLive(sess, s.sessionClock()) {
		return nil
	}
	acc, err := s.store.Account(ctx, sess.AccountID)
	if err != nil || acc.Status != store.AccountActive {
		return nil
	}
	origins, err := s.allowedEmbedOrigins(ctx, sess.AccountID)
	if err != nil {
		return nil
	}
	return origins
}
