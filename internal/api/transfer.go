// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
)

// syncCopyNodes is the largest copy made inside the request. Larger copies are queued as a job. A
// Server holds it in copySyncNodes, so a test can lower it.
const syncCopyNodes = 10000

// jobCopyNode is the job kind of a copy too large for the request.
const jobCopyNode = "node.copy"

// maxNameBytes is the longest stored name.
const maxNameBytes = 255

// maxExtBytes is the longest extension kept when a copy is renamed; a longer one is part of the name.
const maxExtBytes = 32

// transferBody says where a move or copy goes. Folder defaults to the node's own folder. Parent (a
// directory's ID) or Path (relative to the target folder's root) picks the directory; by default
// it is the node's own directory, or the target folder's root when the folder changes. Name
// renames on the way. OnConflict is "fail" (the default), "rename" (name (1).ext) or "replace"
// (a file over a file).
type transferBody struct {
	Folder     string `json:"folder"`
	Parent     string `json:"parent"`
	Path       string `json:"path"`
	Name       string `json:"name"`
	OnConflict string `json:"on_conflict"`
}

// transferPlan is where a node will go, and what it displaces.
type transferPlan struct {
	folderID string
	parent   *store.Node
	name     string
	key      string
	replaced *store.Node // a file the transfer replaces, or nil
}

// planTransfer works out the target of a move or copy, inside tx. The caller has checked access to
// the folders involved. A node may not go into itself or below itself.
// resolveDestination finds the folder and the directory a move or copy of src goes to, from the
// request's folder, parent or path. It only reads, so a caller can check its role on that directory
// before any name is checked. A node may not go into itself, which is checked by planTransfer.
// A destination the caller does not reach is not_found, the same answer a missing one gets.
func resolveDestination(ctx context.Context, tx *store.Tx, p *auth.Principal, src *store.Node,
	in transferBody) (string, *store.Node, error) {
	acc := src.AccountID
	folderID := src.FolderID
	if in.Folder != "" {
		if err := validID("fld", in.Folder); err != nil {
			return "", nil, err
		}
		folderID = in.Folder
	}
	if in.Parent != "" && in.Path != "" {
		return "", nil, apierr.New(apierr.InvalidRequest, "give parent or path, not both")
	}
	if _, err := tx.Folder(ctx, acc, folderID); errors.Is(err, store.ErrNotFound) {
		return "", nil, notFound("folder")
	} else if err != nil {
		return "", nil, err
	}
	var parent *store.Node
	switch {
	case in.Parent != "":
		if err := validID("nod", in.Parent); err != nil {
			return "", nil, err
		}
		n, err := tx.Node(ctx, acc, in.Parent)
		if errors.Is(err, store.ErrNotFound) || (err == nil && n.FolderID != folderID) {
			return "", nil, notFound("parent")
		}
		if err != nil {
			return "", nil, err
		}
		// Only a parent the caller reaches may say it is a file.
		if err := reachedOr(ctx, tx, p, n.ID, "parent"); err != nil {
			return "", nil, err
		}
		if n.Kind != store.KindDir {
			return "", nil, apierr.New(apierr.InvalidRequest, "the parent is a file, not a directory")
		}
		parent = n
	case in.Path != "":
		segs, err := parsePath(in.Path)
		if err != nil {
			return "", nil, err
		}
		root, err := tx.RootNode(ctx, acc, folderID)
		if err != nil {
			return "", nil, err
		}
		parent, err = walkDirs(ctx, func(ctx context.Context, parentID, key string) (*store.Node, error) {
			return tx.Child(ctx, acc, parentID, key)
		}, root, segs)
		if err != nil {
			return "", nil, fileOnPath(ctx, tx, p, err, "parent")
		}
		if parent == nil {
			return "", nil, notFound("parent")
		}
		if err := reachedOr(ctx, tx, p, parent.ID, "parent"); err != nil {
			return "", nil, err
		}
	case folderID == src.FolderID && !src.IsRoot():
		p, err := tx.Node(ctx, acc, src.ParentID)
		if err != nil {
			return "", nil, err
		}
		parent = p
	default:
		root, err := tx.RootNode(ctx, acc, folderID)
		if err != nil {
			return "", nil, err
		}
		if err := reachedOr(ctx, tx, p, root.ID, "folder"); err != nil {
			return "", nil, err
		}
		parent = root
	}
	return folderID, parent, nil
}

