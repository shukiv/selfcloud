// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"math/rand"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestReachRowsMatchPerNodeCheck is the property test that keeps the batched reach query and the per-node check in
// step. Over random trees, with grants, groups, expired grants, trashed nodes and their ancestors, organisation links
// (live and expired, reset, paused by a deterministic cycle of the team space's policy, or on a trashed node) with
// visits, and the callers that reach them (members, an owner, delegated tokens acting as a member: folder-list,
// all-reachable, node-scoped, read and write), reachRoles must give exactly the role nodeAccessWith gives on every node,
// and no role for a node it refuses. A disabled and a pending user are checked to be refused the same way. A second
// account's nodes, grants, links and tokens are probed from the first account and back, so every account filter runs.
// The seed is logged, and the test fails if a caller never reaches a node or never gets a refusal.
func TestReachRowsMatchPerNodeCheck(t *testing.T) {
	t.Parallel()
	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	t.Logf("seed %d", seed)
	fx := newLinkFixture(t)
	owner, ownerSess := ownerUser(t, fx.s, fx.key, fx.acc.ID, "owner@example.com")
	carl, carlSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "carl@example.com")
	dave, _ := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dave@example.com")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+dave.ID, fx.key,
		map[string]any{"status": "disabled"}), 200)
	erin := pendingUser(t, fx, "erin@example.com")
	annPersonal := fx.personalFolder(t, fx.annSess)
	bobPersonal := fx.personalFolder(t, fx.bobSess)
	other := newOtherAccount(t, fx)
	root := rootOfFolder(t, fx, fx.fld.ID)

	var all []string // every node made so far, so older folders stay in the comparison
	// Tokens are minted per account key at 60 an minute, so the rounds mint two or three tokens each.
	const iterations = 25
	counts := map[string][2]int{}
	for i := 0; i < iterations; i++ {
		// Each round builds its tree in a random folder: the team space, a new one, or one of the members' personal folders.
		folders := []string{fx.fld.ID, annPersonal, bobPersonal}
		if i%2 == 0 {
			folders = append(folders, newFolder(t, fx.s, fx.key, fx.acc.ID, fmt.Sprintf("R%d", i)).ID)
		}
		fld := folders[rng.Intn(len(folders))]
		var nodes []string
		dirs := []string{rootOfFolder(t, fx, fld)}
		for k := 0; k < 4+rng.Intn(6); k++ {
			parent := dirs[rng.Intn(len(dirs))]
			name := fmt.Sprintf("d%d_%d", i, k)
			if rng.Intn(3) == 0 {
				nodes = append(nodes, putUnder(t, fx, fld, parent, name+".txt"))
				continue
			}
			d := mkdirUnder(t, fx, fld, parent, name)
			dirs = append(dirs, d)
			nodes = append(nodes, d)
		}
		all = append(all, nodes...)
		all = append(all, dirs...)

		// Permissions, some already expired, on random nodes for the members, a group, and the disabled and pending users.
		grp := fx.group(t, fmt.Sprintf("G%d", i))
		if rng.Intn(2) == 0 {
			fx.inGroup(t, grp, fx.ann.ID, true)
		}
		if rng.Intn(2) == 0 {
			fx.inGroup(t, grp, fx.bob.ID, true)
		}
		if rng.Intn(2) == 0 {
			fx.inGroup(t, grp, carl.ID, true)
		}
		roles := []string{"viewer", "commenter", "contributor", "editor", "manager"}
		subjects := []string{fx.ann.ID, fx.bob.ID, carl.ID, dave.ID, erin.ID, grp}
		pool := append(append([]string{}, nodes...), dirs...)
		for k := 0; k < 3+rng.Intn(3); k++ {
			node := pool[rng.Intn(len(pool))]
			subj := subjects[rng.Intn(len(subjects))]
			role := roles[rng.Intn(len(roles))]
			if rng.Intn(4) == 0 {
				expireGrant(t, fx, node, subj, role)
				continue
			}
			// A members-only space refuses some grants; the refusal is part of the mix, so it is not an error here.
			call(t, fx.s, "PUT", "/api/v1/nodes/"+node+"/permissions/"+subj, fx.key, map[string]any{"role": role})
		}

		// The team space's link policy follows a fixed cycle, so all four combinations of allow_links and members_only
		// are exercised in every run: (on, off), (off, off), (on, on), (off, on).
		policy := map[string]any{"allow_links": i%4 == 0 || i%4 == 2, "members_only": i%4 >= 2}
		fx.settings(t, fx.fld.ID, policy)
		if fld != annPersonal && fld != bobPersonal {
			fx.settings(t, fld, policy)
		}

		// Organisation links, visited by the members, some expired (past or future), reset, or on trashed nodes.
		for k := 0; k < 2+rng.Intn(3); k++ {
			node := pool[rng.Intn(len(pool))]
			body := map[string]any{"audience": "organisation", "role": roles[rng.Intn(4)]}
			rec := call(t, fx.s, "POST", "/api/v1/nodes/"+node+"/share-links", fx.key, body)
			if rec.Code != 201 {
				continue
			}
			out := decode[shareLinkCreated](t, rec)
			// The members open the link first, so an expiry set afterwards ends a visit that was live.
			if rng.Intn(3) != 0 {
				fx.open(t, fx.annSess, secretOf(t, out.URL))
			}
			if rng.Intn(3) == 0 {
				fx.open(t, fx.bobSess, secretOf(t, out.URL))
			}
			switch rng.Intn(4) {
			case 0:
				fx.setExpiry(t, out.Link.ID, time.Now().Add(-time.Hour))
			case 1:
				fx.setExpiry(t, out.Link.ID, time.Now().Add(time.Hour))
			}
			if rng.Intn(4) == 0 {
				call(t, fx.s, "POST", "/api/v1/share-links/"+out.Link.ID+"/reset", fx.key, nil)
			}
		}

		// Callers. Tokens name the members, with a folder list or all-reachable, a node scope and a role.
		callers := map[string]*auth.Principal{
			"ann":   sessionPrincipal(t, fx.s, fx.annSess),
			"bob":   sessionPrincipal(t, fx.s, fx.bobSess),
			"carl":  sessionPrincipal(t, fx.s, carlSess),
			"owner": sessionPrincipal(t, fx.s, ownerSess),
		}
		scope := map[string]any{"folders": []string{fld}, "permissions": pick(rng, "read", "write")}
		if d := dirs[rng.Intn(len(dirs))]; rng.Intn(2) == 0 {
			scope["node"] = d
		}
		callers["ann-token"] = tokenPrincipal(t, fx, mintFor(t, fx.s, fx.key, fx.acc.ID,
			withUser(scope, fx.ann.ID)))
		callers["bob-token-all"] = tokenPrincipal(t, fx, mintFor(t, fx.s, fx.key, fx.acc.ID,
			map[string]any{"user": fx.bob.ID, "all": true, "permissions": pick(rng, "read", "write")}))
		if i%5 == 0 {
			callers["owner-token-all"] = tokenPrincipal(t, fx, mintFor(t, fx.s, fx.key, fx.acc.ID,
				map[string]any{"user": owner.ID, "all": true, "permissions": "read"}))
		}
		// The second account's user, by session and by an all-reachable token; and the account key's own token.
		other.callers(t, fx, callers, rng)

		// Trash a few nodes, below the root, so their subtrees go with them. Tokens are minted first, so a
		// token may name a directory that is trashed afterwards.
		for k := 0; k < rng.Intn(3); k++ {
			node := nodes[rng.Intn(len(nodes))]
			call(t, fx.s, "DELETE", "/api/v1/nodes/"+node, fx.key, nil)
		}

		// The round's own nodes, a sample of earlier ones, and the second account's nodes, so every account filter runs.
		probe := append([]string{}, nodes...)
		probe = append(probe, dirs...)
		for k := 0; k < 30 && len(all) > 0; k++ {
			probe = append(probe, all[rng.Intn(len(all))])
		}
		probe = append(probe, other.nodes...)
		for name, p := range callers {
			if name == "dan" || name == "dan-token-all" {
				continue
			}
			comparePrincipal(t, fx.s, name, p, probe, counts)
		}
		// The second account's callers probe the first account's nodes too.
		foreign := append(append([]string{}, probe...), other.callerProbe()...)
		for name, p := range callers {
			if name == "dan" || name == "dan-token-all" {
				comparePrincipal(t, fx.s, name, p, foreign, counts)
			}
		}
		// The disabled and the pending user are refused by both checks, and reach nothing.
		compareRefused(t, fx.s, "dave-disabled", &auth.Principal{Kind: auth.KindUser, ID: dave.ID,
			AccountID: fx.acc.ID, UserID: dave.ID, Role: auth.RoleMember}, root, probe, store.UserDisabled)
		compareRefused(t, fx.s, "erin-pending", &auth.Principal{Kind: auth.KindUser, ID: erin.ID,
			AccountID: fx.acc.ID, UserID: erin.ID, Role: auth.RoleMember}, root, probe, store.UserPending)
	}
	// Every caller of the run must have been compared, and must both reach and be refused some node; otherwise the run
	// proves nothing about it.
	for _, name := range []string{"ann", "bob", "carl", "owner", "ann-token", "bob-token-all", "dan", "dan-token-all"} {
		c, ok := counts[name]
		t.Logf("%s: %d nodes reached, %d refused", name, c[0], c[1])
		if !ok || c[0] == 0 || c[1] == 0 {
			t.Errorf("%s: a run that never both reaches and refuses proves nothing (compared %v, reached %d, refused %d)",
				name, ok, c[0], c[1])
		}
	}
}

