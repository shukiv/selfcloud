// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// The trash (slice m, design section 8). A delete moves the node to the trash: it keeps its contents,
// its permissions and its usage, and a restore brings it back. Trash retention purges it for good.
// Everything in the trash is unreachable through the normal routes, since nodeAccessWith refuses a node
// in the trash or below one as not_found.

const (
	// trashPage is how many roots one store call reads.
	trashPage = 200
	// trashScanMax bounds how many trash roots one listing or empty-trash call examines, so a call whose
	// roots the caller mostly cannot see or purge stays bounded. A listing that stops here returns a next
	// cursor; an empty-trash call replies more. The client goes on from there.
	trashScanMax = 5000
	// emptyTrashMax is the most roots one empty-trash call purges. The reply says more when there are
	// others, and the client calls again.
	emptyTrashMax = 1000
)

// trashItemJSON is a trash listing entry: the node, and what the trash knows about it.
type trashItemJSON struct {
	nodeJSON
	TrashedAt        time.Time `json:"trashed_at"`
	TrashedBy        string    `json:"trashed_by"`
	OriginalParentID string    `json:"original_parent_id"`
	PurgeAt          time.Time `json:"purge_at"`
}

// trashedBy reports whether the caller is the one that trashed the node. A token acting for a user counts
// as that user, since its principal's ID is the user's ID.
func trashedBy(p *auth.Principal, n *store.Node) bool {
	return p.Kind == auth.KindUser && n.TrashedBy == p.ID
}

// mayRestore says whether the caller may restore or purge a trashed node: the one that trashed it, or a
// manager of it. The role is computed as if the node were not trashed.
func mayRestore(p *auth.Principal, n *store.Node, role string) bool {
	return store.RoleRank(role) >= store.RoleRank(store.RoleManager) || trashedBy(p, n)
}

// permanentParam reads ?permanent=. Absent or false means the trash; true means delete at once.
func permanentParam(r *http.Request) (bool, error) {
	switch v := r.URL.Query().Get("permanent"); v {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, apierr.New(apierr.InvalidRequest, "permanent must be true or false")
	}
}

// trashFolderParam reads the optional ?folder= of a trash call. A token sees only its own folders.
func trashFolderParam(r *http.Request, p *auth.Principal) (string, error) {
	fld := r.URL.Query().Get("folder")
	if fld == "" {
		return "", nil
	}
	if err := validID("fld", fld); err != nil {
		return "", err
	}
	if p.Token != nil && !p.Token.Reaches(fld) {
		return "", notFound("folder")
	}
	return fld, nil
}

// trashItem is the listing entry for an explicit trash root, if the caller may see it: the caller reaches
// the node through its own folders and node scope, and it trashed the node or manages it. An owner does not
// see another user's personal-folder trash, since it is not a manager of that folder.
func (s *Server) trashItem(ctx context.Context, p *auth.Principal, n *store.Node) (trashItemJSON, bool, error) {
	cur, role, err := nodeAccessIn(ctx, s.store, p, n.ID, true)
	if err != nil {
		if apierr.From(err).Code == apierr.NotFound {
			return trashItemJSON{}, false, nil
		}
		return trashItemJSON{}, false, err
	}
	if cur.TrashedAt == nil || !mayRestore(p, cur, role) {
		return trashItemJSON{}, false, nil
	}
	j := toNodeJSON(cur)
	j.Role = role
	return trashItemJSON{nodeJSON: j, TrashedAt: *cur.TrashedAt, TrashedBy: cur.TrashedBy,
		OriginalParentID: cur.ParentID, PurgeAt: cur.TrashedAt.Add(s.cfg.Trash.Retention.D())}, true, nil
}

// handleListTrash lists the trash the caller may see, newest first. The page is cursor-paged. A page is
// filled from the store in batches and filtered here, so it may be short of its limit only when the scan
// limit was reached, and then the reply carries a cursor to go on from.
func (s *Server) handleListTrash(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	fld, err := trashFolderParam(r, p)
	if err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out []trashItemJSON
	var positions []string
	cursor := page.Cursor
	examined := 0
	next := ""
scan:
	for {
		batch, err := s.store.TrashRoots(ctx, p.AccountID, fld, store.Page{Limit: trashPage, Cursor: cursor})
		if err != nil {
			return storeErr(err)
		}
		for _, n := range batch {
			examined++
			cursor = store.TrashCursor(n)
			item, ok, err := s.trashItem(ctx, p, n)
			if err != nil {
				return err
			}
			if ok {
				out = append(out, item)
				positions = append(positions, cursor)
			}
			if len(out) > page.Limit {
				break scan // one beyond the page: there is more
			}
			if examined >= s.trashScanLimit {
				next = cursor
				break scan
			}
		}
		if len(batch) < trashPage {
			break
		}
	}
	if len(out) > page.Limit {
		out = out[:page.Limit]
		next = positions[page.Limit-1]
	}
	if out == nil {
		out = []trashItemJSON{}
	}
	// A shortcut in the trash shows its target to a caller who reaches it, as anywhere else.
	ptrs := make([]*nodeJSON, len(out))
	for i := range out {
		ptrs[i] = &out[i].nodeJSON
	}
	if err := s.withTargets(ctx, p, ptrs...); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, listResponse[trashItemJSON]{Items: out, NextCursor: next})
	return nil
}

