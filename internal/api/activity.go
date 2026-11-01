// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// The activity of a node (design section 14, slice r4, decision 50) is a projection of the audit log: the entries whose
// target is the node, filtered by what the reader may see and cut down to the few facts a pane needs. Nothing is written
// for it. The projection is a list of actions per kind, so an action the code adds later is not shown until it is listed
// here, and each action has a list of the details it keeps. A raw ID of a person, a node, a group, a field or a version
// never leaves in an entry: people and fields are named, and a name the reader may not learn is left out.

// activityKind is a group of actions the pane lists and the filter ?kind= names.
type activityKind struct {
	name string
	// manager says that only a manager of the node, or the account key, reads this kind. A viewer asking for it gets an
	// empty page, the same answer as a node with no such events.
	manager bool
	actions []string
}

// activityKinds is every action the pane may list. An action that is in no kind is never listed. link.create and its
// kin are not here although the design lists them: they name the link as their target and carry no node, so a read by
// the node's ID cannot find them (decision 50).
var activityKinds = []activityKind{
	{name: "content", actions: []string{"node.create", "node.write", "node.rename", "node.move", "node.trash",
		"node.restore", "node.version.restore", "node.copy"}},
	{name: "comment", actions: []string{"comment.create", "comment.delete"}},
	{name: "access", manager: true, actions: []string{"permission.set", "permission.expired"}},
	{name: "label", manager: true, actions: []string{"node.label_set", "node.label_clear"}},
}

// activityKindNames are the values ?kind= accepts, for the error message.
func activityKindNames() string {
	names := make([]string, len(activityKinds))
	for i, k := range activityKinds {
		names[i] = k.name
	}
	return strings.Join(names, ", ")
}

// activityKindOf returns the kind an action belongs to.
func activityKindOf(action string) string {
	for _, k := range activityKinds {
		if slices.Contains(k.actions, action) {
			return k.name
		}
	}
	return ""
}

// activityActions returns the actions a reader of the given role may list, narrowed to one kind when kind is not empty.
// Comment actions are left out while the account's comments are off, as the comment routes are closed then.
func activityActions(role, kind string, commentsOn bool) []string {
	var out []string
	for _, k := range activityKinds {
		if kind != "" && k.name != kind {
			continue
		}
		if k.manager && store.RoleRank(role) < store.RoleRank(store.RoleManager) {
			continue
		}
		if k.name == "comment" && !commentsOn {
			continue
		}
		out = append(out, k.actions...)
	}
	return out
}

// activityActor is who did it, as a reader may learn it: a kind and a name, never an ID. Gone is set for a person who
// no longer reaches the node (or no longer exists), whose name is not given: the interface shows "former member". Admin
// is an installation admin who acted while signed in as a member (decision 41), named by no one.
type activityActor struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Gone bool   `json:"gone,omitempty"`
	Mine bool   `json:"mine,omitempty"`
}

// activityJSON is one entry of the pane. ID is opaque and stable, so a client can tell entries apart; it is not the audit
// ID. Details hold only the keys activityDetails allows for the action, plus the names the handler adds.
type activityJSON struct {
	ID      string         `json:"id"`
	Time    time.Time      `json:"time"`
	Kind    string         `json:"kind"`
	Action  string         `json:"action"`
	Actor   activityActor  `json:"actor"`
	Details map[string]any `json:"details,omitempty"`
}

// pickString copies a detail that is one of the listed words. Nothing else of a string detail passes, so a name cannot.
func pickString(out, d map[string]any, key string, allowed ...string) {
	if s, ok := d[key].(string); ok && slices.Contains(allowed, s) {
		out[key] = s
	}
}

// pickFlag copies a detail that is a yes-or-no.
func pickFlag(out, d map[string]any, key string) {
	if v, ok := d[key].(bool); ok {
		out[key] = v
	}
}

var activityRoles = []string{store.RoleViewer, store.RoleCommenter, store.RoleContributor, store.RoleEditor, store.RoleManager}

