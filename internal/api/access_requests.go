// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// requestableRoles are the roles a user may ask for. Manager is not among them: managing is granted, never
// asked for.
var requestableRoles = []string{store.RoleViewer, store.RoleCommenter, store.RoleEditor}

// recentRequestWindow is how long after a request on a node the same user's next request there is recorded
// without mail. It stops a user re-asking in a loop from mailing the managers each time.
const recentRequestWindow = 24 * time.Hour

// maxPendingScan bounds how many pending requests of an account one listing reads. A listing answers the
// requests on nodes the caller may manage among those, so it reads the oldest of the account's pending
// requests up to this many, and no more.
const maxPendingScan = 5000

// accessRequestJSON is an access request. UserName is the requester's display name or email, shown to
// those who may decide the request.
type accessRequestJSON struct {
	ID        string     `json:"id"`
	NodeID    string     `json:"node_id"`
	UserID    string     `json:"user_id"`
	UserName  string     `json:"user_name,omitempty"`
	Role      string     `json:"role"`
	Message   string     `json:"message"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at"`
	DecidedBy string     `json:"decided_by"`
}

func (s *Server) toAccessRequestJSON(ctx context.Context, accountID string, ar *store.AccessRequest) accessRequestJSON {
	out := accessRequestJSON{ID: ar.ID, NodeID: ar.NodeID, UserID: ar.UserID, Role: ar.Role, Message: ar.Message,
		Status: ar.Status, CreatedAt: ar.CreatedAt, DecidedAt: ar.DecidedAt, DecidedBy: ar.DecidedBy}
	out.UserName, _ = s.subjectDetails(ctx, accountID, store.SubjectUser, ar.UserID)
	return out
}

// validRequestRole checks a role asked for in a request.
func validRequestRole(role string) error {
	for _, r := range requestableRoles {
		if role == r {
			return nil
		}
	}
	return apierr.New(apierr.InvalidRequest, "role must be viewer, commenter or editor")
}