// otherAccount is a second account on the same server, with a team space, a shared directory, a file, a member who
// has been granted and has visited a link, and a token. Its IDs are probed from the first account and back.
type otherAccount struct {
	acc   accountJSON
	key   string
	dir   string
	file  string
	dan   userResponse
	sess  testSession
	nodes []string
}

func newOtherAccount(t *testing.T, fx *linkFixture) *otherAccount {
	t.Helper()
	o := &otherAccount{}
	o.acc, o.key = newAccount(t, fx.s, fx.admin, "Beta")
	f := newFolder(t, fx.s, o.key, o.acc.ID, "Other")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/folders/"+f.ID+"/settings", o.key,
		map[string]any{"allow_links": true}), 200)
	o.dir = mkdirs(t, fx.s, o.key, f.ID, "/x").ID
	o.file = putNode(t, fx.s, o.key, f.ID, "/x/y.txt", "y").ID
	o.nodes = []string{o.dir, o.file}
	o.dan, o.sess = verifiedUser(t, fx.s, o.key, o.acc.ID, "dan@example.com")
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+o.dir+"/permissions/"+o.dan.ID, o.key,
		map[string]any{"role": "viewer"}), 200)
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+o.dir+"/share-links", o.key, map[string]any{"audience": "organisation", "role": "viewer"})
	wantStatus(t, rec, 201)
	wantStatus(t, callSession(t, fx.s, o.sess, "POST", "/api/v1/share/open", map[string]any{"secret": secretOf(t, decode[shareLinkCreated](t, rec).URL)}), 200)
	return o
}