// activityDetails keeps, of the details an audit entry holds, the facts the pane shows for its action. It names no node
// and no person: a rename keeps neither name, a move neither parent, a copy not its source, a restore not its parent.
// Every other key is dropped, so a key a later change adds to an entry is not shown until it is listed here.
func activityDetails(action string, d map[string]any) map[string]any {
	out := map[string]any{}
	switch action {
	case "node.create":
		// The web sentence for a creation says folder or file, and a visitor's upload, from these two.
		pickString(out, d, "kind", store.KindFile, store.KindDir)
		pickString(out, d, "via", "share_link")
	case "comment.create":
		// The web sentence says a reply.
		pickFlag(out, d, "reply")
	case "permission.set", "permission.expired":
		// The role granted is the point of an access entry.
		pickString(out, d, "role", activityRoles...)
	}
	return out
}

// activityCursorLabel keeps the key of activity cursors and entry IDs apart from every other use of the links key.
const (
	activityCursorLabel = "selfcloud activity cursor v1"
	activityIDLabel     = "selfcloud activity id v1"
)

// activityCursorBad is the one answer to a cursor that does not open, whatever the reason.
var activityCursorBad = apierr.New(apierr.InvalidRequest, "the cursor is not valid for this list; start again")

func (s *Server) activityKey(label string) ([]byte, error) {
	return hkdf.Key(sha256.New, s.links.current, nil, label, 32)
}

// activityCursorAAD binds a cursor to its account, its caller, its node and its kind filter. A cursor from another
// node, another caller or another filter does not open.
func activityCursorAAD(p *auth.Principal, nodeID, kind string) []byte {
	return []byte(strings.Join([]string{p.AccountID, string(p.Kind), p.ID, nodeID, kind}, "\x00"))
}

// sealActivityCursor returns the cursor that resumes after the entry with the given audit ID. It is sealed with AES-GCM,
// as a search cursor is, because audit IDs run on one sequence for every account: a plain number would show how much
// the other accounts write.
func (s *Server) sealActivityCursor(p *auth.Principal, nodeID, kind string, auditID int64) (string, error) {
	key, err := s.activityKey(activityCursorLabel)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	plain := binary.BigEndian.AppendUint64(nil, uint64(auditID))
	sealed := aead.Seal(nonce, nonce, plain, activityCursorAAD(p, nodeID, kind))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openActivityCursor returns the audit ID a cursor resumes after.
func (s *Server) openActivityCursor(p *auth.Principal, nodeID, kind, c string) (int64, error) {
	key, err := s.activityKey(activityCursorLabel)
	if err != nil {
		return 0, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return 0, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return 0, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || len(raw) < aead.NonceSize() {
		return 0, activityCursorBad
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], activityCursorAAD(p, nodeID, kind))
	if err != nil || len(plain) != 8 {
		return 0, activityCursorBad
	}
	v := int64(binary.BigEndian.Uint64(plain))
	if v <= 0 {
		return 0, activityCursorBad
	}
	return v, nil
}

// activityEntryID is the opaque ID of an entry: a keyed hash of the account and the audit ID. It is stable, and it shows
// neither the number nor anything about the other accounts.
func (s *Server) activityEntryID(accountID string, auditID int64) (string, error) {
	key, err := s.activityKey(activityIDLabel)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(accountID))
	m.Write([]byte{0})
	m.Write([]byte(strconv.FormatInt(auditID, 10)))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:15]), nil
}

// activityPeople answers, for the people an entry names, whether each reaches the node now and what the reader may
// learn of its name. A person is learned once per page. The caller reaches the node by the check that came before, so it
// is not walked again.
type activityPeople struct {
	reach map[string]bool
	names map[string]string
	// unchecked are the people past the bound: not walked, so neither named nor called gone. Gone says the person lost the
	// role, and a person nobody looked at must not be said to have.
	unchecked map[string]bool
}

// maxActivityWalks is the most distinct people one request walks for reach. Each walk is about five queries, and a page
// of 500 entries could name hundreds of people. Memoised within the request: a person is walked once.
const maxActivityWalks = 40

