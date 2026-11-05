// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// PutRelativeFile, the WOPI operation behind Save As (design section 10, decision 87). The editor posts the whole file as
// it should be saved as, and the host makes a new file beside the source, or, in the specific mode, replaces a file the
// caller may edit. The answer is the new file's name and a WOPI address with its own token, so the editor can open the
// copy.
//
// Two modes, as the specification sets them out. X-WOPI-SuggestedTarget (a file extension, or a full name) never answers
// 400 or 409: the host picks a free name, "name (2).ext" and so on. X-WOPI-RelativeTarget is the exact name: a name taken
// without X-WOPI-OverwriteRelativeTarget answers 409 with a free name in X-WOPI-ValidRelativeTarget, so the editor retries
// by itself; a target that is locked answers 409 with X-WOPI-Lock; a target the caller may not overwrite answers 501, as
// the specification says.
//
// The copy goes in the source's directory and needs the upload rule: contributor on that directory. A no-download source
// is not copied, since a copy is a download; CheckFileInfo says so with UserCanNotWriteRelative. The copy's body goes
// through writeBlob, the one-request write path (decision 84), so the disk, the quota and office.max_file_bytes refuse it
// before its bytes land. The new file is audited as node.create, which reports file.created; an overwrite is replaceContent,
// which reports file.updated.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/wopi"
)

// saveAsReader is what the target is decided from: the store before the body is read, or a write transaction inside the
// write, so the decision is made again on the state the write sees.
type saveAsReader interface {
	accessReader
	Child(ctx context.Context, accountID, parentID, key string) (*store.Node, error)
	LiveLock(ctx context.Context, accountID, nodeID string) (*store.WOPILock, error)
}

// saveAsConflict is a name the caller asked for that a file already has, without overwrite: 409 with valid in
// X-WOPI-ValidRelativeTarget.
type saveAsConflict struct{ valid string }

func (e *saveAsConflict) Error() string {
	return "wopi: the target name is taken; a free name is " + e.valid
}

// errSaveAsNotAuthorized is an overwrite the caller may not make. The specification answers 501 for it.
var errSaveAsNotAuthorized = errors.New("wopi: the caller may not overwrite the target file")

// saveAsDecision is where the copy lands: its directory, its name, and the file it replaces, when it replaces one.
type saveAsDecision struct {
	parent  *store.Node
	role    string // the caller's role on the directory, which decides the mode of the new file's token
	name    string
	key     string
	replace *store.Node
}

// saveAsNames reads the name a PutRelativeFile asks for, as the target that is exact (requested, suggested false) or
// the one the host may change (suggested true). A suggested target that is an extension gets the source's base name; a
// suggested name the server cannot use is replaced with the source's own base and extension, since the specification
// says a suggested target is never refused.
func saveAsNames(source, relative, suggestedHdr string) (string, bool, error) {
	if relative != "" {
		name, err := wopi.DecodeUTF7(relative)
		if err != nil {
			return "", false, &wopiNameError{reason: "the relative target is not valid UTF-7"}
		}
		if name == "" {
			return "", false, &wopiNameError{reason: "the relative target is empty"}
		}
		return name, false, nil
	}
	base, ext := strings.TrimSuffix(source, path.Ext(source)), path.Ext(source)
	fallback := base + ext
	v, err := wopi.DecodeUTF7(suggestedHdr)
	if err != nil || v == "" {
		return fallback, true, nil
	}
	name := v
	if strings.HasPrefix(v, ".") {
		name = base + v
	}
	if _, _, err := names.Clean(name); err != nil {
		name = fallback
	}
	return name, true, nil
}

// saveAsPrincipal is the caller as the directory check reads it. A WOPI token names one file, so its scope reaches only that
// file; a copy needs the person's right on the file's folder, and the token's folder list still bounds it. The write cap is
// lifted for the check, since Save As is offered from the read-only view too (the specification's example), and the copy's
// own token decides whether the copy can be edited. Any other caller is returned as it is.
func saveAsPrincipal(p *auth.Principal) *auth.Principal {
	if p.Token == nil {
		return p
	}
	scope := *p.Token
	scope.Node = ""
	scope.Write = true
	cp := *p
	cp.Token = &scope
	return &cp
}

