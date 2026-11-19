// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/dav"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
)

// The read-only WebDAV mount (decision 236). It presents a person's spaces as a drive: the root holds the personal
// folder, the team spaces the person reaches and a virtual "Shared with me"; below each space are its directories and
// files. It answers PROPFIND, GET, HEAD and OPTIONS only. A credential is an app password over Basic, or a delegated or
// device token over Bearer; a real password, a key and a share-link credential are refused. It is not an API route: it
// authenticates on its own and answers in XML, so it is registered on the mux apart from the route table.

const (
	davPrefix     = "/dav/"
	davRootName   = "Selfcloud"
	davSharedName = "Shared with me"
	davAllow      = "OPTIONS, PROPFIND, GET, HEAD"

	davReadRate       = "dav.read"
	davAuthFailedRate = "dav.auth_failed"
)

// davSpace is one top-level entry of the mount: a personal folder, a team space, or the virtual shared space.
type davSpace struct {
	name   string // the display name, disambiguated against its siblings
	key    string // its name key, for matching a path segment
	folder *store.Folder
	role   string
	shared bool
}

// davLoc is a resolved place in the mount: the root, a space root, or a node within a space. node is nil at the root
// and at the shared space's root; a folder space's root carries its folder's root node.
type davLoc struct {
	root  bool
	space *davSpace
	node  *store.Node
	role  string
	href  string
	name  string // a shared root's disambiguated display name, when it differs from the node's own
}

func (s *Server) davHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if info := infoFrom(r.Context()); info != nil {
			info.route = "DAV /dav/"
		}
		if err := s.serveDAV(w, r); err != nil {
			s.writeDAVError(w, r, err)
		}
	})
}

func (s *Server) serveDAV(w http.ResponseWriter, r *http.Request) error {
	if err := s.davEnabled(); err != nil {
		return err
	}
	info := infoFrom(r.Context())
	// Failed sign-ins are charged per address, and per app password when the credential names one; a success gives the
	// tokens back, so a working drive client never spends the budget. A request with no Authorization header is not a
	// failed sign-in: Windows and Finder probe anonymously first, so it is never charged.
	var charges []charge
	if r.Header.Get("Authorization") != "" {
		charges = append(charges, charge{name: davAuthFailedRate, key: info.ip.String()})
		if apwID, ok := davBasicID(r); ok {
			charges = append(charges, charge{name: davAuthFailedRate, key: apwID})
		}
	}
	hold, err := s.reserveAll(charges...)
	if err != nil {
		return err
	}
	p, apw, err := s.davAuthenticate(r)
	if err != nil {
		return err
	}
	s.refund(hold)
	if info != nil {
		info.principal = p
	}
	if err := s.rateLimit(davReadRate, p.UserID); err != nil {
		return err
	}
	if apw != nil {
		s.touchAppPassword(r.Context(), apw)
	}
	switch r.Method {
	case http.MethodOptions:
		w.Header().Set("DAV", "1")
		w.Header().Set("Allow", davAllow)
		w.WriteHeader(http.StatusOK)
		return nil
	case "PROPFIND":
		return s.davPropfind(w, r, p)
	case http.MethodGet, http.MethodHead:
		return s.davGet(w, r, p, apw)
	default:
		davMethodNotAllowed(w)
		return nil
	}
}

// writeDAVError renders an error as the mount's plain-text answer, with the headers its status calls for. It is not the
// API's JSON envelope: a drive client reads the status and the challenge, not a JSON code.
func (s *Server) writeDAVError(w http.ResponseWriter, r *http.Request, err error) {
	e := apierr.From(err)
	info := infoFrom(r.Context())
	if info != nil {
		info.err = e
		if info.wroteHeader() {
			return
		}
	}
	if e.Code == apierr.Unauthenticated {
		w.Header().Set("WWW-Authenticate", `Basic realm="Selfcloud", charset="UTF-8"`)
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(e.RetryAfter.Seconds()+0.5)))
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(e.Code.Status())
	fmt.Fprintln(w, e.Message)
}

func davMethodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", davAllow)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusMethodNotAllowed)
	fmt.Fprintln(w, "the WebDAV mount is read-only")
}

func davUnauth() error {
	return apierr.New(apierr.Unauthenticated, "the mount needs an app password or a delegated token")
}

// davAuthenticate identifies the caller from a Basic app password or a Bearer delegated/device token. It returns the
// app password row too, so its last use can be recorded; a token caller has none.
func (s *Server) davAuthenticate(r *http.Request) (*auth.Principal, *store.AppPassword, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return nil, nil, davUnauth()
	}
	scheme, cred, ok := strings.Cut(h, " ")
	cred = strings.TrimSpace(cred)
	if !ok || cred == "" {
		return nil, nil, davUnauth()
	}
	switch {
	case strings.EqualFold(scheme, "Basic"):
		return s.davAuthBasic(r, cred)
	case strings.EqualFold(scheme, "Bearer"):
		p, err := s.davAuthBearer(r.Context(), cred)
		return p, nil, err
	default:
		return nil, nil, davUnauth()
	}
}

func (s *Server) davAuthBasic(r *http.Request, cred string) (*auth.Principal, *store.AppPassword, error) {
	dec, err := base64.StdEncoding.DecodeString(cred)
	if err != nil {
		return nil, nil, davUnauth()
	}
	username, password, ok := strings.Cut(string(dec), ":")
	if !ok {
		return nil, nil, davUnauth()
	}
	// Basic carries a secret in the clear, so it is accepted only over https, or from the loopback interface.
	if !strings.HasPrefix(s.origin, "https://") {
		info := infoFrom(r.Context())
		if info == nil || !info.ip.IsLoopback() {
			return nil, nil, davUnauth()
		}
	}
	// Only an app password is a Basic credential; the person's real password does not parse as one and is refused.
	// The secret names its row by ID; the row is loaded by that ID and its hash compared in constant time, so a guess
	// at the random tail learns nothing from the lookup or the comparison.
	apwID, ok := appPasswordIDOf(password)
	if !ok {
		return nil, nil, davUnauth()
	}
	ctx := r.Context()
	a, err := s.store.AppPasswordByID(ctx, apwID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, davUnauth()
	}
	if err != nil {
		return nil, nil, err
	}
	if subtle.ConstantTimeCompare(a.SecretHash, auth.HashKey(password)) != 1 {
		return nil, nil, davUnauth()
	}
	u, err := s.store.User(ctx, a.AccountID, a.UserID)
	if err != nil || u.Status != store.UserActive || u.MustChangePassword {
		return nil, nil, davUnauth()
	}
	// The Basic user name is the person's email; its key must be the key of the user the app password belongs to.
	if emailKey(username) != u.EmailKey {
		return nil, nil, davUnauth()
	}
	acc, err := s.store.Account(ctx, a.AccountID)
	if err != nil || acc.Status != store.AccountActive {
		return nil, nil, davUnauth()
	}
	p := &auth.Principal{Kind: auth.KindUser, ID: u.ID, AccountID: a.AccountID, UserID: u.ID, Role: u.Role,
		Token: &auth.TokenScope{All: true, Write: a.Access == store.AppAccessWrite, Audience: "api", IssuedAt: store.Now()}}
	return p, a, nil
}

func (s *Server) davAuthBearer(ctx context.Context, cred string) (*auth.Principal, error) {
	var (
		p   *auth.Principal
		err error
	)
	switch {
	case strings.HasPrefix(cred, auth.TokenPrefix):
		p, err = s.authenticateToken(ctx, cred)
	case strings.HasPrefix(cred, auth.DevicePrefix):
		p, err = s.authenticateDevice(ctx, cred)
	default:
		return nil, davUnauth()
	}
	if err != nil {
		return nil, err
	}
	if p.Kind != auth.KindUser || p.MustChangePassword {
		return nil, davUnauth()
	}
	return p, nil
}

// davBasicID reads the app password ID out of a Basic credential, if it carries one, to key the failed-sign-in budget.
func davBasicID(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	scheme, cred, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return "", false
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cred))
	if err != nil {
		return "", false
	}
	_, password, ok := strings.Cut(string(dec), ":")
	if !ok {
		return "", false
	}
	return appPasswordIDOf(password)
}

