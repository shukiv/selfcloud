// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Opening a link that anyone can open (design section 8, Share links). The secret alone opens it, or the
// secret and the link's password when it has one. No session is read: the visitor is not a user. Every
// dead link answers not_found, and a wrong password, a missing one and a lock each answer their own code,
// since the visitor who holds the secret already knows the link is live.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// The password limits. A wrong password counts against its client address and link: ten wrong ones in fifteen
// minutes lock that address out of that link for fifteen minutes. Thirty wrong ones in an hour slow every
// attempt on the link by passwordSlowdown, for everyone, and refuse none, so a holder of the secret cannot shut
// the link for its real visitors.
const (
	passwordIPWindow   = 15 * time.Minute
	passwordIPMax      = 10
	passwordLockFor    = 15 * time.Minute
	passwordLinkWindow = time.Hour
	passwordLinkBudget = 30
	passwordSlowdown   = 2 * time.Second
	// passwordSlotWait bounds the wait for a link's password check turn. Past it, the request is refused rate_limited.
	passwordSlotWait = 10 * time.Second
	// throttleAuditGap is the least time between two link.throttled entries for one key, so a flood of
	// refusals writes one entry, not one each.
	throttleAuditGap = 15 * time.Minute
	// guardMaxKeys bounds each per-address map of the guard, so a flood of addresses cannot grow it without limit.
	guardMaxKeys = 100000
)

// shareGuard holds the counts behind the password limits. It lives in memory: a restart clears it, which
// only shortens a lock.
type shareGuard struct {
	mu          sync.Mutex
	ipFails     map[string][]time.Time // "ip|link" -> wrong passwords in the window
	lockedUntil map[string]time.Time   // "ip|link" -> the lock's end
	linkFails   map[string][]time.Time // link -> wrong passwords in the window, at most passwordLinkBudget
	lastAudit   map[string]time.Time   // key -> the last audit entry for it
	linkSlots   map[string]*linkSlot   // link -> its one password check at a time; present only while in use
	lastSweep   time.Time
	// slotWait bounds the wait for a link's password check turn; passwordSlotWait in production, lower in a test.
	slotWait time.Duration
	// slowdown is the delay an attempt on an over-budget link takes; passwordSlowdown in production, lower in a test.
	slowdown time.Duration
}

// linkSlot is the turn a link's password check takes. ch holds one token while a check runs; users counts the
// requests holding or waiting for it, so the slot is dropped once it is idle.
type linkSlot struct {
	ch    chan struct{}
	users int
	// addrs holds the address keys that hold or wait for the turn: one per address at a time.
	addrs map[string]bool
}

func newShareGuard() *shareGuard {
	return &shareGuard{ipFails: map[string][]time.Time{}, lockedUntil: map[string]time.Time{},
		linkFails: map[string][]time.Time{}, lastAudit: map[string]time.Time{}, linkSlots: map[string]*linkSlot{},
		slotWait: passwordSlotWait, slowdown: passwordSlowdown}
}

// lockLink takes the password check's turn on a link, and returns the func that gives it back. Only one check runs
// on a link at a time, so the lock and the budget are read and written by one attempt at once, and the slowdown
// bounds a link to one guess per passwordSlowdown. Two limits keep the turn from being held hostage. One request
// per address (ipKey) may hold or wait for a link's turn: a second from the same address is refused at once. A wait
// gives up after the guard's slotWait, with rate_limited, and so does a wait that ctx ends first. A holder of the
// secret can therefore slow the real visitors of a password link, but cannot queue them behind an unbounded line.
func (g *shareGuard) lockLink(ctx context.Context, linkID, ipKey string) (func(), error) {
	g.mu.Lock()
	sl := g.linkSlots[linkID]
	if sl == nil {
		sl = &linkSlot{ch: make(chan struct{}, 1), addrs: map[string]bool{}}
		g.linkSlots[linkID] = sl
	}
	if sl.addrs[ipKey] {
		g.mu.Unlock()
		return nil, apierr.New(apierr.RateLimited, "a password check from this address is already running on this link").
			Retry(g.slowdown)
	}
	sl.addrs[ipKey] = true
	sl.users++
	wait := g.slotWait
	g.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case sl.ch <- struct{}{}:
		return func() {
			<-sl.ch
			g.leaveLink(linkID, sl, ipKey)
		}, nil
	case <-timer.C:
		g.leaveLink(linkID, sl, ipKey)
		return nil, apierr.New(apierr.RateLimited, "the link is busy with other password checks; try again shortly").
			Retry(wait)
	case <-ctx.Done():
		g.leaveLink(linkID, sl, ipKey)
		return nil, ctx.Err()
	}
}

