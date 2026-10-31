// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Office editing through WOPI (design section 10, decision 31). Two parts: the route that opens an editor for a
// person, and the WOPI host that Collabora Online calls while the editor is open. The lock operations and the save
// that honours them are in office_locks.go (decision 32).
//
// A WOPI request is checked in this order, and each refusal is answered as the WOPI specification says: the
// proof-key signature and its timestamp (500 when they fail), the access token under the WOPI key (401), the token's
// editing session (401), the token's file against the path (404), and the role the person holds on the file now
// (404 when it is too low for the operation, which the specification calls "user unauthorized").

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/wopi"
)

// wopiTimeFormat is the LastModifiedTime format of CheckFileInfo: UTC with milliseconds.
const wopiTimeFormat = "2006-01-02T15:04:05.000Z"

// officeRetryAfter is the Retry-After, in seconds, of an open that finds no discovery document.
const officeRetryAfter = "60"

var (
	// errBadProof is a request whose proof-key headers or timestamp do not verify. A forged request is a client's
	// fault, so it is logged as a warning, never as an error.
	errBadProof = errors.New("wopi: the proof of the request is not valid")
	// errNoDiscovery is a WOPI request that cannot be verified because no discovery document is cached.
	errNoDiscovery = errors.New("wopi: no discovery document is available to verify the request")
	// errNotImplemented is what a WOPI operation answers that this slice does not implement.
	errNotImplemented = errors.New("wopi: this operation is not implemented yet")
	// errTooLargeForCaller is a GetFile whose file is larger than the X-WOPI-MaxExpectedSize the caller sent: 412.
	errTooLargeForCaller = errors.New("wopi: the file is larger than X-WOPI-MaxExpectedSize")
)

// officeJSON is the answer to POST .../office. The token is shown here and nowhere else.
type officeJSON struct {
	URL       string    `json:"url"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// wopiBase is the address Collabora calls Selfcloud at: office.wopi_url when it is set, else the origin of
// public_url. The proof-key check signs URLs built from it, so it must match the address Collabora uses.
func (s *Server) wopiBase() string {
	if s.cfg.Office.WOPIURL != "" {
		return strings.TrimSuffix(s.cfg.Office.WOPIURL, "/")
	}
	return s.origin
}

// wopiFileURL is the WOPI address of one file: WOPISrc in the editor's URL, and the base of every WOPI call.
func (s *Server) wopiFileURL(nodeID string) string {
	return s.wopiBase() + "/wopi/files/" + nodeID
}

// unquoteETag gives a file's ETag without its quotes, which is the form WOPI's Version and X-WOPI-ItemVersion use.
func unquoteETag(etag string) string {
	return strings.Trim(etag, `"`)
}

