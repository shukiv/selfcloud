// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// extPermJSON is an external grant as the API shows it.
type extPermJSON struct {
	Email         string     `json:"email"`
	Role          string     `json:"role"`
	ExpiresAt     *time.Time `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	CreatedBy     string     `json:"created_by"`
	CreatedByName string     `json:"created_by_name"`
}

// extPolicyJSON is the external-sharing field of the account's sharing policy.
type extPolicyJSON struct {
	ExternalSharing bool `json:"external_sharing"`
}

// extSharedItem is a node another account shared with the caller. It carries the fields a link's
// visitor sees, and the grant's own.
type extSharedItem struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Kind         string     `json:"kind"`
	Role         string     `json:"role"`
	AccountName  string     `json:"account_name"`
	SharedByName string     `json:"shared_by_name"`
	ExpiresAt    *time.Time `json:"expires_at"`
	CreatedBy    string     `json:"created_by"`
	OwnerID      string     `json:"owner_id"`
	FolderID     string     `json:"folder_id"`
}

// extSharedList is the answer of GET /shared/external.
type extSharedList struct {
	Items       []extSharedItem `json:"items"`
	EmailProved bool            `json:"email_proved"`
}

// extPath is the external-permissions path of a node.
func extPath(nodeID string) string { return "/api/v1/nodes/" + nodeID + "/external-permissions" }

// enableExternal turns the account's external-sharing switch on.
func enableExternal(t *testing.T, s *Server, key, accID string) {
	t.Helper()
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+accID+"/sharing-policy", key,
		map[string]any{"external_sharing": true}), 200)
}

// extGrant calls POST on a node's external permissions.
func extGrantCall(t *testing.T, s *Server, cred, nodeID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, s, "POST", extPath(nodeID), cred, body)
}

// TestExternalGrantFlow: the switch gates new grants; a grant is made, listed, updated in place and
// removed. A member who reaches the node reads the list; one who does not, gets not_found.
func TestExternalGrantFlow(t *testing.T) {
	t.Parallel()
	fx := newFixtureWithMailer(t, &mail.Recorder{})
	s, key := fx.s, fx.key
	e := wantError(t, extGrantCall(t, s, key, fx.b.ID,
		map[string]any{"email": "guest@other.example", "role": "viewer"}), 403, apierr.Forbidden)
	if e.Details["reason"] != "external_sharing_off" {
		t.Fatalf("reason with the switch off = %v", e.Details["reason"])
	}
	enableExternal(t, s, key, fx.acc.ID)
	policy := decode[extPolicyJSON](t, call(t, s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/sharing-policy", key, nil))
	if !policy.ExternalSharing {
		t.Fatal("the policy does not show the switch on")
	}
	r := extGrantCall(t, s, key, fx.b.ID, map[string]any{"email": "guest@other.example", "role": "viewer"})
	wantStatus(t, r, 200)
	g := decode[extPermJSON](t, r)
	if g.Email != "guest@other.example" || g.Role != "viewer" || g.CreatedBy == "" || g.ExpiresAt != nil || g.CreatedByName != "" {
		t.Fatalf("grant = %+v", g)
	}
	list := decode[listResponse[extPermJSON]](t, call(t, s, "GET", extPath(fx.b.ID), key, nil))
	if len(list.Items) != 1 || list.Items[0].Email != "guest@other.example" {
		t.Fatalf("list = %+v", list.Items)
	}
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	annList := decode[listResponse[extPermJSON]](t, callSession(t, s, fx.annSess, "GET", extPath(fx.b.ID), nil))
	if len(annList.Items) != 1 || annList.Items[0].CreatedByName != "" {
		t.Fatalf("a viewer's list = %+v", annList.Items)
	}
	wantError(t, callSession(t, s, fx.bobSess, "GET", extPath(fx.b.ID), nil), 404, apierr.NotFound)
	// A grant made by a named user carries that name; the key's carries none.
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "manager")
	wantStatus(t, callSession(t, s, fx.annSess, "PATCH", "/api/v1/me", map[string]any{"display_name": "Ann External"}), 200)
	r = callSession(t, s, fx.annSess, "POST", extPath(fx.b.ID), map[string]any{"email": "named@other.example", "role": "viewer"})
	wantStatus(t, r, 200)
	named := decode[extPermJSON](t, r)
	if named.CreatedBy != fx.ann.ID || named.CreatedByName != "Ann External" {
		t.Fatalf("named grant = %+v", named)
	}
	wantStatus(t, call(t, s, "DELETE", extPath(fx.b.ID)+"/named@other.example", key, nil), 204)
	// The same address again updates the grant in place.
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	r = extGrantCall(t, s, key, fx.b.ID,
		map[string]any{"email": "guest@other.example", "role": "viewer", "expires_at": exp})
	wantStatus(t, r, 200)
	g2 := decode[extPermJSON](t, r)
	if !g2.CreatedAt.Equal(g.CreatedAt) || g2.ExpiresAt == nil || !g2.ExpiresAt.Equal(exp) {
		t.Fatalf("updated grant = %+v, first was %+v", g2, g)
	}
	list = decode[listResponse[extPermJSON]](t, call(t, s, "GET", extPath(fx.b.ID), key, nil))
	if len(list.Items) != 1 {
		t.Fatalf("list after the update = %+v", list.Items)
	}
	wantStatus(t, call(t, s, "DELETE", extPath(fx.b.ID)+"/guest@other.example", key, nil), 204)
	wantError(t, call(t, s, "DELETE", extPath(fx.b.ID)+"/guest@other.example", key, nil), 404, apierr.NotFound)
	list = decode[listResponse[extPermJSON]](t, call(t, s, "GET", extPath(fx.b.ID), key, nil))
	if len(list.Items) != 0 {
		t.Fatalf("list after the removal = %+v", list.Items)
	}
}

// TestExternalGrantValidation: the body is checked as a user's email is, the role is viewer only,
// the expiry lies in the future, and an address of the account itself is refused. An address that
// names a user of another account is granted like any other: the answer does not tell whether anyone
// holds it.
func TestExternalGrantValidation(t *testing.T) {
	t.Parallel()
	fx := newFixtureWithMailer(t, &mail.Recorder{})
	s, key := fx.s, fx.key
	enableExternal(t, s, key, fx.acc.ID)
	long := strings.Repeat("a", 250) + "@example.com"
	bad := []struct {
		name   string
		body   map[string]any
		reason string
	}{
		{"empty email", map[string]any{"email": "", "role": "viewer"}, ""},
		{"not an address", map[string]any{"email": "not an address", "role": "viewer"}, ""},
		{"display name", map[string]any{"email": "Ann <ann@other.example>", "role": "viewer"}, ""},
		{"too long", map[string]any{"email": long, "role": "viewer"}, ""},
		{"role", map[string]any{"email": "guest@other.example", "role": "editor"}, ""},
		{"past expiry", map[string]any{"email": "guest@other.example", "role": "viewer",
			"expires_at": time.Now().Add(-time.Hour)}, ""},
		{"same account", map[string]any{"email": "ann@example.com", "role": "viewer"}, "same_account"},
		{"same account, other case", map[string]any{"email": "ANN@example.com", "role": "viewer"}, "same_account"},
	}
	for _, b := range bad {
		e := wantError(t, extGrantCall(t, s, key, fx.b.ID, b.body), 400, apierr.InvalidRequest)
		if b.reason != "" && e.Details["reason"] != b.reason {
			t.Errorf("%s: reason = %v, want %s", b.name, e.Details["reason"], b.reason)
		}
	}
	other, otherKey := newAccount(t, s, fx.admin, "Other")
	verifiedUser(t, s, otherKey, other.ID, "taken@other.example")
	r1 := extGrantCall(t, s, key, fx.b.ID, map[string]any{"email": "taken@other.example", "role": "viewer"})
	wantStatus(t, r1, 200)
	r2 := extGrantCall(t, s, key, fx.b.ID, map[string]any{"email": "nobody@other.example", "role": "viewer"})
	wantStatus(t, r2, 200)
	g1, g2 := decode[extPermJSON](t, r1), decode[extPermJSON](t, r2)
	if g1.Role != g2.Role || g1.Role != "viewer" || (g1.ExpiresAt == nil) != (g2.ExpiresAt == nil) {
		t.Fatalf("answers differ by whether the address is held: %+v vs %+v", g1, g2)
	}
}

// TestExternalGrantCallerRules: only a sharer of the node makes a grant; a delegated token makes
// none, whatever its user's role.
func TestExternalGrantCallerRules(t *testing.T) {
	t.Parallel()
	fx := newFixtureWithMailer(t, &mail.Recorder{})
	s, key := fx.s, fx.key
	enableExternal(t, s, key, fx.acc.ID)
	body := map[string]any{"email": "guest@other.example", "role": "viewer"}
	wantError(t, callSession(t, s, fx.bobSess, "POST", extPath(fx.b.ID), body), 404, apierr.NotFound)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	wantError(t, callSession(t, s, fx.annSess, "POST", extPath(fx.b.ID), body), 403, apierr.Forbidden)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	wantError(t, callSession(t, s, fx.annSess, "POST", extPath(fx.b.ID), body), 403, apierr.Forbidden)
	tok := mintFor(t, s, key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "user": fx.ann.ID})
	wantError(t, extGrantCall(t, s, tok, fx.b.ID, body), 403, apierr.Forbidden)
	// A manager who is not an owner may grant: ann becomes a manager.
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "manager")
	wantStatus(t, callSession(t, s, fx.annSess, "POST", extPath(fx.b.ID), body), 200)
}

// TestExternalGrantCap: a node holds at most 100 external grants; an address already granted is
// updated even at the cap.
func TestExternalGrantCap(t *testing.T) {
	t.Parallel()
	fx := newFixtureWithMailer(t, &mail.Recorder{})
	s, key := fx.s, fx.key
	enableExternal(t, s, key, fx.acc.ID)
	// The first hundred are written directly: the API's own per-caller limit would refuse the calls.
	err := s.store.Write(context.Background(), func(tx *store.Tx) error {
		for i := 0; i < 100; i++ {
			email := fmt.Sprintf("guest%d@other.example", i)
			if err := tx.SetExternalPermission(context.Background(), &store.ExternalPermission{
				NodeID: fx.b.ID, AccountID: fx.acc.ID, Email: email, EmailKey: email,
				Role: store.RoleViewer, CreatedBy: fx.ann.ID, CreatedAt: store.Now()}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e := wantError(t, extGrantCall(t, s, key, fx.b.ID,
		map[string]any{"email": "guest100@other.example", "role": "viewer"}), 409, apierr.Conflict)
	if e.Details["reason"] != "too_many_external" {
		t.Fatalf("reason at the cap = %v", e.Details["reason"])
	}
	wantStatus(t, extGrantCall(t, s, key, fx.b.ID,
		map[string]any{"email": "guest0@other.example", "role": "viewer",
			"expires_at": time.Now().Add(time.Hour)}), 200)
}

// TestExternalGrantMembersOnly: a members-only space admits an external grant on its root only, as
// it admits an internal permission there only.
func TestExternalGrantMembersOnly(t *testing.T) {
	t.Parallel()
	fx := newFixtureWithMailer(t, &mail.Recorder{})
	s, key := fx.s, fx.key
	enableExternal(t, s, key, fx.acc.ID)
	wantStatus(t, call(t, s, "PATCH", "/api/v1/folders/"+fx.fld.ID+"/settings", key,
		map[string]any{"members_only": true}), 200)
	body := map[string]any{"email": "guest@other.example", "role": "viewer"}
	wantError(t, extGrantCall(t, s, key, fx.b.ID, body), 409, apierr.Conflict)
	wantStatus(t, extGrantCall(t, s, key, fx.rootOf(t, fx.fld.ID), body), 200)
}

// TestExternalGrantSwitchOffKeepsRows: turning the switch off refuses new grants but keeps the rows,
// and they are still listed and removed.
func TestExternalGrantSwitchOffKeepsRows(t *testing.T) {
	t.Parallel()
	fx := newFixtureWithMailer(t, &mail.Recorder{})
	s, key := fx.s, fx.key
	enableExternal(t, s, key, fx.acc.ID)
	wantStatus(t, extGrantCall(t, s, key, fx.b.ID,
		map[string]any{"email": "guest@other.example", "role": "viewer"}), 200)
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/sharing-policy", key,
		map[string]any{"external_sharing": false}), 200)
	list := decode[listResponse[extPermJSON]](t, call(t, s, "GET", extPath(fx.b.ID), key, nil))
	if len(list.Items) != 1 {
		t.Fatalf("list with the switch off = %+v", list.Items)
	}
	e := wantError(t, extGrantCall(t, s, key, fx.b.ID,
		map[string]any{"email": "second@other.example", "role": "viewer"}), 403, apierr.Forbidden)
	if e.Details["reason"] != "external_sharing_off" {
		t.Fatalf("reason with the switch off = %v", e.Details["reason"])
	}
	wantStatus(t, call(t, s, "DELETE", extPath(fx.b.ID)+"/guest@other.example", key, nil), 204)
}

// TestSharedExternal: a proved user of an account whose switch is on sees the live grants other
// accounts made to its address, with the fields a link's visitor sees. Every gate — either switch,
// the proof, the expiry, the trash, a delegated token — hides the list; an admin is refused.
func TestSharedExternal(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	accA, keyA := newAccount(t, s, admin, "Sharer")
	fldA := newFolder(t, s, keyA, accA.ID, "Team")
	fileA := putNode(t, s, keyA, fldA.ID, "/file.txt", "hello")
	accB, keyB := newAccount(t, s, admin, "Recipient")
	fldB := newFolder(t, s, keyB, accB.ID, "Team")
	guest, guestSess := verifiedUser(t, s, keyB, accB.ID, "guest@example.com")
	token := proveByMail(t, s, guestSess, rec)
	wantStatus(t, redeemProof(t, s, token), 204)
	enableExternal(t, s, keyA, accA.ID)
	enableExternal(t, s, keyB, accB.ID)
	shared := func(sess testSession) extSharedList {
		t.Helper()
		return decode[extSharedList](t, callSession(t, s, sess, "GET", "/api/v1/shared/external", nil))
	}
	if l := shared(guestSess); len(l.Items) != 0 || !l.EmailProved {
		t.Fatalf("list before any grant = %+v", l)
	}
	wantStatus(t, extGrantCall(t, s, keyA, fileA.ID,
		map[string]any{"email": "guest@example.com", "role": "viewer"}), 200)
	l := shared(guestSess)
	if len(l.Items) != 1 || !l.EmailProved {
		t.Fatalf("list = %+v", l)
	}
	it := l.Items[0]
	if it.ID != fileA.ID || it.Name != "file.txt" || it.Kind != "file" || it.Role != "viewer" ||
		it.AccountName != "Sharer" || it.SharedByName != "" || it.ExpiresAt != nil {
		t.Fatalf("item = %+v", it)
	}
	if it.CreatedBy != "" || it.OwnerID != "" || it.FolderID != "" {
		t.Fatalf("item carries fields a link's visitor does not see: %+v", it)
	}
	// A grant made by a named sharer shows that name; an update by another caller keeps the origin.
	wantStatus(t, call(t, s, "DELETE", extPath(fileA.ID)+"/guest@example.com", keyA, nil), 204)
	sharer, sharerSess := verifiedUser(t, s, keyA, accA.ID, "sharer@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/nodes/"+fileA.ID+"/permissions/"+sharer.ID, keyA,
		map[string]any{"role": "manager"}), 200)
	wantStatus(t, callSession(t, s, sharerSess, "PATCH", "/api/v1/me",
		map[string]any{"display_name": "The Sharer"}), 200)
	wantStatus(t, callSession(t, s, sharerSess, "POST", extPath(fileA.ID),
		map[string]any{"email": "guest@example.com", "role": "viewer"}), 200)
	if l := shared(guestSess); len(l.Items) != 1 || l.Items[0].SharedByName != "The Sharer" {
		t.Fatalf("list with a named sharer = %+v", l)
	}
	wantStatus(t, extGrantCall(t, s, keyA, fileA.ID,
		map[string]any{"email": "guest@example.com", "role": "viewer"}), 200)
	if l := shared(guestSess); len(l.Items) != 1 || l.Items[0].SharedByName != "The Sharer" {
		t.Fatalf("list after another caller's update = %+v", l)
	}
	// The sharer's switch off hides the grant.
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+accA.ID+"/sharing-policy", keyA,
		map[string]any{"external_sharing": false}), 200)
	if l := shared(guestSess); len(l.Items) != 0 {
		t.Fatalf("list with the sharer's switch off = %+v", l)
	}
	enableExternal(t, s, keyA, accA.ID)
	// The recipient's own switch off hides it too.
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+accB.ID+"/sharing-policy", keyB,
		map[string]any{"external_sharing": false}), 200)
	if l := shared(guestSess); len(l.Items) != 0 {
		t.Fatalf("list with the recipient's switch off = %+v", l)
	}
	enableExternal(t, s, keyB, accB.ID)
	// An unproved user of the same account sees nothing, even with a grant of its own.
	_, guest2Sess := verifiedUser(t, s, keyB, accB.ID, "guest2@example.com")
	wantStatus(t, extGrantCall(t, s, keyA, fileA.ID,
		map[string]any{"email": "guest2@example.com", "role": "viewer"}), 200)
	if l := shared(guest2Sess); len(l.Items) != 0 || l.EmailProved {
		t.Fatalf("unproved user's list = %+v", l)
	}
	// A delegated token acting as the proved user sees nothing; one with no user is refused.
	tok := mintFor(t, s, keyB, accB.ID, map[string]any{"folders": []string{fldB.ID}, "user": guest.ID})
	if l := decode[extSharedList](t, call(t, s, "GET", "/api/v1/shared/external", tok, nil)); len(l.Items) != 0 {
		t.Fatalf("delegated token's list = %+v", l)
	}
	keyTok := mintFor(t, s, keyB, accB.ID, map[string]any{"folders": []string{fldB.ID}})
	wantError(t, call(t, s, "GET", "/api/v1/shared/external", keyTok, nil), 403, apierr.Forbidden)
	// An expired grant is left out.
	past := time.Now().Add(-time.Hour)
	err := s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.SetExternalPermission(context.Background(), &store.ExternalPermission{
			NodeID: fileA.ID, AccountID: accA.ID, Email: "guest@example.com", EmailKey: "guest@example.com",
			Role: store.RoleViewer, CreatedBy: sharer.ID, CreatedAt: store.Now(), ExpiresAt: &past})
	})
	if err != nil {
		t.Fatal(err)
	}
	if l := shared(guestSess); len(l.Items) != 0 {
		t.Fatalf("list with an expired grant = %+v", l)
	}
	// A live grant on a trashed node is left out too.
	err = s.store.Write(context.Background(), func(tx *store.Tx) error {
		if err := tx.SetExternalPermission(context.Background(), &store.ExternalPermission{
			NodeID: fileA.ID, AccountID: accA.ID, Email: "guest@example.com", EmailKey: "guest@example.com",
			Role: store.RoleViewer, CreatedBy: sharer.ID, CreatedAt: store.Now()}); err != nil {
			return err
		}
		return tx.TrashNode(context.Background(), accA.ID, fileA.ID, sharer.ID, store.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	if l := shared(guestSess); len(l.Items) != 0 {
		t.Fatalf("list with a trashed node = %+v", l)
	}
	// An installation admin is refused.
	_, adminSess := newAdminUser(t, s, admin, "admin@example.com")
	wantError(t, callSession(t, s, adminSess, "GET", "/api/v1/shared/external", nil), 403, apierr.Forbidden)
}

// TestExternalGrantRoleOptional: the role may be left out or empty; both mean viewer.
func TestExternalGrantRoleOptional(t *testing.T) {
	t.Parallel()
	fx := newFixtureWithMailer(t, &mail.Recorder{})
	s, key := fx.s, fx.key
	enableExternal(t, s, key, fx.acc.ID)
	for _, body := range []map[string]any{
		{"email": "guest@other.example"},
		{"email": "guest@other.example", "role": ""},
	} {
		r := extGrantCall(t, s, key, fx.b.ID, body)
		wantStatus(t, r, 200)
		if g := decode[extPermJSON](t, r); g.Role != "viewer" {
			t.Fatalf("body %v answered role %q, want viewer", body, g.Role)
		}
	}
}

// TestExternalGrantNoReveal: a grant for an address no user holds anywhere answers exactly like one
// for an address a user of another account holds, apart from the address itself.
func TestExternalGrantNoReveal(t *testing.T) {
	t.Parallel()
	fx := newFixtureWithMailer(t, &mail.Recorder{})
	s, key := fx.s, fx.key
	enableExternal(t, s, key, fx.acc.ID)
	other, otherKey := newAccount(t, s, fx.admin, "Other")
	verifiedUser(t, s, otherKey, other.ID, "taken@other.example")
	r1 := extGrantCall(t, s, key, fx.b.ID, map[string]any{"email": "nobody@other.example", "role": "viewer"})
	r2 := extGrantCall(t, s, key, fx.b.ID, map[string]any{"email": "taken@other.example", "role": "viewer"})
	wantStatus(t, r1, 200)
	wantStatus(t, r2, 200)
	j1 := decode[map[string]any](t, r1)
	j2 := decode[map[string]any](t, r2)
	for _, j := range []map[string]any{j1, j2} {
		j["email"] = ""
		j["created_at"] = ""
	}
	if !reflect.DeepEqual(j1, j2) {
		t.Fatalf("answers reveal whether the address is held: %+v vs %+v", j1, j2)
	}
}

// TestExternalGrantOpensReadPath: a grant now opens the read path (decision 173, slice xs1b). A proved user
// of another account, both switches on, stats the granted node at viewer. This guard was
// TestExternalGrantOpensNoReadPath in xs1a (decision 172), which asserted the node was still not_found while
// no read path existed; xs1b lands the read path, so the answer flips to 200.
func TestExternalGrantOpensReadPath(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	accA, keyA := newAccount(t, s, admin, "Sharer")
	fldA := newFolder(t, s, keyA, accA.ID, "Team")
	fileA := putNode(t, s, keyA, fldA.ID, "/file.txt", "hello")
	accB, keyB := newAccount(t, s, admin, "Recipient")
	_, guestSess := verifiedUser(t, s, keyB, accB.ID, "guest@example.com")
	token := proveByMail(t, s, guestSess, rec)
	wantStatus(t, redeemProof(t, s, token), 204)
	enableExternal(t, s, keyA, accA.ID)
	enableExternal(t, s, keyB, accB.ID)
	wantStatus(t, extGrantCall(t, s, keyA, fileA.ID, map[string]any{"email": "guest@example.com", "role": "viewer"}), 200)
	stat := callSession(t, s, guestSess, "GET", "/api/v1/nodes/"+fileA.ID, nil)
	wantStatus(t, stat, 200)
	if j := decode[nodeJSON](t, stat); j.Role != "viewer" {
		t.Fatalf("the granted node's role = %q, want viewer", j.Role)
	}
}
