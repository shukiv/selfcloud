// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// embedBody is the body of the embed routes: the account's allowed host origins.
type embedBody struct {
	Origins []string `json:"origins"`
}

// TestEmbedOriginsRoutes: the account key and the owner read and replace the account's embed origins;
// a member and another account's key are refused; a bad origin writes nothing; the change is audited;
// and the origins go with the account (decision 185).
func TestEmbedOriginsRoutes(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	other, otherKey := newAccount(t, s, admin, "Other")
	makeOwner(t, s, key, acc.ID, "owner@example.com")
	ownerSess := sessionFrom(t, s, login(t, s, "owner@example.com", testPassword, ""))
	_, memSess := verifiedUser(t, s, key, acc.ID, "member@example.com")
	path := "/api/v1/accounts/" + acc.ID + "/embed"

	// A new account has none.
	rec := call(t, s, "GET", path, key, nil)
	wantStatus(t, rec, 200)
	if got := decode[embedBody](t, rec); len(got.Origins) != 0 {
		t.Fatalf("new account origins = %v", got.Origins)
	}

	// The key replaces the list; the answer and the next read are sorted.
	rec = call(t, s, "PUT", path, key, embedBody{Origins: []string{"https://b.example", "https://a.example"}})
	wantStatus(t, rec, 200)
	if got := decode[embedBody](t, rec); !reflect.DeepEqual(got.Origins, []string{"https://a.example", "https://b.example"}) {
		t.Fatalf("put answer = %v", got.Origins)
	}
	rec = call(t, s, "GET", path, key, nil)
	if got := decode[embedBody](t, rec); !reflect.DeepEqual(got.Origins, []string{"https://a.example", "https://b.example"}) {
		t.Fatalf("get = %v", got.Origins)
	}

	// The owner's session reads and replaces too.
	wantStatus(t, callSession(t, s, ownerSess, "GET", path, nil), 200)
	rec = callSession(t, s, ownerSess, "PUT", path, embedBody{Origins: []string{"https://c.example"}})
	wantStatus(t, rec, 200)
	if got := decode[embedBody](t, rec); !reflect.DeepEqual(got.Origins, []string{"https://c.example"}) {
		t.Fatalf("owner put = %v", got.Origins)
	}

	// A member cannot manage the account, and another account's key cannot reach it.
	wantError(t, callSession(t, s, memSess, "PUT", path, embedBody{Origins: []string{"https://x.example"}}), 404, apierr.NotFound)
	wantError(t, call(t, s, "GET", path, otherKey, nil), 404, apierr.NotFound)
	wantError(t, call(t, s, "PUT", "/api/v1/accounts/"+other.ID+"/embed", key, embedBody{Origins: []string{"https://x.example"}}), 404, apierr.NotFound)

	// A bad origin is named and writes nothing.
	rec = call(t, s, "PUT", path, key, embedBody{Origins: []string{"http://evil.example"}})
	e := wantError(t, rec, 400, apierr.InvalidRequest)
	if !strings.Contains(e.Message, "http://evil.example") {
		t.Errorf("error %q does not name the origin", e.Message)
	}
	rec = call(t, s, "GET", path, key, nil)
	if got := decode[embedBody](t, rec); !reflect.DeepEqual(got.Origins, []string{"https://c.example"}) {
		t.Fatalf("a refused write changed the list: %v", got.Origins)
	}

	// Eleven origins are refused; duplicates collapse after normalisation.
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = "https://app" + string(rune('a'+i)) + ".example"
	}
	wantError(t, call(t, s, "PUT", path, key, embedBody{Origins: eleven}), 400, apierr.InvalidRequest)
	rec = call(t, s, "PUT", path, key, embedBody{Origins: []string{"https://a.example", "https://a.example/", "HTTPS://A.Example:443"}})
	wantStatus(t, rec, 200)
	if got := decode[embedBody](t, rec); !reflect.DeepEqual(got.Origins, []string{"https://a.example"}) {
		t.Fatalf("duplicates did not collapse: %v", got.Origins)
	}

	// An empty list is allowed.
	rec = call(t, s, "PUT", path, key, embedBody{Origins: []string{}})
	wantStatus(t, rec, 200)
	if got := decode[embedBody](t, rec); len(got.Origins) != 0 {
		t.Fatalf("empty put = %v", got.Origins)
	}

	// The change is audited with the new list.
	rec = call(t, s, "PUT", path, key, embedBody{Origins: []string{"https://audited.example"}})
	wantStatus(t, rec, 200)
	rec = call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/audit", key, nil)
	wantStatus(t, rec, 200)
	found := false
	for _, a := range decode[listResponse[auditJSON]](t, rec).Items {
		if a.Action == "account.embed_origins" {
			found = true
			if a.Target != acc.ID {
				t.Errorf("audit target = %q", a.Target)
			}
		}
	}
	if !found {
		t.Error("no account.embed_origins audit entry")
	}

	// Deleting the account removes its origins.
	if err := s.store.Write(context.Background(), func(tx *store.Tx) error { return tx.DeleteAccount(context.Background(), acc.ID) }); err != nil {
		t.Fatal(err)
	}
	if got, err := s.store.EmbedOrigins(context.Background(), acc.ID); err != nil || len(got) != 0 {
		t.Fatalf("origins after account delete = %v, %v", got, err)
	}
}

// TestEmbedOriginsImpersonation: an admin signed in as a user may read the list but not replace it
// (decision 185, decision 41).
func TestEmbedOriginsImpersonation(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, _ := fx.signedInAsMember(t)
	path := "/api/v1/accounts/" + fx.acc.ID + "/embed"
	wantStatus(t, callSession(t, fx.s, imp, "GET", path, nil), 200)
	wantError(t, callSession(t, fx.s, imp, "PUT", path, embedBody{Origins: []string{"https://x.example"}}), 403, apierr.Forbidden)
}

// TestEmbedOriginsUnion: the origins allowed for an account are the installation list and the
// account's together, sorted, each once (decision 185).
func TestEmbedOriginsUnion(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) {
		c.Embed.Origins = []string{"https://install.example", "https://shared.example"}
	})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/embed", key, embedBody{Origins: []string{"https://account.example", "https://shared.example"}})
	got, err := s.allowedEmbedOrigins(context.Background(), acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://account.example", "https://install.example", "https://shared.example"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("union = %v; want %v", got, want)
	}
}
