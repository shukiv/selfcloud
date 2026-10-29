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
	"unicode"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// A share link gives access to a file or a directory without a permission row. This file holds the
// management routes and the open of organisation links, which open for members of the account, and of
// links that anyone can open, which open for whoever holds the secret (share_open.go). The account's
// policy switch is here too. Drop links come in a later slice and are refused here.
//
// A link follows the same rules as a permission: the caller must share the node (mayShare), it may
// not grant a role above its own, and a delegated token never reaches a link. An anyone link also needs
// the node's manager. Opening an organisation link records a visit (share_link_visits), and the rank
// queries count the visit; the link itself is never a permission row. See design.md section 8.

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
	// PasswordSet says whether an anyone link has a password. The password itself is never shown.
	PasswordSet *bool `json:"password_set,omitempty"`
	// UploadOnly marks a drop link: its role is "" and it takes uploads only. Title, Message, AskName and Notify
	// belong to drop links and are "" or false on others. Notify is shown to the creator's side only.
	UploadOnly bool   `json:"upload_only"`
	Title      string `json:"title"`
	Message    string `json:"message"`
	AskName    bool   `json:"ask_name"`
	Notify     bool   `json:"notify"`
}

// shareLinkCreated is the answer to a new link or a reset: the link, and its URL. The URL is the only
// place the secret is shown, so the client copies it at once.
type shareLinkCreated struct {
	Link shareLinkJSON `json:"link"`
	URL  string        `json:"url"`
}

// sharingPolicyJSON is the account's switch, and how many links that anyone can open the account has (live or
// paused by the switch; expired ones are left out).
type sharingPolicyJSON struct {
	PublicLinks bool `json:"public_links"`
	AnyoneLinks int  `json:"anyone_links"`
}

// toShareLinkJSON shows a stored link. node is the node the listing is about; sharer says whether the
// caller sees the sharer-only fields.
func toShareLinkJSON(l *store.ShareLink, v *store.ShareLinkView, node string, sharer bool) shareLinkJSON {
	j := shareLinkJSON{ID: l.ID, NodeID: l.NodeID, Audience: l.Audience, Role: l.Role,
		ExpiresAt: l.ExpiresAt, CreatedAt: l.CreatedAt, UploadOnly: l.UploadOnly, Title: l.Title,
		Message: l.Message, AskName: l.AskName}
	if l.NodeID != node {
		from := l.NodeID
		j.InheritedFrom = &from
	}
	if sharer {
		by := l.CreatedBy
		j.CreatedBy = &by
		j.Notify = l.Notify
		if v != nil {
			paused := v.Paused
			j.Paused = &paused
			// Visits belong to organisation links; an anyone link has no member who visits it.
			if l.Audience == store.AudienceOrganisation {
				visitors := v.Visitors
				j.Visitors = &visitors
			}
		}
		set := l.PasswordHash != ""
		j.PasswordSet = &set
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

// checkLinkShape checks an audience's name, and that a drop link is an anyone link: a drop link takes uploads from
// whoever holds its secret, so it is never an organisation link.
func checkLinkShape(audience string, uploadOnly bool) error {
	switch {
	case audience != store.AudienceOrganisation && audience != store.AudienceAnyone:
		return apierr.New(apierr.InvalidRequest, "audience must be organisation or anyone")
	case uploadOnly && audience != store.AudienceAnyone:
		return apierr.New(apierr.InvalidRequest, "a drop link is a link that anyone can open")
	}
	return nil
}

// Limits on a drop link's text. Line breaks are not kept: a title or message is one line in the page and in a mail.
const (
	dropTitleMax   = 100
	dropMessageMax = 1000
)

// cleanLine makes a user's text one plain line, and trims it. Every control character (Unicode category Cc, which
// includes the line breaks, tabs and NUL) and the Unicode line and paragraph separators become a space: a line break
// in a title, message or name would start a new line in the page or a header in a mail, and the others would do the
// same in a log or a viewer (slice o3, item 7). A CR LF pair is one line break, so it becomes one space.
func cleanLine(v string) string {
	v = strings.ReplaceAll(v, "\r\n", " ")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, v))
}

