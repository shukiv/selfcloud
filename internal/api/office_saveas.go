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
// Who may Save As is one rule, saveAsOffered, and CheckFileInfo, the handler's refusal and the write's own re-check all read
// it (decision 87). It is never offered to a token minted by a delegated key, since the WOPI token keeps none of that key's
// own cap, and never for a no-download source, since a copy is a download. Otherwise the caller must be allowed to add a
// file to the source's directory (contributor), and a refusal is 501 with UserCanNotWriteRelative true. A browser view token
// may make a new copy; an overwrite needs the source token to be writable, and a view token that asks for one gets 501.
//
// The copy goes in the source's directory. Its body goes through writeBlob, the one-request write path (decision 84), so
// the disk, the quota and office.max_file_bytes refuse it before its bytes land. The new file is audited as node.create,
// which reports file.created; an overwrite is replaceContent, which reports file.updated.

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
	noDownloadReader
	Child(ctx context.Context, accountID, parentID, key string) (*store.Node, error)
	LiveLock(ctx context.Context, accountID, nodeID string) (*store.WOPILock, error)
}

// saveAsSource is the source file of a PutRelativeFile as the write reads it: the node, whether the token may write it (its
// role is editor or more), and whether the no-download rule binds the caller.
type saveAsSource struct {
	node       *store.Node
	writable   bool
	restricted bool
}

