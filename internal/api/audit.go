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
	s.nameImpersonators(r.Context(), out.Items)
	writeJSON(w, http.StatusOK, out)
	return nil
}

// nameImpersonators adds the installation admin's name to each row a sign-in-as made, so an owner sees who acted and
// that it was done signed in as the user (decision 41). The name is empty when the admin no longer exists.
func (s *Server) nameImpersonators(ctx context.Context, items []auditJSON) {
	names := map[string]string{}
	for i := range items {
		id, _ := items[i].Details["impersonated_by"].(string)
		if id == "" {
			continue
		}
		name, seen := names[id]
		if !seen {
			if u, err := s.store.User(ctx, "", id); err == nil {
				name = u.DisplayName
				if name == "" {
					name = u.Email
				}
			}
			names[id] = name
		}
		items[i].Details["impersonated_by_name"] = name
	}
}