// handleOpenOffice opens a file in the editor for the caller. It answers the editor's URL, with WOPISrc set to
// the file's WOPI address, and an access token the client posts into the editor's form. The token is made here,
// bound to this one file, this person and this editing session, and it ends at the session's end or at the
// token_minutes limit, whichever is first. It never outlives the delegated token that made the request.
//
// Only a person opens the editor: an account key or an installation admin has no person to attribute the edits to.
// The edit mode needs the editor role on the file; the view mode needs the viewer role and gives a read-only token.
// The no_download rule does not refuse an open: it binds a viewer, whose editor disables print, export and copy, and
// it does not bind an editor, who may save and download (decision 20 and restrict.go).
func (s *Server) handleOpenOffice(w http.ResponseWriter, r *http.Request) error {
	if s.office == nil {
		return notFound("node")
	}
	p := principalOf(r)
	if p == nil || p.Kind != auth.KindUser {
		return apierr.New(apierr.Forbidden, "the editor opens for a signed-in person: use a session, or a token that acts as a user")
	}
	if err := s.rateLimit("office.open", p.ID); err != nil {
		return err
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Mode != "edit" && in.Mode != "view" {
		return apierr.New(apierr.InvalidRequest, "mode must be edit or view")
	}
	n, role, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	if n.Kind != store.KindFile {
		return apierr.New(apierr.InvalidRequest, "a folder cannot be opened in the editor")
	}
	if n.Size > int64(s.cfg.Office.MaxFileBytes) {
		return apierr.Newf(apierr.TooLarge, "a file opened in the editor may be at most %d bytes", int64(s.cfg.Office.MaxFileBytes)).
			With("limit_bytes", int64(s.cfg.Office.MaxFileBytes))
	}
	ctx := r.Context()
	doc, err := s.office.Get(ctx)
	if err != nil {
		w.Header().Set("Retry-After", officeRetryAfter)
		return apierr.New(apierr.Unavailable, "the editor is not reachable right now; try again later")
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(n.Name)), ".")
	// A view opens through the edit action when discovery lists no view action for the type. The token is still
	// read-only, and CheckFileInfo says so, so the editor shows the file without saving it.
	action := in.Mode
	if action == "view" && !doc.Openable(ext, "view") {
		action = "edit"
	}
	if !doc.Openable(ext, action) {
		return apierr.New(apierr.InvalidRequest, "this file type does not open in the editor in this mode")
	}
	write := in.Mode == "edit"
	minRole := store.RoleViewer
	if write {
		minRole = store.RoleEditor
		if store.RoleRank(role) < store.RoleRank(minRole) {
			return apierr.New(apierr.Forbidden, "editing needs the editor role on the file")
		}
	}

	now := s.store.Clock()
	expires := now.Add(time.Duration(s.cfg.Office.TokenMinutes) * time.Minute)
	minted := ""
	if p.Token != nil {
		minted = p.Token.MintedBy
		if p.Token.ExpiresAt.Before(expires) {
			expires = p.Token.ExpiresAt
		}
	}
	// A browser session that opens the editor is recorded, so the editor ends with it (authenticateWOPI). A delegated
	// token has no browser session; it ends with its own key, as before.
	var browser []byte
	if p.Token == nil {
		bs := sessionOf(r)
		if bs == nil {
			return apierr.New(apierr.Unauthenticated, "the editor opens from a signed-in session or a token that acts as a user")
		}
		browser = bs.IDHash
	}
	sid := id.New("wps")
	pl := tokenPayload{
		Version:    tokenVersion,
		KeyID:      s.wopiTokens.current.kid,
		AccountID:  n.AccountID,
		UserID:     p.UserID,
		MintedBy:   minted,
		Folders:    []string{n.FolderID},
		Node:       n.ID,
		Permission: permRead,
		Audience:   wopiAudience,
		IssuedAt:   now.UnixMilli(),
		ExpiresAt:  expires.UnixMilli(),
		Session:    sid,
	}
	if write {
		pl.Permission = permWrite
	}
	token, err := s.wopiTokens.sign(pl)
	if err != nil {
		return err
	}
	editorURL, ok := doc.EditorURL(ext, action, s.wopiFileURL(n.ID))
	if !ok {
		return apierr.New(apierr.Internal, "the editor action for this file type is missing")
	}

	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := requireNodeRole(ctx, tx, p, n.ID, minRole)
		if err != nil {
			return err
		}
		// The editor is given the file as it is now: the session's tag is its content tag, which Lock compares.
		sess := &store.WOPISession{ID: sid, AccountID: n.AccountID, NodeID: n.ID, UserID: p.UserID,
			Role: "read", IssuedAt: now, ExpiresAt: expires, BrowserSession: browser, ServedTag: cur.ContentTag}
		if write {
			sess.Role = "write"
		}
		if err := tx.InsertWOPISession(ctx, sess); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "node.office", n.ID,
			map[string]any{"mode": in.Mode, "session": sid}, n.FolderID))
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, officeJSON{URL: editorURL, Token: token, ExpiresAt: expires.UTC()})
	return nil
}

// wopiGrant is a WOPI request that has passed its checks: the caller, the file the token names, and the role the
// caller holds on the file now.
type wopiGrant struct {
	p    *auth.Principal
	node *store.Node
	role string
	// restricted is the no-download rule for this caller on this file (downloadRestricted): it binds a viewer or a
	// commenter, not an editor, the same as for downloads. A restricted grant has print, export and copy disabled.
	restricted bool
	// token is the access token the request carried, checked again inside a write (PutFile, the lock operations).
	token string
	// session is the editing session the token names: the row whose content tag Lock and PutFile keep.
	session string
}

// writable reports whether the grant may change the file: the role is editor or more. The no-download rule does not
// change that, as it does not for an editor's download (decision 20 and restrict.go). A grant that may not write is
// read-only, for every WOPI operation that changes the file.
func (g *wopiGrant) writable() bool {
	return store.RoleRank(g.role) >= store.RoleRank(store.RoleEditor)
}

