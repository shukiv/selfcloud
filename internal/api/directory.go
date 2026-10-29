// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// Directory search bounds: the query is 2 to 100 characters, and an answer holds at most this many
// entries, users and groups together.
const (
	directoryMinQuery = 2
	directoryMaxQuery = 100
	directoryMax      = 20
)

// directoryJSON is one answer of a directory search. Email is set for a user only. Nothing else about
// a person or a group is returned: not its status, role, locale or external ID.
type directoryJSON struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // user or group
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

// handleDirectory finds people and groups of the caller's account, so a share dialog can name them. Any
// user of the account may search by session, and so may the account key itself. An installation admin is
// refused, and so is every delegated token: a token is narrowed to its folders, and the account's people
// are not among them (section 8). Only active users are found: a pending or disabled user is not listed,
// and no other account is searched.
func (s *Server) handleDirectory(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if p.Token != nil || (p.Kind != auth.KindUser && p.Kind != auth.KindAccountKey) {
		return apierr.New(apierr.Forbidden, "only a signed-in user of the account or its key may search its directory")
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if n := utf8.RuneCountInString(q); n < directoryMinQuery || n > directoryMaxQuery {
		return apierr.Newf(apierr.InvalidRequest, "q must be between %d and %d characters", directoryMinQuery, directoryMaxQuery)
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "all"
	}
	if kind != "user" && kind != "group" && kind != "all" {
		return apierr.New(apierr.InvalidRequest, "kind must be user, group or all")
	}
	if err := s.rateLimit("directory.search", string(p.Kind)+":"+p.ID); err != nil {
		return err
	}
	ctx := r.Context()
	var found []directoryJSON
	if kind == "user" || kind == "all" {
		users, err := s.store.SearchUsers(ctx, p.AccountID, q, directoryMax)
		if err != nil {
			return err
		}
		found = append(found, toDirectory(users)...)
	}
	if kind == "group" || kind == "all" {
		groups, err := s.store.SearchGroups(ctx, p.AccountID, q, directoryMax)
		if err != nil {
			return err
		}
		found = append(found, toDirectory(groups)...)
	}
	// Users and groups are merged by name, and the answer holds at most directoryMax of them.
	sort.SliceStable(found, func(i, j int) bool { return strings.ToLower(found[i].Name) < strings.ToLower(found[j].Name) })
	if len(found) > directoryMax {
		found = found[:directoryMax]
	}
	if found == nil {
		found = []directoryJSON{}
	}
	writeJSON(w, http.StatusOK, listResponse[directoryJSON]{Items: found})
	return nil
}

func toDirectory(entries []store.DirectoryEntry) []directoryJSON {
	out := make([]directoryJSON, 0, len(entries))
	for _, e := range entries {
		j := directoryJSON{ID: e.ID, Kind: "group", Name: e.Name}
		if e.Kind == store.SubjectUser {
			j.Kind = "user"
			j.Email = e.Email
		}
		out = append(out, j)
	}
	return out
}
