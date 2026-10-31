// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// The WOPI locks and the save that honours them (design section 10, decision 32). A lock is a row in wopi_locks
// (store/wopi_locks.go). Lock takes it, Lock or RefreshLock with the ID held extends it, Unlock ends it, and it lapses
// 30 minutes after its last refresh. While it is live, the store refuses every other writer of the file, so an API
// write made while an editor holds the file answers locked.
//
// The answers are the WOPI specification's. A refusal of a lock or a save is 409 with X-WOPI-Lock set to the lock on
// the file, or "" when no editor holds it. A missing lock ID is 400. A caller who may not change the file is 404.
// A save made without a lock is accepted only into an empty file the editor's session holds, which is the specification's
// document creation. A lock can be replaced with X-WOPI-OldLock (UnlockAndRelock).
// A session that was given an older content of the file cannot take a lock, so an API write made before the lock is
// never overwritten by the editor's save.

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// wopiLockMax is the longest lock ID WOPI allows, in ASCII characters.
const wopiLockMax = 1024

// wopiLockID reads the X-WOPI-Lock header. The lock ID is the editor's own string, opaque to Selfcloud. It is checked
// only to fit the header and the lock row: at most 1024 characters, all printable ASCII. required says that a missing
// or empty header is an error. PutFile may omit it, when the file is new and has no lock.
func wopiLockID(r *http.Request, required bool) (string, error) {
	v := r.Header.Get("X-WOPI-Lock")
	if v == "" {
		if required {
			return "", apierr.New(apierr.InvalidRequest, "X-WOPI-Lock is required")
		}
		return "", nil
	}
	if err := checkWOPILockValue("X-WOPI-Lock", v); err != nil {
		return "", err
	}
	return v, nil
}

// wopiOldLockID reads the X-WOPI-OldLock header of a LOCK that unlocks and relocks: the lock it replaces. "" when the
// header is absent, which is an ordinary LOCK.
func wopiOldLockID(r *http.Request) (string, error) {
	v := r.Header.Get("X-WOPI-OldLock")
	if v == "" {
		return "", nil
	}
	if err := checkWOPILockValue("X-WOPI-OldLock", v); err != nil {
		return "", err
	}
	return v, nil
}

// checkWOPILockValue refuses a lock ID that does not fit the header and the lock row: at most 1024 printable ASCII
// characters.
func checkWOPILockValue(header, v string) error {
	if len(v) > wopiLockMax {
		return apierr.New(apierr.InvalidRequest, header+" may be at most 1024 characters")
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return apierr.New(apierr.InvalidRequest, header+" must be printable ASCII")
		}
	}
	return nil
}

// lockHolder names the caller in a lock row: the user, or the account key that acts for the user.
func lockHolder(p *auth.Principal) string {
	if p.UserID != "" {
		return p.UserID
	}
	return p.ID
}

// lockUntil is the expiry of a lock taken or refreshed at now: office.lock_minutes later, which Validate keeps at
// the 30 minutes WOPI sets.
func (s *Server) lockUntil(now time.Time) time.Time {
	return now.Add(time.Duration(s.cfg.Office.LockMinutes) * time.Minute)
}

// lockReader is what a live lock is read from: the store, or a write transaction.
type lockReader interface {
	LiveLock(ctx context.Context, accountID, nodeID string) (*store.WOPILock, error)
}