// wopiRequest runs the checks every WOPI operation shares, in the order given at the top of this file.
func (s *Server) wopiRequest(w http.ResponseWriter, r *http.Request) (*wopiGrant, error) {
	if s.office == nil {
		return nil, notFound("node")
	}
	ctx := r.Context()
	nod := r.PathValue("nod")
	if !id.Valid("nod", nod) {
		return nil, nodeNotFound()
	}
	token := r.URL.Query().Get("access_token")
	now := s.store.Clock()
	doc, err := s.office.Get(ctx)
	if err != nil {
		return nil, errNoDiscovery
	}
	ts, err := strconv.ParseInt(r.Header.Get(wopi.HeaderTimeStamp), 10, 64)
	if err != nil || !wopi.TimestampFresh(ts, now) {
		return nil, errBadProof
	}
	signed := s.wopiBase() + r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		signed += "?" + r.URL.RawQuery
	}
	proof, proofOld := r.Header.Get(wopi.HeaderProof), r.Header.Get(wopi.HeaderProofOld)
	if !doc.VerifyProof(token, signed, ts, proof, proofOld) {
		// The key may have been rotated since the last fetch. One fetch at most per RetryGap, so forged requests
		// cannot make Selfcloud fetch discovery over and over.
		doc, err = s.office.Retry(ctx)
		if err != nil || !doc.VerifyProof(token, signed, ts, proof, proofOld) {
			return nil, errBadProof
		}
	}
	p, pl, err := s.authenticateWOPI(ctx, s.store, token, now)
	if err != nil {
		return nil, err
	}
	if pl.Node != nod {
		return nil, nodeNotFound()
	}
	if info := infoFrom(ctx); info != nil {
		info.principal = p
	}
	n, role, err := nodeAccessWith(ctx, s.store, p, nod)
	if err != nil {
		return nil, err
	}
	restricted, err := s.downloadRestricted(ctx, n, role)
	if err != nil {
		return nil, err
	}
	return &wopiGrant{p: p, node: n, role: role, restricted: restricted, token: token, session: pl.Session}, nil
}