// callers adds the second account's principals to callers: the user's session, and a token acting as the user, over
// the whole account.
func (o *otherAccount) callers(t *testing.T, fx *linkFixture, callers map[string]*auth.Principal, rng *rand.Rand) {
	t.Helper()
	callers["dan"] = sessionPrincipal(t, fx.s, o.sess)
	callers["dan-token-all"] = tokenPrincipal(t, fx, mintFor(t, fx.s, o.key, o.acc.ID,
		map[string]any{"user": o.dan.ID, "all": true, "permissions": pick(rng, "read", "write")}))
}

// callerProbe is the second account's IDs as the first account's callers see them: they must all be refused.
func (o *otherAccount) callerProbe() []string { return o.nodes }

// pendingUser makes a user that has not verified its address yet: it is pending, and every request is refused.
func pendingUser(t *testing.T, fx *linkFixture, email string) userResponse {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": email, "verification": "link"})
	wantStatus(t, rec, 201)
	return decode[userResponse](t, rec)
}

// compareRefused checks that a principal whose user is inactive is refused by both checks with the same code, and
// that the batch reaches nothing for it.
func compareRefused(t *testing.T, s *Server, name string, p *auth.Principal, root string, ids []string, status string) {
	t.Helper()
	ctx := context.Background()
	want := map[string]apierr.Code{store.UserDisabled: apierr.UserDisabled, store.UserPending: apierr.Unauthenticated}[status]
	// root is a node that is never trashed, so the per-node check reaches the user's status.
	_, _, err := nodeAccessWith(ctx, s.store, p, root)
	if got := apierr.From(err).Code; got != want {
		t.Fatalf("%s: per-node check answered %v, want %v", name, got, want)
	}
	if _, err := reachRoles(ctx, s.store, p, ids); apierr.From(err).Code != want {
		t.Fatalf("%s: batched check answered %v, want %v", name, err, want)
	}
}