// saveAsSourceOf reads the source as rd sees it. A node that is gone, in the trash, or not reached by the caller is not
// found, so the write refuses a source that changed while its body arrived.
func saveAsSourceOf(ctx context.Context, rd saveAsReader, p *auth.Principal, nodeID string) (*saveAsSource, error) {
	n, role, err := nodeAccessWith(ctx, rd, p, nodeID)
	if err != nil {
		return nil, err
	}
	restricted, err := downloadRestrictedIn(ctx, rd, n, role)
	if err != nil {
		return nil, err
	}
	return &saveAsSource{node: n, writable: store.RoleRank(role) >= store.RoleRank(store.RoleEditor), restricted: restricted}, nil
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

// saveAsPrincipal is the caller as the directory check and the new copy's check read it, for a browser-origin token. A WOPI
// token names one file, so its node scope is lifted: a copy needs the person's right on the file's folder, and the token's
// folder list still bounds it. The write cap is lifted too, since a copy is offered from the read-only view (the
// specification's example); an overwrite is refused separately, for a token that is not writable. A delegated token keeps
// its scope, though saveAsOffered never offers Save As to one.
func saveAsPrincipal(p *auth.Principal) *auth.Principal {
	if p.Token == nil || delegatedOrigin(p.Token) {
		return p
	}
	scope := *p.Token
	scope.Node = ""
	scope.Write = true
	cp := *p
	cp.Token = &scope
	return &cp
}

// copyPrincipal is the caller as the new copy's own role is read: the token's folder list and its write cap stand, and only
// its node scope is cleared, since the copy is another node. The copy's token takes its permission from this role (decision
// 87), so a lifted cap never reaches it.
func copyPrincipal(p *auth.Principal) *auth.Principal {
	// A token minted by a delegated key is never lifted, whatever the write refuses earlier: returned as it is, its node
	// scope still refuses the copy (decision 87, the defence in depth of the round 3 review).
	if p.Token == nil || delegatedOrigin(p.Token) {
		return p
	}
	scope := *p.Token
	scope.Node = ""
	cp := *p
	cp.Token = &scope
	return &cp
}

// saveAsDecide decides where a copy named requested goes, or which file it replaces. The directory must take a file from
// the caller (contributor). A name that is free is taken as it is; a suggested name that is taken gets the first free
// "name (n).ext". An exact name that is taken, without overwrite, answers 409 with a free name, before any lock is looked
// at. With overwrite, a source that is not writable answers 501; then a live lock answers 409 with the lock; then a file the
// caller may not edit answers 501, and otherwise the file is replaced.
func saveAsDecide(ctx context.Context, rd saveAsReader, p *auth.Principal, src *store.Node,
	requested string, suggested, overwrite, writable bool) (*saveAsDecision, error) {
	parent, err := requireNodeRole(ctx, rd, p, src.ParentID, store.RoleContributor)
	if err != nil {
		return nil, err
	}
	if parent.Kind != store.KindDir {
		return nil, notFound("parent")
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
		return &saveAsDecision{parent: parent, name: name, key: key}, nil
	}
	if suggested {
		name, key, err = saveAsFreeName(ctx, rd, src.AccountID, parent.ID, name)
		if err != nil {
			return nil, err
		}
		return &saveAsDecision{parent: parent, name: name, key: key}, nil
	}
	if !overwrite || taken.Kind != store.KindFile {
		valid, _, err := saveAsFreeName(ctx, rd, src.AccountID, parent.ID, name)
		if err != nil {
			return nil, err
		}
		return nil, &saveAsConflict{valid: valid}
	}
	if !writable {
		return nil, errSaveAsNotAuthorized
	}
	live, err := liveLockOf(ctx, rd, src.AccountID, taken.ID)
	if err != nil {
		return nil, err
	}
	if live != nil {
		return nil, &store.LockedError{Current: live.LockID, Reason: "the target file is locked by an editor"}
	}
	cur, err := requireNodeRole(ctx, rd, p, taken.ID, store.RoleEditor)
	if err != nil {
		if apierr.From(err).Code == apierr.Internal {
			return nil, err
		}
		return nil, errSaveAsNotAuthorized
	}
	return &saveAsDecision{parent: parent, name: name, key: key, replace: cur}, nil
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
	// A suggested target never answers 409 (the specification), so exhausting the names is a server answer, with the reason
	// logged by wopiFail. It is not realistic, since a thousand names of one base must all be taken.
	return "", "", apierr.New(apierr.Internal, "no free name was found for the copy: all 1000 suggested names are taken")
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

// saveAsOffered is the one Save As rule (decision 87): whether the caller may make a copy of the file n. It is false for a
// token minted by a delegated key, for a no-download file (a copy is a download), and for a caller who may not add a file
// to the file's directory. CheckFileInfo says UserCanNotWriteRelative when it is false, and PUT_RELATIVE answers 501 then.
// A caller the directory does not reach is refused as the others are, and any other failure is returned.
func saveAsOffered(ctx context.Context, rd saveAsReader, p *auth.Principal, n *store.Node, restricted bool) (bool, error) {
	if p == nil || (p.Token != nil && delegatedOrigin(p.Token)) {
		return false, nil
	}
	if restricted || n.ParentID == "" {
		return false, nil
	}
	_, role, err := nodeAccessWith(ctx, rd, saveAsPrincipal(p), n.ParentID)
	if err != nil {
		if apierr.From(err).Code == apierr.Internal {
			return false, err
		}
		return false, nil
	}
	return store.RoleRank(role) >= store.RoleRank(store.RoleContributor), nil
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
	// The refusal is the one CheckFileInfo states: UserCanNotWriteRelative true is a 501 (the specification). That covers a
	// delegated token, a no-download file (a copy is a download, decision 20) and a caller who may not add files there.
	offered, err := saveAsOffered(r.Context(), s.store, g.p, g.node, g.restricted)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	if !offered {
		s.saveAsFail(w, r, errSaveAsNotAuthorized)
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
	limit := int64(s.settings().Office.MaxFileBytes)
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
	acc := g.node.AccountID
	if _, err := saveAsDecide(ctx, s.store, saveAsPrincipal(p), g.node, requested, suggested, overwrite, g.writable()); err != nil {
		s.saveAsFail(w, r, err)
		return nil
	}
	// The quota is checked on the whole new size, with replaces 0: an overwrite is refused while the file is locked, so the
	// transaction charges all of the new bytes (replaceContent) and keeps the old content as a version. A pre-check that
	// subtracted the old size would pass a write the transaction refuses (decision 87, L1).
	c, err := s.writeBlob(w, r, acc, limit, 0)
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
		// Everything is decided again on the state this write sees (decision 87, L2): the token, the source (gone or
		// trashed is 404, and the copy's directory is the source's parent as read now), the rule, and the target.
		p, pl, err := s.authenticateWOPI(ctx, tx, g.token, s.store.Clock())
		if err != nil {
			return err
		}
		src, err := saveAsSourceOf(ctx, tx, p, g.node.ID)
		if err != nil {
			return err
		}
		offered, err := saveAsOffered(ctx, tx, p, src.node, src.restricted)
		if err != nil {
			return err
		}
		if !offered {
			return errSaveAsNotAuthorized
		}
		// The name comes from the source as this write reads it, not from the name it had before the body arrived, so a
		// source renamed during the upload gives a copy named from its new name (the round 3 nit).
		inWrite, inWriteSuggested, err := saveAsNames(src.node.Name, rel, sug)
		if err != nil {
			return err
		}
		dec, err := saveAsDecide(ctx, tx, saveAsPrincipal(p), src.node, inWrite, inWriteSuggested, overwrite, src.writable)
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
		// The copy's token may write when the caller may edit the copy itself, as an open of an editable file would. The role
		// is read on the copy's own node (the new file, or the file it replaced), never the directory's (decision 87, L4).
		_, cr, err := nodeAccessWith(ctx, tx, copyPrincipal(p), out.ID)
		if err != nil {
			return err
		}
		perm, role := permRead, "read"
		if store.RoleRank(cr) >= store.RoleRank(store.RoleEditor) {
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