// leaveLink drops one user of a link's slot, and the slot itself when no user is left.
func (g *shareGuard) leaveLink(linkID string, sl *linkSlot, ipKey string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(sl.addrs, ipKey)
	sl.users--
	if sl.users == 0 {
		delete(g.linkSlots, linkID)
	}
}

// trimBefore keeps the times at or after cutoff.
func trimBefore(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	return ts[i:]
}

// sweep forgets what has aged out, at most once a minute. The caller holds g.mu.
func (g *shareGuard) sweep(now time.Time) {
	if now.Sub(g.lastSweep) < time.Minute {
		return
	}
	g.sweepNow(now)
}

// sweepNow is sweep without the minute's wait. The caller holds g.mu.
func (g *shareGuard) sweepNow(now time.Time) {
	g.lastSweep = now
	for k, ts := range g.ipFails {
		if ts = trimBefore(ts, now.Add(-passwordIPWindow)); len(ts) == 0 {
			delete(g.ipFails, k)
		} else {
			g.ipFails[k] = ts
		}
	}
	for k, ts := range g.linkFails {
		if ts = trimBefore(ts, now.Add(-passwordLinkWindow)); len(ts) == 0 {
			delete(g.linkFails, k)
		} else {
			g.linkFails[k] = ts
		}
	}
	for k, until := range g.lockedUntil {
		if !now.Before(until) {
			delete(g.lockedUntil, k)
		}
	}
	for k, at := range g.lastAudit {
		if now.Sub(at) > throttleAuditGap {
			delete(g.lastAudit, k)
		}
	}
}

// fits reports whether a new key may enter m, a map of at most guardMaxKeys entries. A full map is swept at once,
// unless a sweep ran in the last second, so a flood that keeps it full does not pay for a whole sweep on every
// attempt. A map still full after that has no room, and the caller records nothing for the key. The caller holds
// g.mu.
func fits[V any](g *shareGuard, m map[string]V, now time.Time) bool {
	if len(m) < guardMaxKeys {
		return true
	}
	if now.Sub(g.lastSweep) >= time.Second {
		g.sweepNow(now)
	}
	return len(m) < guardMaxKeys
}

// locked reports how long a client address stays out of a link, when it is locked.
func (g *shareGuard) locked(ipKey string, now time.Time) (time.Duration, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	until, ok := g.lockedUntil[ipKey]
	if !ok || !now.Before(until) {
		return 0, false
	}
	return until.Sub(now), true
}

// wrongPassword records a wrong password from ipKey on link linkID. It reports whether this one locks the
// address out of the link. When the per-address maps are full, the address is not recorded: the link's budget
// and the slot still apply.
func (g *shareGuard) wrongPassword(ipKey, linkID string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweep(now)
	fails := append(g.linkFails[linkID], now)
	if over := len(fails) - passwordLinkBudget; over > 0 {
		fails = append([]time.Time(nil), fails[over:]...) // keep the newest passwordLinkBudget
	}
	g.linkFails[linkID] = fails
	ts := append(trimBefore(g.ipFails[ipKey], now.Add(-passwordIPWindow)), now)
	if len(ts) < passwordIPMax {
		if _, known := g.ipFails[ipKey]; !known && !fits(g, g.ipFails, now) {
			return false
		}
		g.ipFails[ipKey] = ts
		return false
	}
	delete(g.ipFails, ipKey)
	if _, known := g.lockedUntil[ipKey]; !known && !fits(g, g.lockedUntil, now) {
		return false
	}
	g.lockedUntil[ipKey] = now.Add(passwordLockFor)
	return true
}

// rightPassword forgets the wrong passwords of one client address on one link.
func (g *shareGuard) rightPassword(ipKey string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.ipFails, ipKey)
}

// linkOverBudget reports whether a link has had its hourly budget of wrong passwords spent.
func (g *shareGuard) linkOverBudget(linkID string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	ts := trimBefore(g.linkFails[linkID], now.Add(-passwordLinkWindow))
	return len(ts) >= passwordLinkBudget
}

// auditDue reports whether an entry for key may be written now, and marks it written. A key that finds no room
// in the full map is not written, so the map stays bounded.
func (g *shareGuard) auditDue(key string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweep(now)
	if at, ok := g.lastAudit[key]; ok && now.Sub(at) < throttleAuditGap {
		return false
	}
	if _, known := g.lastAudit[key]; !known && !fits(g, g.lastAudit, now) {
		return false
	}
	g.lastAudit[key] = now
	return true
}

// passwordAccepted is the most characters a password may have to be checked at all. A longer one cannot be the
// link's password, so it is refused without the hash.
func passwordAccepted(p string) bool {
	return utf8.RuneCountInString(p) <= auth.MaxPasswordLen
}