// handleCreateAccessRequest asks for a role on a node. It answers 202 with no body, whether or not the
// node exists in the caller's account, and whether or not the caller can reach it. Otherwise a caller
// could find out which nodes exist. The request is recorded only when the node exists there and the
// caller's role is below the one asked for; otherwise nothing is recorded and nothing says so.
//
// A request that repeats a pending one collapses into it. Managers of the node's team space, or the
// owner of its personal folder, are mailed when a new request is recorded. Only a user asks: an account
// key holds manager everywhere and never needs to.
func (s *Server) handleCreateAccessRequest(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if p == nil {
		return apierr.New(apierr.Unauthenticated, "credentials are required")
	}
	var in struct {
		Role    string `json:"role"`
		Message string `json:"message"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := validRequestRole(in.Role); err != nil {
		return err
	}
	in.Message = cleanNote(in.Message)
	if utf8.RuneCountInString(in.Message) > maxMessageRunes {
		return apierr.Newf(apierr.InvalidRequest, "message may be at most %d characters", maxMessageRunes)
	}
	// Only a user asks for access, and never through a delegated token: a token's folders and node scope
	// do not reach a node it is asking about. Such a caller gets the same silent 202 as any other answer
	// that would otherwise reveal something about nodes.
	if p.Kind != auth.KindUser || p.Token != nil || p.IsAdmin() {
		w.WriteHeader(http.StatusAccepted)
		return nil
	}
	ctx := r.Context()
	if err := s.rateLimit("access.request", p.UserID); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	var created *store.AccessRequest
	var node *store.Node
	var folder *store.Folder
	mute := false
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if !id.Valid("nod", nodeID) {
			return nil
		}
		// The node must exist in the caller's own account. Reachability is not required: a user with no
		// role on the node is exactly who asks for one, and a node the caller cannot reach answers the same
		// as a missing one, so nothing is revealed.
		n, err := tx.Node(ctx, p.AccountID, nodeID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// A node in the trash does not exist for this purpose: nothing is recorded, as for a missing node.
		if self, above, err := tx.TrashState(ctx, n.AccountID, n.ID); err != nil {
			return err
		} else if self || above {
			return nil
		}
		f, err := tx.Folder(ctx, n.AccountID, n.FolderID)
		if err != nil {
			return err
		}
		u, err := tx.User(ctx, n.AccountID, p.UserID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && u.Status != store.UserActive) {
			return nil
		}
		if err != nil {
			return err
		}
		// A role held only through a share link is not counted: it ends with the link, so its holder may
		// still ask for a lasting one.
		role := implicitRole(f, u)
		rank, err := tx.PermissionRank(ctx, n.AccountID, n.ID, u.ID)
		if err != nil {
			return err
		}
		if rank > store.RoleRank(role) {
			role = store.RoleOfRank(rank)
		}
		if store.RoleRank(role) >= store.RoleRank(in.Role) {
			return nil // the caller already holds the role, or more
		}
		req := &store.AccessRequest{ID: id.New("arq"), AccountID: n.AccountID, NodeID: n.ID, UserID: u.ID,
			Role: in.Role, Message: in.Message, Status: store.RequestPending, CreatedAt: store.Now()}
		recent, err := tx.RecentAccessRequest(ctx, n.ID, u.ID, store.Now().Add(-recentRequestWindow))
		if err != nil {
			return err
		}
		stored, err := tx.InsertAccessRequest(ctx, req)
		if err != nil {
			return err
		}
		if stored.ID != req.ID {
			return nil // a repeat collapsed into the pending request: no new row, no audit entry, no mail
		}
		if err := tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "access.request", stored.ID,
			map[string]any{"node": n.ID, "role": in.Role}, n.FolderID)); err != nil {
			return err
		}
		created, node, folder = stored, n, f
		mute = recent
		return nil
	})
	if err != nil {
		return err
	}
	if created != nil && !mute {
		s.mailManagers(ctx, p, node, folder, created)
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

// mailManagers mails the managers of a request's node, in their own locales. A personal folder's request
// goes to its owner; a team space's goes to the managers of its space. A failed mail never fails the
// request, which is already recorded.
func (s *Server) mailManagers(ctx context.Context, p *auth.Principal, n *store.Node, f *store.Folder, req *store.AccessRequest) {
	if s.mailer == nil {
		return
	}
	var ids []string
	if f.Kind == store.KindPersonal {
		ids = []string{f.OwnerUserID}
	} else {
		var err error
		if ids, err = s.store.TeamManagerIDs(ctx, n.AccountID, f.ID); err != nil {
			s.log.Error("cannot list managers for an access request", "error", err)
			return
		}
	}
	var users []*store.User
	for _, uid := range ids {
		u, err := s.store.User(ctx, n.AccountID, uid)
		if err == nil && u.Status == store.UserActive {
			users = append(users, u)
		}
	}
	actor := mailText(s.actorName(ctx, p))
	item := mailText(itemName(n, f))
	link := s.nodeLink(n.ID)
	note := cleanNote(req.Message)
	// The requester's own bucket is charged once per request. Over its budget, the request mails nobody.
	if len(users) == 0 || s.requesterBudget(p.UserID) != nil {
		return
	}
	s.sendEach(ctx, p, users, s.requestBudget(n.AccountID), func(u *store.User) (string, string, error) {
		return s.templates.Render("access_request", u.Locale, shareData{Actor: actor, Item: item,
			Role: mail.RoleLabel(u.Locale, req.Role), Link: link, Message: note})
	})
}

// handleListNodeAccessRequests lists the requests on a node. The caller needs to be allowed to share the
// node (see mayShare). A delegated token gets forbidden, as it does for permissions.
func (s *Server) handleListNodeAccessRequests(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot list access requests")
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	n, role, err := nodeSharingAccess(r.Context(), s.store, p, nodeID)
	if err != nil {
		return err
	}
	ok, err := mayShare(r.Context(), s.store, n, role)
	if err != nil {
		return err
	}
	if !ok {
		return roleTooLow(store.RoleManager)
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	reqs, next, err := s.store.NodeAccessRequests(r.Context(), n.AccountID, n.ID, page)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[accessRequestJSON]{Items: []accessRequestJSON{}, NextCursor: next}
	for _, ar := range reqs {
		out.Items = append(out.Items, s.toAccessRequestJSON(r.Context(), n.AccountID, ar))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleListPendingAccessRequests lists the pending requests on nodes the caller may share. It reads the
// oldest pending requests of the account, up to maxPendingScan, and answers those it may decide, up to
// the page limit. A longer backlog is decided through the node's own list, which pages.
func (s *Server) handleListPendingAccessRequests(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot list access requests")
	}
	if err := s.rateLimit("access.list", string(p.Kind)+":"+p.ID); err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	scan, _, err := s.store.PendingAccessRequests(ctx, p.AccountID, store.Page{Limit: maxPendingScan})
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[accessRequestJSON]{Items: []accessRequestJSON{}}
	for _, ar := range scan {
		if len(out.Items) == page.Limit {
			break
		}
		n, role, err := nodeSharingAccess(ctx, s.store, p, ar.NodeID)
		if apierr.From(err).Code == apierr.NotFound {
			continue
		}
		if err != nil {
			return err
		}
		ok, err := mayShare(ctx, s.store, n, role)
		if err != nil {
			return err
		}
		if ok {
			out.Items = append(out.Items, s.toAccessRequestJSON(ctx, p.AccountID, ar))
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// decideAccessRequest is the work both decisions share: it finds the pending request and checks that the
// caller may share its node. The caller then grants or not, and closes the request. It returns the
// request, its node and folder, and the role the caller holds on the node, all read inside the transaction.
func decideAccessRequest(ctx context.Context, tx *store.Tx, p *auth.Principal, arqID string) (*store.AccessRequest, *store.Node, *store.Folder, string, error) {
	if !id.Valid("arq", arqID) {
		return nil, nil, nil, "", notFound("access request")
	}
	req, err := tx.AccessRequest(ctx, p.AccountID, arqID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, nil, "", notFound("access request")
	}
	if err != nil {
		return nil, nil, nil, "", err
	}
	n, role, err := nodeSharingAccess(ctx, tx, p, req.NodeID)
	if apierr.From(err).Code == apierr.NotFound {
		return nil, nil, nil, "", notFound("access request")
	}
	if err != nil {
		return nil, nil, nil, "", err
	}
	ok, err := mayShare(ctx, tx, n, role)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if !ok {
		return nil, nil, nil, "", roleTooLow(store.RoleManager)
	}
	if req.Status != store.RequestPending {
		return nil, nil, nil, "", apierr.New(apierr.Conflict, "the request was already decided")
	}
	f, err := tx.Folder(ctx, n.AccountID, n.FolderID)
	if err != nil {
		return nil, nil, nil, "", err
	}
	return req, n, f, role, nil
}

// handleApproveAccessRequest grants the requested role, or a role the caller names, which may not be above
// the caller's own. A grant only raises: a requester who already holds that role or more keeps it. It
// mails the requester.
func (s *Server) handleApproveAccessRequest(w http.ResponseWriter, r *http.Request) error {
	return s.decideRequest(w, r, true)
}

// handleDenyAccessRequest closes a request without granting anything, and mails the requester.
func (s *Server) handleDenyAccessRequest(w http.ResponseWriter, r *http.Request) error {
	return s.decideRequest(w, r, false)
}

func (s *Server) decideRequest(w http.ResponseWriter, r *http.Request, approve bool) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot decide access requests")
	}
	var in struct {
		Role string `json:"role"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &in); err != nil {
			return err
		}
	}
	if in.Role != "" {
		if err := validRequestRole(in.Role); err != nil {
			return err
		}
	}
	ctx := r.Context()
	var out *store.AccessRequest
	var node *store.Node
	var folder *store.Folder
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		req, n, f, _, err := decideAccessRequest(ctx, tx, p, r.PathValue("arq"))
		if err != nil {
			return err
		}
		node, folder = n, f
		action := "access.deny"
		status := store.RequestDenied
		if approve {
			action, status = "access.approve", store.RequestApproved
			grant := req.Role
			if in.Role != "" {
				grant = in.Role
			}
			if err := checkGrantRole(ctx, tx, p, n.ID, grant); err != nil {
				return err
			}
			if err := s.raiseRole(ctx, tx, p, n, f, req.UserID, grant, r); err != nil {
				return err
			}
			req.Role = grant
		}
		req.Status = status
		if err := tx.DecideAccessRequest(ctx, req, p.ID, store.Now()); err != nil {
			return err
		}
		out = req
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, action, req.ID,
			map[string]any{"node": n.ID, "user": req.UserID, "role": req.Role}, n.FolderID))
	})
	if err != nil {
		return err
	}
	s.mailDecision(ctx, p, node, folder, out)
	writeJSON(w, http.StatusOK, s.toAccessRequestJSON(ctx, out.AccountID, out))
	return nil
}

