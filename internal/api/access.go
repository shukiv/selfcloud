// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// Grant levels, as the grants routes name them. They map to roles: read is viewer and write is
// editor (see grantRole and grantLevel in folders.go).
const (
	accessRead  = "read"
	accessWrite = "write"
)

// accessReader is what the access rules read: the store, or a write transaction. Checks made
// inside a transaction see the permissions, groups and users as they are then, not as they were
// when the request arrived. That is what stops a permission revoked mid-request from still
// letting the write through.
type accessReader interface {
	Folder(ctx context.Context, accountID, id string) (*store.Folder, error)
	Node(ctx context.Context, accountID, id string) (*store.Node, error)
	User(ctx context.Context, accountID, id string) (*store.User, error)
	EffectiveRank(ctx context.Context, accountID, nodeID, userID string) (int, error)
	PermissionRank(ctx context.Context, accountID, nodeID, userID string) (int, error)
	IsWithin(ctx context.Context, accountID, id, ancestor string) (bool, error)
	ShareLinkStateByID(ctx context.Context, accountID, id string) (*store.ShareLinkState, error)
	RootNode(ctx context.Context, accountID, folderID string) (*store.Node, error)
	Child(ctx context.Context, accountID, parentID, key string) (*store.Node, error)
	TrashState(ctx context.Context, accountID, id string) (self, above bool, err error)
}

// tokenRoleCap is the highest role a delegated token can carry: editor with write, viewer without.
// A token never manages anything, so manager is out of reach whatever its user holds.
func tokenRoleCap(t *auth.TokenScope) string {
	if t.Write {
		return store.RoleEditor
	}
	return store.RoleViewer
}

// implicitRole is the role a user holds on a whole folder by rule, not by a permission row. An
// account owner manages every team space. The user of a personal folder manages it. Nobody else
// gets a role on a personal folder by rule, so an owner does not reach another user's files.
func implicitRole(f *store.Folder, u *store.User) string {
	switch {
	case f.Kind == store.KindPersonal && f.OwnerUserID == u.ID:
		return store.RoleManager
	case f.Kind == store.KindTeam && u.Role == store.RoleOwner:
		return store.RoleManager
	}
	return ""
}

// nodeAccessWith returns the node with the given ID and the role p holds on it, read through rd.
// The role is the highest of the user's implicit role on the folder, the permissions on the node
// and on its ancestors, and any role a group the user is in gives there. An account key holds
// manager. Installation admins, keys and users alike, get forbidden on contents: an admin has no
// direct path to a tenant's files. To act inside a tenant, an admin creates an account key, and the
// audit log records that creation.
//
// A node the caller cannot reach is not_found, so node IDs cannot be probed. Reaching a node means
// holding some role on it; a role too low for the operation is the caller's own check to make.
//
// A delegated token narrows this. The node must lie in one of the token's folders, and in the
// token's own directory when it names one. The role is capped: viewer without write, editor with it.
// The token cannot widen anything.
//
// A node in the trash, or below one, is not_found too. This check sits in the one walk every access
// makes, so no route can forget it. Trashing takes a node out of every route at once.
func nodeAccessWith(ctx context.Context, rd accessReader, p *auth.Principal, nodeID string) (*store.Node, string, error) {
	return nodeAccessIn(ctx, rd, p, nodeID, false)
}

// nodeAccessIn is nodeAccessWith, with one difference for the trash routes. With withTrash, the node may be a
// trash root itself, and may sit below one: a child trashed before its parent must still be restorable, and
// the restore then falls back to the folder root. The role is worked out as if nothing were trashed, since
// permissions are kept in the trash. Every other route passes withTrash false, and gets not_found.
func nodeAccessIn(ctx context.Context, rd accessReader, p *auth.Principal, nodeID string, withTrash bool) (*store.Node, string, error) {
	n, role, err := nodeRoleIn(ctx, rd, p, nodeID, withTrash, true)
	if err != nil {
		return nil, "", err
	}
	if store.RoleRank(role) == 0 {
		return nil, "", nodeNotFound()
	}
	return n, role, nil
}

// nodeSharingAccess returns the node the caller reaches, as nodeAccessWith does, and the role it may use to
// share or decide: only what its permissions, groups and implicit rule give, never an organisation link. A
// role held through a link opens the content; it does not share, decide requests or make links (the design's
// Share links section). The role is "" when only a link gives access, so the caller is refused there.
func nodeSharingAccess(ctx context.Context, rd accessReader, p *auth.Principal, nodeID string) (*store.Node, string, error) {
	if _, _, err := nodeAccessWith(ctx, rd, p, nodeID); err != nil {
		return nil, "", err
	}
	n, role, err := nodeRoleIn(ctx, rd, p, nodeID, false, false)
	if err != nil {
		return nil, "", err
	}
	return n, role, nil
}

