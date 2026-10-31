// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Breaking a lock (decision 32). A manager of a file, an owner of the account and the account key may end the office
// editor's lock on it. The lock ends at once, and the holder's next refresh or save then answers with no lock on the
// file, so the holder never overwrites what came after the break. The break is audited as node.unlock with reason
// "broken" and the caller. No delegated token and no share link breaks a lock: their kinds are refused before the role
// is read. The WOPI routes do not take this route, so an editor's own token cannot break its lock.

import (
	"errors"
	"net/http"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// handleBreakNodeLock ends the live lock on the file nod. It answers 204, or 409 when no editor holds the file.
func (s *Server) handleBreakNodeLock(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if p.Token != nil || p.Kind == auth.KindShareLink {
		return apierr.New(apierr.Forbidden, "only a user or the account key may break a lock")
	}
	nodeID := r.PathValue("nod")
	ctx := r.Context()
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := requireNodeRole(ctx, tx, p, nodeID, store.RoleManager)
		if err != nil {
			return err
		}
		if cur.Kind != store.KindFile {
			return notFound("node")
		}
		l, err := tx.BreakLock(ctx, cur.AccountID, cur.ID)
		if errors.Is(err, store.ErrNotFound) {
			return apierr.New(apierr.Conflict, "no editor holds this file")
		}
		if err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.unlock", cur.ID,
			map[string]any{"reason": "broken", "holder": l.Holder}, cur.FolderID))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
