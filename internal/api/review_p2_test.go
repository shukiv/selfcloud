// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// sharedPages reads GET /shared with limit=1 page by page, for one order, and returns every node ID it saw in order.
// It fails the test on any answer that is not a 200, so a page that breaks the cursor shows up here.
func sharedPages(t *testing.T, fx *sharingFixture, sess testSession, order string) []string {
	t.Helper()
	var ids []string
	cursor := ""
	for i := 0; i < 20; i++ {
		q := url.Values{"limit": {"1"}}
		if order != "" {
			q.Set("order", order)
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		rec := callSession(t, fx.s, sess, "GET", "/api/v1/shared?"+q.Encode(), nil)
		wantStatus(t, rec, 200)
		page := decode[listResponse[sharedItem]](t, rec)
		for _, it := range page.Items {
			ids = append(ids, it.ID)
		}
		if page.NextCursor == "" {
			return ids
		}
		cursor = page.NextCursor
	}
	t.Fatalf("order %q: more than 20 pages", order)
	return nil
}

// TestSharedPagingBothOrders: with three shares and a page size of one, every share is listed exactly once, through
// the default order (node ID) and through order=recent. The default order used to answer 400 on its second page.
func TestSharedPagingBothOrders(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.deep.ID, fx.ann.ID, "viewer")
	want := map[string]bool{fx.b.ID: true, fx.c.ID: true, fx.deep.ID: true}
	for _, order := range []string{"", "recent"} {
		got := sharedPages(t, fx, fx.annSess, order)
		seen := map[string]bool{}
		for _, id := range got {
			if seen[id] {
				t.Errorf("order %q: %s listed twice in %v", order, id, got)
			}
			seen[id] = true
		}
		if len(seen) != len(want) {
			t.Errorf("order %q: listed %v, want the three shares", order, got)
		}
		for id := range want {
			if !seen[id] {
				t.Errorf("order %q: share %s missing from %v", order, id, got)
			}
		}
	}
}

// TestTokensCannotChangeStarsOrRecent: a read-only token may not star, unstar or touch the recent list, and a write
// token may change the list only for a node inside its scope. Clearing or removing an entry is refused for every token,
// since that changes the list the user keeps and the token only acts for it.
func TestTokensCannotChangeStarsOrRecent(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "editor")
	// A user star and a recent entry the tokens must not disturb.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.sib.ID+"/star", nil), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.sib.ID, nil), 200)

	read := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID},
		"node": fx.b.ID, "permissions": "read"})
	write := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID},
		"node": fx.b.ID, "permissions": "write"})

	// A read token refused on star, unstar, clear and remove.
	wantError(t, rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", read, "", nil), 403, apierr.Forbidden)
	wantError(t, rawCall(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID+"/star", read, "", nil), 403, apierr.Forbidden)
	wantError(t, rawCall(t, fx.s, "DELETE", "/api/v1/recent", read, "", nil), 403, apierr.Forbidden)
	wantError(t, rawCall(t, fx.s, "DELETE", "/api/v1/recent/"+fx.deep.ID, read, "", nil), 403, apierr.Forbidden)

	// A write token refused on clear and remove, whatever its scope.
	wantError(t, rawCall(t, fx.s, "DELETE", "/api/v1/recent", write, "", nil), 403, apierr.Forbidden)
	wantError(t, rawCall(t, fx.s, "DELETE", "/api/v1/recent/"+fx.deep.ID, write, "", nil), 403, apierr.Forbidden)

	// A write token outside its scope is refused on star and unstar, as a node it does not reach.
	wantError(t, rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+fx.c.ID+"/star", write, "", nil), 404, apierr.NotFound)
	wantError(t, rawCall(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.sib.ID+"/star", write, "", nil), 404, apierr.NotFound)

	// Within its scope a write token may star and unstar.
	wantStatus(t, rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", write, "", nil), 204)
	wantStatus(t, rawCall(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID+"/star", write, "", nil), 204)

	// The user's own star and recent state survived every refusal.
	if ids := starredIDs(t, fx, fx.annSess); !contains(ids, fx.sib.ID) {
		t.Errorf("a token disturbed the user's stars: %v", ids)
	}
}