// nodeRoleIn is the walk nodeAccessIn makes. It returns the node and the role without the not_found check on
// a zero role, so callers can decide what a zero role means. links says whether organisation links count.
func nodeRoleIn(ctx context.Context, rd accessReader, p *auth.Principal, nodeID string, withTrash, links bool) (*store.Node, string, error) {
	if p == nil {
		return nil, "", apierr.New(apierr.Unauthenticated, "credentials are required")
	}
	if p.IsAdmin() {
		return nil, "", apierr.New(apierr.Forbidden, "admins manage tenants and may not read their files")
	}
	n, err := rd.Node(ctx, p.AccountID, nodeID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", nodeNotFound()
	}
	if err != nil {
		return nil, "", err
	}
	if p.Token != nil && !p.Token.Reaches(n.FolderID) {
		return nil, "", nodeNotFound()
	}
	self, above, err := rd.TrashState(ctx, p.AccountID, nodeID)
	if err != nil {
		return nil, "", err
	}
	if !withTrash && (self || above) {
		return nil, "", nodeNotFound()
	}
	f, err := rd.Folder(ctx, p.AccountID, n.FolderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", nodeNotFound()
	}
	if err != nil {
		return nil, "", err
	}

	if p.Kind == auth.KindShareLink {
		// A visitor of a link that anyone can open reaches the link's node and everything below it, at the link's
		// role, and nothing else: no permission, group or implicit rule applies to it. A drop link's visitor reaches
		// no node at all: it uploads into the link's directory (see uploadAccess).
		if p.Link == nil {
			return nil, "", apierr.New(apierr.Unauthenticated, "the credential is not valid")
		}
		if p.Link.UploadOnly {
			return nil, "", nodeNotFound()
		}
		within, err := rd.IsWithin(ctx, p.AccountID, nodeID, p.Link.Node)
		if err != nil {
			return nil, "", err
		}
		if !within {
			return nil, "", nodeNotFound()
		}
		return n, p.Link.Role, nil
	}

	role := store.RoleManager // an account key, which reaches everything in its account
	if p.Kind == auth.KindUser {
		u, err := rd.User(ctx, p.AccountID, p.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, "", apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
		}
		if err != nil {
			return nil, "", err
		}
		switch u.Status {
		case store.UserDisabled:
			return nil, "", apierr.New(apierr.UserDisabled, "the user is disabled")
		case store.UserActive:
		default:
			return nil, "", apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
		}
		role = implicitRole(f, u)
		var rank int
		if links {
			rank, err = rd.EffectiveRank(ctx, p.AccountID, nodeID, u.ID)
		} else {
			rank, err = rd.PermissionRank(ctx, p.AccountID, nodeID, u.ID)
		}
		if err != nil {
			return nil, "", err
		}
		if rank > store.RoleRank(role) {
			role = store.RoleOfRank(rank)
		}
	}

	if p.Token != nil {
		if p.Token.Node != "" {
			within, err := rd.IsWithin(ctx, p.AccountID, nodeID, p.Token.Node)
			if err != nil {
				return nil, "", err
			}
			if !within {
				return nil, "", nodeNotFound()
			}
		}
		if store.RoleRank(role) > store.RoleRank(tokenRoleCap(p.Token)) {
			role = tokenRoleCap(p.Token)
		}
	}
	return n, role, nil
}

// requireNodeRole is nodeAccessWith plus the role check: the caller must hold at least minRole on
// the node. It is the check every operation makes, outside a transaction for the early refusal and
// inside one for the final word, where it sees the permissions as they are then.
func requireNodeRole(ctx context.Context, rd accessReader, p *auth.Principal, nodeID, minRole string) (*store.Node, error) {
	n, role, err := nodeAccessWith(ctx, rd, p, nodeID)
	if err != nil {
		return nil, err
	}
	if store.RoleRank(role) < store.RoleRank(minRole) {
		return nil, roleTooLow(minRole)
	}
	return n, nil
}

// roleTooLow is the refusal for a reachable node where the caller's role is below the one needed.
// The caller already knows the node exists, so this is forbidden, not not_found.
func roleTooLow(minRole string) error {
	return apierr.New(apierr.Forbidden, "this needs the "+minRole+" role on the item")
}

// folderAccessWith returns the folder with the given ID and the role p holds on its root, read
// through rd. A team space or a personal folder is reached at its root: a permission deeper down
// does not reach the root, so the root stays not_found for a user whose only permission is deep.
func folderAccessWith(ctx context.Context, rd accessReader, p *auth.Principal, folderID string) (*store.Folder, string, error) {
	if p == nil {
		return nil, "", apierr.New(apierr.Unauthenticated, "credentials are required")
	}
	if p.IsAdmin() {
		return nil, "", apierr.New(apierr.Forbidden, "admins manage tenants and may not read their files")
	}
	root, err := rd.RootNode(ctx, p.AccountID, folderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", notFound("folder")
	}
	if err != nil {
		return nil, "", err
	}
	_, role, err := nodeAccessWith(ctx, rd, p, root.ID)
	if err != nil {
		if apierr.From(err).Code == apierr.NotFound {
			return nil, "", notFound("folder")
		}
		return nil, "", err
	}
	f, err := rd.Folder(ctx, p.AccountID, folderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", notFound("folder")
	}
	if err != nil {
		return nil, "", err
	}
	return f, role, nil
}

