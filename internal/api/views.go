// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// The view recorder keeps the Recent list's views off the request path (slice p2, decision 3). A view is recorded
// at most once per user and file in viewGap. The views go through a buffered channel, and a writer goroutine stores
// them in batches, every viewFlush or once viewBatch are waiting. When the channel is full a view is dropped, so a
// request never waits on the database for it. Views waiting to be written are lost in a crash; design section 9.
const (
	viewGap   = 10 * time.Minute
	viewKeys  = 100000 // the most gate keys held at once, as the o2 address maps hold theirs
	viewQueue = 4096   // views waiting to be written
	viewBatch = 256    // rows per write transaction
	viewFlush = 2 * time.Second
)

// viewRecorder is the in-memory gate and the queue of views to write. The gate keeps, for each user and file, the
// time of the last view taken, and a queue of those marks in the order they were taken. The marks are time-ordered,
// so an expired mark is always at the head: the gate sheds its expired keys from the head, a few at a time, and each
// check costs time in proportion to the keys it sheds, not to the size of the gate.
type viewRecorder struct {
	st   *store.Store
	log  *slog.Logger
	ch   chan store.ViewRecord
	keys int // the most gate keys held at once; viewKeys in production

	mu    sync.Mutex
	last  map[string]time.Time // user and file -> when its view was last taken
	marks []gateMark           // the marks in the order they were taken; marks[head:] are live
	head  int
}

// gateMark is one entry of the gate's queue: the key, and the time its view was taken.
type gateMark struct {
	key string
	at  time.Time
}

func newViewRecorder(st *store.Store, log *slog.Logger) *viewRecorder {
	return newViewRecorderSize(st, log, viewQueue)
}

// newViewRecorderSize is newViewRecorder with a queue of the given size. The tests use a small one.
func newViewRecorderSize(st *store.Store, log *slog.Logger, queue int) *viewRecorder {
	return newViewRecorderCaps(st, log, queue, viewKeys)
}

// newViewRecorderCaps is newViewRecorderSize with the gate's key cap as well. The tests fill a small gate.
func newViewRecorderCaps(st *store.Store, log *slog.Logger, queue, keys int) *viewRecorder {
	return &viewRecorder{st: st, log: log, ch: make(chan store.ViewRecord, queue), keys: keys,
		last: map[string]time.Time{}}
}

// record takes one view. It never blocks. A view within viewGap of the user's last view of the file is dropped, and so
// is a view for a new key once the gate is full, or once the queue is. A view that is queued takes its mark only then,
// so a dropped view leaves no mark behind. It reports whether the view was queued.
func (v *viewRecorder) record(rec store.ViewRecord) bool {
	key := rec.UserID + "|" + rec.NodeID
	v.mu.Lock()
	defer v.mu.Unlock()
	v.expireLocked(rec.At)
	at, seen := v.last[key]
	if seen && rec.At.Sub(at) < viewGap {
		return false
	}
	if !seen && len(v.last) >= v.keys {
		return false
	}
	select {
	case v.ch <- rec:
	default:
		return false
	}
	v.last[key] = rec.At
	v.marks = append(v.marks, gateMark{key: key, at: rec.At})
	return true
}

// expireLocked sheds the gate's keys whose marks are at least viewGap old at now. A mark whose key was taken again
// later is stale and only leaves the queue. The caller holds mu.
func (v *viewRecorder) expireLocked(now time.Time) {
	for v.head < len(v.marks) && now.Sub(v.marks[v.head].at) >= viewGap {
		m := v.marks[v.head]
		if cur, ok := v.last[m.key]; ok && cur.Equal(m.at) {
			delete(v.last, m.key)
		}
		v.head++
	}
	// Reclaim the queue's dead prefix once it is half the slice, so the slice stays in proportion to the live marks.
	if v.head > 1024 && v.head*2 >= len(v.marks) {
		v.marks = append([]gateMark(nil), v.marks[v.head:]...)
		v.head = 0
	}
}

// run writes queued views until ctx is done, then writes what is still queued and returns.
func (v *viewRecorder) run(ctx context.Context) {
	t := time.NewTicker(viewFlush)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			v.flush(context.WithoutCancel(ctx))
			return
		case now := <-t.C:
			v.flush(ctx)
			v.mu.Lock()
			v.expireLocked(now)
			v.mu.Unlock()
		}
	}
}

// flush writes every view queued at the time of the call, in batches of viewBatch.
func (v *viewRecorder) flush(ctx context.Context) {
	var batch []store.ViewRecord
	for {
		select {
		case rec := <-v.ch:
			batch = append(batch, rec)
			if len(batch) < viewBatch {
				continue
			}
		default:
		}
		if len(batch) == 0 {
			return
		}
		if err := v.st.WriteViews(ctx, batch); err != nil {
			v.log.Warn("cannot record views", "views", len(batch), "error", err)
		}
		batch = batch[:0]
		if len(v.ch) == 0 {
			return
		}
	}
}

// noteView records a view of file n by the caller, when the caller is a signed-in user acting for itself. A
// delegated token, an account key, an installation admin and a link visitor record nothing, and neither does a
// directory. A HEAD request is not a view: the caller passes GET only.
func (s *Server) noteView(p *auth.Principal, n *store.Node) {
	if p == nil || p.Kind != auth.KindUser || p.Token != nil || n.Kind != store.KindFile {
		return
	}
	s.views.record(store.ViewRecord{AccountID: n.AccountID, UserID: p.UserID, NodeID: n.ID, At: time.Now()})
}

// noteEdit records, in the write transaction, that the caller edited node. Any user principal records it, including a
// delegated token acting as its user. An account key, a link visitor and an admin record nothing.
func noteEdit(ctx context.Context, tx *store.Tx, p *auth.Principal, accountID, nodeID string) error {
	if p == nil || p.Kind != auth.KindUser {
		return nil
	}
	return tx.RecordEdit(ctx, accountID, p.UserID, nodeID, store.Now())
}