// anyoneOpenAnswer is the answer to a good open: the visitor's credential, when it ends, the link and its node.
type anyoneOpenAnswer struct {
	Token     string         `json:"token"`
	ExpiresAt time.Time      `json:"expires_at"`
	Link      *shareLinkJSON `json:"link,omitempty"`
	Node      string         `json:"node,omitempty"`
	// UploadOnly is set for a drop link: the visitor may upload into the link's directory and nothing else. Such
	// an answer names no node and no link, so the visitor learns no directory ID.
	UploadOnly bool `json:"upload_only,omitempty"`
}

// openAnyoneLink opens a link that anyone can open for a visitor who holds its secret, and the password when the
// link has one. st is the link's state as the secret's hash found it; the open re-reads it inside its write, so a
// reset or deletion between the reads is the same not_found.
func (s *Server) openAnyoneLink(w http.ResponseWriter, r *http.Request, st *store.ShareLinkState, hash []byte,
	password string) error {
	dead := notFound("link")
	ctx := r.Context()
	now := time.Now()
	ipKey := addressKey(r) + "|" + st.Link.ID
	// A locked address is refused before it takes or waits for the link's turn, so it neither holds the turn nor
	// queues behind it. The check is made again once the turn is held, since a guess may have locked it meanwhile.
	if wait, locked := s.guard.locked(ipKey, now); locked {
		return apierr.New(apierr.RateLimited, "too many wrong passwords; try again later").Retry(wait)
	}
	if st.Link.PasswordHash != "" {
		// One password check at a time on this link. The turn is held through the lockout check, the slowdown, the
		// verify and the recording of the outcome, so none of them can be raced by another guess.
		release, err := s.guard.lockLink(ctx, st.Link.ID, ipKey)
		if err != nil {
			return err
		}
		defer release()
		if wait, locked := s.guard.locked(ipKey, now); locked {
			return apierr.New(apierr.RateLimited, "too many wrong passwords; try again later").Retry(wait)
		}
	}
	if !shareOpenable(st, now) {
		return dead
	}
	// A node under a trashed ancestor is dead, as a trashed node is. Checked before the password, so the answer is
	// the same for every guess.
	if self, above, err := s.store.TrashState(ctx, st.Link.AccountID, st.Link.NodeID); err != nil {
		return err
	} else if self || above {
		return dead
	}
	if st.Link.PasswordHash != "" {
		if password == "" {
			return apierr.New(apierr.Unauthenticated, "this link needs its password").With("reason", "password_required")
		}
		if err := s.slowIfOverBudget(ctx, st.Link.ID); err != nil {
			return err
		}
		ok := false
		if passwordAccepted(password) {
			var err error
			ok, err = auth.VerifyPassword(ctx, st.Link.PasswordHash, password)
			if err != nil {
				return err
			}
		}
		if !ok {
			return s.wrongOpenPassword(r, st, ipKey, now)
		}
		s.guard.rightPassword(ipKey)
	}
	var out anyoneOpenAnswer
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		at := time.Now()
		cur, err := tx.ShareLinkStateByID(ctx, st.Link.AccountID, st.Link.ID)
		if errors.Is(err, store.ErrNotFound) {
			return dead
		}
		if err != nil {
			return err
		}
		// The secret and the password are the ones checked above, and the link is openable at this moment.
		if !bytes.Equal(cur.Link.SecretHash, hash) || cur.Link.PasswordHash != st.Link.PasswordHash ||
			!shareOpenable(cur, at) {
			return dead
		}
		trashed, above, err := tx.TrashState(ctx, cur.Link.AccountID, cur.Link.NodeID)
		if err != nil {
			return err
		}
		if trashed || above {
			return dead
		}
		tok, exp, err := s.issueShareCredential(cur, at)
		if err != nil {
			return err
		}
		out = anyoneOpenAnswer{Token: tok, ExpiresAt: exp}
		if cur.Link.UploadOnly {
			out.UploadOnly = true
		} else {
			link := toShareLinkJSON(&cur.Link, nil, cur.Link.NodeID, false)
			out.Node, out.Link = cur.Link.NodeID, &link
		}
		// The open itself is always written above; its audit entry is written at most once per throttleAuditGap for
		// the link, so a link opened often does not fill the log.
		if !s.guard.auditDue("open|"+cur.Link.ID, at) {
			return nil
		}
		e := s.auditEntry(r, cur.Link.AccountID, "link.open", cur.Link.ID, nil, cur.FolderID)
		e.ActorKind, e.ActorID = string(auth.KindShareLink), cur.Link.ID
		return tx.InsertAudit(ctx, e)
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
	return nil
}

