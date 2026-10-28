package api

import (
	"net/http"
	"strconv"
	"time"
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
	entries, next, err := s.store.AuditLog(r.Context(), acc.ID, page)
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
	writeJSON(w, http.StatusOK, out)
	return nil
}
