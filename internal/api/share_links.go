// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// A share link gives access to a file or a directory without a permission row. This file holds the
// slice o1 routes: organisation links, which open for members of the account, and the account's policy
// switch. Links that anyone can open and drop links come in later slices and are refused here.
//
// A link follows the same rules as a permission: the caller must share the node (mayShare), it may
// not grant a role above its own, and a delegated token never reaches a link. Opening a link records
// a visit (share_link_visits), and the rank queries count the visit; the link itself is never a
// permission row. See design.md section 8 and docs/design.md "Share links".

// linkRoles are the roles a link may give. A link never gives manager.
var linkRoles = []string{store.RoleViewer, store.RoleCommenter, store.RoleContributor, store.RoleEditor}

// shareLinkJSON is a link as the API shows it. InheritedFrom is the node the link sits on, when that is
// not the node asked about; it is null on the node itself. The fields after it are shown to a sharer
// only (a manager of the node, or an account key): who made the link, how many members have opened it,
// and whether policy pauses it. Nothing here is the secret or the URL.
type shareLinkJSON struct {
	ID            string     `json:"id"`
	NodeID        string     `json:"node_id"`
	Audience      string     `json:"audience"`
	Role          string     `json:"role"`
	ExpiresAt     *time.Time `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	InheritedFrom *string    `json:"inherited_from"`
	CreatedBy     *string    `json:"created_by,omitempty"`
	Visitors      *int       `json:"visitors,omitempty"`
	Paused        *bool      `json:"paused,omitempty"`
}

// shareLinkCreated is the answer to a new link or a reset: the link, and its URL. The URL is the only
// place the secret is shown, so the client copies it at once.
type shareLinkCreated struct {
	Link shareLinkJSON `json:"link"`
	URL  string        `json:"url"`
}

type sharingPolicyJSON struct {
	PublicLinks bool `json:"public_links"`
}

// toShareLinkJSON shows a stored link. node is the node the listing is about; sharer says whether the
// caller sees the sharer-only fields.
func toShareLinkJSON(l *store.ShareLink, v *store.ShareLinkView, node string, sharer bool) shareLinkJSON {
	j := shareLinkJSON{ID: l.ID, NodeID: l.NodeID, Audience: l.Audience, Role: l.Role,
		ExpiresAt: l.ExpiresAt, CreatedAt: l.CreatedAt}
	if l.NodeID != node {
		from := l.NodeID
		j.InheritedFrom = &from
	}
	if sharer {
		by := l.CreatedBy
		j.CreatedBy = &by
		if v != nil {
			visitors, paused := v.Visitors, v.Paused
			j.Visitors, j.Paused = &visitors, &paused
		}
	}
	return j
}

// shareURL is the address that opens a link. The secret sits in the fragment, which a browser never
// sends to a server, a proxy or a Referer.
func (s *Server) shareURL(secret string) string {
	return strings.TrimSuffix(s.cfg.PublicURL, "/") + "/s#" + secret
}

// creatorKind names what made a link, for the audit log and the row: a user, or an account key.
func creatorKind(p *auth.Principal) string {
	if p.Kind == auth.KindUser {
		return store.CreatorUser
	}
	return store.CreatorAccountKey
}

// shareLinkCaller refuses the callers that never manage links: no caller, an installation admin, and a
// delegated token, which is narrowed to its folders and never shares.
func shareLinkCaller(r *http.Request) (*auth.Principal, error) {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return nil, err
	}
	if p.Token != nil {
		return nil, apierr.New(apierr.Forbidden, "a delegated token cannot manage share links")
	}
	return p, nil
}

// checkLinkShape refuses what slice o1 does not build yet: links that anyone can open, and drop links.
// It also checks the audience's name.
func checkLinkShape(audience string, uploadOnly bool) error {
	switch {
	case uploadOnly:
		return apierr.New(apierr.InvalidRequest, "drop links are not available yet")
	case audience == store.AudienceAnyone:
		return apierr.New(apierr.InvalidRequest, "links that anyone can open are not available yet")
	case audience != store.AudienceOrganisation:
		return apierr.New(apierr.InvalidRequest, "audience must be organisation")
	}
	return nil
}

// checkLinkRole refuses a role a link may not give: manager, or a name that is no role.
func checkLinkRole(role string) error {
	for _, r := range linkRoles {
		if role == r {
			return nil
		}
	}
	return apierr.New(apierr.InvalidRequest, "a link's role is viewer, commenter, contributor or editor")
}

// linkPolicyRefusal is the team-space policy for a link on n in folder f. A team space needs allow_links;
// with members_only, a link is allowed on the space's root only. A personal folder (My files) always
// allows an organisation link. The same rule is applied when a link is used (store linkLiveSQL), so a
// setting changed later pauses the link at once.
func linkPolicyRefusal(f *store.Folder, n *store.Node) error {
	if f.Kind == store.KindTeam && !f.AllowLinks {
		return apierr.New(apierr.Forbidden, "links are off in this team space").With("reason", "links_off")
	}
	if f.MembersOnly && !n.IsRoot() {
		return apierr.New(apierr.Conflict, "the team space is members only: links are allowed on its root only").
			With("reason", "members_only")
	}
	return nil
}

// handleCreateShareLink makes the node's link. The caller shares the node (mayShare) and may not give a
// role above its own. A node has one organisation link; a second one is a conflict, and changing the
// existing one is the way to change it. The secret is returned here, once.
func (s *Server) handleCreateShareLink(w http.ResponseWriter, r *http.Request) error {
	p, err := shareLinkCaller(r)
	if err != nil {
		return err
	}
	var in struct {
		Audience   string     `json:"audience"`
		Role       string     `json:"role"`
		UploadOnly bool       `json:"upload_only"`
		ExpiresAt  *time.Time `json:"expires_at"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := checkLinkShape(in.Audience, in.UploadOnly); err != nil {
		return err
	}
	if in.Role == "" {
		in.Role = store.RoleViewer
	}
	if err := checkLinkRole(in.Role); err != nil {
		return err
	}
	now := time.Now()
	if err := s.checkExpiry(in.ExpiresAt, now); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return notFound("node")
	}
	ctx := r.Context()
	var (
		link   *store.ShareLink
		secret string
	)
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		n, own, err := requireSharer(ctx, tx, p, nodeID)
		if err != nil {
			return err
		}
		if store.RoleRank(in.Role) > store.RoleRank(own) {
			return apierr.New(apierr.Forbidden, "you may not give a link a role above your own")
		}
		f, err := tx.Folder(ctx, n.AccountID, n.FolderID)
		if err != nil {
			return err
		}
		if err := linkPolicyRefusal(f, n); err != nil {
			return err
		}
		var hash []byte
		secret, hash = auth.NewToken()
		link = &store.ShareLink{ID: id.New("shl"), AccountID: n.AccountID, NodeID: n.ID,
			Audience: in.Audience, Role: in.Role, SecretHash: hash, ExpiresAt: in.ExpiresAt,
			CredentialsAt: now, CreatedBy: p.ID, CreatedKind: creatorKind(p), CreatedAt: now,
			UpdatedBy: p.ID, UpdatedKind: creatorKind(p), UpdatedAt: now}
		if err := tx.InsertShareLink(ctx, link); errors.Is(err, store.ErrConflict) {
			return apierr.New(apierr.Conflict, "this item already has a link; change that one instead").
				With("reason", "exists")
		} else if err != nil {
			return err
		}
		details := map[string]any{"audience": link.Audience, "role": link.Role}
		if link.ExpiresAt != nil {
			details["expires_at"] = link.ExpiresAt.UTC().Format(time.RFC3339)
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "link.create", link.ID, details, n.FolderID))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, shareLinkCreated{
		Link: toShareLinkJSON(link, &store.ShareLinkView{ShareLink: *link}, link.NodeID, true), URL: s.shareURL(secret)})
	return nil
}