func planTransfer(ctx context.Context, tx *store.Tx, p *auth.Principal, src *store.Node, in transferBody,
	excludeSelf bool) (*transferPlan, error) {
	folderID, parent, err := resolveDestination(ctx, tx, p, src, in)
	if err != nil {
		return nil, err
	}
	acc := src.AccountID
	if src.Kind == store.KindDir {
		within, err := tx.IsWithin(ctx, acc, parent.ID, src.ID)
		if err != nil {
			return nil, err
		}
		if within {
			return nil, apierr.New(apierr.InvalidRequest, "a folder cannot go into itself or one of its descendants")
		}
	}

	plan := &transferPlan{folderID: folderID, parent: parent, name: src.Name, key: src.NameKey}
	if in.Name != "" {
		name, key, err := names.Clean(in.Name)
		if err != nil {
			return nil, err
		}
		plan.name, plan.key = name, key
	}
	taken, err := nameTakenBy(tx.Child(ctx, acc, parent.ID, plan.key))
	if err != nil {
		return nil, err
	}
	if taken == nil || (excludeSelf && taken.ID == src.ID) {
		return plan, nil
	}
	if err := applyConflict(ctx, tx, plan, taken, in.OnConflict, src.Kind, src.ID); err != nil {
		return nil, err
	}
	return plan, nil
}

// applyConflict decides what a name already taken in the target directory means: refuse, rename
// the plan, or mark the file it replaces. kind is the kind of the node placed; selfID is that
// node's ID, or "" for a node not yet in the tree (an upload).
func applyConflict(ctx context.Context, tx *store.Tx, plan *transferPlan, taken *store.Node, onConflict, kind, selfID string) error {
	switch onConflict {
	case "", "fail":
		return nameConflict(taken)
	case "rename":
		return freeName(ctx, tx, plan, kind)
	case "replace":
		if taken.ID == selfID {
			return apierr.New(apierr.Conflict, "a file cannot replace itself")
		}
		if kind != store.KindFile || taken.Kind != store.KindFile {
			return apierr.New(apierr.Conflict, "replace works only file over file; a folder is never replaced").
				With("id", taken.ID)
		}
		plan.replaced = taken
		return nil
	}
	return apierr.New(apierr.InvalidRequest, "on_conflict must be fail, rename or replace")
}

// freeName changes plan's name to the first "base (n).ext" that is free in the target directory.
// The base is cut, on a character boundary, so the whole name fits in maxNameBytes.
func freeName(ctx context.Context, tx *store.Tx, plan *transferPlan, kind string) error {
	base, ext := plan.name, ""
	if kind == store.KindFile {
		if i := strings.LastIndex(plan.name, "."); i > 0 && len(plan.name)-i <= maxExtBytes {
			base, ext = plan.name[:i], plan.name[i:]
		}
	}
	for n := 1; n <= 1000; n++ {
		suffix := fmt.Sprintf(" (%d)%s", n, ext)
		cand, key, err := names.Clean(truncateBytes(base, maxNameBytes-len(suffix)) + suffix)
		if err != nil {
			return err
		}
		taken, err := nameTakenBy(tx.Child(ctx, plan.parent.AccountID, plan.parent.ID, key))
		if err != nil {
			return err
		}
		if taken == nil {
			plan.name, plan.key = cand, key
			return nil
		}
	}
	return apierr.New(apierr.Conflict, "no free name was found for the copy")
}

// truncateBytes cuts s to at most max bytes, without splitting a character.
func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// applyReplace moves the file a transfer displaces to the trash, inside the transaction, and returns it. It
// returns nil when the transfer displaces nothing. A move or copy with on_conflict=replace does not version
// the displaced file: it is a different node from the one that takes its place, so it goes to the trash
// instead, where a restore can still reach it. It stays in usage there, so the transfer's usage does not
// change for it.
// requireReplaceRole is the check a replace needs on the file it displaces: editor on that file, inside
// the write's transaction. Contributor may place a file in a directory but may not overwrite one, so a
// replace with only contributor is forbidden. A file the caller does not reach is not_found.
func requireReplaceRole(ctx context.Context, tx *store.Tx, p *auth.Principal, plan *transferPlan) error {
	if plan.replaced == nil {
		return nil
	}
	_, err := requireNodeRole(ctx, tx, p, plan.replaced.ID, store.RoleEditor)
	return err
}