// saveAsDecide decides where a copy named requested goes, or which file it replaces. The directory must take a file from
// the caller (contributor). A name that is free is taken as it is; a suggested name that is taken gets the first free
// "name (n).ext"; an exact name that is taken is refused in this order: a live lock answers 409 with the lock, a file the
// caller may not overwrite answers 501, and otherwise a taken name answers 409 with a free name, unless overwrite is set,
// which replaces the file.
func saveAsDecide(ctx context.Context, rd saveAsReader, p *auth.Principal, src *store.Node,
	requested string, suggested, overwrite bool) (*saveAsDecision, error) {
	parent, err := requireNodeRole(ctx, rd, p, src.ParentID, store.RoleContributor)
	if err != nil {
		return nil, err
	}
	if parent.Kind != store.KindDir {
		return nil, notFound("parent")
	}
	_, role, err := nodeAccessWith(ctx, rd, p, parent.ID)
	if err != nil {
		return nil, err
	}
	name, key, err := names.Clean(requested)
	if err != nil {
		return nil, &wopiNameError{reason: apierr.From(err).Message}
	}
	taken, err := nameTakenBy(rd.Child(ctx, src.AccountID, parent.ID, key))
	if err != nil {
		return nil, err
	}
	if taken == nil {
		return &saveAsDecision{parent: parent, role: role, name: name, key: key}, nil
	}
	if suggested {
		name, key, err = saveAsFreeName(ctx, rd, src.AccountID, parent.ID, name)
		if err != nil {
			return nil, err
		}
		return &saveAsDecision{parent: parent, role: role, name: name, key: key}, nil
	}
	live, err := liveLockOf(ctx, rd, src.AccountID, taken.ID)
	if err != nil {
		return nil, err
	}
	if live != nil {
		return nil, &store.LockedError{Current: live.LockID, Reason: "the target file is locked by an editor"}
	}
	if !overwrite || taken.Kind != store.KindFile {
		valid, _, err := saveAsFreeName(ctx, rd, src.AccountID, parent.ID, name)
		if err != nil {
			return nil, err
		}
		return nil, &saveAsConflict{valid: valid}
	}
	cur, err := requireNodeRole(ctx, rd, p, taken.ID, store.RoleEditor)
	if err != nil {
		return nil, errSaveAsNotAuthorized
	}
	return &saveAsDecision{parent: parent, role: role, name: name, key: key, replace: cur}, nil
}

// saveAsFreeName is the first "base (n).ext" free in the directory parentID, for a name taken there. It follows
// freeName (transfer.go), which works on a write transaction only; the same cut at maxNameBytes applies.
func saveAsFreeName(ctx context.Context, rd saveAsReader, accountID, parentID, name string) (string, string, error) {
	base, ext := name, ""
	if i := strings.LastIndex(name, "."); i > 0 && len(name)-i <= maxExtBytes {
		base, ext = name[:i], name[i:]
	}
	for n := 1; n <= 1000; n++ {
		suffix := fmt.Sprintf(" (%d)%s", n, ext)
		cand, key, err := names.Clean(truncateBytes(base, maxNameBytes-len(suffix)) + suffix)
		if err != nil {
			return "", "", err
		}
		taken, err := nameTakenBy(rd.Child(ctx, accountID, parentID, key))
		if err != nil {
			return "", "", err
		}
		if taken == nil {
			return cand, key, nil
		}
	}
	return "", "", apierr.New(apierr.Conflict, "no free name was found for the copy")
}