// trashedAccess loads a node in the trash that the caller reaches, with the role it holds as if the node were
// not trashed. A node that is not in the trash is not_found, the same as a missing one.
func (s *Server) trashedAccess(r *http.Request, nodeID string) (*store.Node, string, error) {
	if !id.Valid("nod", nodeID) {
		return nil, "", nodeNotFound()
	}
	n, role, err := nodeAccessIn(r.Context(), s.store, principalOf(r), nodeID, true)
	if err != nil {
		return nil, "", err
	}
	if n.TrashedAt == nil {
		return nil, "", nodeNotFound()
	}
	return n, role, nil
}

// purgeIn permanently deletes a node inside the caller's transaction, with its usage and an audit entry.
// The caller has checked the role.
func (s *Server) purgeIn(r *http.Request, tx *store.Tx, n *store.Node) error {
	files, bytes, err := tx.PurgeNode(r.Context(), n.AccountID, n.ID)
	if err != nil {
		return err
	}
	return tx.InsertAudit(r.Context(), s.auditEntry(r, n.AccountID, "node.purge", n.ID,
		map[string]any{"kind": n.Kind, "name": n.Name, "files": files, "bytes": bytes, "trashed_by": n.TrashedBy},
		n.FolderID))
}

// trashIn moves a live node to the trash inside the caller's transaction. Open uploads aimed into its subtree
// end with it, and their reservations are released. The caller has checked the role.
func (s *Server) trashIn(r *http.Request, tx *store.Tx, n *store.Node, by string) error {
	ctx := r.Context()
	if err := tx.TrashNode(ctx, n.AccountID, n.ID, by, store.Now()); err != nil {
		return err
	}
	dropped, err := tx.DropUploadsUnder(ctx, n.AccountID, n.ID)
	if err != nil {
		return err
	}
	return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "node.trash", n.ID,
		map[string]any{"kind": n.Kind, "name": n.Name, "uploads_dropped": dropped}, n.FolderID))
}

// purgeTrashed purges one explicit trash root in its own transaction. The caller needs manager on the item,
// checked again inside the transaction: a role revoked after the early check refuses the purge, and nothing
// is written.
func (s *Server) purgeTrashed(r *http.Request, p *auth.Principal, nodeID string) error {
	ctx := r.Context()
	s.atWrite()
	return s.store.Write(ctx, func(tx *store.Tx) error {
		n, role, err := nodeAccessIn(ctx, tx, p, nodeID, true)
		if err != nil {
			return err
		}
		if n.TrashedAt == nil {
			return nodeNotFound()
		}
		if store.RoleRank(role) < store.RoleRank(store.RoleManager) {
			return roleTooLow(store.RoleManager)
		}
		return s.purgeIn(r, tx, n)
	})
}