// TestUnstarNeedsNoReachForAUser: a signed-in user removes its own star on a trashed or no-longer-shared node, and
// the count goes down, so the limit is not held by stars nobody can see. Any well-formed node ID answers 204; a
// malformed one is not_found.
func TestUnstarNeedsNoReachForAUser(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)

	// Trashed: the star stays while trashed, and the user removes it anyway.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	if n := countStars(t, fx, fx.ann.ID); n != 0 {
		t.Errorf("stars after removing one on a trashed node = %d, want 0", n)
	}

	// No longer shared: the grant goes, a star is still removable.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.sib.ID+"/star", nil), 404)
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.sib.ID+"/star", nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.c.ID+"/permissions/"+fx.ann.ID, fx.key, nil), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.sib.ID+"/star", nil), 204)
	if n := countStars(t, fx, fx.ann.ID); n != 0 {
		t.Errorf("stars after removing one on an unshared node = %d, want 0", n)
	}

	// A well-formed ID the user never could reach, and one that names no node, both answer 204.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/nod_00000000000000000000000000/star", nil), 204)
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "DELETE", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	// A malformed ID is not_found.
	wantError(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/not-an-id/star", nil), 404, apierr.NotFound)
}

// TestViewBatchSkipsARowThatNoLongerHasANode: a view whose node was purged before the flush is skipped, and the good
// views of the same batch are still written.
func TestViewBatchSkipsARowThatNoLongerHasANode(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	now := time.Now()
	batch := []store.ViewRecord{
		{AccountID: fx.acc.ID, UserID: fx.ann.ID, NodeID: fx.deep.ID, At: now},
		{AccountID: fx.acc.ID, UserID: fx.ann.ID, NodeID: "nod_gone0000000000000000000000", At: now},
		{AccountID: fx.acc.ID, UserID: "usr_gone00000000000000000000000", NodeID: fx.sib.ID, At: now},
	}
	if err := fx.s.store.WriteViews(context.Background(), batch); err != nil {
		t.Fatalf("a batch with one bad row failed as a whole: %v", err)
	}
	ids := recentIDs(t, fx, fx.annSess)
	if !contains(ids, fx.deep.ID) {
		t.Errorf("the good view of the batch was lost: %v", ids)
	}
}

// TestNoViewForAKeyOrAVisitor: only a signed-in user acting for itself records a view. Calling the recorder with an
// account key and with a link visitor queues nothing; removing the user guard in noteView must fail this.
func TestNoViewForAKeyOrAVisitor(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	n := &store.Node{ID: fx.deep.ID, AccountID: fx.acc.ID, Kind: store.KindFile}
	fx.s.noteView(&auth.Principal{Kind: auth.KindAccountKey, ID: "key", AccountID: fx.acc.ID}, n)
	fx.s.noteView(&auth.Principal{Kind: auth.KindShareLink, ID: "visitor", AccountID: fx.acc.ID,
		Link: &auth.LinkScope{Node: fx.b.ID, Role: "viewer"}}, n)
	if q := len(fx.s.views.ch); q != 0 {
		t.Errorf("%d views queued for a key or a visitor, want none", q)
	}
	fx.s.noteView(&auth.Principal{Kind: auth.KindUser, ID: fx.ann.ID, AccountID: fx.acc.ID, UserID: fx.ann.ID}, n)
	if q := len(fx.s.views.ch); q != 1 {
		t.Errorf("%d views queued for a signed-in user, want 1", q)
	}
}