// comparePrincipal checks, for each node, that the batched reach gives the role the per-node check gives, and that a
// node the per-node check refuses is absent from the batch. counts[name] tallies the nodes reached and refused.
func comparePrincipal(t *testing.T, s *Server, name string, p *auth.Principal, ids []string, counts map[string][2]int) {
	t.Helper()
	ctx := context.Background()
	batch, err := reachRoles(ctx, s.store, p, ids)
	if err != nil {
		t.Fatalf("%s: reachRoles: %v", name, err)
	}
	tally := counts[name]
	for _, id := range ids {
		want := ""
		_, role, err := nodeAccessWith(ctx, s.store, p, id)
		switch {
		case err == nil:
			want = role
		case apierr.From(err).Code == apierr.NotFound:
		default:
			t.Fatalf("%s: nodeAccessWith(%s): %v", name, id, err)
		}
		if want != "" {
			tally[0]++
		} else {
			tally[1]++
		}
		if got := batch[id]; got != want {
			t.Errorf("%s on %s: batched role %q, per-node role %q", name, id, got, want)
		}
	}
	counts[name] = tally
}

// ownerUser makes a verified owner of the account and returns it with its session.
func ownerUser(t *testing.T, s *Server, key, accID, email string) (userResponse, testSession) {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts/"+accID+"/users", key,
		map[string]any{"email": email, "role": "owner", "verification": "link"})
	wantStatus(t, rec, 201)
	created := decode[userResponse](t, rec)
	rec = call(t, s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": created.Verification.Token, "password": testPassword})
	wantStatus(t, rec, 200)
	return created, sessionFrom(t, s, rec)
}

// sessionPrincipal authenticates a session cookie the way a request would.
func sessionPrincipal(t *testing.T, s *Server, sess testSession) *auth.Principal {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.AddCookie(sess.cookie)
	p, _, err := s.authenticate(req)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// tokenPrincipal authenticates a delegated token the way a request would.
func tokenPrincipal(t *testing.T, fx *linkFixture, tok string) *auth.Principal {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/nodes/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	p, _, err := fx.s.authenticate(req)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func withUser(m map[string]any, user string) map[string]any {
	out := map[string]any{"user": user}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func pick(rng *rand.Rand, opts ...string) string { return opts[rng.Intn(len(opts))] }

// rootOfFolder is the root node of a folder, read from the store.
func rootOfFolder(t *testing.T, fx *linkFixture, folderID string) string {
	t.Helper()
	return fx.rootOf(t, folderID)
}

// putUnder writes a small file under dir, in folder fld, and returns its ID.
func putUnder(t *testing.T, fx *linkFixture, fld, dir, name string) string {
	t.Helper()
	rec := rawCall(t, fx.s, "POST", "/api/v1/folders/"+fld+"/files?"+q(childPath(t, fx, dir, name)), fx.key,
		"x", nil)
	wantStatus(t, rec, 201)
	return decode[nodeJSON](t, rec).ID
}

// mkdirUnder makes a directory named name under dir, in folder fld, and returns its ID.
func mkdirUnder(t *testing.T, fx *linkFixture, fld, dir, name string) string {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/folders/"+fld+"/directories", fx.key,
		map[string]any{"path": childPath(t, fx, dir, name), "parents": true})
	wantStatus(t, rec, 201)
	return decode[nodeJSON](t, rec).ID
}

// childPath is the path of name inside dir, read from the store. Each directory the test makes is one segment below
// its folder's root, so the path is the directory's own path with name appended.
func childPath(t *testing.T, fx *linkFixture, dir, name string) string {
	t.Helper()
	p := ""
	cur := dir
	for {
		n, err := fx.s.store.Node(context.Background(), fx.acc.ID, cur)
		if err != nil {
			t.Fatal(err)
		}
		if n.IsRoot() {
			break
		}
		p = "/" + n.Name + p
		cur = n.ParentID
	}
	return p + "/" + name
}

// expireGrant writes a permission that has already expired, which must grant nothing.
func expireGrant(t *testing.T, fx *linkFixture, node, subj, role string) {
	t.Helper()
	kind := store.SubjectUser
	if subj != fx.ann.ID && subj != fx.bob.ID {
		kind = store.SubjectGroup
	}
	past := time.Now().Add(-time.Hour)
	err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.SetPermission(context.Background(), &store.Permission{NodeID: node, AccountID: fx.acc.ID,
			SubjectKind: kind, SubjectID: subj, Role: role, CreatedAt: store.Now(), CreatedBy: "test", ExpiresAt: &past})
	})
	if err != nil {
		t.Fatal(err)
	}
}