// wopiStatus is the HTTP status a WOPI request answers for err. The specification's codes are used where it gives
// one; a quota refusal answers 413 because the specification has no code for it (decision 31 says so).
func wopiStatus(err error) int {
	var le *store.LockedError
	if errors.As(err, &le) {
		return http.StatusConflict
	}
	if errors.Is(err, errBadProof) || errors.Is(err, errNoDiscovery) {
		return http.StatusInternalServerError
	}
	if errors.Is(err, errNotImplemented) {
		return http.StatusNotImplemented
	}
	if errors.Is(err, errTooLargeForCaller) {
		return http.StatusPreconditionFailed
	}
	e := apierr.From(err)
	switch e.Code {
	case apierr.InvalidRequest:
		return http.StatusBadRequest
	case apierr.Unauthenticated, apierr.TokenExpired:
		return http.StatusUnauthorized
	case apierr.NotFound, apierr.Forbidden:
		return http.StatusNotFound
	case apierr.TooLarge, apierr.QuotaExceeded:
		return http.StatusRequestEntityTooLarge
	case apierr.Conflict, apierr.Locked:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// wopiFail answers a WOPI request that failed. The body is empty, as the specification's error answers are, and no
// token is logged: the log line names the route's request ID and the reason only.
func (s *Server) wopiFail(w http.ResponseWriter, r *http.Request, err error) {
	status := wopiStatus(err)
	code := apierr.From(err).Code
	var le *store.LockedError
	locked := errors.As(err, &le)
	// Every refusal the client caused is a client fault in the access log, whatever its status: a 501 for an operation
	// this slice does not have, a lock refused by another editor, or a 404 that is an answer, is not a server error.
	// Only an internal failure is one.
	clientFault := locked || code != apierr.Internal || errors.Is(err, errBadProof) || errors.Is(err, errNoDiscovery) ||
		errors.Is(err, errNotImplemented) || errors.Is(err, errTooLargeForCaller)
	if info := infoFrom(r.Context()); info != nil && clientFault {
		info.clientFault = true
	}
	switch {
	case errors.Is(err, errBadProof), errors.Is(err, errNoDiscovery):
		s.log.Warn("wopi request refused", "request_id", requestID(r), "reason", err.Error())
	case code == apierr.Internal && !errors.Is(err, errNotImplemented) && !locked:
		s.log.Error("wopi request failed", "request_id", requestID(r), "error", err)
	}
	w.Header().Set("Cache-Control", "no-store")
	if locked {
		// A 409 always carries X-WOPI-Lock: the lock on the file, or "" when no editor holds it. The reason is one of
		// the fixed texts in the store, and names no lock.
		w.Header().Set("X-WOPI-Lock", le.Current)
		if le.Reason != "" {
			w.Header().Set("X-WOPI-LockFailureReason", le.Reason)
		}
	}
	w.WriteHeader(status)
}

// handleWOPICheckFileInfo answers CheckFileInfo: what the editor shows, and what it may do. UserCanWrite and
// ReadOnly follow the grant; the three Disable properties follow no_download, which the proposal's read-only rule
// sets. SupportsLocks and SupportsGetLock are true: the lock operations are in office_locks.go.
func (s *Server) handleWOPICheckFileInfo(w http.ResponseWriter, r *http.Request) error {
	g, err := s.wopiRequest(w, r)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	n := g.node
	write := g.writable()
	user := g.p.UserID
	if user == "" {
		user = g.p.ID
	}
	// UserFriendlyName is what other editors see beside this person's edits, so it is the display name, or a neutral
	// word when there is none. It is never the ID or the email.
	friendly := "User"
	if g.p.UserID != "" {
		names, err := s.store.UserNames(r.Context(), n.AccountID, []string{g.p.UserID})
		if err != nil {
			s.wopiFail(w, r, err)
			return nil
		}
		if v := names[g.p.UserID]; v != "" {
			friendly = v
		}
	}
	info := map[string]any{
		"BaseFileName":            n.Name,
		"OwnerId":                 n.OwnerID,
		"Size":                    n.Size,
		"UserId":                  user,
		"UserFriendlyName":        friendly,
		"Version":                 unquoteETag(n.ETag),
		"UserCanWrite":            write,
		"ReadOnly":                !write,
		"UserCanNotWriteRelative": true,
		"SupportsUpdate":          true,
		"SupportsLocks":           true,
		"SupportsGetLock":         true,
		"DisablePrint":            g.restricted,
		"DisableExport":           g.restricted,
		"DisableCopy":             g.restricted,
		"LastModifiedTime":        n.ModifiedAt.UTC().Format(wopiTimeFormat),
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	return json.NewEncoder(w).Encode(info)
}

// handleWOPIGetFile streams the file's content. The body is copied from the blob's file in pieces, so no part of it
// is held in memory, whatever its size; the length is checked against the record before anything is sent.
func (s *Server) handleWOPIGetFile(w http.ResponseWriter, r *http.Request) error {
	g, err := s.wopiRequest(w, r)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	n := g.node
	if n.Kind != store.KindFile {
		s.wopiFail(w, r, notFound("node"))
		return nil
	}
	f, err := s.blobs.Open(n.AccountID, n.BlobID)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() != n.Size {
		s.wopiFail(w, r, errors.New("wopi: the stored file does not match its record"))
		return nil
	}
	// The caller may cap the size it is ready to take. A file over the cap is refused before any byte is sent.
	if v := r.Header.Get("X-WOPI-MaxExpectedSize"); v != "" {
		limit, err := strconv.ParseInt(v, 10, 64)
		if err != nil || limit < 0 {
			s.wopiFail(w, r, apierr.New(apierr.InvalidRequest, "X-WOPI-MaxExpectedSize must be a whole number of bytes"))
			return nil
		}
		if n.Size > limit {
			s.wopiFail(w, r, errTooLargeForCaller)
			return nil
		}
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.FormatInt(n.Size, 10))
	h.Set("Cache-Control", "no-store")
	h.Set("X-WOPI-ItemVersion", unquoteETag(n.ETag))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		// The status is sent; a copy that stops here is a client that went away.
		s.log.Warn("wopi file send stopped", "request_id", requestID(r), "error", err)
	}
	return nil
}

// precheckSaveQuota refuses a save of size bytes that the account's quota would refuse, before the body is read.
func (s *Server) precheckSaveQuota(ctx context.Context, p *auth.Principal, accountID string, size int64) error {
	acc, err := s.store.Account(ctx, accountID)
	if err != nil {
		return err
	}
	return s.checkQuota(p, accountID, acc, store.UsageChange{Bytes: size})
}
