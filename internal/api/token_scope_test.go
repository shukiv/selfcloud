// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// userRevokePath is the route that ends one user's delegated tokens.
func userRevokePath(accID, usrID string) string {
	return "/api/v1/accounts/" + accID + "/users/" + usrID + "/tokens/revoke"
}

// TestRevokeOneUsersTokens: revoking one user ends that user's tokens at once and leaves the other
// tokens of the account working. A token minted afterwards works.
func TestRevokeOneUsersTokens(t *testing.T) {
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	bob, _ := verifiedUser(t, s, key, accID, "bob@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/accounts/"+accID+"/folders/"+m.f.ID+"/grants/"+bob.ID, key,
		map[string]any{"level": "read"}), 200)
	annTok := mintFor(t, s, key, accID, map[string]any{"user": m.ann.ID, "folders": []string{m.f.ID}})
	bobTok := mintFor(t, s, key, accID, map[string]any{"user": bob.ID, "folders": []string{m.f.ID}})
	keyTok := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}})
	kids := childrenPath(m.f.ID)
	wantStatus(t, getWith(t, s, annTok, kids), 200)

	rec := call(t, s, "POST", userRevokePath(accID, m.ann.ID), key, nil)
	wantStatus(t, rec, 200)
	if decode[map[string]time.Time](t, rec)["tokens_not_before"].IsZero() {
		t.Error("tokens_not_before is zero; want the cut-off")
	}
	wantError(t, getWith(t, s, annTok, kids), 401, apierr.Unauthenticated)
	wantStatus(t, getWith(t, s, bobTok, kids), 200)
	wantStatus(t, getWith(t, s, keyTok, kids), 200)

	// A token minted after the cut-off works. The sleep keeps its issue time past the cut-off.
	time.Sleep(5 * time.Millisecond)
	fresh := mintFor(t, s, key, accID, map[string]any{"user": m.ann.ID, "folders": []string{m.f.ID}})
	wantStatus(t, getWith(t, s, fresh, kids), 200)
}

// TestRevokeOneUsersTokensMovesForward: a second revoke keeps the earlier one's voids, and its own
// cut-off is not earlier than the first.
func TestRevokeOneUsersTokensMovesForward(t *testing.T) {
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	kids := childrenPath(m.f.ID)
	old := mintFor(t, s, key, accID, map[string]any{"user": m.ann.ID, "folders": []string{m.f.ID}})
	first := decode[map[string]time.Time](t, call(t, s, "POST", userRevokePath(accID, m.ann.ID), key, nil))["tokens_not_before"]
	time.Sleep(5 * time.Millisecond)
	second := decode[map[string]time.Time](t, call(t, s, "POST", userRevokePath(accID, m.ann.ID), key, nil))["tokens_not_before"]
	if second.Before(first) {
		t.Errorf("second cut-off %v is before the first %v", second, first)
	}
	wantError(t, getWith(t, s, old, kids), 401, apierr.Unauthenticated)
}

// TestRevokeOneUsersTokensCallers: the account key and an owner may revoke; a member, a token, a key of
// another account, an unknown user and a user of another account all get not_found, as the account-wide
// route answers them. The call is audited with the user named.
func TestRevokeOneUsersTokensCallers(t *testing.T) {
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	admin := adminKey(t, s)
	path := userRevokePath(accID, m.ann.ID)
	tok := mintFor(t, s, key, accID, map[string]any{"user": m.ann.ID, "folders": []string{m.f.ID}})

	wantError(t, callSession(t, s, m.sess, "POST", path, nil), 404, apierr.NotFound) // a member session
	wantError(t, call(t, s, "POST", path, tok, nil), 404, apierr.NotFound)           // a delegated token

	otherAcc, otherKey := newAccount(t, s, admin, "Other")
	wantError(t, call(t, s, "POST", path, otherKey, nil), 404, apierr.NotFound)
	otherUser, _ := verifiedUser(t, s, otherKey, otherAcc.ID, "out@example.com")
	wantError(t, call(t, s, "POST", userRevokePath(accID, otherUser.ID), key, nil), 404, apierr.NotFound)
	wantError(t, call(t, s, "POST", userRevokePath(accID, id.New("usr")), key, nil), 404, apierr.NotFound)

	// An owner session may revoke.
	owner, ownerSess := verifiedUser(t, s, key, accID, "owner@example.com")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+accID+"/users/"+owner.ID, key,
		map[string]any{"role": "owner"}), 200)
	wantStatus(t, callSession(t, s, ownerSess, "POST", path, nil), 200)

	entries, _, err := s.store.AuditLog(context.Background(), accID, "", true, store.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !auditHas(entries, "user.tokens_revoke", m.ann.ID) {
		t.Errorf("no user.tokens_revoke audit entry naming %s", m.ann.ID)
	}
}

