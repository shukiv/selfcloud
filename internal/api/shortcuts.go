// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
)

// shortcutTarget is what a shortcut points to, as a caller who reaches it sees it.
type shortcutTarget struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

// withTargets sets the target of each shortcut among items that the caller reaches, and leaves every other shortcut
// unavailable. A shortcut grants nothing: the target is shown only under the caller's own access to it, which is the
// same check a direct read makes. A signed-in user and a delegated token acting for one use one batched reach query;
// an account key reaches every node of its account that is not trashed, so each target is checked on its own.
// Visitors and admins get no target at all.
func (s *Server) withTargets(ctx context.Context, p *auth.Principal, items ...*nodeJSON) error {
	if p == nil {
		return nil
	}
	var ids []string
	seen := map[string]bool{}
	for _, it := range items {
		if it.Kind == store.KindShortcut && it.targetID != "" && !seen[it.targetID] {
			seen[it.targetID] = true
			ids = append(ids, it.targetID)
		}
	}
	reached := map[string]string{} // target ID -> its kind, for the targets the caller reaches
	if len(ids) > 0 {
		switch p.Kind {
		case auth.KindUser:
			roles, err := reachRoles(ctx, s.store, p, ids)
			if err != nil {
				return err
			}
			var reachable []string
			for id := range roles {
				reachable = append(reachable, id)
			}
			nodes, err := s.store.NodesByID(ctx, p.AccountID, reachable)
			if err != nil {
				return err
			}
			for _, id := range reachable {
				if n := nodes[id]; n != nil {
					reached[id] = n.Kind
				}
			}
		case auth.KindAccountKey:
			for _, id := range ids {
				n, _, err := nodeAccessWith(ctx, s.store, p, id)
				if err != nil {
					if apierr.From(err).Code == apierr.NotFound {
						continue
					}
					return err
				}
				reached[id] = n.Kind
			}
		}
	}
	for _, it := range items {
		if it.Kind != store.KindShortcut || it.targetID == "" {
			continue
		}
		if kind, ok := reached[it.targetID]; ok {
			it.Target = &shortcutTarget{ID: it.targetID, Kind: kind}
			it.Unavailable = false
		}
	}
	return nil
}

// shortcutBody is the body of POST /nodes/{nod}/shortcuts.
type shortcutBody struct {
	Target string `json:"target"`
	Name   string `json:"name"`
}

