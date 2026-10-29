// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// A role held only through an organisation link opens the content. It never shares, decides requests or
// makes links (security review M1). The tests put the link's editor role on a node whose editors may share,
// so the only thing that could let the caller share is the link.

// linkEditorOnB gives ann editor through an organisation link on the team node b, where editors may share.
func (fx *linkFixture) linkEditorOnB(t *testing.T) shareLinkJSON {
	t.Helper()
	fx.flag(t, fx.b.ID, map[string]any{"editors_can_share": true})
	link, url := fx.makeLink(t, fx.b.ID, "editor")
	fx.openedAs(t, fx.annSess, secretOf(t, url))
	return link
}

func TestLinkEditorCannotShare(t *testing.T) {
	fx := newLinkFixture(t)
	link := fx.linkEditorOnB(t)
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/access-requests",
		map[string]any{"role": "viewer"}), 202)
	arq := onlyPending(t, fx.sharingFixture)

	// The content opens for ann; sharing and deciding do not.
	wantStatus(t, fx.stat(t, fx.annSess, fx.deep.ID), 200)
	wantError(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/permissions/"+fx.bob.ID,
		map[string]any{"role": "viewer"}), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/share-links",
		map[string]any{"audience": "organisation"}), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/share-links/"+link.ID,
		map[string]any{"role": "viewer"}), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/access-requests/"+arq+"/approve",
		map[string]any{}), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/access-requests/"+arq+"/deny",
		map[string]any{}), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/share-links/"+link.ID, nil), 403, apierr.Forbidden)
	// The pending list offers her nothing to decide.
	list := decode[listResponse[accessRequestJSON]](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/access-requests", nil))
	if len(list.Items) != 0 {
		t.Errorf("a link editor is offered %d requests to decide, want none", len(list.Items))
	}
	// The refused permission and approval wrote nothing for bob.
	if rows := fx.rowsOn(t, fx.deep.ID); len(rows) != 0 {
		t.Errorf("permission rows after the refused changes = %+v, want none", rows)
	}
}

// TestRealEditorStillShares is the control for TestLinkEditorCannotShare: the same node, with ann holding
// editor by a permission of her own, may share and decide.
func TestRealEditorStillShares(t *testing.T) {
	fx := newLinkFixture(t)
	fx.flag(t, fx.b.ID, map[string]any{"editors_can_share": true})
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/access-requests",
		map[string]any{"role": "viewer"}), 202)
	arq := onlyPending(t, fx.sharingFixture)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/permissions/"+fx.bob.ID,
		map[string]any{"role": "viewer"}), 200)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/access-requests/"+arq+"/approve",
		map[string]any{}), 200)
}

// TestTransferWritesEditorForALinkHolder (M2): the previous owner holds editor on a personal item only through
// a link when the transfer runs. The transfer must still leave it a row, since the link can be deleted. The
// item moves into the recipient's My files, so the row is what keeps the previous owner's access.
func TestTransferWritesEditorForALinkHolder(t *testing.T) {
	fx := newLinkFixture(t)
	pf := fx.personalFolder(t, fx.annSess)
	file := fx.sessionFile(t, fx.annSess, pf, "/mine.txt", "mine")
	_, url := fx.makeLink(t, file.ID, "editor")
	fx.openedAs(t, fx.annSess, secretOf(t, url))
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+file.ID+"/transfer-ownership",
		map[string]any{"to": fx.bob.ID}), 200)
	var held bool
	for _, r := range fx.rowsOn(t, file.ID) {
		if r.SubjectID == fx.ann.ID && r.Role == store.RoleEditor {
			held = true
		}
	}
	if !held {
		t.Errorf("rows after the transfer = %+v, want ann as editor", fx.rowsOn(t, file.ID))
	}
}

// TestApprovalWritesARowForALinkHolder (M2): the requester holds the requested role only through a link when
// the approval runs. The approval must still write its row, so the grant outlives the link.
func TestApprovalWritesARowForALinkHolder(t *testing.T) {
	fx := newLinkFixture(t)
	link, url := fx.makeLink(t, fx.b.ID, "viewer")
	fx.openedAs(t, fx.annSess, secretOf(t, url))
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/access-requests",
		map[string]any{"role": "editor"}), 202)
	arq := onlyPending(t, fx.sharingFixture)
	// The link is raised to editor, so ann holds the requested role through it when the approval runs.
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+link.ID, fx.key, map[string]any{"role": "editor"}), 200)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/access-requests/"+arq+"/approve", fx.key, map[string]any{}), 200)
	// The link goes; the approved row keeps ann at editor on the node.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/share-links/"+link.ID, fx.key, nil), 204)
	rows := fx.rowsOn(t, fx.deep.ID)
	if len(rows) != 1 || rows[0].SubjectID != fx.ann.ID || rows[0].Role != store.RoleEditor {
		t.Errorf("rows after the approval = %+v, want ann as editor", rows)
	}
	wantStatus(t, fx.stat(t, fx.annSess, fx.deep.ID), 200)
}