// TestRevokeOneUsersTokenWithKeyMode: a token that acts as the account key has no user, so revoking a
// user leaves it working.
func TestRevokeOneUsersTokenKeyModeUnaffected(t *testing.T) {
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	kids := childrenPath(m.f.ID)
	keyTok := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}})
	wantStatus(t, call(t, s, "POST", userRevokePath(accID, m.ann.ID), key, nil), 200)
	wantStatus(t, getWith(t, s, keyTok, kids), 200)
}

// allReachable is the fixture for the all-reachable scope: the member fixture, a team space ann does not
// reach, a second user with no grants, and an owner. Each user has a personal folder with a file in it.
type allReachable struct {
	*memberFixture
	hidden    folderJSON // a team space ann holds no permission on
	mine      nodeJSON   // a file in ann's My files
	bob       userResponse
	bobFile   nodeJSON // a file in bob's My files
	owner     userResponse
	ownerSess testSession
}

func newAllReachable(t *testing.T) *allReachable {
	t.Helper()
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	ctx := context.Background()
	fx := &allReachable{memberFixture: m}
	fx.hidden = newFolder(t, s, key, accID, "Hidden")
	annPersonal, err := s.store.PersonalFolder(ctx, accID, m.ann.ID)
	if err != nil {
		t.Fatal(err)
	}
	fx.mine = putNode(t, s, key, annPersonal.ID, "/mine.txt", "mine")
	fx.bob, _ = verifiedUser(t, s, key, accID, "bob@example.com")
	bobPersonal, err := s.store.PersonalFolder(ctx, accID, fx.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	fx.bobFile = putNode(t, s, key, bobPersonal.ID, "/bob.txt", "bob")
	fx.owner, fx.ownerSess = verifiedUser(t, s, key, accID, "owner@example.com")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+accID+"/users/"+fx.owner.ID, key,
		map[string]any{"role": "owner"}), 200)
	return fx
}

// listedRoles maps the folders a token lists to their roles.
func listedRoles(t *testing.T, s *Server, cred string) map[string]string {
	t.Helper()
	rec := getWith(t, s, cred, "/api/v1/folders")
	wantStatus(t, rec, 200)
	out := map[string]string{}
	for _, f := range decode[listResponse[folderAccessJSON]](t, rec).Items {
		out[f.ID] = f.Role
	}
	return out
}

// TestAllReachableTokenReachesWhatTheUserReaches: a token for a user with no folders reads the team
// spaces the user holds, and the user's My files; it gets not_found for a space the user does not hold
// and for another user's My files.
func TestAllReachableTokenReachesWhatTheUserReaches(t *testing.T) {
	fx := newAllReachable(t)
	s, m := fx.s, fx.memberFixture
	tok := mintFor(t, s, m.key, m.acc.ID, map[string]any{"all": true, "user": m.ann.ID})

	roles := listedRoles(t, s, tok)
	for _, f := range []string{m.f.ID, m.g.ID} {
		if _, ok := roles[f]; !ok {
			t.Errorf("folder %s the user holds is not listed", f)
		}
	}
	if _, ok := roles[fx.hidden.ID]; ok {
		t.Error("a team space the user does not hold is listed")
	}
	wantStatus(t, getWith(t, s, tok, childrenPath(m.f.ID)), 200)
	wantError(t, getWith(t, s, tok, childrenPath(fx.hidden.ID)), 404, apierr.NotFound)
	wantStatus(t, getWith(t, s, tok, "/api/v1/nodes/"+fx.mine.ID), 200)
	wantError(t, getWith(t, s, tok, "/api/v1/nodes/"+fx.bobFile.ID), 404, apierr.NotFound)
}

// TestAllReachableWriteCappedAtEditor: an owner holds manager on every team space; a token carries at
// most editor, and the share and settings routes refuse tokens as before.
func TestAllReachableWriteCappedAtEditor(t *testing.T) {
	fx := newAllReachable(t)
	s, m := fx.s, fx.memberFixture
	tok := mintFor(t, s, m.key, m.acc.ID, map[string]any{"all": true, "user": fx.owner.ID, "permissions": "write"})

	if got := listedRoles(t, s, tok)[fx.hidden.ID]; got != store.RoleEditor {
		t.Errorf("owner's write token carries %q on a team space; want %q", got, store.RoleEditor)
	}
	rec := call(t, s, "PATCH", "/api/v1/folders/"+m.f.ID+"/settings", tok, map[string]any{"no_download_for_viewers": true})
	wantError(t, rec, 403, apierr.Forbidden)
	rec = call(t, s, "PUT", "/api/v1/nodes/"+m.fileF.ID+"/permissions/"+fx.bob.ID, tok,
		map[string]any{"role": "viewer"})
	wantError(t, rec, 403, apierr.Forbidden)
}