// liveLockOf returns the live lock on the file, or nil when no editor holds it.
func liveLockOf(ctx context.Context, rd lockReader, accountID, nodeID string) (*store.WOPILock, error) {
	l, err := rd.LiveLock(ctx, accountID, nodeID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return l, err
}

// saveRefusal is the answer to a save made with lockID, or nil when the save may go on. A locked file takes only its
// holder's save, and a save with another lock ID answers with the lock that holds the file. An unlocked file takes a
// save only when it is empty (document creation), and then only when the editor's session holds the file's current
// content (sessTag is the tag the session was given, contentTag the file's); otherwise the answer is "" as the current
// lock. An empty file that an API write emptied after the editor opened it is therefore not overwritten.
func saveRefusal(live *store.WOPILock, lockID string, size int64, sessTag, contentTag string) error {
	if live != nil {
		if live.LockID != lockID {
			return &store.LockedError{Current: live.LockID, Reason: "the lock ID does not match the lock on the file"}
		}
		return nil
	}
	if size != 0 {
		return &store.LockedError{Reason: "the file is not locked by an editor and is not empty"}
	}
	if sessTag == "" || sessTag != contentTag {
		return &store.LockedError{Reason: "the file changed after this editor opened it"}
	}
	return nil
}

// saveGrowth is the quota growth a save of size bytes makes. A file whose live lock already has its session's version
// replaces its current content without a version (replaceContent), so only the difference in size is charged.
func (s *Server) saveGrowth(ctx context.Context, live *store.WOPILock, n *store.Node, size int64) (int64, error) {
	if live == nil || live.VersionID == "" {
		return size, nil
	}
	_, err := s.store.Version(ctx, n.AccountID, n.ID, live.VersionID)
	if errors.Is(err, store.ErrNotFound) {
		return size, nil
	}
	if err != nil {
		return 0, err
	}
	return size - n.Size, nil
}

// lockWrite runs one lock operation in a write transaction. The token is checked again inside it, so a revocation
// made meanwhile ends the operation, and the file must still be one the caller may edit. op gets the file's live lock
// (nil when there is none), the editing session and the file as it is in the transaction.
func (s *Server) lockWrite(r *http.Request, g *wopiGrant,
	op func(tx *store.Tx, live *store.WOPILock, sess *store.WOPISession, cur *store.Node) error) error {
	ctx := r.Context()
	s.atWrite()
	return s.store.Write(ctx, func(tx *store.Tx) error {
		if _, _, err := s.authenticateWOPI(ctx, tx, g.token, s.store.Clock()); err != nil {
			return err
		}
		sess, err := tx.WOPISession(ctx, g.node.AccountID, g.session)
		if err != nil {
			return err
		}
		cur, err := requireNodeRole(ctx, tx, g.p, g.node.ID, store.RoleEditor)
		if err != nil {
			return err
		}
		if cur.Kind != store.KindFile {
			return notFound("node")
		}
		live, err := liveLockOf(ctx, tx, cur.AccountID, cur.ID)
		if err != nil {
			return err
		}
		return op(tx, live, sess, cur)
	})
}

// handleWOPILock takes the file's lock for the editor. A lock with the ID already held is refreshed. The file must
// not be held by another editor, and the editor must hold the file's current content, which is the check that keeps
// an API write made before the lock from being overwritten.
func (s *Server) handleWOPILock(w http.ResponseWriter, r *http.Request) error {
	g, err := s.wopiRequest(w, r)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	lockID, err := wopiLockID(r, true)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	oldID, err := wopiOldLockID(r)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	err = s.lockWrite(r, g, func(tx *store.Tx, live *store.WOPILock, sess *store.WOPISession, cur *store.Node) error {
		if oldID != "" {
			return s.relock(r, tx, g, live, cur, oldID, lockID)
		}
		if live != nil {
			if live.LockID == lockID {
				return tx.RefreshWOPILock(r.Context(), cur.AccountID, cur.ID, lockID, s.lockUntil(s.store.Clock()))
			}
			return &store.LockedError{Current: live.LockID, Reason: "the file is locked by another editor"}
		}
		if sess.ServedTag == "" || sess.ServedTag != cur.ContentTag {
			return &store.LockedError{Reason: "the file changed after this editor opened it"}
		}
		now := s.store.Clock()
		expired, err := tx.PutLock(r.Context(), &store.WOPILock{NodeID: cur.ID, AccountID: cur.AccountID,
			LockID: lockID, Holder: lockHolder(g.p), CreatedAt: now, ExpiresAt: s.lockUntil(now)})
		if err != nil {
			return err
		}
		if expired != nil {
			// The sweep has not yet recorded this expiry, so it is recorded here, before the new lock.
			if err := tx.InsertAudit(r.Context(), s.auditEntry(r, cur.AccountID, "node.unlock", cur.ID,
				map[string]any{"reason": "expired"}, cur.FolderID)); err != nil {
				return err
			}
		}
		return tx.InsertAudit(r.Context(), s.auditEntry(r, cur.AccountID, "node.lock", cur.ID,
			map[string]any{"session": g.session}, cur.FolderID))
	})
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	return nil
}

// relock is LOCK with X-WOPI-OldLock (WOPI's UnlockAndRelock): the live lock oldID on the file becomes lockID, held by the
// caller for another lock time. The row keeps the version its editing session made, so the session keeps its one version.
// A file with no live lock, or with another one, answers 409 with the current lock, which is "" when none holds the file.
func (s *Server) relock(r *http.Request, tx *store.Tx, g *wopiGrant, live *store.WOPILock, cur *store.Node,
	oldID, lockID string) error {
	if live == nil {
		return &store.LockedError{Reason: "the file is not locked"}
	}
	if live.LockID != oldID {
		return &store.LockedError{Current: live.LockID, Reason: "the lock ID does not match the lock on the file"}
	}
	if err := tx.RelockWOPILock(r.Context(), cur.AccountID, cur.ID, oldID, lockID, lockHolder(g.p),
		s.lockUntil(s.store.Clock())); err != nil {
		return err
	}
	if err := tx.InsertAudit(r.Context(), s.auditEntry(r, cur.AccountID, "node.unlock", cur.ID,
		map[string]any{"reason": "relock"}, cur.FolderID)); err != nil {
		return err
	}
	return tx.InsertAudit(r.Context(), s.auditEntry(r, cur.AccountID, "node.lock", cur.ID,
		map[string]any{"session": g.session, "relock": true}, cur.FolderID))
}

// handleWOPIRefreshLock extends the live lock that has the given ID, for another 30 minutes from now.
func (s *Server) handleWOPIRefreshLock(w http.ResponseWriter, r *http.Request) error {
	g, err := s.wopiRequest(w, r)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	lockID, err := wopiLockID(r, true)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	err = s.lockWrite(r, g, func(tx *store.Tx, live *store.WOPILock, _ *store.WOPISession, cur *store.Node) error {
		if live == nil {
			return &store.LockedError{Reason: "the file is not locked"}
		}
		if live.LockID != lockID {
			return &store.LockedError{Current: live.LockID, Reason: "the lock ID does not match the lock on the file"}
		}
		return tx.RefreshWOPILock(r.Context(), cur.AccountID, cur.ID, lockID, s.lockUntil(s.store.Clock()))
	})
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	return nil
}

// handleWOPIUnlock ends the live lock that has the given ID. Any editor that holds a valid token for the file may end
// it with the ID, as the WOPI specification says: the lock belongs to the ID, not to a person.
func (s *Server) handleWOPIUnlock(w http.ResponseWriter, r *http.Request) error {
	g, err := s.wopiRequest(w, r)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	lockID, err := wopiLockID(r, true)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	err = s.lockWrite(r, g, func(tx *store.Tx, live *store.WOPILock, _ *store.WOPISession, cur *store.Node) error {
		if live == nil {
			return &store.LockedError{Reason: "the file is not locked"}
		}
		if live.LockID != lockID {
			return &store.LockedError{Current: live.LockID, Reason: "the lock ID does not match the lock on the file"}
		}
		if err := tx.UnlockNode(r.Context(), cur.AccountID, cur.ID, lockID); err != nil {
			return err
		}
		return tx.InsertAudit(r.Context(), s.auditEntry(r, cur.AccountID, "node.unlock", cur.ID,
			map[string]any{"reason": "editor"}, cur.FolderID))
	})
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	return nil
}

// handleWOPIGetLock answers the lock on the file in X-WOPI-Lock, "" when none. It changes nothing.
func (s *Server) handleWOPIGetLock(w http.ResponseWriter, r *http.Request) error {
	g, err := s.wopiRequest(w, r)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	live, err := liveLockOf(r.Context(), s.store, g.node.AccountID, g.node.ID)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	current := ""
	if live != nil {
		current = live.LockID
	}
	w.Header().Set("X-WOPI-Lock", current)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	return nil
}

// handleWOPIPutFile replaces the file's content with the body, which is the whole file. It goes through replaceContent,
// the path PUT uses, so the quota, the version kept and the node.write audit entry are the same. Within one lock, the
// first save keeps a version of the content before it; the later saves of the same lock replace the content without a
// version (decision 32). The body is streamed to a blob first, limited to office.max_file_bytes; a refusal removes the
// blob.
//
// The save is refused, before the body is read and again inside the write, when the lock does not allow it: a save
// with another lock ID, or with no lock to an empty file (saveRefusal). Its answer is 409 with X-WOPI-Lock.
func (s *Server) handleWOPIPutFile(w http.ResponseWriter, r *http.Request) error {
	g, err := s.wopiRequest(w, r)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	if r.Header.Get("X-WOPI-Override") != "PUT" {
		s.wopiFail(w, r, errNotImplemented)
		return nil
	}
	if !g.writable() {
		s.wopiFail(w, r, apierr.New(apierr.Forbidden, "the token is read-only"))
		return nil
	}
	n := g.node
	if n.Kind != store.KindFile {
		s.wopiFail(w, r, notFound("node"))
		return nil
	}
	lockID, err := wopiLockID(r, false)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	ctx := r.Context()
	p := g.p
	live, err := liveLockOf(ctx, s.store, n.AccountID, n.ID)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	sess, err := s.store.WOPISession(ctx, n.AccountID, g.session)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	if err := saveRefusal(live, lockID, n.Size, sess.ServedTag, n.ContentTag); err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	// A body whose length is known is refused over the account's quota before a byte of it is read. The growth is what
	// the save adds: the whole body, or only the difference when the lock already keeps a version.
	if r.ContentLength >= 0 {
		growth, err := s.saveGrowth(ctx, live, n, r.ContentLength)
		if err != nil {
			s.wopiFail(w, r, err)
			return nil
		}
		if growth > 0 {
			if err := s.precheckSaveQuota(ctx, p, n.AccountID, growth); err != nil {
				s.wopiFail(w, r, err)
				return nil
			}
		}
	}
	c, err := s.writeBlob(w, r, n.AccountID, int64(s.cfg.Office.MaxFileBytes))
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	var out *store.Node
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		// The token and the lock are checked again inside the write: a revocation or a lock change made while the body
		// was arriving ends the save.
		if _, _, err := s.authenticateWOPI(ctx, tx, g.token, s.store.Clock()); err != nil {
			return err
		}
		cur, err := requireNodeRole(ctx, tx, p, n.ID, store.RoleEditor)
		if err != nil {
			return err
		}
		if cur.Kind != store.KindFile {
			return notFound("node")
		}
		live, err := liveLockOf(ctx, tx, cur.AccountID, cur.ID)
		if err != nil {
			return err
		}
		sess, err := tx.WOPISession(ctx, cur.AccountID, g.session)
		if err != nil {
			return err
		}
		if err := saveRefusal(live, lockID, cur.Size, sess.ServedTag, cur.ContentTag); err != nil {
			return err
		}
		out, err = s.replaceContent(ctx, r, tx, p, cur, c, n.MimeType, lockID)
		if err != nil {
			return err
		}
		// The editor now holds the content this save made, so its session is given that content tag.
		return tx.SetWOPISessionTag(ctx, cur.AccountID, g.session, out.ContentTag)
	})
	if err != nil {
		s.discardBlob(n.AccountID, c.ID)
		s.wopiFail(w, r, err)
		return nil
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-WOPI-ItemVersion", unquoteETag(out.ETag))
	w.WriteHeader(http.StatusOK)
	return nil
}

// handleWOPIOverride answers POST /wopi/files/{nod}. It carries the lock operations by X-WOPI-Override. RenameFile
// (slice l4), UnlockAndRelock and the other overrides are not implemented: the request is checked, then answered 501.
func (s *Server) handleWOPIOverride(w http.ResponseWriter, r *http.Request) error {
	switch r.Header.Get("X-WOPI-Override") {
	case "LOCK":
		return s.handleWOPILock(w, r)
	case "GET_LOCK":
		return s.handleWOPIGetLock(w, r)
	case "REFRESH_LOCK":
		return s.handleWOPIRefreshLock(w, r)
	case "UNLOCK":
		return s.handleWOPIUnlock(w, r)
	}
	if _, err := s.wopiRequest(w, r); err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	if info := infoFrom(r.Context()); info != nil {
		info.clientFault = true // a 501 for an operation this slice does not have, not a server failure
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotImplemented)
	return nil
}