// slowIfOverBudget delays an attempt on a link whose hourly budget of wrong passwords is spent. It delays every
// attempt, the right ones too, and refuses none.
func (s *Server) slowIfOverBudget(ctx context.Context, linkID string) error {
	if !s.guard.linkOverBudget(linkID, time.Now()) {
		return nil
	}
	select {
	case <-time.After(s.guard.slowdown):
		return nil
	case <-ctx.Done():
		return apierr.New(apierr.RateLimited, "too many attempts; try again later").Retry(s.guard.slowdown)
	}
}

// wrongOpenPassword records a wrong password. The one that locks the address out writes a link.throttled entry.
func (s *Server) wrongOpenPassword(r *http.Request, st *store.ShareLinkState, ipKey string, now time.Time) error {
	if !s.guard.wrongPassword(ipKey, st.Link.ID, now) {
		return apierr.New(apierr.Unauthenticated, "the password is not right").With("reason", "password_wrong")
	}
	s.throttled(r, st, "password_lockout")
	return apierr.New(apierr.RateLimited, "too many wrong passwords; try again later").Retry(passwordLockFor)
}

// throttled writes a link.throttled entry for a limit that tripped, at most one per throttleAuditGap for each link
// and reason. It is best effort: a failed write does not change the refusal the visitor gets.
func (s *Server) throttled(r *http.Request, st *store.ShareLinkState, reason string) {
	if !s.guard.auditDue(st.Link.ID+"|"+reason, time.Now()) {
		return
	}
	ctx := r.Context()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		e := s.auditEntry(r, st.Link.AccountID, "link.throttled", st.Link.ID,
			map[string]any{"reason": reason}, st.FolderID)
		e.ActorKind, e.ActorID = string(auth.KindShareLink), st.Link.ID
		return tx.InsertAudit(ctx, e)
	})
	if err != nil {
		s.log.Warn("cannot record a throttled link", "request_id", requestID(r), "error", err)
	}
}

// handleRenewShareCredential gives a live visitor credential a fresh life. The caller is the credential, and
// authenticate has already read its link; the node's trash is read here, since the renewal reads no node.
func (s *Server) handleRenewShareCredential(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if p == nil || p.Kind != auth.KindShareLink || p.Link == nil {
		return apierr.New(apierr.Forbidden, "renew is for a share link credential")
	}
	ctx := r.Context()
	s.atWrite()
	var (
		tok string
		exp time.Time
	)
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		self, above, err := tx.TrashState(ctx, p.AccountID, p.Link.Node)
		if err != nil {
			return err
		}
		if self || above {
			return notFound("link")
		}
		now := time.Now()
		st, err := tx.ShareLinkStateByID(ctx, p.AccountID, p.ID)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("link")
		}
		if err != nil {
			return err
		}
		if !shareOpenable(st, now) {
			return notFound("link")
		}
		// The presented credential must have been issued after the link's current secret, judged on this read:
		// a reset that landed after the credential was checked ends it here, and nothing is minted for it.
		if !p.Link.IssuedAt.After(st.Link.CredentialsAt) {
			return notFound("link")
		}
		tok, exp, err = s.renewShareCredential(st, p.Link.IssuedAt, now, p.Link.VisitorID)
		return err
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "expires_at": exp})
	return nil
}

// dropInfo is what a drop link's visitor is shown: the owner's title and message, whether a name and email are asked
// for, when the link ends, and the largest file it takes.
type dropInfo struct {
	Title       string    `json:"title"`
	Message     string    `json:"message"`
	AskName     bool      `json:"ask_name"`
	ExpiresAt   time.Time `json:"expires_at"`
	MaxFileSize int64     `json:"max_file_size"`
}

// handleShareInfo answers a drop link's visitor what the drop page shows. Only a drop credential reaches it (the
// route admits drop credentials alone). The link is read now, so a dead link answers not_found at once.
func (s *Server) handleShareInfo(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if p == nil || p.Kind != auth.KindShareLink || p.Link == nil || !p.Link.UploadOnly {
		return apierr.New(apierr.Forbidden, "info is for a drop link's visitor").With("reason", "link_route")
	}
	ctx := r.Context()
	st, err := s.store.ShareLinkStateByID(ctx, p.AccountID, p.ID)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("link")
	}
	if err != nil {
		return err
	}
	now := time.Now()
	if !shareOpenable(st, now) || !p.Link.IssuedAt.After(st.Link.CredentialsAt) {
		return notFound("link")
	}
	if self, above, err := s.store.TrashState(ctx, p.AccountID, st.Link.NodeID); err != nil {
		return err
	} else if self || above {
		return notFound("link")
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, dropInfo{Title: st.Link.Title, Message: st.Link.Message, AskName: st.Link.AskName,
		ExpiresAt: *st.Link.ExpiresAt, MaxFileSize: int64(s.cfg.Upload.MaxFileSize)})
	return nil
}