// raiseRole gives userID the role on n, unless it already holds that role or more there. The grant is an
// ordinary permission, audited as one. The requester must still be an active user of the account.
func (s *Server) raiseRole(ctx context.Context, tx *store.Tx, p *auth.Principal, n *store.Node, f *store.Folder,
	userID, role string, r *http.Request) error {
	u, err := tx.User(ctx, n.AccountID, userID)
	if errors.Is(err, store.ErrNotFound) {
		return apierr.New(apierr.Conflict, "the requester is no longer a user of the account")
	}
	if err != nil {
		return err
	}
	if u.Status != store.UserActive {
		return apierr.New(apierr.Conflict, "the requester is not an active user")
	}
	held, err := tx.PermissionRank(ctx, n.AccountID, n.ID, userID)
	if err != nil {
		return err
	}
	if held >= store.RoleRank(role) {
		return nil
	}
	// An approval is a grant like any other, so a members-only space refuses an outsider below its root,
	// with the same answer a direct grant gets.
	if err := checkMembersOnly(ctx, tx, f, n, store.SubjectUser, userID); err != nil {
		return err
	}
	pm := &store.Permission{NodeID: n.ID, AccountID: n.AccountID, SubjectKind: store.SubjectUser,
		SubjectID: userID, Role: role, CreatedAt: store.Now(), CreatedBy: p.ID}
	if err := tx.SetPermission(ctx, pm); err != nil {
		return err
	}
	return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "permission.set", n.ID,
		map[string]any{"subject_kind": store.SubjectUser, "subject_id": userID, "role": role, "by_request": true},
		n.FolderID))
}

