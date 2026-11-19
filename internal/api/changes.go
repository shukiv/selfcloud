// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// The changes feed (slice cf1, decision 235) reports what changed in a space since a token. A client asks /changes/start
// for a cursor at the journal's head, then reads /changes after it. The journal rows fold to one entry per node, carrying
// the node as a stat answers it, or removed when it is gone, trashed, or has left the space. A read may wait for the next
// write rather than answer empty at once.

// changesReadEvery bounds how often a held wait re-reads the journal, so a burst of writes does not spin the reader.
const changesReadEvery = 250 * time.Millisecond

// changesMaxWaits is the most long polls one caller holds at once; a further wait answers at once.
const changesMaxWaits = 4

// changesCursorPrefix marks a changes cursor. The body is base64url of the feed id and the seq it names; it is opaque to
// the client and carries no authority, since the feed itself is access-checked on every read.
const changesCursorPrefix = "c1_"

type changesCursor struct {
	S string `json:"s"`
	Q int64  `json:"q"`
}

func encodeChangesCursor(feed string, seq int64) string {
	b, _ := json.Marshal(changesCursor{S: feed, Q: seq})
	return changesCursorPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// decodeChangesCursor reads a cursor, or refuses it as invalid. A cursor of another feed is refused by the caller, which
// compares the feed id it names with the feed the request reads.
func decodeChangesCursor(tok string) (changesCursor, error) {
	body, ok := strings.CutPrefix(tok, changesCursorPrefix)
	if !ok {
		return changesCursor{}, apierr.New(apierr.InvalidRequest, "cursor is not valid for this feed")
	}
	b, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return changesCursor{}, apierr.New(apierr.InvalidRequest, "cursor is not valid for this feed")
	}
	var c changesCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return changesCursor{}, apierr.New(apierr.InvalidRequest, "cursor is not valid for this feed")
	}
	return c, nil
}

// changeEntry is one folded change: the node as a stat answers it while it is live, and removed with no node once it is
// gone, trashed, or out of the space. Relist marks a restore whose children's own rows did not change.
type changeEntry struct {
	NodeID  string    `json:"node_id"`
	Removed bool      `json:"removed"`
	Relist  bool      `json:"relist"`
	Node    *nodeJSON `json:"node,omitempty"`
}

// changesResponse is a page of the feed. Changes is never null; Cursor names the last raw row read, and HasMore says
// rows remain after it.
type changesResponse struct {
	Changes []changeEntry `json:"changes"`
	Cursor  string        `json:"cursor"`
	HasMore bool          `json:"has_more"`
}

// changesFeed resolves the feed a call reads and returns the id its cursor names. A space feed needs reach on the
// folder's root; a staged, missing or unreachable folder is not_found. The account feed (space "") is the bare account
// key's alone: a session, a delegated token, or a token acting as its user is refused.
func (s *Server) changesFeed(r *http.Request, p *auth.Principal, space string) (string, error) {
	if space == "" {
		if p.Kind != auth.KindAccountKey || p.Token != nil {
			return "", apierr.New(apierr.InvalidRequest, "space is required")
		}
		return p.AccountID, nil
	}
	if !id.Valid("fld", space) {
		return "", nodeNotFound()
	}
	ctx := r.Context()
	// Folder reads only live folders, so a staged or missing space is not_found here.
	if _, err := s.store.Folder(ctx, p.AccountID, space); err != nil {
		return "", nodeNotFound()
	}
	root, err := s.store.RootNode(ctx, p.AccountID, space)
	if err != nil {
		return "", nodeNotFound()
	}
	if _, _, err := nodeAccessWith(ctx, s.store, p, root.ID); err != nil {
		return "", err
	}
	return space, nil
}

