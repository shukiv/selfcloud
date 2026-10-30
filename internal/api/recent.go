// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// recentItem is a node in the recent list, with the caller's last view and edit of it.
type recentItem struct {
	nodeJSON
	ViewedAt *time.Time `json:"viewed_at"`
	EditedAt *time.Time `json:"edited_at"`
	LastAt   time.Time  `json:"last_at"`
}

// handleListRecent lists the caller's recent items, the latest first. As for stars, an item the caller no longer
// reaches or that is in the trash is kept in the database and not listed, so a page can come back shorter.
func (s *Server) handleListRecent(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := userCaller(p, "a recent list"); err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	rows, err := s.store.RecentPage(ctx, p.UserID, page)
	if err != nil {
		return storeErr(err)
	}
	ids := make([]string, len(rows))
	for i, rw := range rows {
		ids[i] = rw.NodeID
	}
	reach, err := reachRoles(ctx, s.store, p, ids)
	if err != nil {
		return err
	}
	nodes, err := s.store.NodesByID(ctx, p.AccountID, ids)
	if err != nil {
		return err
	}
	var items []recentItem
	for _, rw := range rows {
		n, role := nodes[rw.NodeID], reach[rw.NodeID]
		if n == nil || role == "" {
			continue
		}
		j := toNodeJSON(n)
		j.Role = role
		items = append(items, recentItem{nodeJSON: j, ViewedAt: rw.ViewedAt, EditedAt: rw.EditedAt, LastAt: rw.LastAt})
	}
	kept := make([]*nodeJSON, len(items))
	for i := range items {
		kept[i] = &items[i].nodeJSON
	}
	if err := s.withStarred(r, p.UserID, kept); err != nil {
		return err
	}
	out := listResponse[recentItem]{Items: []recentItem{}}
	out.Items = append(out.Items, items...)
	if len(rows) == page.Limit {
		out.NextCursor = store.RecentCursor(rows[len(rows)-1])
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleClearRecent empties the caller's recent list.
func (s *Server) handleClearRecent(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := ownListCaller(p); err != nil {
		return err
	}
	s.atWrite()
	err := s.store.Write(r.Context(), func(tx *store.Tx) error {
		return tx.DeleteAllRecent(r.Context(), p.UserID)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleRemoveRecent removes one entry from the caller's recent list. It answers 204 whether or not the entry was
// there, and it does not check reach: the entry is the caller's own, and removing it reveals nothing. Only the
// signed-in user may do it; a token may not.
func (s *Server) handleRemoveRecent(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := ownListCaller(p); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	s.atWrite()
	err := s.store.Write(r.Context(), func(tx *store.Tx) error {
		return tx.DeleteRecent(r.Context(), p.UserID, nodeID)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