// requestBudget charges access-request mail to its own buckets, not the verification buckets, so a burst
// of requests cannot use up the mail a user needs to sign in. Each account has a bucket, and so does each
// address the mail goes to.
func (s *Server) requestBudget(accountID string) func(*store.User) error {
	return func(u *store.User) error {
		return s.chargeAll(charge{"mail.requests.account", accountID}, charge{"mail.requests.target", emailKey(u.Email)})
	}
}

// requesterBudget charges the requesting user's own bucket once per request, before any of its mail goes
// out: three requests an hour are mailed in full, and a fourth is recorded but not mailed. So one user
// asking over and over cannot use up the account's bucket and silence the others.
func (s *Server) requesterBudget(requesterID string) error {
	return s.chargeAll(charge{"mail.requests.requester", requesterID})
}

// mailDecision tells the requester the outcome. An approval names the item, since the requester now has
// access to it. A denial does not: the requester may no longer be meant to know what the item is, so the
// mail says only that an item was involved.
func (s *Server) mailDecision(ctx context.Context, p *auth.Principal, n *store.Node, f *store.Folder, req *store.AccessRequest) {
	if s.mailer == nil {
		return
	}
	users, reason := s.activeRecipients(ctx, n.AccountID, store.SubjectUser, req.UserID)
	if reason != "" {
		return
	}
	actor := mailText(s.actorName(ctx, p))
	name := mailText(itemName(n, f))
	link := s.nodeLink(n.ID)
	approved := req.Status == store.RequestApproved
	s.sendEach(ctx, p, users, s.requestBudget(n.AccountID), func(u *store.User) (string, string, error) {
		item := name
		if !approved {
			item = mail.GenericItemLabel(u.Locale)
		}
		return s.templates.Render("access_decision", u.Locale, decisionData{Actor: actor, Item: item,
			Role: mail.RoleLabel(u.Locale, req.Role), Link: link, Approved: approved})
	})
}