func (s *Server) touchAppPassword(ctx context.Context, a *store.AppPassword) {
	now := store.Now()
	if a.LastUsedAt != nil && now.Sub(*a.LastUsedAt) <= keyTouchInterval {
		return
	}
	if err := s.store.TouchAppPassword(ctx, a.ID, now); err != nil {
		s.log.Warn("cannot record app password use", "app_password", a.ID, "error", err)
	}
}

// davPathSegs splits the request path below /dav/ into checked name segments. A trailing slash is ignored; an encoded
// dot segment, an empty segment or a name the store would refuse makes the path invalid.
func davPathSegs(r *http.Request) ([]pathSeg, error) {
	ep := r.URL.EscapedPath()
	if !strings.HasPrefix(ep, davPrefix) {
		return nil, apierr.New(apierr.InvalidPath, "the path is outside the mount")
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(ep, davPrefix), "/")
	if rest == "" {
		return nil, nil
	}
	var out []pathSeg
	for _, part := range strings.Split(rest, "/") {
		dec, err := url.PathUnescape(part)
		if err != nil {
			return nil, apierr.New(apierr.InvalidPath, "the path is not valid")
		}
		name, key, err := names.Clean(dec)
		if err != nil {
			return nil, apierr.New(apierr.InvalidPath, "the path is not valid")
		}
		out = append(out, pathSeg{name: name, key: key})
	}
	return out, nil
}

