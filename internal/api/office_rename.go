// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// RenameFile, the WOPI operation the editor calls to rename the file it holds (decision 72). It goes through the rules a
// PATCH rename goes through: the name is cleaned by names.Clean, a sibling with the same name refuses it, the rename is
// audited as node.rename (which the webhook maps to file.updated), and the store's lock rule applies. The holder of the
// lock may rename its own locked file; any other holder, or a locked file with no lock ID sent, answers 409 with the
// lock on the file.
//
// The extension is not part of X-WOPI-RequestedName and is kept. An invalid or clashing name answers 400 with the
// X-WOPI-InvalidFileNameError header, as the specification says; this slice does not generate another name.

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/wopi"
)

// wopiNameError is a RenameFile name the server refuses. Its reason goes in X-WOPI-InvalidFileNameError, for logs.
type wopiNameError struct{ reason string }

func (e *wopiNameError) Error() string { return "wopi: the file name is refused: " + e.reason }

// handleWOPIRename renames the file to X-WOPI-RequestedName (UTF-7, without its extension) and answers {"Name": ...}
// with the new name, also without the extension.
func (s *Server) handleWOPIRename(w http.ResponseWriter, r *http.Request) error {
	g, err := s.wopiRequest(w, r)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	if !g.writable() {
		s.wopiFail(w, r, apierr.New(apierr.Forbidden, "the token is read-only"))
		return nil
	}
	if g.node.Kind != store.KindFile {
		s.wopiFail(w, r, notFound("node"))
		return nil
	}
	requested, err := wopiRequestedName(r)
	if err != nil {
		s.wopiNameRefused(w, r, err)
		return nil
	}
	lockID, err := wopiLockID(r, false)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	var base string
	err = s.lockWrite(r, g, func(tx *store.Tx, live *store.WOPILock, _ *store.WOPISession, cur *store.Node) error {
		// An unlocked file takes the rename. A locked file takes it only from its holder.
		if live != nil && live.LockID != lockID {
			return &store.LockedError{Current: live.LockID, Reason: "the lock ID does not match the lock on the file"}
		}
		ext := path.Ext(cur.Name)
		name, key, err := names.Clean(requested + ext)
		if err != nil {
			return &wopiNameError{reason: apierr.From(err).Message}
		}
		if taken, err := nameTakenBy(tx.Child(r.Context(), cur.AccountID, cur.ParentID, key)); err != nil {
			return err
		} else if taken != nil && taken.ID != cur.ID {
			return &wopiNameError{reason: "a file or folder with this name already exists here"}
		}
		from := cur.Name
		cur.Name, cur.NameKey = name, key
		cur.ModifiedAt, cur.ModifiedBy = store.Now(), g.p.ID
		cur.ETag = newETag()
		holder := ""
		if live != nil {
			holder = lockID
		}
		if err := tx.UpdateNodeHeld(r.Context(), cur, holder); err != nil {
			if errors.Is(err, store.ErrConflict) {
				return &wopiNameError{reason: "a file or folder with this name already exists here"}
			}
			return err
		}
		base = strings.TrimSuffix(name, ext)
		return tx.InsertAudit(r.Context(), s.auditEntry(r, cur.AccountID, "node.rename", cur.ID,
			map[string]any{"from": from, "to": name}, cur.FolderID))
	})
	var ne *wopiNameError
	if errors.As(err, &ne) {
		s.wopiNameRefused(w, r, ne)
		return nil
	}
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]string{"Name": base})
}

// wopiRequestedName reads and decodes X-WOPI-RequestedName. A missing or undecodable name is a name the server refuses.
func wopiRequestedName(r *http.Request) (string, error) {
	v := r.Header.Get("X-WOPI-RequestedName")
	if v == "" {
		return "", &wopiNameError{reason: "the requested name is missing"}
	}
	name, err := wopi.DecodeUTF7(v)
	if err != nil {
		return "", &wopiNameError{reason: "the requested name is not valid UTF-7"}
	}
	if name == "" {
		return "", &wopiNameError{reason: "the requested name is empty"}
	}
	return name, nil
}

// wopiNameRefused answers 400 with the reason in X-WOPI-InvalidFileNameError. The file keeps its name. The client caused
// it, so the access log has no error for it.
func (s *Server) wopiNameRefused(w http.ResponseWriter, r *http.Request, err error) {
	var ne *wopiNameError
	reason := "the file name is refused"
	if errors.As(err, &ne) {
		reason = ne.reason
	}
	if info := infoFrom(r.Context()); info != nil {
		info.clientFault = true
	}
	s.log.Warn("wopi rename refused", "request_id", requestID(r), "reason", reason)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-WOPI-InvalidFileNameError", reason)
	w.WriteHeader(http.StatusBadRequest)
}
