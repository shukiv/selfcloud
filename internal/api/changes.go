// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
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

// changesRetryAfter is the Retry-After a refused wait carries, so a client backs off before asking again.
const changesRetryAfter = "5"

// A changes cursor is sealed like a search cursor (decision 40): AES-GCM under a key derived from the links signing key,
// with the feed id as its additional data. The body carries only the seq, so it shows no feed id and no write volume in
// the clear, and a nonce makes two cursors of the same feed and seq differ. It opens for any caller of the same feed,
// since the feed itself is access-checked on every read. A cursor that does not open, for any reason (another feed, a
// rotated key, a malformed body), is gone with reason reset, telling the client to start again.
const changesCursorLabel = "selfcloud changes cursor v1"
const changesCursorPrefix = "c1_"

// changesCursorGone is the one answer to a cursor that can no longer be served.
var changesCursorGone = apierr.New(apierr.Gone, "the cursor can no longer be served; start again").With("reason", "reset")

// changesCursorAEAD returns the cipher that seals changes cursors, made from the current links key on each use.
func (s *Server) changesCursorAEAD() (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, s.links.current, nil, changesCursorLabel, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealChangesCursor returns the cursor that reads feed after seq.
func (s *Server) sealChangesCursor(feed string, seq int64) (string, error) {
	aead, err := s.changesCursorAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	plain := make([]byte, 8)
	binary.BigEndian.PutUint64(plain, uint64(seq))
	sealed := aead.Seal(nonce, nonce, plain, []byte(feed))
	return changesCursorPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openChangesCursor returns the seq a cursor names for feed. A cursor that does not open, for any reason, is gone with
// reason reset, so the answer tells nothing about which part was wrong.
func (s *Server) openChangesCursor(feed, tok string) (int64, error) {
	body, ok := strings.CutPrefix(tok, changesCursorPrefix)
	if !ok {
		return 0, changesCursorGone
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return 0, changesCursorGone
	}
	aead, err := s.changesCursorAEAD()
	if err != nil {
		return 0, err
	}
	if len(raw) < aead.NonceSize() {
		return 0, changesCursorGone
	}
	nonce, sealed := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, sealed, []byte(feed))
	if err != nil || len(plain) != 8 {
		return 0, changesCursorGone
	}
	seq := int64(binary.BigEndian.Uint64(plain))
	if seq < 0 {
		return 0, changesCursorGone
	}
	return seq, nil
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

// handleChangesStart answers a cursor at the journal's head, the point a client reads changes after. The head never
// lowers with the rows, so a start after a full prune still names a seq the journal keeps.
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
	seq, err := s.store.NodeChangesHead(r.Context())
	if err != nil {
		return storeErr(err)
	}
	cur, err := s.sealChangesCursor(feed, seq)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"cursor": cur})
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
	curTok := q.Get("cursor")
	if curTok == "" {
		return apierr.New(apierr.InvalidRequest, "cursor is required")
	}
	seq, err := s.openChangesCursor(feed, curTok)
	if err != nil {
		return err
	}
	// A cursor above the head (kept across a restore from an older backup) can no longer be served.
	head, err := s.store.NodeChangesHead(r.Context())
	if err != nil {
		return storeErr(err)
	}
	if seq > head {
		return changesCursorGone
	}
	limit, err := changesBound(q.Get("limit"), 200, 1, 1000)
	if err != nil {
		return err
	}
	waitSecs, err := changesBound(q.Get("wait"), 0, 0, 60)
	if err != nil {
		return err
	}
	return s.changesWaitLoop(w, r, p, feed, q.Get("space"), seq, limit, time.Duration(waitSecs)*time.Second, curTok)
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

// changesWaitKey is the caller a long poll counts against. An end user behind a delegated token counts as that user, so
// each user holds their own waits; a token acting as the account key keeps the key's own key.
func changesWaitKey(p *auth.Principal) string {
	if p.Token != nil && p.UserID != "" {
		return "user:" + p.UserID
	}
	return string(p.Kind) + ":" + p.ID
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
// deadline with the cursor unchanged. A wait refused a slot answers at once with Retry-After.
func (s *Server) changesWaitLoop(w http.ResponseWriter, r *http.Request, p *auth.Principal, feed, space string,
	after int64, limit int, wait time.Duration, curTok string) error {
	ctx := r.Context()
	key := changesWaitKey(p)
	holding := wait > 0 && s.acquireChangesWaiter(key, s.cfg.Changes.MaxWaiters)
	if wait > 0 && !holding {
		w.Header().Set("Retry-After", changesRetryAfter)
	}
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
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-sub:
			timer.Stop()
			// The throttle is computed after the wake, so the first wake after a long idle answers at once; it is
			// capped at the deadline, so a late wake never holds the answer past it.
			throttle := time.Until(lastRead.Add(changesReadEvery))
			if d := time.Until(deadline); throttle > d {
				throttle = d
			}
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

// readChanges reads the mark and limit+1 journal rows after seq in one snapshot, so a prune landing between them cannot
// make the feed skip rows silently. It folds the rows to one entry per node ordered by the node's latest row, skipping
// the staged rows of the account feed, and decorates the live nodes as a stat does. The cursor advances to the last raw
// row read (staged included); with no rows it is the cursor the caller passed, unchanged.
func (s *Server) readChanges(r *http.Request, p *auth.Principal, feed, space string, after int64, limit int,
	curTok string) (changesResponse, error) {
	ctx := r.Context()
	var mark int64
	var rows []store.NodeChange
	if err := s.store.Read(ctx, func(q store.Querier) error {
		var err error
		mark, err = store.NodeChangesMarkIn(ctx, q)
		if err != nil {
			return err
		}
		if s.changesPruneHook != nil {
			s.changesPruneHook()
		}
		rows, err = store.NodeChangesAfterIn(ctx, q, p.AccountID, space, after, limit+1)
		return err
	}); err != nil {
		return changesResponse{}, storeErr(err)
	}
	if after < mark {
		return changesResponse{}, changesCursorGone
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	out := changesResponse{Changes: []changeEntry{}, Cursor: curTok, HasMore: hasMore}
	if len(rows) == 0 {
		return out, nil
	}
	cur, err := s.sealChangesCursor(feed, rows[len(rows)-1].Seq)
	if err != nil {
		return changesResponse{}, err
	}
	out.Cursor = cur

	type fold struct {
		seq    int64
		relist bool
	}
	folds := map[string]*fold{}
	var order []string
	for _, row := range rows {
		if row.Staged {
			continue // a staged folder's rows are not reported until the publish
		}
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
	if len(order) == 0 {
		return out, nil
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