// handlePurgeTrashed deletes one item in the trash for good, without waiting for the retention. It needs
// manager on the item, or an account key.
func (s *Server) handlePurgeTrashed(w http.ResponseWriter, r *http.Request) error {
	nodeID := r.PathValue("nod")
	_, role, err := s.trashedAccess(r, nodeID)
	if err != nil {
		return err
	}
	if store.RoleRank(role) < store.RoleRank(store.RoleManager) {
		return roleTooLow(store.RoleManager)
	}
	if err := s.purgeTrashed(r, principalOf(r), nodeID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleEmptyTrash purges trash roots the caller may delete for good, at most emptyTrashMax per call. It
// examines at most trashScanLimit roots, the ones the caller cannot purge included. more says the call
// stopped early, because the cap on purges or on examined roots was reached: the trash may hold more, and
// the client calls again until more is false.
func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	fld, err := trashFolderParam(r, p)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var targets []string
	more := false
	examined := 0
	cursor := ""
collect:
	for {
		batch, err := s.store.TrashRoots(ctx, p.AccountID, fld, store.Page{Limit: trashPage, Cursor: cursor})
		if err != nil {
			return storeErr(err)
		}
		for _, n := range batch {
			if examined >= s.trashScanLimit {
				more = true // there is a root beyond the scan cap
				break collect
			}
			examined++
			cur, role, err := nodeAccessIn(ctx, s.store, p, n.ID, true)
			if apierr.From(err).Code == apierr.NotFound {
				continue
			}
			if err != nil {
				return err
			}
			if cur.TrashedAt == nil || store.RoleRank(role) < store.RoleRank(store.RoleManager) {
				continue
			}
			if len(targets) == s.emptyTrashLimit {
				more = true
				break collect
			}
			targets = append(targets, cur.ID)
		}
		if len(batch) < trashPage {
			break
		}
		cursor = store.TrashCursor(batch[len(batch)-1])
	}
	purged := 0
	for _, nodeID := range targets {
		err := s.purgeTrashed(r, p, nodeID)
		switch {
		case err == nil:
			purged++
		case isRefusal(err):
			// Its role went, or it left the trash, after the listing: it is not purged, and nothing is written.
		default:
			return err
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"purged": purged, "more": more})
	return nil
}

// isRefusal reports whether err is an access refusal (not_found or forbidden), as opposed to a fault.
func isRefusal(err error) bool {
	if errors.Is(err, store.ErrNotFound) {
		return true
	}
	switch apierr.From(err).Code {
	case apierr.NotFound, apierr.Forbidden:
		return true
	}
	return false
}

// handleRestoreNode brings a trashed node back to where it was. The caller must be the one that trashed it,
// or a manager of it, and must hold editor on the destination: the original parent. When that parent is
// gone or in the trash itself, the node goes to the root of its folder, which needs editor there too, or
// the restore is a conflict. A name the destination now has is renamed, as a copy is.
func (s *Server) handleRestoreNode(w http.ResponseWriter, r *http.Request) error {
	nodeID := r.PathValue("nod")
	p := principalOf(r)
	ctx := r.Context()
	early, role, err := s.trashedAccess(r, nodeID)
	if err != nil {
		return err
	}
	if !mayRestore(p, early, role) {
		return apierr.New(apierr.Forbidden, "only the one who trashed the item, or a manager of it, can restore it")
	}
	s.atWrite()
	var out *store.Node
	var outRole string
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		n, role, err := nodeAccessIn(ctx, tx, p, nodeID, true)
		if err != nil {
			return err
		}
		if n.TrashedAt == nil {
			return nodeNotFound()
		}
		if !mayRestore(p, n, role) {
			return apierr.New(apierr.Forbidden, "only the one who trashed the item, or a manager of it, can restore it")
		}
		dest, err := restoreDestination(ctx, tx, p, n)
		if err != nil {
			return err
		}
		plan := &transferPlan{folderID: n.FolderID, parent: dest, name: n.Name, key: n.TrashedNameKey}
		taken, err := nameTakenBy(tx.Child(ctx, n.AccountID, dest.ID, plan.key))
		if err != nil {
			return err
		}
		if taken != nil {
			if err := freeName(ctx, tx, plan, n.Kind); err != nil {
				return err
			}
		}
		renamed := plan.name != n.Name
		n.ParentID = dest.ID
		n.Name, n.NameKey = plan.name, plan.key
		n.ModifiedAt, n.ModifiedBy = store.Now(), p.ID
		if err := tx.UpdateNode(ctx, n); err != nil {
			return storeNameErr(err)
		}
		if err := tx.Untrash(ctx, n.AccountID, n.ID); err != nil {
			return err
		}
		out, outRole = n, role
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "node.restore", n.ID,
			map[string]any{"parent": dest.ID, "name": plan.name, "renamed": renamed, "to_root": dest.IsRoot()},
			n.FolderID))
	})
	if err != nil {
		return err
	}
	j := toNodeJSON(out)
	j.Role = outRole
	if err := s.withTargets(r.Context(), principalOf(r), &j); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, j)
	return nil
}

// restoreDestination is where a restore puts the node: its original parent when the caller reaches it and
// holds editor there, or else the root of its folder. The root needs editor too, and a caller who lacks it
// gets a conflict, since the node has nowhere it may go.
func restoreDestination(ctx context.Context, tx *store.Tx, p *auth.Principal, n *store.Node) (*store.Node, error) {
	if n.ParentID != "" {
		parent, err := tx.Node(ctx, n.AccountID, n.ParentID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			// Gone: the root takes the item.
		case err != nil:
			return nil, err
		default:
			self, above, err := tx.TrashState(ctx, n.AccountID, parent.ID)
			if err != nil {
				return nil, err
			}
			if !self && !above {
				// A live parent is the destination, whether or not the caller reaches it: a caller without
				// editor there is refused, and does not fall back to the root.
				if _, err := requireNodeRole(ctx, tx, p, parent.ID, store.RoleEditor); err != nil {
					if isRefusal(err) {
						return nil, roleTooLow(store.RoleEditor)
					}
					return nil, err
				}
				return parent, nil
			}
		}
	}
	root, err := tx.RootNode(ctx, n.AccountID, n.FolderID)
	if err != nil {
		return nil, err
	}
	_, role, err := nodeAccessWith(ctx, tx, p, root.ID)
	if err != nil && !isRefusal(err) {
		return nil, err
	}
	if err != nil || store.RoleRank(role) < store.RoleRank(store.RoleEditor) {
		return nil, apierr.New(apierr.Conflict, "the original folder is in the trash or gone, and the folder root "+
			"needs the editor role to take the item")
	}
	return root, nil
}
