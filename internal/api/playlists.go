// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// The bounds of saved playlists (decision 58). The code refuses past each one.
const (
	// maxPlaylistsPerUser is how many playlists one user may hold.
	maxPlaylistsPerUser = 100
	// maxPlaylistEntries is how many entries one playlist may hold. It is the queue's own cap (MAX_QUEUE in the web
	// player), so saving a whole queue always fits.
	maxPlaylistEntries = 500
	// maxPlaylistNameRunes is the longest name, in characters.
	maxPlaylistNameRunes = 120
)

// Rate limiter names of the playlist routes (server.go sets their sizes).
const (
	playlistReadRate  = "playlist.read"
	playlistWriteRate = "playlist.write"
)

// playlistCaller is the caller of the playlist routes: the signed-in user, and no delegated token, as the media resume
// and the recent list are. A playlist is the user's own, so a token acting for the user has no part in it, and neither
// does an account key or an installation admin (impersonation is refused on each route).
func playlistCaller(p *auth.Principal) error {
	if err := userCaller(p, "a playlist"); err != nil {
		return err
	}
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token has no part in a playlist; only the signed-in user does")
	}
	return nil
}

func playlistNotFound() error { return notFound("playlist") }

// playlistJSON is a playlist without its entries, as the list and the rename answer it.
type playlistJSON struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	EntryCount int       `json:"entry_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// playlistEntryJSON is one place in a playlist. A reachable file carries its node, with the caller's role on it. An
// entry the caller cannot reach, whose node was purged or trashed, or which is not a file, carries only its ID, its
// position and the node ID the caller gave, and unavailable is true. Those cases answer alike, so the answer does not
// say which one it is.
type playlistEntryJSON struct {
	EntryID     string    `json:"entry_id"`
	Position    int       `json:"position"`
	NodeID      string    `json:"node_id"`
	Unavailable bool      `json:"unavailable"`
	Node        *nodeJSON `json:"node,omitempty"`
}

// playlistDetailJSON is one playlist with its entries in order.
type playlistDetailJSON struct {
	playlistJSON
	Entries []playlistEntryJSON `json:"entries"`
}

type playlistListJSON struct {
	Items []playlistJSON `json:"items"`
}

// playlistNameIn is a playlist's name as the body gives it: trimmed, 1 to 120 characters, and no control characters.
type playlistNameIn struct {
	Name string `json:"name"`
}

func playlistName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(name)
	if n == 0 || n > maxPlaylistNameRunes {
		return "", apierr.Newf(apierr.InvalidRequest, "name must be from 1 to %d characters", maxPlaylistNameRunes)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", apierr.New(apierr.InvalidRequest, "name may not hold control characters")
		}
	}
	return name, nil
}

func playlistJSONOf(p store.Playlist) playlistJSON {
	return playlistJSON{ID: p.ID, Name: p.Name, EntryCount: p.EntryCount, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

// playlistAudit writes an audit entry about a playlist. The entry is private to its user (PrivateUserID), so the
// account's owner does not read it; its details are counts and the change, never a name or a node.
func (s *Server) playlistAudit(ctx context.Context, tx *store.Tx, r *http.Request, p *auth.Principal, accountID,
	action, playlistID string, details map[string]any) error {
	e := s.auditEntry(r, accountID, action, playlistID, details)
	e.PrivateUserID = p.UserID
	return tx.InsertAudit(ctx, e)
}

// playlistView reads the entries of a playlist for the caller now. Reach is checked for every entry, on each read, and
// the node is read only for an entry the caller reaches.
func (s *Server) playlistView(ctx context.Context, p *auth.Principal, pl store.Playlist, entries []store.PlaylistEntry) (playlistDetailJSON, error) {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.NodeID
	}
	roles, err := reachRoles(ctx, s.store, p, ids)
	if err != nil {
		return playlistDetailJSON{}, err
	}
	nodes, err := s.store.NodesByID(ctx, p.AccountID, ids)
	if err != nil {
		return playlistDetailJSON{}, err
	}
	out := playlistDetailJSON{playlistJSON: playlistJSONOf(pl), Entries: make([]playlistEntryJSON, 0, len(entries))}
	for _, e := range entries {
		item := playlistEntryJSON{EntryID: e.ID, Position: e.Position, NodeID: e.NodeID}
		n, role := nodes[e.NodeID], roles[e.NodeID]
		if n == nil || role == "" || n.Kind != store.KindFile {
			item.Unavailable = true
		} else {
			j := toNodeJSON(n)
			j.Role = role
			item.Node = &j
		}
		out.Entries = append(out.Entries, item)
	}
	return out, nil
}

// handleListPlaylists lists the caller's playlists, oldest first. The list is whole: a user holds at most
// maxPlaylistsPerUser.
func (s *Server) handleListPlaylists(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := playlistCaller(p); err != nil {
		return err
	}
	if err := s.rateLimit(playlistReadRate, p.UserID); err != nil {
		return err
	}
	pls, err := s.store.PlaylistsOf(r.Context(), p.UserID)
	if err != nil {
		return storeErr(err)
	}
	out := playlistListJSON{Items: make([]playlistJSON, 0, len(pls))}
	for _, pl := range pls {
		out.Items = append(out.Items, playlistJSONOf(pl))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// playlistCreateIn is the body of a create: a name, and optionally the files the playlist starts with, in order.
type playlistCreateIn struct {
	Name  string   `json:"name"`
	Nodes []string `json:"nodes"`
}

// handleCreatePlaylist creates a playlist, and the files given with it (if any) in the same write, so a create that a
// file refuses leaves no playlist behind and a retry does not make a second one. A user past maxPlaylistsPerUser is
// refused with conflict and reason limit.
func (s *Server) handleCreatePlaylist(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := playlistCaller(p); err != nil {
		return err
	}
	if err := s.rateLimit(playlistWriteRate, p.UserID); err != nil {
		return err
	}
	var in playlistCreateIn
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	name, err := playlistName(in.Name)
	if err != nil {
		return err
	}
	if err := playlistNodeIDs(in.Nodes); err != nil {
		return err
	}
	ctx := r.Context()
	at := store.Now()
	pl := store.Playlist{ID: id.New("pls"), AccountID: p.AccountID, UserID: p.UserID, Name: name, CreatedAt: at, UpdatedAt: at}
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		n, err := tx.CountPlaylists(ctx, p.UserID)
		if err != nil {
			return err
		}
		if n >= maxPlaylistsPerUser {
			return apierr.Newf(apierr.Conflict, "you may keep at most %d playlists; delete some first", maxPlaylistsPerUser).
				With("reason", "limit").With("limit", maxPlaylistsPerUser)
		}
		if err := tx.InsertPlaylist(ctx, pl, at); err != nil {
			return err
		}
		// The files go in the same write: a file the caller does not reach refuses the create, and nothing is kept.
		if len(in.Nodes) > 0 {
			if err := addableIn(ctx, tx, p, in.Nodes); err != nil {
				return err
			}
			next := make([]store.PlaylistEntry, len(in.Nodes))
			for i, node := range in.Nodes {
				next[i] = store.PlaylistEntry{ID: id.New("ple"), PlaylistID: pl.ID, NodeID: node}
			}
			if err := tx.ReplacePlaylistEntries(ctx, pl.ID, next, at); err != nil {
				return err
			}
			pl.EntryCount = len(next)
		}
		return s.playlistAudit(ctx, tx, r, p, p.AccountID, "playlist.create", pl.ID, nil)
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, playlistJSONOf(pl))
	return nil
}

// playlistOfCaller reads a playlist of the caller, or answers not_found for one of another user or none.
func (s *Server) playlistOfCaller(ctx context.Context, p *auth.Principal, pid string) (store.Playlist, []store.PlaylistEntry, error) {
	if !id.Valid("pls", pid) {
		return store.Playlist{}, nil, playlistNotFound()
	}
	var pl store.Playlist
	var entries []store.PlaylistEntry
	var found bool
	err := s.store.Read(ctx, func(q store.Querier) error {
		var err error
		pl, found, err = store.PlaylistOf(ctx, q, p.UserID, pid)
		if err != nil || !found {
			return err
		}
		entries, err = store.PlaylistEntries(ctx, q, pid)
		return err
	})
	if err != nil {
		return store.Playlist{}, nil, storeErr(err)
	}
	if !found {
		return store.Playlist{}, nil, playlistNotFound()
	}
	return pl, entries, nil
}

// handleGetPlaylist reads one playlist of the caller with its entries. Every entry's reach is checked now.
func (s *Server) handleGetPlaylist(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := playlistCaller(p); err != nil {
		return err
	}
	if err := s.rateLimit(playlistReadRate, p.UserID); err != nil {
		return err
	}
	ctx := r.Context()
	pl, entries, err := s.playlistOfCaller(ctx, p, r.PathValue("pls"))
	if err != nil {
		return err
	}
	out, err := s.playlistView(ctx, p, pl, entries)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleRenamePlaylist changes a playlist's name.
func (s *Server) handleRenamePlaylist(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := playlistCaller(p); err != nil {
		return err
	}
	pid := r.PathValue("pls")
	if !id.Valid("pls", pid) {
		return playlistNotFound()
	}
	if err := s.rateLimit(playlistWriteRate, p.UserID); err != nil {
		return err
	}
	var in playlistNameIn
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	name, err := playlistName(in.Name)
	if err != nil {
		return err
	}
	ctx := r.Context()
	s.atWrite()
	var out store.Playlist
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		ok, err := tx.RenamePlaylist(ctx, p.UserID, pid, name, store.Now())
		if err != nil {
			return err
		}
		if !ok {
			return playlistNotFound()
		}
		if err := s.playlistAudit(ctx, tx, r, p, p.AccountID, "playlist.rename", pid, nil); err != nil {
			return err
		}
		got, found, err := store.PlaylistOf(ctx, tx.Querier(), p.UserID, pid)
		if err != nil {
			return err
		}
		if !found {
			return playlistNotFound()
		}
		out = got
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, playlistJSONOf(out))
	return nil
}

// handleDeletePlaylist removes a playlist and its entries. No file is touched.
func (s *Server) handleDeletePlaylist(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := playlistCaller(p); err != nil {
		return err
	}
	pid := r.PathValue("pls")
	if !id.Valid("pls", pid) {
		return playlistNotFound()
	}
	if err := s.rateLimit(playlistWriteRate, p.UserID); err != nil {
		return err
	}
	ctx := r.Context()
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		ok, err := tx.DeletePlaylist(ctx, p.UserID, pid)
		if err != nil {
			return err
		}
		if !ok {
			return playlistNotFound()
		}
		return s.playlistAudit(ctx, tx, r, p, p.AccountID, "playlist.delete", pid, nil)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// playlistNodeIDs checks the node IDs of a body: a well-formed ID of a node of this kind of ID. A malformed ID answers as
// a node the caller does not reach does, so the answer is the same for every node that is not addable.
func playlistNodeIDs(nodes []string) error {
	if len(nodes) > maxPlaylistEntries {
		return apierr.Newf(apierr.InvalidRequest, "a playlist holds at most %d entries", maxPlaylistEntries)
	}
	seen := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if !id.Valid("nod", n) {
			return nodeNotFound()
		}
		if seen[n] {
			return apierr.New(apierr.InvalidRequest, "a file may be in a playlist only once")
		}
		seen[n] = true
	}
	return nil
}

// addableIn checks, inside a write, that the caller reaches each node now and that it is a file. Any node it refuses
// answers the same nodeNotFound, whether the node is missing, of another account, trashed, or out of the caller's reach.
func addableIn(ctx context.Context, tx *store.Tx, p *auth.Principal, nodes []string) error {
	if len(nodes) == 0 {
		return nil
	}
	roles, err := reachRolesIn(ctx, tx.Querier(), p, nodes, "")
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if roles[n] == "" {
			return nodeNotFound()
		}
		node, err := tx.Node(ctx, p.AccountID, n)
		if errors.Is(err, store.ErrNotFound) {
			return nodeNotFound()
		}
		if err != nil {
			return err
		}
		if node.Kind != store.KindFile {
			return nodeNotFound()
		}
	}
	return nil
}

// playlistEntryIn is the body of an append.
type playlistEntryIn struct {
	NodeID string `json:"node_id"`
}

// handleAddPlaylistEntry appends one file to a playlist. Adding a file the caller already holds in the playlist changes
// nothing. The reach of the file is checked now, and an unreachable, missing or non-file node answers not_found.
func (s *Server) handleAddPlaylistEntry(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := playlistCaller(p); err != nil {
		return err
	}
	pid := r.PathValue("pls")
	if !id.Valid("pls", pid) {
		return playlistNotFound()
	}
	if err := s.rateLimit(playlistWriteRate, p.UserID); err != nil {
		return err
	}
	var in playlistEntryIn
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if !id.Valid("nod", in.NodeID) {
		return nodeNotFound()
	}
	ctx := r.Context()
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		_, found, err := store.PlaylistOf(ctx, tx.Querier(), p.UserID, pid)
		if err != nil {
			return err
		}
		if !found {
			return playlistNotFound()
		}
		// The reach is checked before anything else, so a node the caller cannot reach answers the same whether or not
		// the playlist already holds it.
		if err := addableIn(ctx, tx, p, []string{in.NodeID}); err != nil {
			return err
		}
		entries, err := store.PlaylistEntries(ctx, tx.Querier(), pid)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.NodeID == in.NodeID {
				return nil
			}
		}
		if len(entries) >= maxPlaylistEntries {
			return apierr.Newf(apierr.Conflict, "a playlist holds at most %d entries", maxPlaylistEntries).
				With("reason", "limit").With("limit", maxPlaylistEntries)
		}
		if err := tx.AppendPlaylistEntry(ctx, store.PlaylistEntry{ID: id.New("ple"), PlaylistID: pid, NodeID: in.NodeID}, store.Now()); err != nil {
			return err
		}
		if err := tx.TouchPlaylist(ctx, pid, store.Now()); err != nil {
			return err
		}
		return s.playlistAudit(ctx, tx, r, p, p.AccountID, "playlist.entries", pid,
			map[string]any{"change": "add", "count": len(entries) + 1})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// playlistReplaceIn is the body of a replace: the node IDs, in order.
type playlistReplaceIn struct {
	Nodes []string `json:"nodes"`
}

// handleReplacePlaylistEntries sets a playlist's entries to the nodes given, in order. A node the playlist already
// holds is kept as it is, with no reach check, so an entry that became unavailable can still be kept, reordered or
// removed. Each node that is new to the playlist must be reachable and a file, or the whole write is refused with
// not_found.
func (s *Server) handleReplacePlaylistEntries(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := playlistCaller(p); err != nil {
		return err
	}
	pid := r.PathValue("pls")
	if !id.Valid("pls", pid) {
		return playlistNotFound()
	}
	if err := s.rateLimit(playlistWriteRate, p.UserID); err != nil {
		return err
	}
	var in playlistReplaceIn
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := playlistNodeIDs(in.Nodes); err != nil {
		return err
	}
	ctx := r.Context()
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		_, found, err := store.PlaylistOf(ctx, tx.Querier(), p.UserID, pid)
		if err != nil {
			return err
		}
		if !found {
			return playlistNotFound()
		}
		current, err := store.PlaylistEntries(ctx, tx.Querier(), pid)
		if err != nil {
			return err
		}
		have := make(map[string]store.PlaylistEntry, len(current))
		for _, e := range current {
			have[e.NodeID] = e
		}
		var fresh []string
		for _, n := range in.Nodes {
			if _, ok := have[n]; !ok {
				fresh = append(fresh, n)
			}
		}
		if err := addableIn(ctx, tx, p, fresh); err != nil {
			return err
		}
		next := make([]store.PlaylistEntry, 0, len(in.Nodes))
		for _, n := range in.Nodes {
			e, ok := have[n]
			if !ok {
				e = store.PlaylistEntry{ID: id.New("ple"), PlaylistID: pid, NodeID: n}
			}
			next = append(next, e)
		}
		if err := tx.ReplacePlaylistEntries(ctx, pid, next, store.Now()); err != nil {
			return err
		}
		if err := tx.TouchPlaylist(ctx, pid, store.Now()); err != nil {
			return err
		}
		return s.playlistAudit(ctx, tx, r, p, p.AccountID, "playlist.entries", pid,
			map[string]any{"change": "replace", "count": len(next)})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleRemovePlaylistEntry removes one entry. It works for an unavailable entry too: the entry's own ID is all it needs.
func (s *Server) handleRemovePlaylistEntry(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := playlistCaller(p); err != nil {
		return err
	}
	pid, eid := r.PathValue("pls"), r.PathValue("ple")
	if !id.Valid("pls", pid) || !id.Valid("ple", eid) {
		return playlistNotFound()
	}
	if err := s.rateLimit(playlistWriteRate, p.UserID); err != nil {
		return err
	}
	ctx := r.Context()
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		_, found, err := store.PlaylistOf(ctx, tx.Querier(), p.UserID, pid)
		if err != nil {
			return err
		}
		if !found {
			return playlistNotFound()
		}
		ok, err := tx.DeletePlaylistEntry(ctx, pid, eid)
		if err != nil {
			return err
		}
		if !ok {
			return notFound("entry")
		}
		if err := tx.TouchPlaylist(ctx, pid, store.Now()); err != nil {
			return err
		}
		return s.playlistAudit(ctx, tx, r, p, p.AccountID, "playlist.entries", pid, map[string]any{"change": "remove"})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// playlistOrderIn is the body of a reorder: every entry ID of the playlist, in the new order.
type playlistOrderIn struct {
	Entries []string `json:"entries"`
}

// handleReorderPlaylistEntries sets the order of a playlist. The IDs must be exactly the playlist's entries, each once.
func (s *Server) handleReorderPlaylistEntries(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := playlistCaller(p); err != nil {
		return err
	}
	pid := r.PathValue("pls")
	if !id.Valid("pls", pid) {
		return playlistNotFound()
	}
	if err := s.rateLimit(playlistWriteRate, p.UserID); err != nil {
		return err
	}
	var in playlistOrderIn
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		_, found, err := store.PlaylistOf(ctx, tx.Querier(), p.UserID, pid)
		if err != nil {
			return err
		}
		if !found {
			return playlistNotFound()
		}
		current, err := store.PlaylistEntries(ctx, tx.Querier(), pid)
		if err != nil {
			return err
		}
		want := make(map[string]bool, len(current))
		for _, e := range current {
			want[e.ID] = true
		}
		seen := make(map[string]bool, len(in.Entries))
		for _, e := range in.Entries {
			if !want[e] || seen[e] {
				return apierr.New(apierr.InvalidRequest, "entries must name each entry of the playlist once, in its new order")
			}
			seen[e] = true
		}
		if len(in.Entries) != len(current) {
			return apierr.New(apierr.InvalidRequest, "entries must name each entry of the playlist once, in its new order")
		}
		if err := tx.SetPlaylistPositions(ctx, pid, in.Entries); err != nil {
			return err
		}
		if err := tx.TouchPlaylist(ctx, pid, store.Now()); err != nil {
			return err
		}
		return s.playlistAudit(ctx, tx, r, p, p.AccountID, "playlist.entries", pid,
			map[string]any{"change": "reorder", "count": len(current)})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