func applyReplace(ctx context.Context, tx *store.Tx, plan *transferPlan, by string) (*store.Node, error) {
	if plan.replaced == nil {
		return nil, nil
	}
	old := plan.replaced
	if err := tx.TrashNode(ctx, old.AccountID, old.ID, by, store.Now()); err != nil {
		return nil, err
	}
	return old, nil
}

// copyTree makes the copy of src's tree in the target place of plan. The copy of src gets rootID;
// the rest get new IDs. A copy shares its files' blobs. It returns the number of nodes made and the
// file count and bytes they add to usage.
//
// keepSharing says whether the copies keep editors_can_share. Only a caller that manages the destination
// may pass it on: otherwise an editor would be handed a power the caller does not hold.
//
// keepRestricted says whether the copies keep the values of restricted fields. Only a caller that manages the
// source may pass it on (copyKeepsRestricted): a copy must not widen who can read a restricted value.
func copyTree(ctx context.Context, tx *store.Tx, src *store.Node, rootID string, plan *transferPlan, by string,
	keepSharing, keepRestricted bool) (nodes int, files, bytes int64, err error) {
	tree, err := tx.Subtree(ctx, src.AccountID, src.ID)
	if err != nil {
		return 0, 0, 0, err
	}
	now := store.Now()
	// The tree is already in memory, and the copy is capped at copy.max_nodes. The files are counted as the store
	// counts them (files and shortcuts, not folders), and the store decides how the copy reports (decision 27).
	var treeFiles int
	for _, n := range tree {
		if n.Kind != store.KindDir {
			treeFiles++
		}
	}
	report, err := tx.ReportFor(ctx, src.AccountID, "file.created", len(tree), treeFiles)
	if err != nil {
		return 0, 0, 0, err
	}
	newIDs := map[string]string{}
	for i, n := range tree {
		c := *n
		if i == 0 {
			c.ID = rootID
		} else {
			c.ID = id.New("nod")
		}
		newIDs[n.ID] = c.ID
		c.FolderID = plan.folderID
		c.CreatedAt, c.ModifiedAt = now, now
		c.CreatedBy, c.ModifiedBy = by, by
		// The copy's owner is its creator, or the user of a personal folder it lands in; the source's
		// owner does not carry over. The store applies that rule when the node is inserted.
		c.OwnerID = ""
		if !keepSharing {
			c.EditorsCanShare = false
		}
		if i == 0 {
			c.ParentID, c.Name, c.NameKey = plan.parent.ID, plan.name, plan.key
		} else {
			c.ParentID = newIDs[n.ParentID]
		}
		if c.Kind == store.KindFile {
			c.ETag = newETag()
			bytes += c.Size
			files++
		}
		if err := tx.InsertNode(ctx, &c); err != nil {
			return 0, 0, 0, storeNameErr(err)
		}
		// The audit entry of a copy names only its root, so each file the copy makes reports itself here, unless the
		// copy is too big to report file by file (store.ReportFor): then the new folder reports once, below.
		if report == store.ReportEachFile && c.Kind != store.KindDir {
			if err := tx.EmitEvent(ctx, c.AccountID, "file.created", now, store.FileEvent(&c)); err != nil {
				return 0, 0, 0, err
			}
		}
	}
	// A copy keeps its labels: each new node takes the values of the node it was copied from, in one statement.
	hasLabels, err := tx.HasLabelFields(ctx, src.AccountID)
	if err != nil {
		return 0, 0, 0, err
	}
	if hasLabels {
		pairs := make([]store.LabelCopy, 0, len(tree))
		for _, n := range tree {
			pairs = append(pairs, store.LabelCopy{From: n.ID, To: newIDs[n.ID]})
		}
		if err := tx.CopyLabelValues(ctx, src.AccountID, pairs, keepRestricted); err != nil {
			return 0, 0, 0, err
		}
	}
	if report == store.ReportFolder {
		root := &store.Node{ID: rootID, FolderID: plan.folderID, ParentID: plan.parent.ID}
		if err := tx.EmitEvent(ctx, src.AccountID, "file.created", now, store.FolderEvent(root)); err != nil {
			return 0, 0, 0, err
		}
	}
	return len(tree), files, bytes, nil
}