// saveAsFail answers a PutRelativeFile that failed: a taken name with a free name, an overwrite the caller may not make,
// an illegal name, and every other refusal the WOPI way (wopiFail).
func (s *Server) saveAsFail(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *saveAsConflict
	var nameErr *wopiNameError
	switch {
	case errors.As(err, &conflict):
		if info := infoFrom(r.Context()); info != nil {
			info.clientFault = true
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-WOPI-ValidRelativeTarget", wopi.EncodeUTF7(conflict.valid))
		w.WriteHeader(http.StatusConflict)
	case errors.Is(err, errSaveAsNotAuthorized):
		if info := infoFrom(r.Context()); info != nil {
			info.clientFault = true
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotImplemented)
	case errors.As(err, &nameErr):
		s.wopiNameRefused(w, r, nameErr)
	default:
		s.wopiFail(w, r, err)
	}
}

// saveAsAllowed is the UserCanNotWriteRelative answer for a caller: a copy is allowed when the caller may add a file to
// the source's directory and the source is not no-download, since a copy is a download.
func (s *Server) saveAsAllowed(ctx context.Context, p *auth.Principal, n *store.Node, restricted bool) bool {
	if restricted || n.ParentID == "" {
		return false
	}
	_, role, err := nodeAccessWith(ctx, s.store, saveAsPrincipal(p), n.ParentID)
	if err != nil {
		return false
	}
	return store.RoleRank(role) >= store.RoleRank(store.RoleContributor)
}

// handleWOPIPutRelative makes the copy and answers its name and its WOPI address. The decision is made before the body
// is read, so a refusal of the name, the lock or the directory costs no upload, and again inside the write, where the
// token is checked once more. The body is a whole file: its size is limited by writeBlob, which also reserves the disk
// and checks the account quota before any byte lands. A failed write removes its blob.
func (s *Server) handleWOPIPutRelative(w http.ResponseWriter, r *http.Request) error {
	g, err := s.wopiRequest(w, r)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	if g.node.Kind != store.KindFile {
		s.wopiFail(w, r, notFound("node"))
		return nil
	}
	if g.restricted {
		// A copy is a download, and the file is no-download for this caller (decision 20).
		s.wopiFail(w, r, apierr.New(apierr.Forbidden, "a no-download file cannot be saved as a copy"))
		return nil
	}
	rel, sug := r.Header.Get("X-WOPI-RelativeTarget"), r.Header.Get("X-WOPI-SuggestedTarget")
	if (rel == "") == (sug == "") {
		s.saveAsFail(w, r, &wopiNameError{reason: "give exactly one of X-WOPI-RelativeTarget and X-WOPI-SuggestedTarget"})
		return nil
	}
	overwrite := r.Header.Get("X-WOPI-OverwriteRelativeTarget") == "true"
	requested, suggested, err := saveAsNames(g.node.Name, rel, sug)
	if err != nil {
		s.saveAsFail(w, r, err)
		return nil
	}
	limit := int64(s.cfg.Office.MaxFileBytes)
	if v := r.Header.Get("X-WOPI-Size"); v != "" {
		size, err := strconv.ParseInt(v, 10, 64)
		if err != nil || size < 0 {
			s.wopiFail(w, r, apierr.New(apierr.InvalidRequest, "X-WOPI-Size must be a whole number of bytes"))
			return nil
		}
		if size > limit {
			s.wopiFail(w, r, tooLarge(limit))
			return nil
		}
	}
	ctx := r.Context()
	p := g.p
	person := saveAsPrincipal(p)
	acc := g.node.AccountID
	pre, err := saveAsDecide(ctx, s.store, person, g.node, requested, suggested, overwrite)
	if err != nil {
		s.saveAsFail(w, r, err)
		return nil
	}
	replaces := int64(0)
	if pre.replace != nil {
		replaces = pre.replace.Size
	}
	c, err := s.writeBlob(w, r, acc, limit, replaces)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	var out *store.Node
	var token string
	var expires time.Time
	sid := id.New("wps")
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		_, pl, err := s.authenticateWOPI(ctx, tx, g.token, s.store.Clock())
		if err != nil {
			return err
		}
		dec, err := saveAsDecide(ctx, tx, person, g.node, requested, suggested, overwrite)
		if err != nil {
			return err
		}
		if dec.replace != nil {
			mt := mimeOf(r.Header.Get("Content-Type"), dec.replace.Name)
			out, err = s.replaceContent(ctx, r, tx, p, dec.replace, c, mt, "")
			if err != nil {
				return err
			}
		} else {
			if err := tx.InsertBlob(ctx, &store.Blob{ID: c.ID, AccountID: acc, Size: c.Size,
				SHA256: c.SHA256, State: store.BlobLive, CreatedAt: store.Now()}); err != nil {
				return err
			}
			n, err := s.newNode(ctx, tx, acc, dec.parent.FolderID, dec.parent.ID,
				pathSeg{name: dec.name, key: dec.key}, store.KindFile, p.ID)
			if err != nil {
				return err
			}
			n.Size, n.MimeType, n.BlobID, n.ETag, n.SHA256 = c.Size, mimeOf(r.Header.Get("Content-Type"), dec.name),
				c.ID, newETag(), c.SHA256
			n.ContentTag = store.NewContentTag()
			if err := tx.UpdateNode(ctx, n); err != nil {
				return err
			}
			if err := s.applyUsage(ctx, tx, p, acc, store.FolderChange{FolderID: dec.parent.FolderID,
				Change: store.UsageChange{Bytes: c.Size, Files: 1}}); err != nil {
				return err
			}
			out = n
			if err := noteEdit(ctx, tx, p, acc, n.ID); err != nil {
				return err
			}
			if err := tx.InsertAudit(ctx, s.auditEntry(r, acc, "node.create", n.ID,
				map[string]any{"kind": store.KindFile, "name": n.Name, "parent": dec.parent.ID, "size": c.Size,
					"via": "save_as", "from": g.node.ID}, dec.parent.FolderID)); err != nil {
				return err
			}
		}
		// The copy opens with a session of its own, for the same user, under the same token rules: it ends at the token
		// limit or the end of the token that asked, whichever is first, and with the browser session that opened the
		// original editor, if one did.
		orig, err := tx.WOPISession(ctx, acc, g.session)
		if err != nil {
			return err
		}
		now := s.store.Clock()
		expires = now.Add(time.Duration(s.cfg.Office.TokenMinutes) * time.Minute)
		if pl.ExpiresAt > 0 && time.UnixMilli(pl.ExpiresAt).Before(expires) {
			expires = time.UnixMilli(pl.ExpiresAt)
		}
		// The copy's token may write when the caller may edit the directory's files, as an open of an editable file would.
		perm, role := permRead, "read"
		if store.RoleRank(dec.role) >= store.RoleRank(store.RoleEditor) {
			perm, role = permWrite, "write"
		}
		token, err = s.wopiTokens.sign(tokenPayload{
			Version:    tokenVersion,
			KeyID:      s.wopiTokens.current.kid,
			AccountID:  acc,
			UserID:     p.UserID,
			MintedBy:   pl.MintedBy,
			Folders:    []string{out.FolderID},
			Node:       out.ID,
			Permission: perm,
			Audience:   wopiAudience,
			IssuedAt:   now.UnixMilli(),
			ExpiresAt:  expires.UnixMilli(),
			Session:    sid,
		})
		if err != nil {
			return err
		}
		return tx.InsertWOPISession(ctx, &store.WOPISession{ID: sid, AccountID: acc, NodeID: out.ID, UserID: p.UserID,
			Role: role, IssuedAt: now, ExpiresAt: expires, BrowserSession: orig.BrowserSession, ServedTag: out.ContentTag})
	})
	if err != nil {
		s.discardBlob(acc, c.ID)
		s.saveAsFail(w, r, err)
		return nil
	}
	// The host pages are the Selfcloud editor's own address for the copy, in each mode; the editor opens them as the user.
	answer := map[string]string{
		"Name":        out.Name,
		"Url":         s.wopiFileURL(out.ID) + "?access_token=" + token,
		"HostEditUrl": s.origin + "/edit/" + out.ID + "?" + url.Values{"mode": {"edit"}}.Encode(),
		"HostViewUrl": s.origin + "/edit/" + out.ID + "?" + url.Values{"mode": {"view"}}.Encode(),
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(answer)
}