// handleListShareLinks lists the links on a node and on its ancestors, the node's own first. A sharer
// sees them all, with the sharer-only fields. Anyone else sees the live ones on nodes it reaches, so a
// link on a folder above the caller's reach is not shown to it.
func (s *Server) handleListShareLinks(w http.ResponseWriter, r *http.Request) error {
	p, err := shareLinkCaller(r)
	if err != nil {
		return err
	}
	n, role, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	views, err := s.store.ChainShareLinks(ctx, n.AccountID, n.ID, time.Now())
	if err != nil {
		return err
	}
	sharer := seesFullChain(p, role)
	out := listResponse[shareLinkJSON]{Items: []shareLinkJSON{}}
	for _, v := range views {
		if !sharer {
			if v.Paused {
				continue
			}
			if v.NodeID != n.ID {
				if _, _, err := nodeAccessWith(ctx, s.store, p, v.NodeID); apierr.From(err).Code == apierr.NotFound {
					continue
				} else if err != nil {
					return err
				}
			}
		}
		out.Items = append(out.Items, toShareLinkJSON(&v.ShareLink, v, n.ID, sharer))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// changeShareLink loads a link for a change or a reset, inside tx. The link must be in the caller's
// account and its node reached (not_found otherwise). The caller must share that node and hold at least
// the link's role (forbidden otherwise). Policy must allow links now (links_off or members_only).
//
// It returns the link, its node and the role the caller holds there, which bounds the roles it may give.
func changeShareLink(ctx context.Context, tx *store.Tx, p *auth.Principal, linkID string) (*store.ShareLink, *store.Node, string, error) {
	l, err := tx.ShareLink(ctx, p.AccountID, linkID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, "", notFound("link")
	}
	if err != nil {
		return nil, nil, "", err
	}
	n, own, err := requireSharer(ctx, tx, p, l.NodeID)
	if err != nil {
		return nil, nil, "", err
	}
	if store.RoleRank(l.Role) > store.RoleRank(own) {
		return nil, nil, "", apierr.New(apierr.Forbidden, "you may not change a link above your own role")
	}
	f, err := tx.Folder(ctx, n.AccountID, n.FolderID)
	if err != nil {
		return nil, nil, "", err
	}
	if err := linkPolicyRefusal(f, n); err != nil {
		return nil, nil, "", err
	}
	return l, n, own, nil
}

// handleUpdateShareLink changes a link's role or expiry. An absent expires_at keeps the expiry; null
// clears it. Changing the audience to anyone, or a drop link, is refused for now.
func (s *Server) handleUpdateShareLink(w http.ResponseWriter, r *http.Request) error {
	p, err := shareLinkCaller(r)
	if err != nil {
		return err
	}
	var in struct {
		Audience   *string         `json:"audience"`
		Role       *string         `json:"role"`
		UploadOnly *bool           `json:"upload_only"`
		ExpiresAt  json.RawMessage `json:"expires_at"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Audience != nil || in.UploadOnly != nil {
		audience := store.AudienceOrganisation
		if in.Audience != nil {
			audience = *in.Audience
		}
		uploadOnly := in.UploadOnly != nil && *in.UploadOnly
		if err := checkLinkShape(audience, uploadOnly); err != nil {
			return err
		}
	}
	if in.Role != nil {
		if err := checkLinkRole(*in.Role); err != nil {
			return err
		}
	}
	var expires *time.Time
	clearExpiry := false
	if in.ExpiresAt != nil {
		if string(in.ExpiresAt) == "null" {
			clearExpiry = true
		} else if err := json.Unmarshal(in.ExpiresAt, &expires); err != nil || expires == nil {
			return apierr.New(apierr.InvalidRequest, "expires_at must be a time, or null")
		}
	}
	now := time.Now()
	if expires != nil {
		if err := s.checkExpiry(expires, now); err != nil {
			return err
		}
	}
	linkID := r.PathValue("shl")
	if !id.Valid("shl", linkID) {
		return notFound("link")
	}
	ctx := r.Context()
	var out *store.ShareLink
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		l, n, own, err := changeShareLink(ctx, tx, p, linkID)
		if err != nil {
			return err
		}
		details := map[string]any{}
		if in.Role != nil {
			if store.RoleRank(*in.Role) > store.RoleRank(own) {
				return apierr.New(apierr.Forbidden, "you may not give a link a role above your own")
			}
			l.Role = *in.Role
			details["role"] = l.Role
		}
		if clearExpiry {
			l.ExpiresAt = nil
			details["expires_at"] = nil
		} else if expires != nil {
			l.ExpiresAt = expires
			details["expires_at"] = expires.UTC().Format(time.RFC3339)
		}
		l.UpdatedBy, l.UpdatedKind, l.UpdatedAt = p.ID, creatorKind(p), now
		if err := tx.UpdateShareLink(ctx, l); err != nil {
			return err
		}
		out = l
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "link.update", l.ID, details, n.FolderID))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toShareLinkJSON(out, &store.ShareLinkView{ShareLink: *out}, out.NodeID, true))
	return nil
}

// handleResetShareLink gives the link a new secret. The old copies stop working: every visit made
// before the reset no longer counts, and a member opens the new link to get in again. The new URL is
// returned here, once.
func (s *Server) handleResetShareLink(w http.ResponseWriter, r *http.Request) error {
	p, err := shareLinkCaller(r)
	if err != nil {
		return err
	}
	linkID := r.PathValue("shl")
	if !id.Valid("shl", linkID) {
		return notFound("link")
	}
	ctx := r.Context()
	var (
		out    *store.ShareLink
		secret string
	)
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		l, n, _, err := changeShareLink(ctx, tx, p, linkID)
		if err != nil {
			return err
		}
		now := time.Now()
		var hash []byte
		secret, hash = auth.NewToken()
		l.SecretHash, l.CredentialsAt = hash, now
		l.UpdatedBy, l.UpdatedKind, l.UpdatedAt = p.ID, creatorKind(p), now
		if err := tx.UpdateShareLink(ctx, l); err != nil {
			return err
		}
		out = l
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "link.reset", l.ID, nil, n.FolderID))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, shareLinkCreated{
		Link: toShareLinkJSON(out, &store.ShareLinkView{ShareLink: *out}, out.NodeID, true), URL: s.shareURL(secret)})
	return nil
}

// handleDeleteShareLink removes a link at once: its visits go with it, so its members lose the access
// that came through it. The link's creator may always delete it, as long as it still reaches the node;
// anyone else must share the node, and may not remove a link above its own role.
func (s *Server) handleDeleteShareLink(w http.ResponseWriter, r *http.Request) error {
	p, err := shareLinkCaller(r)
	if err != nil {
		return err
	}
	linkID := r.PathValue("shl")
	if !id.Valid("shl", linkID) {
		return notFound("link")
	}
	ctx := r.Context()
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		l, err := tx.ShareLink(ctx, p.AccountID, linkID)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("link")
		}
		if err != nil {
			return err
		}
		n, own, err := nodeSharingAccess(ctx, tx, p, l.NodeID)
		if err != nil {
			return err
		}
		if l.CreatedBy != p.ID {
			ok, err := mayShare(ctx, tx, n, own)
			if err != nil {
				return err
			}
			if !ok {
				return roleTooLow(store.RoleManager)
			}
			if store.RoleRank(l.Role) > store.RoleRank(own) {
				return apierr.New(apierr.Forbidden, "you may not remove a link above your own role")
			}
		}
		if err := tx.DeleteShareLink(ctx, p.AccountID, l.ID); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "link.delete", l.ID,
			map[string]any{"audience": l.Audience, "role": l.Role}, n.FolderID))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// shareOpenAuditGap is how long a member's last visit to a link must be before its next open is audited again.
const shareOpenAuditGap = 24 * time.Hour

// handleOpenShareLink opens a link for a signed-in member of the link's account. It records the visit,
// and answers the node the link opens. Every other caller, and every dead link, gets the same not_found:
// a link that is missing, expired, deleted, paused, trashed, of another account, or opened by a token,
// an admin or someone with no session. The session check is the cookie's own, with its origin and CSRF
// checks, so the answer comes from here and not from the wrap's 401.
func (s *Server) handleOpenShareLink(w http.ResponseWriter, r *http.Request) error {
	dead := notFound("link")
	if err := s.rateLimit("share.open.ip", clientKey(r)); err != nil {
		return err
	}
	// A credential in the header is a key or a token, never a browser session: refuse it as dead.
	if r.Header.Get("Authorization") != "" {
		return dead
	}
	var in struct {
		Secret string `json:"secret"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if !auth.ValidToken(in.Secret) {
		return dead
	}
	p, _, err := s.authenticateSession(r)
	if err != nil || p == nil || p.Kind != auth.KindUser || p.IsAdmin() || p.UserID == "" {
		return dead
	}
	// The per-member limit sits beside the per-address one: a member behind a shared address keeps its own budget.
	if err := s.rateLimit("share.open.user", p.UserID); err != nil {
		return err
	}
	ctx := r.Context()
	hash := auth.HashToken(in.Secret)
	// The early read refuses a stranger cheaply. The write re-reads the link by ID and checks it again, so
	// a link deleted or reset between the two gives the same dead answer.
	l, err := s.store.ShareLinkBySecretHash(ctx, hash)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (l.AccountID != p.AccountID || l.Audience != store.AudienceOrganisation)) {
		return dead
	}
	if err != nil {
		return err
	}
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		now := time.Now()
		cur, err := tx.ShareLink(ctx, l.AccountID, l.ID)
		if errors.Is(err, store.ErrNotFound) {
			return dead
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(cur.SecretHash, hash) || cur.Audience != store.AudienceOrganisation {
			return dead
		}
		if cur.ExpiresAt != nil && !now.Before(*cur.ExpiresAt) {
			return dead
		}
		n, err := tx.Node(ctx, cur.AccountID, cur.NodeID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && n.TrashedAt != nil) {
			return dead
		}
		if err != nil {
			return err
		}
		f, err := tx.Folder(ctx, cur.AccountID, n.FolderID)
		if err != nil {
			return dead
		}
		if linkPolicyRefusal(f, n) != nil {
			return dead
		}
		// The last visit is read before this one is written, to decide whether the open is audited.
		prev, seen, err := tx.ShareLinkVisitAt(ctx, cur.ID, p.UserID)
		if err != nil {
			return err
		}
		// A visit counts only after the credentials it used were made (linkLiveSQL compares strictly), so a visit
		// in the same millisecond as the link's credentials is stamped one millisecond later.
		visitAt := now
		if !visitAt.After(cur.CredentialsAt) {
			visitAt = cur.CredentialsAt.Add(time.Millisecond)
		}
		if err := tx.RecordShareLinkVisit(ctx, cur.ID, p.UserID, visitAt); err != nil {
			return err
		}
		if !seen || visitAt.Sub(prev) > shareOpenAuditGap {
			if err := tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "link.open", cur.ID, nil, n.FolderID)); err != nil {
				return err
			}
		}
		// The visit counts only if the member reaches the node now (an ancestor may be trashed, or
		// the membership gone). A failed check rolls the visit back.
		if _, _, err := nodeAccessWith(ctx, tx, p, n.ID); err != nil {
			if apierr.From(err).Code == apierr.NotFound {
				return dead
			}
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"node_id": l.NodeID})
	return nil
}

// handleGetSharingPolicy answers the account's sharing policy.
func (s *Server) handleGetSharingPolicy(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	on, err := s.store.SharingPolicy(r.Context(), acc.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, sharingPolicyJSON{PublicLinks: on})
	return nil
}

// handleUpdateSharingPolicy sets the account's switch for links that anyone can open. Turning it off
// pauses those links at once; turning it back on revives them. Only the account's owner or key may
// change it, as for any account setting.
func (s *Server) handleUpdateSharingPolicy(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		PublicLinks *bool `json:"public_links"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.PublicLinks == nil {
		return apierr.New(apierr.InvalidRequest, "public_links must be true or false")
	}
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.SetSharingPolicy(ctx, acc.ID, *in.PublicLinks, store.Now()); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "account.sharing_policy", acc.ID,
			map[string]any{"public_links": *in.PublicLinks}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, sharingPolicyJSON{PublicLinks: *in.PublicLinks})
	return nil
}
