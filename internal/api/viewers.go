// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// Who viewed a file (design section 14, slice r5, decision 51). The list is the recent table's view rows for one node:
// one line per user, the user's last view, from the last 30 days. Only a manager of the node and the account key read it,
// and only while the account's who-viewed switch is on. A delegated token never reads it, and an installation admin is
// refused by the caller check. A person is named while that person reaches the node; a person who no longer does is gone,
// and has no name. A person past the walk bound has neither a name nor the gone flag, since nobody checked.

// viewerJSON is one line of the list. UserID is the person's ID, which the people and permission answers also show to a
// manager. Name is set while the person reaches the node. Gone is set when the person no longer reaches it.
type viewerJSON struct {
	UserID   string    `json:"user_id"`
	Name     string    `json:"name,omitempty"`
	Gone     bool      `json:"gone,omitempty"`
	ViewedAt time.Time `json:"viewed_at"`
}

// viewersCaller refuses the callers that never read who viewed a file: no caller, an installation admin (by the caller
// check), and every delegated token, whoever minted it. A token is not the account key, even when an account key minted
// it, so it gets no more than a user's token gets.
func viewersCaller(p *auth.Principal) error {
	if err := callerCheck(p); err != nil {
		return err
	}
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot read who viewed a file; only a manager or the account key can")
	}
	return nil
}

// handleNodeViewers lists who viewed a file, newest view first, a page at a time.
func (s *Server) handleNodeViewers(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := viewersCaller(p); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	if err := s.chargeAll(charge{"viewers", string(p.Kind) + ":" + p.ID}, charge{"viewers.account", p.AccountID}); err != nil {
		return err
	}
	ctx := r.Context()
	n, role, err := nodeAccessWith(ctx, s.store, p, nodeID)
	if err != nil {
		return err
	}
	// A shortcut has no views of its own, as it has no comments or history (decision 39, decision 50).
	if n.Kind == store.KindShortcut {
		return nodeNotFound()
	}
	if p.Kind != auth.KindAccountKey && store.RoleRank(role) < store.RoleRank(store.RoleManager) {
		return apierr.New(apierr.Forbidden, "only a manager of the file or the account key can read who viewed it")
	}
	on, since, err := s.store.WhoViewedSince(ctx, n.AccountID)
	if err != nil {
		return err
	}
	if !on {
		return apierr.New(apierr.Forbidden, "who-viewed is off for this account; the account owner turns it on")
	}
	// One row more than the page holds tells whether a next page exists, without a second query.
	ahead := page
	ahead.Limit++
	// The list starts when the switch went on, or 30 days back if that is later: a view made while the switch was off
	// was made with nobody told it would be shown (decision 51).
	from := time.Now().Add(-store.ViewerWindow)
	if since.After(from) {
		from = since
	}
	rows, err := s.store.ViewersPage(ctx, n.AccountID, nodeID, from, ahead)
	if err != nil {
		return storeErr(err)
	}
	var next string
	if len(rows) > page.Limit {
		rows = rows[:page.Limit]
		next = store.ViewerCursor(rows[len(rows)-1])
	}
	ids := make([]string, len(rows))
	for i, rw := range rows {
		ids[i] = rw.UserID
	}
	walks := maxActivityWalks
	if s.activityWalks > 0 {
		walks = s.activityWalks
	}
	people, err := s.peopleFor(ctx, p, n.AccountID, nodeID, ids, walks)
	if err != nil {
		return err
	}
	out := listResponse[viewerJSON]{Items: make([]viewerJSON, 0, len(rows)), NextCursor: next}
	for _, rw := range rows {
		it := viewerJSON{UserID: rw.UserID, ViewedAt: rw.ViewedAt}
		switch {
		case people.reach[rw.UserID]:
			it.Name = people.names[rw.UserID]
		case !people.unchecked[rw.UserID]:
			it.Gone = true
		}
		out.Items = append(out.Items, it)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