// handleChangesStart answers a cursor at the journal's head, the point a client reads changes after.
func (s *Server) handleChangesStart(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if err := s.rateLimit("changes", string(p.Kind)+":"+p.ID); err != nil {
		return err
	}
	feed, err := s.changesFeed(r, p, r.URL.Query().Get("space"))
	if err != nil {
		return err
	}
	seq, err := s.store.NodeChangesMaxSeq(r.Context())
	if err != nil {
		return storeErr(err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"cursor": encodeChangesCursor(feed, seq)})
	return nil
}

// handleChanges reads a page of the feed after a cursor, optionally waiting for the next write.
func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if err := s.rateLimit("changes", string(p.Kind)+":"+p.ID); err != nil {
		return err
	}
	q := r.URL.Query()
	feed, err := s.changesFeed(r, p, q.Get("space"))
	if err != nil {
		return err
	}
	cur, err := decodeChangesCursor(q.Get("cursor"))
	if err != nil {
		return err
	}
	if cur.S != feed {
		return apierr.New(apierr.InvalidRequest, "cursor is not valid for this feed")
	}
	limit, err := changesBound(q.Get("limit"), 200, 1, 1000)
	if err != nil {
		return err
	}
	waitSecs, err := changesBound(q.Get("wait"), 0, 0, 60)
	if err != nil {
		return err
	}
	mark, err := s.store.NodeChangesMark(r.Context())
	if err != nil {
		return storeErr(err)
	}
	if cur.Q < mark {
		return apierr.New(apierr.Gone, "the cursor is too old; start again").With("reason", "reset")
	}
	return s.changesWaitLoop(w, r, p, feed, q.Get("space"), cur.Q, limit, time.Duration(waitSecs)*time.Second, q.Get("cursor"))
}

// changesBound reads an integer query value with a default and inclusive bounds, refusing a value outside them.
func changesBound(raw string, def, lo, hi int) (int, error) {
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, apierr.New(apierr.InvalidRequest, "value is out of range")
	}
	return n, nil
}

// acquireChangesWaiter takes a long-poll slot for the caller, unless it already holds four or the server holds its cap.
// A caller refused a slot answers its read at once instead of waiting.
func (s *Server) acquireChangesWaiter(key string, max int) bool {
	s.changesMu.Lock()
	defer s.changesMu.Unlock()
	if s.changesWaiters[key] >= changesMaxWaits || s.changesWaiting >= max {
		return false
	}
	s.changesWaiters[key]++
	s.changesWaiting++
	return true
}

func (s *Server) releaseChangesWaiter(key string) {
	s.changesMu.Lock()
	defer s.changesMu.Unlock()
	s.changesWaiters[key]--
	if s.changesWaiters[key] <= 0 {
		delete(s.changesWaiters, key)
	}
	s.changesWaiting--
}

// changesWaitLoop reads the feed, and while it is empty and a wait is held, waits for a write to commit, the deadline,
// the client to go, or the server to drain. It re-reads at most every changesReadEvery, and answers empty at the
// deadline with the cursor unchanged.
func (s *Server) changesWaitLoop(w http.ResponseWriter, r *http.Request, p *auth.Principal, feed, space string,
	after int64, limit int, wait time.Duration, curTok string) error {
	ctx := r.Context()
	key := string(p.Kind) + ":" + p.ID
	holding := wait > 0 && s.acquireChangesWaiter(key, s.cfg.Changes.MaxWaiters)
	var sub <-chan struct{}
	if holding {
		var stop func()
		sub, stop = s.store.WatchWrites()
		defer stop()
		defer s.releaseChangesWaiter(key)
	}
	deadline := time.Now().Add(wait)
	for {
		out, err := s.readChanges(r, p, feed, space, after, limit, curTok)
		if err != nil {
			return err
		}
		lastRead := time.Now()
		if len(out.Changes) > 0 || !holding {
			writeJSON(w, http.StatusOK, out)
			return nil
		}
		if !time.Now().Before(deadline) {
			writeJSON(w, http.StatusOK, out)
			return nil
		}
		throttle := time.Until(lastRead.Add(changesReadEvery))
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-sub:
			timer.Stop()
			if throttle > 0 {
				t := time.NewTimer(throttle)
				select {
				case <-t.C:
				case <-ctx.Done():
					t.Stop()
					return ctx.Err()
				case <-s.changesStop:
					t.Stop()
					writeJSON(w, http.StatusOK, out)
					return nil
				}
			}
		case <-timer.C:
			// The deadline passed: the next read answers empty if still nothing changed.
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-s.changesStop:
			timer.Stop()
			writeJSON(w, http.StatusOK, out)
			return nil
		}
	}
}