// dropText checks a drop link's title and message after cleanLine: the title is at most 100 characters and the message at
// most 1000.
func dropText(title, message string) (string, string, error) {
	title, message = cleanLine(title), cleanLine(message)
	if utf8.RuneCountInString(title) > dropTitleMax {
		return "", "", apierr.Newf(apierr.InvalidRequest, "title may be at most %d characters", dropTitleMax)
	}
	if utf8.RuneCountInString(message) > dropMessageMax {
		return "", "", apierr.Newf(apierr.InvalidRequest, "message may be at most %d characters", dropMessageMax)
	}
	return title, message, nil
}

// expiryField reads an expires_at field: absent (set nil, null false), null (null true), or a time (set). A
// drop link refuses null, since it must end.
func expiryField(raw json.RawMessage) (set *time.Time, null bool, err error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	if string(raw) == "null" {
		return nil, true, nil
	}
	var at time.Time
	if err := json.Unmarshal(raw, &at); err != nil {
		return nil, false, apierr.New(apierr.InvalidRequest, "expires_at must be a time, or null")
	}
	return &at, false, nil
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

// An anyone link must end. Its default end is 30 days from now and its longest is 365 days; sharing.max_expiry
// lowers both when it is set.
const (
	anyoneLinkDefault = 30 * 24 * time.Hour
	anyoneLinkMax     = 365 * 24 * time.Hour
)

// anyoneLimit is the longest an anyone link may run from now: 365 days, or sharing.max_expiry when that is lower.
func (s *Server) anyoneLimit() time.Duration {
	limit := anyoneLinkMax
	if max := s.cfg.Sharing.MaxExpiry.D(); max > 0 && max < limit {
		limit = max
	}
	return limit
}

// anyoneExpiry returns the end an anyone link gets. A nil at is the default; any other end is checked against
// the installation cap and the 365-day maximum.
func (s *Server) anyoneExpiry(at *time.Time, now time.Time) (*time.Time, error) {
	limit := s.anyoneLimit()
	if at == nil {
		end := now.Add(min(anyoneLinkDefault, limit))
		return &end, nil
	}
	if err := s.checkExpiry(at, now); err != nil {
		return nil, err
	}
	if at.After(now.Add(limit)) {
		secs := int64(limit / time.Second)
		return nil, apierr.Newf(apierr.InvalidRequest, "expires_at may be at most %d seconds from now", secs).
			With("max_expiry_seconds", secs)
	}
	return at, nil
}

// hashAnyonePassword checks a new password for an anyone link and returns its argon2id hash. An empty password
// means none, and returns "".
func hashAnyonePassword(ctx context.Context, password string) (string, error) {
	if password == "" {
		return "", nil
	}
	if err := auth.ValidPassword(password); err != nil {
		return "", apierr.New(apierr.InvalidRequest, err.Error())
	}
	return auth.HashPassword(ctx, password)
}

// publicLinksOff is the refusal for an anyone link while the account's switch is off.
func publicLinksOff() error {
	return apierr.New(apierr.Forbidden, "links that anyone can open are turned off for this account").
		With("reason", "public_links_off")
}

// managerRequired is the refusal for an anyone link made or changed by anyone but a manager of the node.
func managerRequired() error {
	return apierr.New(apierr.Forbidden, "only a manager of the item may make or change a link that anyone can open").
		With("reason", "manager_required")
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

// handleCreateShareLink makes the node's link. The caller shares the node (mayShare) and may not give a role
// above its own. A node has one access link and one drop link; a second of a kind is a conflict. An anyone link
// and a drop link need the manager of the node, the account's switch, and an end date. A drop link is for a
// directory, takes no role, and carries its title, message and visitor settings. The secret is returned here, once.
func (s *Server) handleCreateShareLink(w http.ResponseWriter, r *http.Request) error {
	p, err := shareLinkCaller(r)
	if err != nil {
		return err
	}
	var in struct {
		Audience   string          `json:"audience"`
		Role       string          `json:"role"`
		UploadOnly bool            `json:"upload_only"`
		ExpiresAt  json.RawMessage `json:"expires_at"`
		Password   string          `json:"password"`
		Title      string          `json:"title"`
		Message    string          `json:"message"`
		AskName    bool            `json:"ask_name"`
		Notify     bool            `json:"notify"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.UploadOnly && in.Audience == "" {
		in.Audience = store.AudienceAnyone
	}
	if err := checkLinkShape(in.Audience, in.UploadOnly); err != nil {
		return err
	}
	now := time.Now()
	anyone := in.Audience == store.AudienceAnyone
	expires, expiryNull, err := expiryField(in.ExpiresAt)
	if err != nil {
		return err
	}
	title, message, err := dropText(in.Title, in.Message)
	if err != nil {
		return err
	}
	if !in.UploadOnly && (title != "" || message != "" || in.AskName || in.Notify) {
		return apierr.New(apierr.InvalidRequest, "title, message, ask_name and notify belong to a drop link")
	}
	if in.UploadOnly {
		if in.Role != "" {
			return apierr.New(apierr.InvalidRequest, "a drop link gives no role; it takes uploads only")
		}
		if expiryNull {
			return apierr.New(apierr.InvalidRequest, "a drop link needs an end date")
		}
	} else {
		if in.Role == "" {
			in.Role = store.RoleViewer
		}
		if err := checkLinkRole(in.Role); err != nil {
			return err
		}
	}
	if anyone {
		if !in.UploadOnly && in.Role != store.RoleViewer {
			return apierr.New(apierr.InvalidRequest, "a link that anyone can open gives the viewer role only")
		}
		if expires, err = s.anyoneExpiry(expires, now); err != nil {
			return err
		}
	} else {
		if in.Password != "" {
			return apierr.New(apierr.InvalidRequest, "only a link that anyone can open has a password")
		}
		if err := s.checkExpiry(expires, now); err != nil {
			return err
		}
	}
	passwordHash, err := hashAnyonePassword(r.Context(), in.Password)
	if err != nil {
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
		if anyone && own != store.RoleManager {
			return managerRequired()
		}
		if in.UploadOnly && n.Kind != store.KindDir {
			return apierr.New(apierr.InvalidRequest, "a drop link is for a directory")
		}
		if !in.UploadOnly && store.RoleRank(in.Role) > store.RoleRank(own) {
			return apierr.New(apierr.Forbidden, "you may not give a link a role above your own")
		}
		f, err := tx.Folder(ctx, n.AccountID, n.FolderID)
		if err != nil {
			return err
		}
		if anyone {
			on, err := tx.SharingPolicy(ctx, n.AccountID)
			if err != nil {
				return err
			}
			if !on {
				return publicLinksOff()
			}
		}
		if err := linkPolicyRefusal(f, n); err != nil {
			return err
		}
		var hash []byte
		secret, hash = auth.NewToken()
		link = &store.ShareLink{ID: id.New("shl"), AccountID: n.AccountID, NodeID: n.ID,
			Audience: in.Audience, UploadOnly: in.UploadOnly, SecretHash: hash, PasswordHash: passwordHash,
			ExpiresAt: expires, CredentialsAt: now, CreatedBy: p.ID, CreatedKind: creatorKind(p), CreatedAt: now,
			UpdatedBy: p.ID, UpdatedKind: creatorKind(p), UpdatedAt: now, Title: title, Message: message,
			AskName: in.AskName, Notify: in.Notify}
		if !in.UploadOnly {
			link.Role = in.Role
		}
		if err := tx.InsertShareLink(ctx, link); errors.Is(err, store.ErrConflict) {
			if in.UploadOnly {
				return apierr.New(apierr.Conflict, "this item already has a drop link; change that one instead").
					With("reason", "exists")
			}
			return apierr.New(apierr.Conflict, "this item already has a link; change that one instead").
				With("reason", "exists")
		} else if err != nil {
			return err
		}
		details := linkDetails(link)
		if anyone {
			details["password_set"] = passwordHash != ""
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

// linkDetails is what a link's audit entry says about it: its audience, role, upload_only, its end, and for a drop
// link its title and the switches. Never the secret, the password, or an uploader's email.
func linkDetails(l *store.ShareLink) map[string]any {
	details := map[string]any{"audience": l.Audience, "upload_only": l.UploadOnly}
	if l.Role != "" {
		details["role"] = l.Role
	}
	if l.ExpiresAt != nil {
		details["expires_at"] = l.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if l.UploadOnly {
		details["title"] = l.Title
		details["message_set"] = l.Message != ""
		details["ask_name"] = l.AskName
		details["notify"] = l.Notify
	}
	return details
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
			// A drop link is shown to the people who manage it only: a member learns nothing of it.
			if v.Paused || v.UploadOnly {
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
// account and its node reached (not_found otherwise). The caller must share that node (forbidden otherwise),
// hold at least the link's role, and, for an anyone link, be the node's manager with the account's switch on.
// Policy must allow links now (links_off or members_only).
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
	if l.Audience == store.AudienceAnyone {
		if own != store.RoleManager {
			return nil, nil, "", managerRequired()
		}
		on, err := tx.SharingPolicy(ctx, n.AccountID)
		if err != nil {
			return nil, nil, "", err
		}
		if !on {
			return nil, nil, "", publicLinksOff()
		}
	}
	return l, n, own, nil
}

// handleUpdateShareLink changes a link: its audience, role, expiry or password. An absent field keeps its value;
// expires_at null clears the expiry, which an anyone link refuses. Switching to anyone gives the viewer role and,
// when the link has none, an end date; switching to organisation clears the password and ends the anyone visitors.
// A new password, or a removed one, ends the visitors' credentials issued before it. A drop link keeps its audience
// and has no role; its title, message, ask_name and notify may change, and it must keep an end date.
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
		Password   *string         `json:"password"`
		Title      *string         `json:"title"`
		Message    *string         `json:"message"`
		AskName    *bool           `json:"ask_name"`
		Notify     *bool           `json:"notify"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Audience != nil {
		if err := checkLinkShape(*in.Audience, false); err != nil {
			return err
		}
	}
	if in.Role != nil {
		if err := checkLinkRole(*in.Role); err != nil {
			return err
		}
	}
	expires, clearExpiry, err := expiryField(in.ExpiresAt)
	if err != nil {
		return err
	}
	var title, message string
	if in.Title != nil || in.Message != nil {
		t, m := "", ""
		if in.Title != nil {
			t = *in.Title
		}
		if in.Message != nil {
			m = *in.Message
		}
		if title, message, err = dropText(t, m); err != nil {
			return err
		}
	}
	ctx := r.Context()
	newPassword := in.Password != nil
	passwordHash := ""
	if newPassword {
		if passwordHash, err = hashAnyonePassword(ctx, *in.Password); err != nil {
			return err
		}
	}
	linkID := r.PathValue("shl")
	if !id.Valid("shl", linkID) {
		return notFound("link")
	}
	now := time.Now()
	var (
		out      *store.ShareLink
		released []*store.Upload
	)
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		l, n, own, err := changeShareLink(ctx, tx, p, linkID)
		if err != nil {
			return err
		}
		if in.UploadOnly != nil && *in.UploadOnly != l.UploadOnly {
			return apierr.New(apierr.InvalidRequest, "a link cannot change between a drop link and an access link")
		}
		if l.UploadOnly {
			if (in.Audience != nil && *in.Audience != store.AudienceAnyone) || in.Role != nil {
				return apierr.New(apierr.InvalidRequest, "a drop link keeps its audience and has no role")
			}
		} else if in.Title != nil || in.Message != nil || in.AskName != nil || in.Notify != nil {
			return apierr.New(apierr.InvalidRequest, "title, message, ask_name and notify belong to a drop link")
		}
		orig := l.Audience
		final := orig
		if in.Audience != nil {
			final = *in.Audience
		}
		details := map[string]any{}
		if in.Role != nil {
			if store.RoleRank(*in.Role) > store.RoleRank(own) {
				return apierr.New(apierr.Forbidden, "you may not give a link a role above your own")
			}
		}
		anyone := final == store.AudienceAnyone
		if anyone {
			if own != store.RoleManager {
				return managerRequired()
			}
			on, err := tx.SharingPolicy(ctx, n.AccountID)
			if err != nil {
				return err
			}
			if !on {
				return publicLinksOff()
			}
			if in.Role != nil && *in.Role != store.RoleViewer {
				return apierr.New(apierr.InvalidRequest, "a link that anyone can open gives the viewer role only")
			}
			if clearExpiry {
				return apierr.New(apierr.InvalidRequest, "a link that anyone can open needs an end date")
			}
			if expires != nil {
				if expires, err = s.anyoneExpiry(expires, now); err != nil {
					return err
				}
				l.ExpiresAt = expires
				details["expires_at"] = expires.UTC().Format(time.RFC3339)
			} else if l.ExpiresAt == nil {
				end, err := s.anyoneExpiry(nil, now)
				if err != nil {
					return err
				}
				l.ExpiresAt = end
				details["expires_at"] = end.UTC().Format(time.RFC3339)
			} else if limit := s.anyoneLimit(); final != orig && l.ExpiresAt.After(now.Add(limit)) {
				// A link switched to anyone keeps no end past the anyone cap: an end set while it was an organisation
				// link is pulled back to the cap.
				capped := now.Add(limit)
				l.ExpiresAt = &capped
				details["expires_at"] = capped.UTC().Format(time.RFC3339)
			}
		} else {
			if newPassword && *in.Password != "" {
				return apierr.New(apierr.InvalidRequest, "only a link that anyone can open has a password")
			}
			if clearExpiry {
				l.ExpiresAt = nil
				details["expires_at"] = nil
			} else if expires != nil {
				if err := s.checkExpiry(expires, now); err != nil {
					return err
				}
				l.ExpiresAt = expires
				details["expires_at"] = expires.UTC().Format(time.RFC3339)
			}
		}
		if in.Role != nil {
			l.Role = *in.Role
			details["role"] = l.Role
		}
		if l.UploadOnly {
			if in.Title != nil {
				l.Title = title
				details["title"] = title
			}
			if in.Message != nil {
				l.Message = message
				details["message_set"] = message != ""
			}
			if in.AskName != nil {
				l.AskName = *in.AskName
				details["ask_name"] = l.AskName
			}
			if in.Notify != nil {
				l.Notify = *in.Notify
				details["notify"] = l.Notify
			}
		}
		switch {
		case final != orig:
			// A change of audience ends every visitor and every earlier copy's credentials, and the visits of
			// the old audience. The secret stays the same.
			l.Audience = final
			l.CredentialsAt = now
			details["audience"] = final
			if anyone {
				l.Role = store.RoleViewer
				if err := tx.DeleteShareLinkVisits(ctx, l.ID); err != nil {
					return err
				}
			}
			l.PasswordHash = ""
			if anyone {
				l.PasswordHash = passwordHash
			}
		case anyone && newPassword:
			// A new password, or none, ends the visitors' credentials issued before it, and with them a drop
			// link's open uploads.
			l.PasswordHash = passwordHash
			l.CredentialsAt = now
			ups, err := s.releaseDropUploads(r, tx, l)
			if err != nil {
				return err
			}
			released = ups
		}
		if anyone {
			details["password_set"] = l.PasswordHash != ""
		}
		if l.UploadOnly {
			details["upload_only"] = true
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
	for _, up := range released {
		s.discardUploadFile(up)
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
		out      *store.ShareLink
		secret   string
		released []*store.Upload
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
		// The old credentials die with the reset, so the visitors' open uploads die with them.
		ups, err := s.releaseDropUploads(r, tx, l)
		if err != nil {
			return err
		}
		released = ups
		out = l
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "link.reset", l.ID,
			map[string]any{"upload_only": l.UploadOnly}, n.FolderID))
	})
	if err != nil {
		return err
	}
	for _, up := range released {
		s.discardUploadFile(up)
	}
	writeJSON(w, http.StatusCreated, shareLinkCreated{
		Link: toShareLinkJSON(out, &store.ShareLinkView{ShareLink: *out}, out.NodeID, true), URL: s.shareURL(secret)})
	return nil
}

// handleDeleteShareLink removes a link at once: its visits go with it, so its members lose the access
// that came through it. The link's creator may always delete it, as long as it still reaches the node; anyone
// else must share the node, and an anyone link needs the node's manager. A link above the caller's own role is
// not removed by it.
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
	var released []*store.Upload
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		l, err := tx.ShareLink(ctx, p.AccountID, linkID)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("link")
		}
		if err != nil {
			return err
		}
		var n *store.Node
		if p.ManagesAccount(p.AccountID) {
			// An owner session or the account key may delete any link of the account, including one on a member's
			// My files, which the owner cannot reach (decision 11, 2026-10-29). The node is read, not reached, so a
			// link on a trashed item can still be removed from the list.
			if n, err = tx.Node(ctx, p.AccountID, l.NodeID); err != nil {
				return err
			}
		} else {
			var own string
			n, own, err = nodeSharingAccess(ctx, tx, p, l.NodeID)
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
				if l.Audience == store.AudienceAnyone && own != store.RoleManager {
					return managerRequired()
				}
			}
		}
		if err := tx.DeleteShareLink(ctx, p.AccountID, l.ID); err != nil {
			return err
		}
		ups, err := s.releaseDropUploads(r, tx, l)
		if err != nil {
			return err
		}
		released = ups
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "link.delete", l.ID,
			map[string]any{"audience": l.Audience, "role": l.Role, "upload_only": l.UploadOnly}, n.FolderID))
	})
	if err != nil {
		return err
	}
	for _, up := range released {
		s.discardUploadFile(up)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// shareOpenAuditGap is how long a member's last visit to a link must be before its next open is audited again.
const shareOpenAuditGap = 24 * time.Hour

// handleOpenShareLink opens a link. An anyone link opens for whoever holds its secret, and its password when it
// has one (openAnyoneLink); no session is read for it. An organisation link opens for a signed-in member of the
// link's account: it records the visit and answers the node. Every other caller, and every dead link, gets the
// same not_found: a link that is missing, expired, deleted, paused, trashed, of another account, or opened by a
// token, an admin or someone with no session. The session check is the cookie's own, with its origin and CSRF
// checks, so the answer comes from here and not from the wrap's 401.
func (s *Server) handleOpenShareLink(w http.ResponseWriter, r *http.Request) error {
	dead := notFound("link")
	if err := s.rateLimit("share.open.ip", addressKey(r)); err != nil {
		return err
	}
	// A credential in the header is a key or a token, never a browser session: refuse it as dead.
	if r.Header.Get("Authorization") != "" {
		return dead
	}
	var in struct {
		Secret   string `json:"secret"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if !auth.ValidToken(in.Secret) {
		return dead
	}
	ctx := r.Context()
	hash := auth.HashToken(in.Secret)
	early, err := s.store.ShareLinkStateBySecretHash(ctx, hash)
	if errors.Is(err, store.ErrNotFound) {
		return dead
	}
	if err != nil {
		return err
	}
	if early.Link.Audience == store.AudienceAnyone {
		return s.openAnyoneLink(w, r, early, hash, in.Password)
	}
	p, _, err := s.authenticateSession(r)
	if err != nil || p == nil || p.Kind != auth.KindUser || p.IsAdmin() || p.UserID == "" {
		return dead
	}
	// The per-member limit sits beside the per-address one: a member behind a shared address keeps its own budget.
	if err := s.rateLimit("share.open.user", p.UserID); err != nil {
		return err
	}
	// The early read refuses a stranger cheaply. The write re-reads the link by ID and checks it again, so
	// a link deleted or reset between the two gives the same dead answer.
	l := &early.Link
	if l.AccountID != p.AccountID || l.Audience != store.AudienceOrganisation {
		return dead
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
		// in the same millisecond as the link's credentials is stamped one millisecond later. Times are stored in
		// whole milliseconds, so the comparison is made on the stored value, not on the nanoseconds of now.
		visitAt := time.UnixMilli(now.UnixMilli())
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
	n, err := s.store.CountAnyoneLinks(r.Context(), acc.ID, time.Now())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, sharingPolicyJSON{PublicLinks: on, AnyoneLinks: n})
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
	n, err := s.store.CountAnyoneLinks(r.Context(), acc.ID, time.Now())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, sharingPolicyJSON{PublicLinks: *in.PublicLinks, AnyoneLinks: n})
	return nil
}