// folderAccess is folderAccessWith for the request's caller.
func (s *Server) folderAccess(r *http.Request, folderID string) (*store.Folder, string, error) {
	return folderAccessWith(r.Context(), s.store, principalOf(r), folderID)
}

// callerCheck refuses the callers that never reach contents: no caller at all, and an installation
// admin. It runs before any lookup that is keyed on the caller's account, which an admin lacks.
func callerCheck(p *auth.Principal) error {
	if p == nil {
		return apierr.New(apierr.Unauthenticated, "credentials are required")
	}
	if p.IsAdmin() {
		return apierr.New(apierr.Forbidden, "admins manage tenants and may not read their files")
	}
	return nil
}

// notDirError is a walk that stopped at a file: at is the directory it stopped in, name the file's
// name. fileOnPath turns it into the answer a caller may see.
type notDirError struct {
	at   *store.Node
	name string
}

func (e *notDirError) Error() string { return e.name + " is a file, not a directory" }

// fileOnPath is the answer for a walk that stopped at a file. Only a caller who reaches the directory
// the walk stopped in learns that a file stands in the way, since only then does the caller already
// know what that directory holds. Anyone else gets notFound(label), the answer a missing name gets,
// so the two cannot be told apart. Errors that are not a walk stopping at a file pass through.
func fileOnPath(ctx context.Context, rd accessReader, p *auth.Principal, err error, label string) error {
	var nd *notDirError
	if !errors.As(err, &nd) {
		return err
	}
	if _, _, rerr := nodeAccessWith(ctx, rd, p, nd.at.ID); rerr != nil {
		if apierr.From(rerr).Code == apierr.NotFound {
			return notFound(label)
		}
		return rerr
	}
	return apierr.Newf(apierr.InvalidPath, "%s is a file, not a directory", nd.name)
}

// reachedOr returns not_found with label unless p reaches the node, so a node the caller cannot reach
// answers the same as a missing one at that place in the path.
func reachedOr(ctx context.Context, rd accessReader, p *auth.Principal, nodeID, label string) error {
	_, _, err := nodeAccessWith(ctx, rd, p, nodeID)
	if err != nil && apierr.From(err).Code == apierr.NotFound {
		return notFound(label)
	}
	return err
}

// anchorDir follows the directories named by segs from root, as far as they exist, and returns the
// last one reached. A directory the path still needs is not reached: a write that creates it lands
// in the directory before it, so that is the one the caller's role must cover.
func anchorDir(ctx context.Context, find nodeFinder, root *store.Node, segs []pathSeg) (*store.Node, error) {
	cur := root
	for _, seg := range segs {
		child, err := find(ctx, cur.ID, seg.key)
		if errors.Is(err, store.ErrNotFound) {
			return cur, nil
		}
		if err != nil {
			return nil, err
		}
		if child.Kind != store.KindDir {
			return nil, &notDirError{at: cur, name: seg.name}
		}
		cur = child
	}
	return cur, nil
}

// writeAnchor is the check for a write that creates or places items under dirs, a path below a
// folder's root. It finds the folder and the deepest directory that exists on dirs, and demands
// minRole on that directory. The same check runs inside the write's transaction, so a role revoked
// after the early refusal stops the write.
func writeAnchor(ctx context.Context, rd accessReader, p *auth.Principal, folderID string, dirs []pathSeg,
	minRole string) (*store.Folder, *store.Node, error) {
	if err := callerCheck(p); err != nil {
		return nil, nil, err
	}
	if p.Token != nil && !p.Token.Reaches(folderID) {
		return nil, nil, notFound("folder")
	}
	f, err := rd.Folder(ctx, p.AccountID, folderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, notFound("folder")
	}
	if err != nil {
		return nil, nil, err
	}
	root, err := rd.RootNode(ctx, p.AccountID, folderID)
	if err != nil {
		return nil, nil, err
	}
	anchor, err := anchorDir(ctx, func(ctx context.Context, parentID, key string) (*store.Node, error) {
		return rd.Child(ctx, p.AccountID, parentID, key)
	}, root, dirs)
	if err != nil {
		return nil, nil, fileOnPath(ctx, rd, p, err, "node")
	}
	if _, err := requireNodeRole(ctx, rd, p, anchor.ID, minRole); err != nil {
		return nil, nil, err
	}
	return f, anchor, nil
}