// readChanges reads limit+1 journal rows after seq, folds them to one entry per node ordered by the node's latest row,
// and decorates the live nodes as a stat does. The cursor advances to the last raw row read; with no rows it is the
// cursor the caller passed, unchanged.
func (s *Server) readChanges(r *http.Request, p *auth.Principal, feed, space string, after int64, limit int,
	curTok string) (changesResponse, error) {
	ctx := r.Context()
	rows, err := s.store.NodeChangesAfter(ctx, p.AccountID, space, after, limit+1)
	if err != nil {
		return changesResponse{}, storeErr(err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	out := changesResponse{Changes: []changeEntry{}, Cursor: curTok, HasMore: hasMore}
	if len(rows) == 0 {
		return out, nil
	}
	out.Cursor = encodeChangesCursor(feed, rows[len(rows)-1].Seq)

	type fold struct {
		seq    int64
		relist bool
	}
	folds := map[string]*fold{}
	var order []string
	for _, row := range rows {
		f := folds[row.NodeID]
		if f == nil {
			f = &fold{}
			folds[row.NodeID] = f
			order = append(order, row.NodeID)
		}
		if row.Seq > f.seq {
			f.seq = row.Seq
		}
		f.relist = f.relist || row.Relist
	}
	sort.Slice(order, func(i, j int) bool { return folds[order[i]].seq < folds[order[j]].seq })

	nodes, err := s.store.NodesByID(ctx, p.AccountID, order)
	if err != nil {
		return changesResponse{}, storeErr(err)
	}
	// One snapshot reads the reach of every node: a key reaches all but the trashed, a user what its permissions give.
	// A node with no role is removed, as is one that has left the space.
	var roles map[string]string
	within := ""
	if p.Token != nil {
		within = p.Token.Node
	}
	if err := s.store.Read(ctx, func(q store.Querier) error {
		var err error
		roles, err = reachRolesIn(ctx, q, p, order, within)
		return err
	}); err != nil {
		return changesResponse{}, storeErr(err)
	}

	entries := make([]changeEntry, 0, len(order))
	live := make([]nodeJSON, 0, len(order))
	for _, nid := range order {
		n, role := nodes[nid], roles[nid]
		removed := n == nil || role == "" || (space != "" && n.FolderID != space)
		e := changeEntry{NodeID: nid, Removed: removed, Relist: folds[nid].relist}
		if !removed {
			live = append(live, nodeJSONFor(p, n))
			j := &live[len(live)-1]
			j.Role = role
			e.Node = j
		}
		entries = append(entries, e)
	}
	if len(live) > 0 {
		ptrs := make([]*nodeJSON, len(live))
		for i := range live {
			ptrs[i] = &live[i]
		}
		if err := s.addPeopleNames(ctx, p, p.AccountID, ptrs...); err != nil {
			return changesResponse{}, err
		}
		if err := s.withTargets(ctx, p, ptrs...); err != nil {
			return changesResponse{}, err
		}
		if err := s.withThumbnails(ctx, p, p.AccountID, ptrs...); err != nil {
			return changesResponse{}, err
		}
		if err := s.withEditing(ctx, p, p.AccountID, ptrs...); err != nil {
			return changesResponse{}, err
		}
		if p.Kind == auth.KindUser {
			if err := s.withStarred(r, p.UserID, ptrs); err != nil {
				return changesResponse{}, err
			}
		}
	}
	out.Changes = entries
	return out, nil
}