// tooManyNodes is the refusal for a copy above copy.max_nodes.
func (s *Server) tooManyNodes(nodes int) error {
	return apierr.Newf(apierr.TooLarge, "a folder copy may make at most %d nodes", s.cfg.Copy.MaxNodes).
		With("max_nodes", s.cfg.Copy.MaxNodes).With("nodes", nodes)
}

// handleMoveNode moves a file or folder to another place, possibly in another folder of the same
// account. The caller needs write on the node's folder and on the target folder.
func (s *Server) handleMoveNode(w http.ResponseWriter, r *http.Request) error {
	var in transferBody
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	src, err := s.nodeOf(r, r.PathValue("nod"), store.RoleEditor)
	if err != nil {
		return err
	}
	if src.IsRoot() {
		return apierr.New(apierr.InvalidRequest, "a folder's root cannot be moved")
	}
	ctx := r.Context()
	p := principalOf(r)
	var out *store.Node
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Node(ctx, src.AccountID, src.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nodeNotFound()
		}
		if err != nil {
			return err
		}
		_, srcRole, err := nodeAccessWith(ctx, tx, p, cur.ID)
		if err != nil {
			return err
		}
		if store.RoleRank(srcRole) < store.RoleRank(store.RoleEditor) {
			return roleTooLow(store.RoleEditor)
		}
		if err := checkIfMatch(r, cur); err != nil {
			return err
		}
		// Moving in takes contributor on the directory it lands in, and moving out takes editor
		// on the item (above). Contributor is enough to receive, since nothing is removed there.
		// The destination is checked before any name, so a refused role is a 403 whatever the name.
		_, dest, err := resolveDestination(ctx, tx, p, cur, in)
		if err != nil {
			return err
		}
		if _, err := requireNodeRole(ctx, tx, p, dest.ID, store.RoleContributor); err != nil {
			return err
		}
		plan, err := planTransfer(ctx, tx, p, cur, in, true)
		if err != nil {
			return err
		}
		if plan.parent.ID == cur.ParentID && plan.name == cur.Name && plan.folderID == cur.FolderID {
			out = cur
			return nil
		}
		// A move never raises who can read a restricted value unless a manager of the item makes it. So a caller
		// who does not manage the item may not take a subtree that holds one out of its folder, nor into a place
		// where the caller would manage it. The refusal says a restricted value is there: that is the one fact it
		// reveals. Moves that keep the item in its folder with no higher role, and moves of subtrees with no
		// restricted value, are unaffected.
		if store.RoleRank(srcRole) < store.RoleRank(store.RoleManager) {
			_, landRole, err := nodeAccessWith(ctx, tx, p, plan.parent.ID)
			if err != nil {
				return err
			}
			landsOut := plan.folderID != cur.FolderID
			if landsOut || store.RoleRank(landRole) >= store.RoleRank(store.RoleManager) {
				restricted, err := tx.SubtreeHasRestricted(ctx, cur.AccountID, cur.ID)
				if err != nil {
					return err
				}
				if restricted {
					return apierr.New(apierr.Forbidden, "the item holds a restricted value; only a manager of the item may move it there, so ask a manager of the item").
						With("reason", "restricted_label")
				}
			}
		}
		srcFolder := cur.FolderID
		from := map[string]any{"parent": cur.ParentID, "folder": cur.FolderID, "name": cur.Name}
		if err := requireReplaceRole(ctx, tx, p, plan); err != nil {
			return err
		}
		// Moving a displaced file to the trash keeps its bytes in usage, so a move never changes usage.
		replaced, err := applyReplace(ctx, tx, plan, p.ID)
		if err != nil {
			return err
		}
		folderChanged := plan.folderID != cur.FolderID
		// A node placed in another directory takes the directory's place: a caller that does not manage
		// the destination cannot leave editors_can_share on it or below it.
		moved := folderChanged || cur.ParentID != plan.parent.ID
		if folderChanged {
			// Inside a personal folder everything belongs to the folder's user.
			if dst, err := tx.Folder(ctx, cur.AccountID, plan.folderID); err == nil && dst.Kind == store.KindPersonal {
				cur.OwnerID = dst.OwnerUserID
			}
		}
		cur.FolderID, cur.ParentID = plan.folderID, plan.parent.ID
		cur.Name, cur.NameKey = plan.name, plan.key
		cur.ModifiedAt, cur.ModifiedBy = store.Now(), p.ID
		if cur.Kind == store.KindFile {
			cur.ETag = newETag() // a move is a write, so the ETag changes, as on a rename
		}
		if err := tx.UpdateNode(ctx, cur); err != nil {
			return storeNameErr(err)
		}
		if folderChanged {
			// The subtree's bytes and reservations move with it; the destination user's limit is checked first.
			if err := s.checkMoveUsage(ctx, tx, p, cur.AccountID, cur.ID, srcFolder, plan.folderID); err != nil {
				return err
			}
			if err := tx.MoveSubtree(ctx, cur.AccountID, cur.ID, srcFolder, plan.folderID); err != nil {
				return err
			}
		}
		if moved {
			keep, err := sharingKept(ctx, tx, p, plan.parent.ID)
			if err != nil {
				return err
			}
			if !keep {
				if err := tx.ClearSubtreeEditorsCanShare(ctx, cur.AccountID, cur.ID); err != nil {
					return err
				}
				cur.EditorsCanShare = false
			}
		}
		out = cur
		if replaced != nil {
			if err := tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.trash", replaced.ID,
				map[string]any{"kind": replaced.Kind, "name": replaced.Name, "replaced_by": cur.ID}, srcFolder, plan.folderID)); err != nil {
				return err
			}
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.move", cur.ID,
			map[string]any{"from": from, "to": map[string]any{"parent": plan.parent.ID,
				"folder": plan.folderID, "name": plan.name}}, srcFolder, plan.folderID))
	})
	if err != nil {
		return err
	}
	j := toNodeJSON(out)
	if err := s.withTargets(ctx, p, &j); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, j)
	return nil
}