// TestLinkHolderMayRequestDurableAccess: a role held only through a link does not count as held when a
// request is made, so a link editor may ask for editor and the request reaches the managers.
func TestLinkHolderMayRequestDurableAccess(t *testing.T) {
	fx := newLinkFixture(t)
	_, url := fx.makeLink(t, fx.b.ID, "editor")
	fx.openedAs(t, fx.annSess, secretOf(t, url))
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/access-requests",
		map[string]any{"role": "editor"}), 202)
	onlyPending(t, fx.sharingFixture)
}

// TestOpenDeletedBetweenReadAndWrite (L1): the link is deleted after the open reads it and before its write.
// The answer is the uniform not_found, not a failure from the visit's foreign key.
func TestOpenDeletedBetweenReadAndWrite(t *testing.T) {
	fx := newLinkFixture(t)
	link, url := fx.makeLink(t, fx.b.ID, "viewer")
	fired := false
	fx.s.beforeWrite = func() {
		if fired {
			return
		}
		fired = true
		if err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
			return tx.DeleteShareLink(context.Background(), fx.acc.ID, link.ID)
		}); err != nil {
			t.Fatal(err)
		}
	}
	deadLink(t, fx.open(t, fx.annSess, secretOf(t, url)))
	if !fired {
		t.Fatal("the test hook did not run")
	}
}

// TestOpenSecretResetBetweenReadAndWrite (L1): the secret is replaced after the open reads the old hash. The
// old secret must not open the link: the stored hash no longer matches what was presented.
func TestOpenSecretResetBetweenReadAndWrite(t *testing.T) {
	fx := newLinkFixture(t)
	link, url := fx.makeLink(t, fx.b.ID, "viewer")
	oldSecret := secretOf(t, url)
	fired := false
	fx.s.beforeWrite = func() {
		if fired {
			return
		}
		fired = true
		ctx := context.Background()
		_, hash := auth.NewToken()
		if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
			l, err := tx.ShareLink(ctx, fx.acc.ID, link.ID)
			if err != nil {
				return err
			}
			l.SecretHash = hash
			return tx.UpdateShareLink(ctx, l)
		}); err != nil {
			t.Fatal(err)
		}
	}
	deadLink(t, fx.open(t, fx.annSess, oldSecret))
	if !fired {
		t.Fatal("the test hook did not run")
	}
	wantError(t, fx.stat(t, fx.annSess, fx.b.ID), 404, apierr.NotFound)
}

// TestRepeatOpenAuditsOnce (L2): a member who opens the same link again within a day adds no audit entry.
func TestRepeatOpenAuditsOnce(t *testing.T) {
	fx := newLinkFixture(t)
	link, url := fx.makeLink(t, fx.b.ID, "viewer")
	secret := secretOf(t, url)
	for i := 0; i < 3; i++ {
		fx.openedAs(t, fx.annSess, secret)
	}
	n := 0
	for _, e := range fx.auditSeenBy(t, "") {
		if e.Action == "link.open" && e.Target == link.ID {
			n++
		}
	}
	if n != 1 {
		t.Errorf("link.open entries after three opens = %d, want 1", n)
	}
}

// TestOpenIsLimitedPerUser (L2): the per-address limit is raised here, so the per-member limit is what refuses
// the sixty-first open of a link by one member.
func TestOpenIsLimitedPerUser(t *testing.T) {
	fx := newLinkFixture(t)
	_, url := fx.makeLink(t, fx.b.ID, "viewer")
	secret := secretOf(t, url)
	fx.s.limits["share.open.ip"] = newLimiter(1000)
	// The bucket refills a token a second, and a slow run (-race) refills during the opens, so the test does
	// not count on exactly sixty: it allows at least sixty, then wants the refusal.
	opens := 0
	for ; ; opens++ {
		rec := fx.open(t, fx.annSess, secret)
		if rec.Code == http.StatusTooManyRequests {
			wantError(t, rec, 429, apierr.RateLimited)
			break
		}
		wantStatus(t, rec, 200)
		if opens > 500 {
			t.Fatal("no refusal after 500 opens by one member")
		}
	}
	if opens < 60 {
		t.Errorf("refused after %d opens, want at least the burst of 60", opens)
	}
}