// handleCreateShortcut makes a shortcut in a directory. The caller needs contributor on the directory and must reach
// the target. A target that is missing, in another account, or out of the caller's reach is one refusal, not_found.
// The target is a file or a directory: never a shortcut, never a folder's root. The name defaults to the target's
// name. The shortcut takes no quota: it has no size and is not counted.
func (s *Server) handleCreateShortcut(w http.ResponseWriter, r *http.Request) error {
	var in shortcutBody
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := validID("nod", in.Target); err != nil {
		return err
	}
	var nameIn, keyIn string
	if in.Name != "" {
		var err error
		if nameIn, keyIn, err = names.Clean(in.Name); err != nil {
			return err
		}
	}
	ctx := r.Context()
	p := principalOf(r)
	dir, err := s.nodeOf(r, r.PathValue("nod"), store.RoleContributor)
	if err != nil {
		return err
	}
	if dir.Kind != store.KindDir {
		return apierr.New(apierr.InvalidRequest, "a shortcut goes in a directory")
	}
	target, _, err := nodeAccessWith(ctx, s.store, p, in.Target)
	if err != nil {
		return err
	}
	if err := checkShortcutTarget(target); err != nil {
		return err
	}
	var out *store.Node
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Node(ctx, dir.AccountID, dir.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nodeNotFound()
		}
		if err != nil {
			return err
		}
		if _, err := requireNodeRole(ctx, tx, p, cur.ID, store.RoleContributor); err != nil {
			return err
		}
		if cur.Kind != store.KindDir {
			return apierr.New(apierr.InvalidRequest, "a shortcut goes in a directory")
		}
		// The target is checked again inside the write, so a grant revoked during the request stops the shortcut.
		tgt, _, err := nodeAccessWith(ctx, tx, p, in.Target)
		if err != nil {
			return err
		}
		if err := checkShortcutTarget(tgt); err != nil {
			return err
		}
		name, key := nameIn, keyIn
		if name == "" {
			name, key = tgt.Name, tgt.NameKey
		}
		if taken, err := nameTakenBy(tx.Child(ctx, cur.AccountID, cur.ID, key)); err != nil {
			return err
		} else if taken != nil {
			return nameConflict(taken)
		}
		owner, err := ownerIn(ctx, tx, cur.AccountID, cur.FolderID, p.ID)
		if err != nil {
			return err
		}
		now := store.Now()
		n := &store.Node{ID: id.New("nod"), AccountID: cur.AccountID, FolderID: cur.FolderID, ParentID: cur.ID,
			Name: name, NameKey: key, Kind: store.KindShortcut, CreatedAt: now, ModifiedAt: now,
			CreatedBy: p.ID, ModifiedBy: p.ID, OwnerID: owner, TargetID: tgt.ID}
		if err := tx.InsertNode(ctx, n); err != nil {
			return storeNameErr(err)
		}
		out = n
		return tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.shortcut", n.ID,
			map[string]any{"name": name, "parent": cur.ID}, cur.FolderID))
	})
	if err != nil {
		return err
	}
	j := toNodeJSON(out)
	if err := s.withTargets(ctx, p, &j); err != nil {
		return err
	}
	w.Header().Set("Location", "/api/v1/nodes/"+out.ID)
	writeJSON(w, http.StatusCreated, j)
	return nil
}

// checkShortcutTarget refuses a target a shortcut may not point to: another shortcut, and a folder's root.
func checkShortcutTarget(t *store.Node) error {
	switch {
	case t.Kind == store.KindShortcut:
		return apierr.New(apierr.InvalidRequest, "a shortcut cannot point to another shortcut; point to its target")
	case t.IsRoot():
		return apierr.New(apierr.InvalidRequest, "a folder's root cannot be a shortcut target; point to a directory in it")
	}
	return nil
}

// cleanDescription returns the description to store: control characters are removed except line breaks, and the text
// is at most store.MaxDescriptionChars characters. An empty result means no description. The text is never rendered
// as HTML; it is stored and sent as text.
func cleanDescription(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", apierr.New(apierr.InvalidRequest, "the description must be valid UTF-8 text")
	}
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\r' || (!unicode.IsControl(r) && !isBidiControl(r)) {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if utf8.RuneCountInString(out) > store.MaxDescriptionChars {
		return "", apierr.Newf(apierr.InvalidRequest, "a description may be at most %d characters", store.MaxDescriptionChars)
	}
	return out, nil
}

// isBidiControl reports the bidirectional embedding, override and isolate controls: they reorder the text a reader
// sees, so a description must not carry them. The joiners and direction marks (ZWJ, ZWNJ, LRM, RLM) are kept, since
// Hebrew, Arabic and Persian text needs them.
func isBidiControl(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069)
}

// orNil returns s, or nil for "", so an audit entry shows a cleared field as null.
func orNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// optString is a JSON value that may be absent, null, or a string. A present null clears the field.
type optString struct {
	set, null bool
	val       string
}

// UnmarshalJSON records that the field was given, whether it is null or a string.
func (o *optString) UnmarshalJSON(b []byte) error {
	o.set = true
	if string(b) == "null" {
		o.null = true
		return nil
	}
	return json.Unmarshal(b, &o.val)
}

// colorOf returns the palette colour a PATCH sets: "" when it clears, and an error for a name off the palette.
func colorOf(o optString) (string, error) {
	// An empty string clears the colour, as null does.
	if o.null || o.val == "" {
		return "", nil
	}
	if !store.ValidColor(o.val) {
		return "", apierr.New(apierr.InvalidRequest, "the colour must be one of the palette names")
	}
	return o.val, nil
}