// copyPayload is what a queued copy needs. The new root's ID is fixed here, so a repeat of the job
// can tell that the copy is done.
type copyPayload struct {
	Source     string `json:"source"`
	Root       string `json:"root"`
	Folder     string `json:"folder"`
	Parent     string `json:"parent"`
	Name       string `json:"name,omitempty"`
	OnConflict string `json:"on_conflict,omitempty"`
	// Chunked marks a job made as a chunked copy (copy_jobs holds its state); a job without it is a one-write copy.
	Chunked bool         `json:"chunked,omitempty"`
	Actor   actorPayload `json:"actor"`
}

// actorPayload is the caller a queued copy runs for. The job checks access again as this caller,
// and checks that the caller may still act: its account, key or user is live, and a token was not
// revoked since it was issued.
type actorPayload struct {
	Kind   string           `json:"kind"`
	ID     string           `json:"id"`
	UserID string           `json:"user_id,omitempty"`
	Role   string           `json:"role,omitempty"`
	Token  *auth.TokenScope `json:"token,omitempty"`
}

func actorOf(p *auth.Principal) actorPayload {
	return actorPayload{Kind: string(p.Kind), ID: p.ID, UserID: p.UserID, Role: p.Role, Token: p.Token}
}

// handleCopyNode copies a file, or a folder with everything in it. The caller needs read on the
// source and write on the target folder. A copy of a file shares its contents with the original,
// so nothing is copied on disk, but the account is charged for the copy in full. Up to
// syncCopyNodes nodes are copied in the request; a larger copy answers 202 with a job.
func (s *Server) handleCopyNode(w http.ResponseWriter, r *http.Request) error {
	var in transferBody
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	// Checked here for both paths: a small copy used to accept a bad value when no name clashed, and a chunked copy
	// stored it and failed with a server error.
	switch in.OnConflict {
	case "", "fail", "rename", "replace":
	default:
		return apierr.New(apierr.InvalidRequest, "on_conflict must be fail, rename or replace")
	}
	src, role, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	if src.IsRoot() {
		return apierr.New(apierr.InvalidRequest, "a folder's root cannot be copied; copy its contents instead")
	}
	if in.Folder != "" {
		if err := validID("fld", in.Folder); err != nil {
			return err
		}
	}
	ctx := r.Context()
	p := principalOf(r)
	if store.RoleRank(role) < store.RoleRank(store.RoleViewer) {
		return roleTooLow(store.RoleViewer)
	}
	restricted, err := s.copyRestricted(ctx, s.store, src, role)
	if err != nil {
		return err
	}
	if restricted {
		return errDownloadRestricted()
	}
	nodes, err := s.store.SubtreeSize(ctx, src.AccountID, src.ID, s.cfg.Copy.MaxNodes+1)
	if err != nil {
		return err
	}
	if nodes > s.cfg.Copy.MaxNodes {
		return s.tooManyNodes(nodes)
	}
	if nodes > s.copySyncNodes {
		return s.queueChunkedCopy(w, r, src, in, p)
	}

	rootID := id.New("nod")
	var out *store.Node
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Node(ctx, src.AccountID, src.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nodeNotFound()
		}
		if err != nil {
			return err
		}
		// The role on the source is read here, inside the write, so a change of permission since the
		// request started is seen: it decides whether restricted values are copied.
		_, srcRole, err := nodeAccessWith(ctx, tx, p, cur.ID)
		if err != nil {
			return err
		}
		if store.RoleRank(srcRole) < store.RoleRank(store.RoleViewer) {
			return roleTooLow(store.RoleViewer)
		}
		_, dest, err := resolveDestination(ctx, tx, p, cur, in)
		if err != nil {
			return err
		}
		if _, err := requireNodeRole(ctx, tx, p, dest.ID, store.RoleContributor); err != nil {
			return err
		}
		plan, err := planTransfer(ctx, tx, p, cur, in, false)
		if err != nil {
			return err
		}
		if err := requireReplaceRole(ctx, tx, p, plan); err != nil {
			return err
		}
		replaced, err := applyReplace(ctx, tx, plan, p.ID)
		if err != nil {
			return err
		}
		keep, err := sharingKept(ctx, tx, p, plan.parent.ID)
		if err != nil {
			return err
		}
		count, files, bytes, err := copyTree(ctx, tx, cur, rootID, plan, p.ID, keep, copyKeepsRestricted(srcRole))
		if err != nil {
			return err
		}
		// The copy is the copier's own edit: it lands in the copier's Recent as the new copy. A shortcut has no
		// contents, so copying one is not an edit.
		if cur.Kind != store.KindShortcut {
			if err := noteEdit(ctx, tx, p, cur.AccountID, rootID); err != nil {
				return err
			}
		}
		// The copies count in full. What they displace stays in usage, in the trash, so only the copies count.
		change := store.UsageChange{Bytes: bytes, Files: files}
		if err := s.applyUsage(ctx, tx, p, cur.AccountID, store.FolderChange{FolderID: plan.folderID, Change: change}); err != nil {
			return err
		}
		if out, err = tx.Node(ctx, cur.AccountID, rootID); err != nil {
			return err
		}
		if replaced != nil {
			if err := tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.trash", replaced.ID,
				map[string]any{"kind": replaced.Kind, "name": replaced.Name, "replaced_by": rootID}, cur.FolderID, plan.folderID)); err != nil {
				return err
			}
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.copy", rootID,
			map[string]any{"source": cur.ID, "nodes": count, "bytes": bytes}, cur.FolderID, plan.folderID))
	})
	if err != nil {
		return err
	}
	w.Header().Set("Location", "/api/v1/nodes/"+out.ID)
	j := toNodeJSON(out)
	if err := s.withTargets(ctx, p, &j); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, j)
	return nil
}