// TestAllReachableWriteNeedsWriteRole: a user who holds only read on a space cannot write there with a
// write token, though the same token writes where the user holds write.
func TestAllReachableWriteNeedsWriteRole(t *testing.T) {
	fx := newAllReachable(t)
	s, m := fx.s, fx.memberFixture
	tok := mintFor(t, s, m.key, m.acc.ID, map[string]any{"all": true, "user": m.ann.ID, "permissions": "write"})
	body := "x"
	wantStatus(t, rawCall(t, s, "POST", "/api/v1/folders/"+m.f.ID+"/files?"+q("/new.txt"), tok, body, nil), 201)
	wantError(t, rawCall(t, s, "POST", "/api/v1/folders/"+m.g.ID+"/files?"+q("/new.txt"), tok, body, nil), 403, apierr.Forbidden)
}

// TestAllReachableEndsWithUser: disabling the user ends the user's all-reachable tokens.
func TestAllReachableEndsWithUser(t *testing.T) {
	fx := newAllReachable(t)
	s, m := fx.s, fx.memberFixture
	tok := mintFor(t, s, m.key, m.acc.ID, map[string]any{"all": true, "user": m.ann.ID})
	wantStatus(t, getWith(t, s, tok, "/api/v1/nodes/"+m.fileF.ID), 200)
	disableMember(t, m, store.UserDisabled)
	wantError(t, getWith(t, s, tok, "/api/v1/nodes/"+m.fileF.ID), 401, apierr.Unauthenticated)
}

// TestAllReachableMintRules: folders may be omitted only with a user; path needs one folder, so it is
// refused without folders; node confines the token, and the node must be one the user reaches.
func TestAllReachableMintRules(t *testing.T) {
	fx := newAllReachable(t)
	s, m := fx.s, fx.memberFixture
	mint := "/api/v1/accounts/" + m.acc.ID + "/tokens"

	wantError(t, call(t, s, "POST", mint, m.key, map[string]any{}), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "POST", mint, m.key, map[string]any{"user": m.ann.ID, "path": "/"}), 400, apierr.InvalidRequest)

	dir := mkdirs(t, s, m.key, m.f.ID, "/docs")
	confined := mintFor(t, s, m.key, m.acc.ID, map[string]any{"all": true, "user": m.ann.ID, "node": dir.ID})
	wantStatus(t, getWith(t, s, confined, "/api/v1/nodes/"+dir.ID+"/children"), 200)
	wantError(t, getWith(t, s, confined, "/api/v1/nodes/"+m.fileF.ID), 404, apierr.NotFound)
	wantError(t, getWith(t, s, confined, "/api/v1/nodes/"+fx.mine.ID), 404, apierr.NotFound)

	// A node-confined token lists no folder, as a folder-list token confined to a directory lists none.
	if n := len(listedRoles(t, s, confined)); n != 0 {
		t.Errorf("a node-confined all-reachable token lists %d folders; want none", n)
	}

	hiddenDir := mkdirs(t, s, m.key, fx.hidden.ID, "/secret")
	wantError(t, call(t, s, "POST", mint, m.key, map[string]any{"all": true, "user": m.ann.ID, "node": hiddenDir.ID}), 404, apierr.NotFound)
}

// TestAllReachableJobRule: a job queued by an all-reachable token is read only by an all-reachable token
// of the same user with the same node scope. Folder-list tokens, other users and narrower tokens do not
// see it; the account key does.
func TestAllReachableJobRule(t *testing.T) {
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	allTok := mintFor(t, s, key, accID, map[string]any{"all": true, "user": m.ann.ID, "permissions": "write"})
	job := queueCopy(t, m, allTok)
	jobPath := "/api/v1/jobs/" + job.ID

	wantStatus(t, getWith(t, s, mintFor(t, s, key, accID, map[string]any{"all": true, "user": m.ann.ID, "permissions": "write"}), jobPath), 200)
	wantStatus(t, call(t, s, "GET", jobPath, key, nil), 200)
	folderTok := mintFor(t, s, key, accID, map[string]any{"user": m.ann.ID, "folders": []string{m.f.ID, m.g.ID}, "permissions": "write"})
	wantError(t, getWith(t, s, folderTok, jobPath), 404, apierr.NotFound)
	confined := mintFor(t, s, key, accID, map[string]any{"all": true, "user": m.ann.ID, "node": mkdirs(t, s, key, m.f.ID, "/d").ID})
	wantError(t, getWith(t, s, confined, jobPath), 404, apierr.NotFound)
	bobTok := mintFor(t, s, key, accID, map[string]any{"user": newMemberUser(t, s, key, accID, "bob@example.com"), "folders": []string{m.f.ID}})
	wantError(t, getWith(t, s, bobTok, jobPath), 404, apierr.NotFound)

	// A job queued by a folder-list token is not read by an all-reachable one.
	narrow := queueCopy(t, m, mintFor(t, s, key, accID, map[string]any{"user": m.ann.ID, "folders": []string{m.f.ID}, "permissions": "write"}))
	wantError(t, getWith(t, s, allTok, "/api/v1/jobs/"+narrow.ID), 404, apierr.NotFound)
}

