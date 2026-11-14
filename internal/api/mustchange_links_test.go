// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// A link made by a user whose password an admin typed is paused while the user must change it: the
// members who opened it lose its access at once, a new open is the dead answer, and the account list
// and the sharer's chain say paused. Clearing the flag revives the link with nothing rewritten: the
// visits it had still count.
func TestMustChangeCreatorPausesOrganisationLink(t *testing.T) {
	t.Parallel()
	fx := newLinkFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "manager")
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links",
		map[string]any{"audience": "organisation", "role": "viewer"})
	wantStatus(t, rec, 201)
	secret := secretOf(t, decode[shareLinkCreated](t, rec).URL)
	linkID := decode[shareLinkCreated](t, rec).Link.ID

	fx.openedAs(t, fx.bobSess, secret)
	wantStatus(t, fx.stat(t, fx.bobSess, fx.deep.ID), 200)

	setMustChange(t, fx.s, fx.acc.ID, fx.ann.ID, true)
	wantError(t, fx.stat(t, fx.bobSess, fx.deep.ID), 404, apierr.NotFound)
	deadLink(t, fx.open(t, fx.bobSess, secret))
	if by := listedBy(t, accountLinks(t, fx.s, fx.key, fx.acc.ID, "")); !by[linkID].Paused {
		t.Errorf("the account list does not say paused: %+v", by[linkID])
	}
	sharer := decode[listResponse[shareLinkJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID+"/share-links", fx.key, nil))
	if len(sharer.Items) != 1 || sharer.Items[0].Paused == nil || !*sharer.Items[0].Paused {
		t.Errorf("the sharer's chain does not say paused: %+v", sharer.Items)
	}

	setMustChange(t, fx.s, fx.acc.ID, fx.ann.ID, false)
	wantStatus(t, fx.stat(t, fx.bobSess, fx.deep.ID), 200) // the visit it had still counts
	fx.openedAs(t, fx.bobSess, secret)
	if by := listedBy(t, accountLinks(t, fx.s, fx.key, fx.acc.ID, "")); by[linkID].Paused {
		t.Errorf("the account list still says paused: %+v", by[linkID])
	}
}

// An anyone link made by a must-change user is paused: the secret opens nothing, a credential it issued
// reads nothing and renews nothing, and the account list says paused. Clearing the flag revives it.
func TestMustChangeCreatorPausesAnyoneLink(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "manager")
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links",
		map[string]any{"audience": "anyone"})
	wantStatus(t, rec, 201)
	created := decode[shareLinkCreated](t, rec)
	secret := secretOf(t, created.URL)

	tok := fx.visitAnyone(t, secret, "")
	wantStatus(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID), 200)

	setMustChange(t, fx.s, fx.acc.ID, fx.ann.ID, true)
	_, openRec := fx.tryAnyone(t, secret, "")
	deadVisitor(t, openRec)
	deadVisitor(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID))
	deadVisitor(t, fx.visitor(t, tok, "POST", "/api/v1/share/renew"))
	if by := listedBy(t, accountLinks(t, fx.s, fx.key, fx.acc.ID, "")); !by[created.Link.ID].Paused {
		t.Errorf("the account list does not say paused: %+v", by[created.Link.ID])
	}

	setMustChange(t, fx.s, fx.acc.ID, fx.ann.ID, false)
	tok = fx.visitAnyone(t, secret, "")
	wantStatus(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID), 200)
	if by := listedBy(t, accountLinks(t, fx.s, fx.key, fx.acc.ID, "")); by[created.Link.ID].Paused {
		t.Errorf("the account list still says paused: %+v", by[created.Link.ID])
	}
}

// A drop link made by a must-change user is paused: the secret opens nothing, its visitor's info and
// uploads are refused, and no mail goes to the creator for the files that arrived. Clearing the flag
// revives the link, and a file that arrives after it is mailed.
func TestMustChangeCreatorPausesDropLink(t *testing.T) {
	t.Parallel()
	fx := newDropFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "manager")
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links",
		map[string]any{"upload_only": true, "notify": true})
	wantStatus(t, rec, 201)
	created := decode[shareLinkCreated](t, rec)
	secret := secretOf(t, created.URL)

	tok := fx.visitDrop(t, secret, "")
	wantStatus(t, dropUploadCall(t, fx, tok, "one.txt", "1"), 204)

	setMustChange(t, fx.s, fx.acc.ID, fx.ann.ID, true)
	deadVisitor(t, fx.visitorCall(t, secret))
	deadVisitor(t, fx.visitor(t, tok, "GET", "/api/v1/share/info"))
	deadVisitor(t, createUpload(t, fx.s, tok, 1, md("name", "two.txt")))
	ctx := context.Background()
	now := time.Now()
	fx.s.sendDropNotices(ctx, now)
	if got := drops(t, fx); len(got) != 0 {
		t.Errorf("a paused drop link mailed its creator: %+v", got)
	}
	if n := fx.queryString(t, "SELECT unreported_files FROM share_links WHERE id = ?", created.Link.ID); n != "0" {
		t.Errorf("unreported_files = %s; want 0, the count dropped with nothing mailed", n)
	}
	if by := listedBy(t, accountLinks(t, fx.s, fx.key, fx.acc.ID, "")); !by[created.Link.ID].Paused {
		t.Errorf("the account list does not say paused: %+v", by[created.Link.ID])
	}

	setMustChange(t, fx.s, fx.acc.ID, fx.ann.ID, false)
	tok = fx.visitDrop(t, secret, "")
	wantStatus(t, dropUploadCall(t, fx, tok, "three.txt", "3"), 204)
	fx.s.sendDropNotices(ctx, now.Add(61*time.Minute))
	if got := drops(t, fx); len(got) != 1 {
		t.Errorf("the mails after the change = %+v; want one for the file that arrived after it", got)
	}
	if by := listedBy(t, accountLinks(t, fx.s, fx.key, fx.acc.ID, "")); by[created.Link.ID].Paused {
		t.Errorf("the account list still says paused: %+v", by[created.Link.ID])
	}
}

// The rule binds the flagged creator only: another member's link and an account key's link keep working
// while one member must change its password, and the account list pauses only the flagged one's.
func TestMustChangeCreatorLeavesOtherLinksAlone(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "manager")
	fx.setPerm(t, fx.c.ID, fx.bob.ID, "manager")
	annRec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links",
		map[string]any{"audience": "anyone"})
	wantStatus(t, annRec, 201)
	annCreated := decode[shareLinkCreated](t, annRec)
	bobRec := callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.c.ID+"/share-links",
		map[string]any{"audience": "anyone"})
	wantStatus(t, bobRec, 201)
	bobCreated := decode[shareLinkCreated](t, bobRec)
	keyLink, keySecret := fx.makeAnyone(t, fx.a.ID, nil)

	setMustChange(t, fx.s, fx.acc.ID, fx.ann.ID, true)
	_, annOpen := fx.tryAnyone(t, secretOf(t, annCreated.URL), "")
	deadVisitor(t, annOpen)
	fx.visitAnyone(t, secretOf(t, bobCreated.URL), "")
	fx.visitAnyone(t, keySecret, "")
	by := listedBy(t, accountLinks(t, fx.s, fx.key, fx.acc.ID, ""))
	if !by[annCreated.Link.ID].Paused {
		t.Errorf("ann's link is not paused: %+v", by[annCreated.Link.ID])
	}
	if by[bobCreated.Link.ID].Paused || by[keyLink.ID].Paused {
		t.Errorf("bob's or the key's link is paused: %+v / %+v", by[bobCreated.Link.ID], by[keyLink.ID])
	}
}
