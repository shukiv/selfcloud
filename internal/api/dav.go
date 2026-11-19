// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
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
	// tokens back, so a working drive client never spends the budget.
	charges := []charge{{name: davAuthFailedRate, key: info.ip.String()}}
	if apwID, ok := davBasicID(r); ok {
		charges = append(charges, charge{name: davAuthFailedRate, key: apwID})
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
		return s.davGet(w, r, p)
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
		w.Header().Set("WWW-Authenticate", `Basic realm="Selfcloud"`)
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
	_, password, ok := strings.Cut(string(dec), ":")
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
	// Only an app password is a Basic credential; the person's real password is refused.
	if !strings.HasPrefix(password, auth.AppPasswordPrefix) {
		return nil, nil, davUnauth()
	}
	ctx := r.Context()
	a, err := s.store.AppPasswordByHash(ctx, auth.HashKey(password))
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, davUnauth()
	}
	if err != nil {
		return nil, nil, err
	}
	if a.RevokedAt != nil {
		return nil, nil, davUnauth()
	}
	u, err := s.store.User(ctx, a.AccountID, a.UserID)
	if err != nil || u.Status != store.UserActive || u.MustChangePassword {
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
	taken := map[string]bool{}
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
		name := davUniqueName(davSharedName, taken)
		_, key, _ := names.Clean(name)
		spaces = append(spaces, davSpace{name: name, key: key, role: store.RoleViewer, shared: true})
	}
	return spaces, nil
}

// davUniqueName appends " (2)", " (3)", … until the name is one no sibling holds, so two spaces of the same name are
// both addressable.
func davUniqueName(name string, taken map[string]bool) string {
	cand := name
	for i := 2; taken[cand]; i++ {
		cand = fmt.Sprintf("%s (%d)", name, i)
	}
	taken[cand] = true
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
	if sp.shared {
		nodes, err := s.davSharedNodes(ctx, p)
		if err != nil {
			return nil, err
		}
		if len(rest) == 0 {
			return &davLoc{space: sp, href: baseHref, role: store.RoleViewer}, nil
		}
		for _, n := range nodes {
			if n.NameKey == rest[0].key {
				from = n
				break
			}
		}
		if from == nil {
			return nil, notFound("path")
		}
		baseHref += dav.EncodeHref(from.Name)
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
	return &davLoc{space: sp, node: n, href: href, role: role}, nil
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

// davSharedNodes lists the nodes shared with the caller, the children of the virtual shared space. Shortcuts are left
// out, as they are everywhere in the mount.
func (s *Server) davSharedNodes(ctx context.Context, p *auth.Principal) ([]*store.Node, error) {
	var ids []string
	page := store.Page{Limit: 500}
	for {
		rows, next, err := s.store.SharedNodesFiltered(ctx, p.AccountID, p.UserID, false, false, store.ListFilter{}, page)
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
	byID, err := s.store.NodesByID(ctx, p.AccountID, ids)
	if err != nil {
		return nil, err
	}
	var out []*store.Node
	for _, id := range ids {
		if n := byID[id]; n != nil && n.Kind != store.KindShortcut {
			out = append(out, n)
		}
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
		if errors.Is(err, dav.ErrBodyTooLarge) {
			return apierr.New(apierr.TooLarge, "the PROPFIND body is too large")
		}
		return apierr.New(apierr.InvalidRequest, "the PROPFIND body is not a valid propfind")
	}
	loc, err := s.davResolve(r, p)
	if err != nil {
		return err
	}
	ms, err := s.davMultistatus(r.Context(), p, loc, r.Header.Get("Depth") == "1", pf)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_ = dav.WriteMultistatus(w, ms)
	return nil
}

func (s *Server) davMultistatus(ctx context.Context, p *auth.Principal, loc *davLoc, children bool, pf dav.Propfind) (dav.Multistatus, error) {
	var ms dav.Multistatus
	resp, err := s.davResponse(ctx, loc, pf)
	if err != nil {
		return ms, err
	}
	ms.Responses = append(ms.Responses, resp)
	if !children {
		return ms, nil
	}
	kids, err := s.davChildren(ctx, p, loc)
	if err != nil {
		return ms, err
	}
	for _, k := range kids {
		kr, err := s.davResponse(ctx, &k, pf)
		if err != nil {
			return ms, err
		}
		ms.Responses = append(ms.Responses, kr)
	}
	return ms, nil
}

// davChildren lists the children of a place: the spaces at the root, the shared nodes at the shared space's root, and
// the directory's children below a space. Shortcuts are never listed.
func (s *Server) davChildren(ctx context.Context, p *auth.Principal, loc *davLoc) ([]davLoc, error) {
	var out []davLoc
	switch {
	case loc.root:
		spaces, err := s.davSpaces(ctx, p)
		if err != nil {
			return nil, err
		}
		for i := range spaces {
			sp := &spaces[i]
			child := davLoc{space: sp, href: davPrefix + dav.EncodeHref(sp.name) + "/", role: sp.role}
			if !sp.shared {
				root, err := s.store.RootNode(ctx, p.AccountID, sp.folder.ID)
				if err != nil {
					return nil, err
				}
				child.node = root
			}
			out = append(out, child)
		}
	case loc.space != nil && loc.space.shared && loc.node == nil:
		nodes, err := s.davSharedNodes(ctx, p)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			out = append(out, davLoc{space: loc.space, node: n, href: davChildHref(loc.href, n), role: loc.role})
		}
	case loc.node != nil && loc.node.Kind == store.KindDir:
		page := store.Page{Limit: 500}
		for {
			kids, next, err := s.store.Children(ctx, p.AccountID, loc.node.ID, page)
			if err != nil {
				return nil, storeErr(err)
			}
			for _, k := range kids {
				if k.Kind == store.KindShortcut {
					continue
				}
				out = append(out, davLoc{space: loc.space, node: k, href: davChildHref(loc.href, k), role: loc.role})
			}
			if next == "" {
				break
			}
			page.Cursor = next
		}
	}
	return out, nil
}

func davChildHref(parent string, n *store.Node) string {
	href := parent + dav.EncodeHref(n.Name)
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
	props := []dav.Property{dav.TextProp("displayname", name), dav.ResourceType(collection)}
	if n != nil {
		props = append(props,
			dav.TextProp("getlastmodified", n.ModifiedAt.UTC().Format(http.TimeFormat)),
			dav.TextProp("creationdate", n.CreatedAt.UTC().Format(time.RFC3339)),
			dav.TextProp("getetag", nodeETag(n)))
		if n.Kind == store.KindFile {
			props = append(props,
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
		limit := qs.UserLimit
		if loc.space.folder.Kind == store.KindTeam {
			limit = qs.AccountQuota
		}
		if limit > 0 {
			props = append(props, dav.TextProp("quota-available-bytes", strconv.FormatInt(max(limit-used, 0), 10)))
		}
	}
	return props, nil
}

func (s *Server) davGet(w http.ResponseWriter, r *http.Request, p *auth.Principal) error {
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
	return s.serveFile(w, r, loc.node, false, corpSameOrigin)
}