// peopleFor walks reach for each distinct user and reads the names of those who reach the node.
func (s *Server) peopleFor(ctx context.Context, p *auth.Principal, accountID, nodeID string, ids []string, bound int) (*activityPeople, error) {
	out := &activityPeople{reach: map[string]bool{}, names: map[string]string{}, unchecked: map[string]bool{}}
	var reaching []string
	walked := 0
	for _, u := range ids {
		if _, done := out.reach[u]; done {
			continue
		}
		ok := false
		switch {
		case u == "":
		case u == p.UserID && p.Kind == auth.KindUser:
			ok = true
		case walked >= bound:
			// Past the bound a person is not walked: no name and no gone flag, a state of its own.
			out.unchecked[u] = true
		default:
			walked++
			var err error
			if ok, err = reachesNode(ctx, s.store, accountID, nodeID, u); err != nil {
				return nil, err
			}
		}
		out.reach[u] = ok
		if ok {
			reaching = append(reaching, u)
		}
	}
	if peopleNamesSeen(p, accountID) && len(reaching) > 0 {
		names, err := s.store.UserNames(ctx, accountID, reaching)
		if err != nil {
			return nil, err
		}
		out.names = names
	}
	return out, nil
}

// handleNodeActivity lists the activity of a node, newest first. A viewer or above reads the content and comment
// kinds; a manager of the node and the account key also read the access and label kinds. The node must be reached and not
// in the trash (not_found otherwise, as for every node route). A link that anyone can open does not reach the route.
func (s *Server) handleNodeActivity(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	kind := r.URL.Query().Get("kind")
	if kind != "" && !slices.ContainsFunc(activityKinds, func(k activityKind) bool { return k.name == kind }) {
		return apierr.Newf(apierr.InvalidRequest, "kind must be one of %s", activityKindNames())
	}
	if err := s.chargeAll(charge{"activity", string(p.Kind) + ":" + p.ID}, charge{"activity.account", p.AccountID}); err != nil {
		return err
	}
	ctx := r.Context()
	n, role, err := nodeAccessWith(ctx, s.store, p, nodeID)
	if err != nil {
		return err
	}
	// A shortcut has no history of its own to show, as it has no comments (decision 39).
	if n.Kind == store.KindShortcut {
		return nodeNotFound()
	}
	var before int64
	if page.Cursor != "" {
		if before, err = s.openActivityCursor(p, nodeID, kind, page.Cursor); err != nil {
			return err
		}
	}
	commentsOn, err := s.store.CommentsOn(ctx, p.AccountID)
	if err != nil {
		return err
	}
	f, err := s.store.Folder(ctx, n.AccountID, n.FolderID)
	if errors.Is(err, store.ErrNotFound) {
		return nodeNotFound()
	}
	if err != nil {
		return err
	}
	read := store.ActivityRead{
		AccountID: p.AccountID, NodeID: nodeID,
		Actions: activityActions(role, kind, commentsOn),
		// The account key itself, not a token it minted: a token acts with the key's reach but never sees more than a
		// user does (as ManagesAccount does).
		SeeAll:         p.Kind == auth.KindAccountKey && p.Token == nil,
		ViewerUserID:   p.UserID,
		HideRestricted: store.RoleRank(role) < store.RoleRank(store.RoleManager),
		Before:         before, Limit: page.Limit,
	}
	if f.Kind == store.KindPersonal {
		read.PrivateOwner = f.OwnerUserID
	}
	res, err := s.store.Activity(ctx, read, s.activityCeiling)
	if err != nil {
		return err
	}
	items, err := s.activityItems(ctx, p, n, res.Rows)
	if err != nil {
		return err
	}
	out := listResponse[activityJSON]{Items: items}
	if res.Next != 0 {
		if out.NextCursor, err = s.sealActivityCursor(p, nodeID, kind, res.Next); err != nil {
			return err
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// activityItems projects the rows of a page for the caller p. The people and fields the page names are read once each.
func (s *Server) activityItems(ctx context.Context, p *auth.Principal, n *store.Node, rows []*store.ActivityRow) ([]activityJSON, error) {
	var ids, groups []string
	var needFields bool
	for _, r := range rows {
		if r.ActorKind == string(auth.KindUser) && !impersonatedRow(r) {
			ids = append(ids, r.ActorID)
		}
		switch r.Action {
		case "permission.set", "permission.expired":
			subj, _ := r.Details["subject_id"].(string)
			switch r.Details["subject_kind"] {
			case store.SubjectUser:
				ids = append(ids, subj)
			case store.SubjectGroup:
				groups = append(groups, subj)
			}
		case "node.label_set", "node.label_clear":
			needFields = true
		}
	}
	walks := maxActivityWalks
	if s.activityWalks > 0 {
		walks = s.activityWalks
	}
	people, err := s.peopleFor(ctx, p, n.AccountID, n.ID, ids, walks)
	if err != nil {
		return nil, err
	}
	groupNames := map[string]string{}
	groupUnchecked := map[string]bool{}
	for _, g := range groups {
		if _, done := groupNames[g]; done {
			continue
		}
		if len(groupNames) >= walks {
			groupUnchecked[g] = true // past the bound a group is not looked up: no name, not called gone
			continue
		}
		groupNames[g] = ""
		if grp, err := s.store.Group(ctx, n.AccountID, g); err == nil {
			groupNames[g] = grp.Name
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	fields := map[string]*store.LabelField{}
	if needFields {
		all, err := s.store.LabelFields(ctx, n.AccountID, false)
		if err != nil {
			return nil, err
		}
		for _, f := range all {
			fields[f.ID] = f
		}
	}
	out := make([]activityJSON, 0, len(rows))
	for _, r := range rows {
		eid, err := s.activityEntryID(n.AccountID, r.ID)
		if err != nil {
			return nil, err
		}
		it := activityJSON{ID: eid, Time: r.Time, Kind: activityKindOf(r.Action), Action: r.Action,
			Actor: activityActorOf(p, r, people), Details: activityDetails(r.Action, r.Details)}
		switch r.Action {
		case "permission.set", "permission.expired":
			subj, _ := r.Details["subject_id"].(string)
			switch r.Details["subject_kind"] {
			case store.SubjectUser:
				it.Details["subject_kind"] = store.SubjectUser
				if people.unchecked[subj] {
					// Not checked: no name, and not said to be gone.
				} else if people.reach[subj] {
					if name := personName(p, n.AccountID, people.names, subj); name != "" {
						it.Details["subject_name"] = name
					}
				} else {
					it.Details["subject_gone"] = true
				}
			case store.SubjectGroup:
				it.Details["subject_kind"] = store.SubjectGroup
				if groupUnchecked[subj] {
					// Not looked up: no name, and not said to be gone.
				} else if name := groupNames[subj]; name != "" {
					it.Details["subject_name"] = name
				} else {
					it.Details["subject_gone"] = true
				}
			}
		case "node.label_set", "node.label_clear":
			fid, _ := r.Details["field"].(string)
			if f := fields[fid]; f != nil {
				it.Details["field_name"] = f.Name
			} else {
				it.Details["field_gone"] = true
			}
		}
		out = append(out, it)
	}
	return out, nil
}

// impersonatedRow reports whether an installation admin made the entry while signed in as a member (decision 41).
func impersonatedRow(r *store.ActivityRow) bool {
	by, _ := r.Details["impersonated_by"].(string)
	return by != ""
}

// activityActorOf says who made an entry. An admin who acted as a member is shown as an admin, named by no one, to a
// caller who manages the account, which decision 41 gave these entries to. Any other caller sees an actor of kind other,
// with no name and no sign that an admin acted. It is not shown as the member, who did not act.
func activityActorOf(p *auth.Principal, r *store.ActivityRow, people *activityPeople) activityActor {
	if impersonatedRow(r) {
		if p.ManagesAccount(p.AccountID) {
			return activityActor{Kind: "admin"}
		}
		return activityActor{Kind: "other"}
	}
	switch r.ActorKind {
	case string(auth.KindUser):
		a := activityActor{Kind: "user", Mine: p.Kind == auth.KindUser && r.ActorID == p.UserID}
		if people.unchecked[r.ActorID] {
			return activityActor{Kind: "other"}
		}
		if !people.reach[r.ActorID] {
			a.Gone = true
			return a
		}
		a.Name = personName(p, p.AccountID, people.names, r.ActorID)
		return a
	case string(auth.KindAccountKey):
		return activityActor{Kind: "account_key"}
	case string(auth.KindSystem):
		return activityActor{Kind: "system"}
	case string(auth.KindShareLink):
		return activityActor{Kind: "visitor"}
	}
	return activityActor{Kind: "other"}
}