// runCopyJobWhole makes a queued copy that predates the chunked copy (migration 0027) and has no copy row: in one
// write. It is safe to repeat: when the new root already exists, the copy was made, and the job does nothing. It
// checks access as the caller who queued it, so a grant revoked since then stops the copy. runCopyJob, in
// copy_job.go, runs every copy queued since.
func (s *Server) runCopyJobWhole(ctx context.Context, job *store.Job) error {
	var pl copyPayload
	if err := json.Unmarshal([]byte(job.Payload), &pl); err != nil {
		return fmt.Errorf("copy job payload: %w", err)
	}
	p := &auth.Principal{Kind: auth.Kind(pl.Actor.Kind), ID: pl.Actor.ID, AccountID: job.AccountID,
		UserID: pl.Actor.UserID, Role: pl.Actor.Role, Token: pl.Actor.Token}
	return s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := tx.Node(ctx, job.AccountID, pl.Root); err == nil {
			return nil // made by an earlier run
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		// Queued work is checked again: a copy whose caller was revoked since the job was queued
		// writes nothing. Expiry alone does not count, because the copy was allowed when it was asked.
		d, err := tx.Delegation(ctx, job.AccountID, callerKeyID(p), p.UserID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !callerLive(p, d, time.Now())) {
			return apierr.New(apierr.Forbidden, "the caller of this copy no longer has access; nothing was copied")
		}
		if err != nil {
			return err
		}
		src, err := tx.Node(ctx, job.AccountID, pl.Source)
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("the source no longer exists")
		}
		if err != nil {
			return err
		}
		_, role, err := nodeAccessWith(ctx, tx, p, src.ID)
		if err != nil {
			return err
		}
		restricted, err := s.copyRestricted(ctx, tx, src, role)
		if err != nil {
			return err
		}
		if restricted {
			return errDownloadRestricted()
		}
		nodes, err := tx.Subtree(ctx, job.AccountID, src.ID)
		if err != nil {
			return err
		}
		if len(nodes) > s.cfg.Copy.MaxNodes {
			return s.tooManyNodes(len(nodes))
		}
		in := transferBody{Folder: pl.Folder, Parent: pl.Parent, Name: pl.Name, OnConflict: pl.OnConflict}
		_, dest, err := resolveDestination(ctx, tx, p, src, in)
		if err != nil {
			return err
		}
		if _, err := requireNodeRole(ctx, tx, p, dest.ID, store.RoleContributor); err != nil {
			return err
		}
		plan, err := planTransfer(ctx, tx, p, src, in, false)
		if err != nil {
			return err
		}
		if err := requireReplaceRole(ctx, tx, p, plan); err != nil {
			return err
		}
		replaced, err := applyReplace(ctx, tx, plan, p.ID)
		if err != nil {
			return err
		}
		keep, err := sharingKept(ctx, tx, p, plan.parent.ID)
		if err != nil {
			return err
		}
		count, files, bytes, err := copyTree(ctx, tx, src, pl.Root, plan, p.ID, keep, copyKeepsRestricted(role))
		if err != nil {
			return err
		}
		if src.Kind != store.KindShortcut {
			if err := noteEdit(ctx, tx, p, job.AccountID, pl.Root); err != nil {
				return err
			}
		}
		change := store.UsageChange{Bytes: bytes, Files: files}
		if err := s.applyUsage(ctx, tx, p, job.AccountID, store.FolderChange{FolderID: plan.folderID, Change: change}); err != nil {
			return err
		}
		if replaced != nil {
			if err := tx.InsertAudit(ctx, auditAs(p, job.AccountID, "node.trash", replaced.ID,
				map[string]any{"kind": replaced.Kind, "name": replaced.Name, "replaced_by": pl.Root}, src.FolderID, plan.folderID)); err != nil {
				return err
			}
		}
		return tx.InsertAudit(ctx, auditAs(p, job.AccountID, "node.copy", pl.Root,
			map[string]any{"source": src.ID, "nodes": count, "bytes": bytes, "job": job.ID}, src.FolderID, plan.folderID))
	})
}

// auditAs describes an action by p for the audit log, for work that runs outside a request.
func auditAs(p *auth.Principal, accountID, action, target string, details map[string]any, folders ...string) *store.AuditEntry {
	return &store.AuditEntry{Time: store.Now(), AccountID: accountID, ActorKind: string(p.Kind), ActorID: p.ID,
		Action: action, Target: target, Details: delegated(p, details), Folders: folders}
}

// sharingKept reports whether a node placed in the directory parentID keeps editors_can_share: the
// caller must manage that directory. It reads inside the transaction, so a role revoked meanwhile counts.
func sharingKept(ctx context.Context, tx *store.Tx, p *auth.Principal, parentID string) (bool, error) {
	_, role, err := nodeAccessWith(ctx, tx, p, parentID)
	if err != nil {
		return false, err
	}
	return store.RoleRank(role) >= store.RoleRank(store.RoleManager), nil
}