// davSpaces lists the mount's top-level spaces for the caller, in a stable order, with names disambiguated against
// their siblings. A folder-list token lists only its folders; every other caller lists the personal folder, the team
// spaces it reaches, and the virtual shared space.
func (s *Server) davSpaces(ctx context.Context, p *auth.Principal) ([]davSpace, error) {
	var ids []string
	folderList := p.Token != nil && !p.Token.All
	if folderList {
		ids = append(ids, p.Token.Folders...)
	} else {
		own, err := s.store.PersonalFolder(ctx, p.AccountID, p.UserID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if own != nil {
			ids = append(ids, own.ID)
		}
		hide := store.HideFilter{UserID: p.UserID}
		page := store.Page{Limit: 500}
		if p.Role == auth.RoleOwner {
			for {
				folders, next, err := s.store.Folders(ctx, p.AccountID, store.FolderFilter{Kind: store.KindTeam, Hide: hide}, page)
				if err != nil {
					return nil, storeErr(err)
				}
				for _, f := range folders {
					ids = append(ids, f.ID)
				}
				if next == "" {
					break
				}
				page.Cursor = next
			}
		} else {
			for {
				reached, next, err := s.store.RootReachedFolders(ctx, p.AccountID, p.UserID, hide, page)
				if err != nil {
					return nil, storeErr(err)
				}
				for _, rf := range reached {
					ids = append(ids, rf.Folder.ID)
				}
				if next == "" {
					break
				}
				page.Cursor = next
			}
		}
	}

	var spaces []davSpace
	// Siblings are told apart by cleaned key, not by display spelling, so "Sales" and "sales" clash. The virtual
	// shared space's key is reserved first, so a folder named like it takes the suffix and the virtual space keeps
	// its name.
	taken := map[string]bool{}
	_, sharedKey, _ := names.Clean(davSharedName)
	if !folderList {
		taken[sharedKey] = true
	}
	for _, fid := range ids {
		f, role, err := folderAccessWith(ctx, s.store, p, fid)
		if err != nil {
			if apierr.From(err).Code == apierr.NotFound {
				continue
			}
			return nil, err
		}
		name := f.Name
		if f.Kind == store.KindPersonal {
			name = store.PersonalFolderName
		}
		name = davUniqueName(name, taken)
		_, key, _ := names.Clean(name)
		spaces = append(spaces, davSpace{name: name, key: key, folder: f, role: role})
	}
	if !folderList {
		spaces = append(spaces, davSpace{name: davSharedName, key: sharedKey, role: store.RoleViewer, shared: true})
	}
	return spaces, nil
}

// davUniqueName appends " (2)", " (3)", … until the name's cleaned key is one no sibling holds, so two siblings whose
// names differ only in case are both addressable. The base is cut to fit the suffix within the name limit rather than
// overflow it.
func davUniqueName(name string, taken map[string]bool) string {
	_, key, err := names.Clean(name)
	if err != nil {
		return name
	}
	cand, candKey := name, key
	for i := 2; taken[candKey]; i++ {
		suffix := fmt.Sprintf(" (%d)", i)
		base := strings.TrimRight(truncateBytes(name, maxNameBytes-len(suffix)), " ")
		cand = base + suffix
		if _, candKey, err = names.Clean(cand); err != nil {
			return name
		}
	}
	taken[candKey] = true
	return cand
}

func davFindSpace(spaces []davSpace, seg pathSeg) *davSpace {
	for i := range spaces {
		if spaces[i].key == seg.key {
			return &spaces[i]
		}
	}
	return nil
}

// davResolve maps the request path to a place in the mount. A path that names nothing, escapes through a dot segment,
// or passes through a shortcut or a file answers not_found.
func (s *Server) davResolve(r *http.Request, p *auth.Principal) (*davLoc, error) {
	segs, err := davPathSegs(r)
	if err != nil {
		return nil, notFound("path")
	}
	if len(segs) == 0 {
		return &davLoc{root: true, href: davPrefix}, nil
	}
	ctx := r.Context()
	spaces, err := s.davSpaces(ctx, p)
	if err != nil {
		return nil, err
	}
	sp := davFindSpace(spaces, segs[0])
	if sp == nil {
		return nil, notFound("path")
	}
	rest := segs[1:]
	baseHref := davPrefix + dav.EncodeHref(sp.name) + "/"

	var from *store.Node
	var sharedRootName string
	if sp.shared {
		roots, err := s.davSharedRoots(ctx, p)
		if err != nil {
			return nil, err
		}
		if len(rest) == 0 {
			return &davLoc{space: sp, href: baseHref, role: store.RoleViewer}, nil
		}
		for _, rt := range roots {
			if rt.key == rest[0].key {
				from, sharedRootName = rt.node, rt.name
				break
			}
		}
		if from == nil {
			return nil, notFound("path")
		}
		baseHref += dav.EncodeHref(sharedRootName)
		if from.Kind == store.KindDir {
			baseHref += "/"
		}
		rest = rest[1:]
	} else {
		from, err = s.store.RootNode(ctx, p.AccountID, sp.folder.ID)
		if err != nil {
			return nil, notFound("path")
		}
	}
	cur, href, err := s.davWalk(ctx, p, from, rest, baseHref)
	if err != nil {
		return nil, err
	}
	n, role, err := nodeAccessWith(ctx, s.store, p, cur.ID)
	if err != nil {
		if apierr.From(err).Code == apierr.NotFound {
			return nil, notFound("path")
		}
		return nil, err
	}
	if n.Kind == store.KindShortcut {
		return nil, notFound("path")
	}
	loc := &davLoc{space: sp, node: n, href: href, role: role}
	if sp.shared && cur == from {
		loc.name = sharedRootName
	}
	return loc, nil
}

// davWalk follows segs from the directory from, building the href as it goes. A shortcut, or a file stood in the way
// of a deeper segment, answers not_found.
func (s *Server) davWalk(ctx context.Context, p *auth.Principal, from *store.Node, segs []pathSeg, href string) (*store.Node, string, error) {
	cur := from
	for _, seg := range segs {
		if cur.Kind != store.KindDir {
			return nil, "", notFound("path")
		}
		child, err := s.store.Child(ctx, p.AccountID, cur.ID, seg.key)
		if errors.Is(err, store.ErrNotFound) {
			return nil, "", notFound("path")
		}
		if err != nil {
			return nil, "", err
		}
		if child.Kind == store.KindShortcut {
			return nil, "", notFound("path")
		}
		cur = child
		href += dav.EncodeHref(child.Name)
		if child.Kind == store.KindDir {
			href += "/"
		}
	}
	return cur, href, nil
}

// davSharedNodes lists the nodes shared with the caller that the caller actually reaches, the children of the virtual
// shared space. As handleSharedWithMe does, the reach of the whole page is read in one query and a node the caller
// does not reach (a trashed node, one outside a token's scope) is left out; an owner is passed the owner argument so
// team items, already listed as spaces, are not shared a second time. Shortcuts are left out, as everywhere in the mount.
func (s *Server) davSharedNodes(ctx context.Context, p *auth.Principal) ([]*store.Node, error) {
	var ids []string
	page := store.Page{Limit: 500}
	for {
		rows, next, err := s.store.SharedNodesFiltered(ctx, p.AccountID, p.UserID, p.Role == auth.RoleOwner, false, store.ListFilter{}, page)
		if err != nil {
			return nil, storeErr(err)
		}
		for _, r := range rows {
			ids = append(ids, r.NodeID)
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if len(ids) == 0 {
		return nil, nil
	}
	reach, err := reachRoles(ctx, s.store, p, ids)
	if err != nil {
		return nil, err
	}
	byID, err := s.store.NodesByID(ctx, p.AccountID, ids)
	if err != nil {
		return nil, err
	}
	var out []*store.Node
	for _, id := range ids {
		if n := byID[id]; n != nil && reach[id] != "" && n.Kind != store.KindShortcut {
			out = append(out, n)
		}
	}
	return out, nil
}

// davSharedRoot is one child of the virtual shared space: a shared node with its name disambiguated against the other
// shared roots, so two of one name are both addressable.
type davSharedRoot struct {
	name string
	key  string
	node *store.Node
}

// davSharedRoots lists the shared roots with their names disambiguated by cleaned key, in the stable order the shared
// nodes come in.
func (s *Server) davSharedRoots(ctx context.Context, p *auth.Principal) ([]davSharedRoot, error) {
	nodes, err := s.davSharedNodes(ctx, p)
	if err != nil {
		return nil, err
	}
	var out []davSharedRoot
	taken := map[string]bool{}
	for _, n := range nodes {
		name := davUniqueName(n.Name, taken)
		_, key, _ := names.Clean(name)
		out = append(out, davSharedRoot{name: name, key: key, node: n})
	}
	return out, nil
}

func (s *Server) davPropfind(w http.ResponseWriter, r *http.Request, p *auth.Principal) error {
	switch r.Header.Get("Depth") {
	case "0", "1":
	default:
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_ = dav.WriteFiniteDepthError(w)
		return nil
	}
	pf, err := dav.ParsePropfind(w, r)
	if err != nil {
		switch {
		case errors.Is(err, dav.ErrBodyTooLarge):
			return apierr.New(apierr.TooLarge, "the PROPFIND body is too large")
		case errors.Is(err, dav.ErrTooManyProps):
			return apierr.New(apierr.InvalidRequest, "a PROPFIND may ask for at most 64 properties")
		default:
			return apierr.New(apierr.InvalidRequest, "the PROPFIND body is not a valid propfind")
		}
	}
	loc, err := s.davResolve(r, p)
	if err != nil {
		return err
	}
	ctx := r.Context()
	children := r.Header.Get("Depth") == "1"
	// The child count is bounded before any of the answer is written, so an over-large listing is a clean 403 rather
	// than a truncated 207, and the answer that follows is streamed one response at a time.
	if children {
		n, err := s.davCountChildren(ctx, p, loc)
		if err != nil {
			return err
		}
		if n > davMaxChildren {
			return apierr.New(apierr.Forbidden, "too many items to list at once; open a narrower folder")
		}
	}
	resp, err := s.davResponse(ctx, loc, pf)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	mw := dav.NewMultistatusWriter(w, pf.Props)
	if err := mw.WriteResponse(resp); err != nil {
		return err
	}
	if children {
		err := s.davEachChild(ctx, p, loc, func(child *davLoc) error {
			cr, err := s.davResponse(ctx, child, pf)
			if err != nil {
				return err
			}
			return mw.WriteResponse(cr)
		})
		if err != nil {
			return err
		}
	}
	return mw.Close()
}

// davMaxChildren is the most children a Depth 1 listing answers; more is refused so one request cannot build an
// unbounded answer.
const davMaxChildren = 10000

// errDavTooManyChildren stops the counting walk once it passes davMaxChildren.
var errDavTooManyChildren = errors.New("dav: too many children to list")

// davCountChildren counts the children of a place, stopping once it passes davMaxChildren so the count itself is
// bounded.
func (s *Server) davCountChildren(ctx context.Context, p *auth.Principal, loc *davLoc) (int, error) {
	n := 0
	err := s.davEachChild(ctx, p, loc, func(*davLoc) error {
		n++
		if n > davMaxChildren {
			return errDavTooManyChildren
		}
		return nil
	})
	if errors.Is(err, errDavTooManyChildren) {
		return n, nil
	}
	return n, err
}

// davEachChild walks the children of a place one at a time, calling yield for each: the spaces at the root, the
// shared roots at the shared space's root, and the directory's children below a space. Shortcuts are never listed.
// A yield error stops the walk.
func (s *Server) davEachChild(ctx context.Context, p *auth.Principal, loc *davLoc, yield func(*davLoc) error) error {
	switch {
	case loc.root:
		spaces, err := s.davSpaces(ctx, p)
		if err != nil {
			return err
		}
		for i := range spaces {
			sp := &spaces[i]
			child := davLoc{space: sp, href: davPrefix + dav.EncodeHref(sp.name) + "/", role: sp.role}
			if !sp.shared {
				root, err := s.store.RootNode(ctx, p.AccountID, sp.folder.ID)
				if err != nil {
					return err
				}
				child.node = root
			}
			if err := yield(&child); err != nil {
				return err
			}
		}
	case loc.space != nil && loc.space.shared && loc.node == nil:
		roots, err := s.davSharedRoots(ctx, p)
		if err != nil {
			return err
		}
		for i := range roots {
			rt := &roots[i]
			child := davLoc{space: loc.space, node: rt.node, href: davChildHref(loc.href, rt.name, rt.node), role: loc.role, name: rt.name}
			if err := yield(&child); err != nil {
				return err
			}
		}
	case loc.node != nil && loc.node.Kind == store.KindDir:
		page := store.Page{Limit: 500}
		for {
			kids, next, err := s.store.Children(ctx, p.AccountID, loc.node.ID, page)
			if err != nil {
				return storeErr(err)
			}
			for _, k := range kids {
				if k.Kind == store.KindShortcut {
					continue
				}
				child := davLoc{space: loc.space, node: k, href: davChildHref(loc.href, k.Name, k), role: loc.role}
				if err := yield(&child); err != nil {
					return err
				}
			}
			if next == "" {
				break
			}
			page.Cursor = next
		}
	}
	return nil
}

func davChildHref(parent, name string, n *store.Node) string {
	href := parent + dav.EncodeHref(name)
	if n.Kind == store.KindDir {
		href += "/"
	}
	return href
}

// davResponse builds one resource's answer for the kind of PROPFIND: all its properties, their names only, or the ones
// it asked for split into found and not found.
func (s *Server) davResponse(ctx context.Context, loc *davLoc, pf dav.Propfind) (dav.Response, error) {
	props, err := s.davProps(ctx, loc)
	if err != nil {
		return dav.Response{}, err
	}
	resp := dav.Response{Href: loc.href}
	switch pf.Kind {
	case dav.Prop:
		byName := make(map[xml.Name]dav.Property, len(props))
		for _, pr := range props {
			byName[pr.Name] = pr
		}
		var found, missing []dav.Property
		for _, want := range pf.Props {
			if pr, ok := byName[want]; ok {
				found = append(found, pr)
			} else {
				missing = append(missing, dav.Property{Name: want})
			}
		}
		if len(found) > 0 {
			resp.Propstats = append(resp.Propstats, dav.Propstat{Status: dav.StatusOK, Props: found})
		}
		if len(missing) > 0 {
			resp.Propstats = append(resp.Propstats, dav.Propstat{Status: dav.StatusNotFound, Props: missing})
		}
	case dav.Propname:
		bare := make([]dav.Property, len(props))
		for i, pr := range props {
			bare[i] = dav.Property{Name: pr.Name}
		}
		resp.Propstats = []dav.Propstat{{Status: dav.StatusOK, Props: bare}}
	default:
		resp.Propstats = []dav.Propstat{{Status: dav.StatusOK, Props: props}}
	}
	return resp, nil
}

// davProps is the set of properties a place carries. A folder space's root also carries its quota.
func (s *Server) davProps(ctx context.Context, loc *davLoc) ([]dav.Property, error) {
	name := davRootName
	collection := true
	var n *store.Node
	switch {
	case loc.root:
	case loc.node == nil:
		name = loc.space.name
	default:
		n = loc.node
		name = n.Name
		collection = n.Kind == store.KindDir
		if n.ParentID == "" && loc.space != nil {
			name = loc.space.name
		}
	}
	if loc.name != "" {
		name = loc.name
	}
	props := []dav.Property{dav.TextProp("displayname", name), dav.ResourceType(collection)}
	if n != nil {
		props = append(props,
			dav.TextProp("getlastmodified", n.ModifiedAt.UTC().Format(http.TimeFormat)),
			dav.TextProp("creationdate", n.CreatedAt.UTC().Format(time.RFC3339)))
		if n.Kind == store.KindFile {
			props = append(props,
				dav.TextProp("getetag", nodeETag(n)),
				dav.TextProp("getcontentlength", strconv.FormatInt(n.Size, 10)),
				dav.TextProp("getcontenttype", n.MimeType))
		}
	}
	if n != nil && n.ParentID == "" && loc.space != nil && loc.space.folder != nil {
		qs, err := s.store.QuotaStateOf(ctx, loc.space.folder.AccountID, loc.space.folder.ID)
		if err != nil {
			return nil, err
		}
		used := qs.Folder.Bytes + qs.Folder.Reserved
		props = append(props, dav.TextProp("quota-used-bytes", strconv.FormatInt(used, 10)))
		// A team space is bounded by the account's quota alone; a personal folder by the smaller of the person's own
		// limit and the account's left. With no bound at all there is no available figure to report.
		accountLeft := int64(0)
		if qs.AccountQuota > 0 {
			accountLeft = max(qs.AccountQuota-(qs.Account.Bytes+qs.Account.Reserved), 0)
		}
		var avail int64
		hasAvail := false
		if loc.space.folder.Kind == store.KindTeam {
			if qs.AccountQuota > 0 {
				avail, hasAvail = accountLeft, true
			}
		} else {
			if qs.UserLimit > 0 {
				avail, hasAvail = max(qs.UserLimit-used, 0), true
			}
			if qs.AccountQuota > 0 && (!hasAvail || accountLeft < avail) {
				avail, hasAvail = accountLeft, true
			}
		}
		if hasAvail {
			props = append(props, dav.TextProp("quota-available-bytes", strconv.FormatInt(avail, 10)))
		}
	}
	return props, nil
}

func (s *Server) davGet(w http.ResponseWriter, r *http.Request, p *auth.Principal, apw *store.AppPassword) error {
	loc, err := s.davResolve(r, p)
	if err != nil {
		return err
	}
	if loc.node == nil || loc.node.Kind != store.KindFile {
		davMethodNotAllowed(w)
		return nil
	}
	restricted, err := s.downloadRestricted(r.Context(), loc.node, loc.role)
	if err != nil {
		return err
	}
	if restricted {
		return errDownloadRestricted()
	}
	// A GET counts as a view, as the content route does; a HEAD does not. An app-password sign-in carries a token
	// scope, which noteView skips, so the view is recorded for the person acting for itself.
	if r.Method == http.MethodGet {
		vp := p
		if apw != nil {
			vp = &auth.Principal{Kind: auth.KindUser, ID: p.UserID, AccountID: p.AccountID, UserID: p.UserID, Role: p.Role}
		}
		s.noteView(vp, loc.node)
	}
	return s.serveFile(w, r, loc.node, false, corpSameOrigin)
}