// TestFolderListTokenStillVerifies: a token minted before the all-reachable scope has no "all" field and a
// folder list; it verifies as it did.
func TestFolderListTokenStillVerifies(t *testing.T) {
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	tok := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}})
	if pl := payloadOf(t, tok); len(pl.Folders) != 1 {
		t.Fatalf("folder token names %d folders; want 1", len(pl.Folders))
	}
	wantStatus(t, getWith(t, s, tok, childrenPath(m.f.ID)), 200)
}

// TestAllReachableRefusesForgedShape: the server signs only shapes it would mint. An all-reachable payload
// without a user, or with folders as well, is refused even when correctly signed.
func TestAllReachableRefusesForgedShape(t *testing.T) {
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	base := payloadOf(t, mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}}))
	probe := "/api/v1/nodes/" + m.fileF.ID

	noUser := base
	noUser.All, noUser.Folders = true, nil
	forged, err := s.tokens.sign(noUser)
	if err != nil {
		t.Fatal(err)
	}
	wantError(t, getWith(t, s, forged, probe), 401, apierr.Unauthenticated)

	withFolders := base
	withFolders.All, withFolders.UserID = true, m.ann.ID
	forged, err = s.tokens.sign(withFolders)
	if err != nil {
		t.Fatal(err)
	}
	wantError(t, getWith(t, s, forged, probe), 401, apierr.Unauthenticated)
}

// TestAllReachableIsOptIn: all-reachable is asked for with "all": true. A user with no folders and no
// "all" is a mistake, so it is refused, not widened. "all" needs a user and no folders.
func TestAllReachableIsOptIn(t *testing.T) {
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	mint := "/api/v1/accounts/" + accID + "/tokens"

	// A user with no folders and no "all": the folder list is required, as before.
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"user": m.ann.ID}), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"user": m.ann.ID, "folders": []string{}}), 400, apierr.InvalidRequest)
	// "all" without a user, and "all" with folders, are refused.
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"all": true}), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"all": true, "user": m.ann.ID, "folders": []string{m.f.ID}}), 400, apierr.InvalidRequest)
	// "all" with a user reaches what the user reaches.
	wantStatus(t, call(t, s, "POST", mint, key, map[string]any{"all": true, "user": m.ann.ID}), 201)
}

// TestAllReachableCopyQueuedBeforeRevokeFails: a copy queued by an all-reachable token fails when it runs
// after the user's tokens were revoked. A revoke of another user leaves the copy working.
func TestAllReachableCopyQueuedBeforeRevokeFails(t *testing.T) {
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	ctx := context.Background()
	bob, _ := verifiedUser(t, s, key, accID, "bob@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/accounts/"+accID+"/folders/"+m.f.ID+"/grants/"+bob.ID, key,
		map[string]any{"level": "write"}), 200)

	// Ann's job is queued, then ann is revoked before the job runs.
	annJob := queueCopy(t, m, mintFor(t, s, key, accID, map[string]any{"all": true, "user": m.ann.ID, "permissions": "write"}))
	wantStatus(t, call(t, s, "POST", userRevokePath(accID, m.ann.ID), key, nil), 200)
	if err := s.runCopyJob(ctx, annJob); apierr.From(err).Code != apierr.Forbidden {
		t.Fatalf("copy queued before ann's revoke = %v; want forbidden", err)
	}
	if n := childCount(t, m); n != 1 {
		t.Errorf("children after the refused copy = %d; want the original 1", n)
	}

	// Control: a revoke of bob leaves ann's copy working.
	ctl := queueCopy(t, m, mintFor(t, s, key, accID, map[string]any{"all": true, "user": m.ann.ID, "permissions": "write"}))
	wantStatus(t, call(t, s, "POST", userRevokePath(accID, bob.ID), key, nil), 200)
	if err := s.runCopyJob(ctx, ctl); err != nil {
		t.Fatalf("copy queued by ann after bob's revoke = %v; want it to run", err)
	}
	if n := childCount(t, m); n != 2 {
		t.Errorf("children after the control copy = %d; want 2", n)
	}
}