// TestFlushOnShutdownWritesPendingViews: when the recorder is told to stop, the views still queued are written before
// run returns, as Serve relies on.
func TestFlushOnShutdownWritesPendingViews(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	v := newViewRecorderSize(fx.s.store, fx.s.log, 8)
	v.record(store.ViewRecord{AccountID: fx.acc.ID, UserID: fx.ann.ID, NodeID: fx.deep.ID, At: time.Now()})
	ctx, stop := context.WithCancel(context.Background())
	stop()
	done := make(chan struct{})
	go func() { v.run(ctx); close(done) }()
	<-done
	if ids := recentIDs(t, fx, fx.annSess); !contains(ids, fx.deep.ID) {
		t.Errorf("the pending view was not written on shutdown: %v", ids)
	}
}

// TestGateFullNeverBlocksOrGrows: a full gate refuses new keys at once, never holds more than its cap, and takes new
// keys again once the old ones have aged past the ten-minute gap. A large gate, filled and refused, must answer each
// refusal promptly: the eviction on each refusal must not scan the whole gate.
func TestGateFullNeverBlocksOrGrows(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	t0 := time.Now()
	key := func(i int) store.ViewRecord {
		return store.ViewRecord{AccountID: fx.acc.ID, UserID: fmt.Sprintf("usr_%d", i), NodeID: "nod_x", At: t0}
	}
	small := newViewRecorderCaps(fx.s.store, fx.s.log, 16, 3)
	for i := 0; i < 3; i++ {
		if !small.record(key(i)) {
			t.Fatalf("key %d refused under the cap", i)
		}
	}
	for i := 3; i < 1000; i++ {
		if small.record(key(i)) {
			t.Fatalf("key %d admitted past the cap", i)
		}
		if len(small.last) > 3 {
			t.Fatalf("gate holds %d keys, cap 3", len(small.last))
		}
	}
	later := t0.Add(11 * time.Minute)
	if !small.record(store.ViewRecord{AccountID: fx.acc.ID, UserID: "usr_late", NodeID: "nod_x", At: later}) {
		t.Error("a new key was refused after the old ones aged out of the gate")
	}

	// Timing: a gate of 100000 keys, full, refusing a stream of new keys.
	big := newViewRecorderCaps(fx.s.store, fx.s.log, 16, 100000)
	for i := 0; i < 100000; i++ {
		big.last[fmt.Sprintf("usr_%d|nod_x", i)] = t0
	}
	start := time.Now()
	for i := 0; i < 2000; i++ {
		big.record(key(200000 + i))
	}
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Errorf("2000 refusals at a full 100000-key gate took %v; eviction must not scan the gate", elapsed)
	}
	if len(big.last) > 100000 {
		t.Errorf("gate grew to %d keys, cap 100000", len(big.last))
	}
}

// TestCopyIsAnEditForTheCopier: a copy made by a signed-in user lands in that user's Recent as an edit of the new copy,
// in the request and when a queued copy job runs. The source is not an edit of the user.
func TestCopyIsAnEditForTheCopier(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "editor")

	// A copy in the request.
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/copy", map[string]any{"parent": fx.c.ID})
	wantStatus(t, rec, 201)
	copyID := decode[nodeJSON](t, rec).ID
	ids := recentIDs(t, fx, fx.annSess)
	if !contains(ids, copyID) {
		t.Errorf("a copy made in the request is not in the copier's recent list: %v", ids)
	}
	if contains(ids, fx.deep.ID) {
		t.Errorf("the source of a copy was recorded as an edit: %v", ids)
	}

	// A copy made by a queued job: the job's copy root is the new copy.
	fx.s.copySyncNodes = 1 // copies above one node are queued
	rec = callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/copy", map[string]any{"parent": fx.c.ID})
	wantStatus(t, rec, 202)
	job := decode[jobJSON](t, rec)
	if err := fx.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ids := recentIDs(t, fx, fx.annSess); !contains(ids, job.Target) {
		t.Errorf("a copy made by a job is not in the copier's recent list: %v", ids)
	}
}
