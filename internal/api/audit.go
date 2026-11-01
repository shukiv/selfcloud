// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

type auditJSON struct {
	ID        string         `json:"id"`
	Time      time.Time      `json:"time"`
	Actor     actorJSON      `json:"actor"`
	Action    string         `json:"action"`
	Target    string         `json:"target"`
	IP        string         `json:"ip"`
	UserAgent string         `json:"user_agent"`
	Outcome   string         `json:"outcome"`
	Details   map[string]any `json:"details,omitempty"`
}

type actorJSON struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	// The account key reads everything. A user reads what is not private to a personal folder, and
	// what is private to itself.
	p := principalOf(r)
	q := r.URL.Query()
	filter := store.AuditFilter{Action: q.Get("action"), ActorID: q.Get("actor")}
	if len(filter.Action) > 100 || len(filter.ActorID) > 100 {
		return apierr.New(apierr.InvalidRequest, "action and actor may be at most 100 characters")
	}
	entries, next, err := s.store.AuditLogFiltered(r.Context(), acc.ID, p.UserID, p.Kind == auth.KindAccountKey, filter, page)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[auditJSON]{Items: []auditJSON{}, NextCursor: next}
	for _, e := range entries {
		out.Items = append(out.Items, auditJSON{
			ID: strconv.FormatInt(e.ID, 10), Time: e.Time, Actor: actorJSON{e.ActorKind, e.ActorID},
			Action: e.Action, Target: e.Target, IP: e.IP, UserAgent: e.UserAgent, Outcome: e.Outcome,
			Details: e.Details,
		})
	}
	s.nameActors(r.Context(), out.Items)
	writeJSON(w, http.StatusOK, out)
	return nil
}

// adminOnlyActions are the actions only an installation admin performs, so their rows are the admin's even when the
// admin no longer exists.
var adminOnlyActions = map[string]bool{
	"impersonation.start":    true,
	"impersonation.end":      true,
	"admin.password_refused": true,
	"user.password_set":      true,
}

// nameActors gives each row the names the viewers need. The tenant's people are looked up by the viewer, but an
// installation admin belongs to no account, so the viewer cannot find it: the row says it is an admin and gives its
// name (decision 41). A row made while impersonating also names the admin in impersonated_by_name. A deleted admin has
// no name, and the viewers say "an installation admin" for it.
func (s *Server) nameActors(ctx context.Context, items []auditJSON) {
	type named struct {
		admin bool
		name  string
	}
	seen := map[string]named{}
	lookup := func(id string) named {
		if v, ok := seen[id]; ok {
			return v
		}
		var v named
		if u, err := s.store.User(ctx, "", id); err == nil {
			v = named{admin: true, name: u.DisplayName}
			if v.name == "" {
				v.name = u.Email
			}
		}
		seen[id] = v
		return v
	}
	for i := range items {
		it := &items[i]
		if it.Details == nil {
			it.Details = map[string]any{}
		}
		if it.Actor.Kind == "user" && it.Actor.ID != "" {
			if v := lookup(it.Actor.ID); v.admin || adminOnlyActions[it.Action] {
				it.Details["actor_admin"] = true
				it.Details["actor_name"] = v.name
			}
		}
		if id, _ := it.Details["impersonated_by"].(string); id != "" {
			it.Details["impersonated_by_name"] = lookup(id).name
		}
	}
}
