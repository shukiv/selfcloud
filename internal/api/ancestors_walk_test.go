// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// flakyReader is the store, with one read of a node failing: the n-th call of Node. The walk reads through the access
// seam, so a failure there is a failure of the store, not a node that is missing.
type flakyReader struct {
	accessReader
	failAt int // the 1-based call of Node that fails
	calls  int
}

func (f *flakyReader) Node(ctx context.Context, accountID, id string) (*store.Node, error) {
	f.calls++
	if f.calls == f.failAt {
		return nil, errors.New("the store is unavailable")
	}
	return f.accessReader.Node(ctx, accountID, id)
}

// trashedReader is the store with one node in the trash, as the access rules see it: a trashed node is not reached,
// and neither is anything below it. Reach is not downward-closed in general, so the walk must stop at the trashed node
// and not read on above it, even where a node above is reached.
type trashedReader struct {
	accessReader
	trashed string
}

func (f *trashedReader) TrashState(ctx context.Context, accountID, id string) (bool, bool, error) {
	if id == f.trashed {
		return true, false, nil
	}
	return f.accessReader.TrashState(ctx, accountID, id)
}

// TestAncestorsStopAtAnUnreachableMiddleEvenWhenTheTopIsReached: the middle parent is trashed, so the caller does not
// reach it; the top (the directory above it) and the folder root are reached. The answer is empty: the walk stops at
// the middle and returns nothing above it.
func TestAncestorsStopAtAnUnreachableMiddleEvenWhenTheTopIsReached(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	ctx := context.Background()
	n, err := fx.s.store.Node(ctx, fx.acc.ID, fx.deep.ID)
	if err != nil {
		t.Fatal(err)
	}
	rd := &trashedReader{accessReader: fx.s.store, trashed: fx.b.ID}
	chain, _, err := fx.s.reachableAncestors(ctx, rd, accountKeyOf(fx.acc.ID), n)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 0 {
		t.Errorf("the walk returned %d ancestors above an unreachable middle; want none", len(chain))
	}
}

// accountKeyOf is the account key's principal: it reaches every node of the account, so only the walk decides the chain.
func accountKeyOf(accID string) *auth.Principal {
	return &auth.Principal{Kind: auth.KindAccountKey, AccountID: accID}
}

// TestAncestorsStoreErrorIsAnErrorNotAShorterChain: the store fails reading the second ancestor. The walk answers that
// error, which the handler sends as a 500, and not the one ancestor it had read before the failure. A shorter answer
// would look like the node's real chain.
func TestAncestorsStoreErrorIsAnErrorNotAShorterChain(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	ctx := context.Background()
	n, err := fx.s.store.Node(ctx, fx.acc.ID, fx.deep.ID)
	if err != nil {
		t.Fatal(err)
	}
	rd := &flakyReader{accessReader: fx.s.store, failAt: 2}
	chain, _, err := fx.s.reachableAncestors(ctx, rd, accountKeyOf(fx.acc.ID), n)
	if err == nil {
		t.Fatalf("the walk answered %d ancestors and no error; a store error must be the answer", len(chain))
	}
	if code := apierr.From(err).Code; code != apierr.Internal {
		t.Errorf("the error is %s, want internal (a 500)", code)
	}
	if chain != nil {
		t.Errorf("the walk returned a partial chain of %d with the error", len(chain))
	}
}

// nodeCountingReader is the store, counting the reads of a node. Each step of the walk reads its node once, so the count is
// the number of nodes the walk read.
type nodeCountingReader struct {
	accessReader
	nodes int
}

func (c *nodeCountingReader) Node(ctx context.Context, accountID, id string) (*store.Node, error) {
	c.nodes++
	return c.accessReader.Node(ctx, accountID, id)
}

// toppedReader is the store with one node read as the top of its folder: its parent_id is empty, as for a chain that ends
// there.
type toppedReader struct {
	accessReader
	top string
}

func (f *toppedReader) Node(ctx context.Context, accountID, id string) (*store.Node, error) {
	n, err := f.accessReader.Node(ctx, accountID, id)
	if err == nil && id == f.top {
		cp := *n
		cp.ParentID = ""
		n = &cp
	}
	return n, err
}

// walkOf is a walk's chain as its IDs and roles, nearest first.
func walkOf(chain []reachedNode) []string {
	out := make([]string, 0, len(chain))
	for _, r := range chain {
		out = append(out, r.node.ID+":"+r.role)
	}
	return out
}

// deepNode makes a file depth directories down in a new team space of the fixture. It returns the file as the store reads
// it, and the IDs of its directories, d1 first.
func (fx *sharingFixture) deepNode(t *testing.T, depth int) (*store.Node, []string) {
	t.Helper()
	f := newFolder(t, fx.s, fx.key, fx.acc.ID, fmt.Sprintf("Depth %d", depth))
	file, dirs := deepTree(t, fx.s, fx.key, fx.acc.ID, f.ID, depth)
	n, err := fx.s.store.Node(context.Background(), fx.acc.ID, file.ID)
	if err != nil {
		t.Fatal(err)
	}
	return n, dirs
}

// TestAncestorsAboveATrashedParentAtTheCapAreNotTruncated: d6, the 65th ancestor of a file 70 directories down, is trashed,
// and the 64 below it are reached (the stand-in for a rule under which reach is not downward-closed). The walk answers d70
// to d7, not truncated: the same answer, item for item, as for a chain whose top is d7.
func TestAncestorsAboveATrashedParentAtTheCapAreNotTruncated(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	ctx := context.Background()
	n, dirs := fx.deepNode(t, 70)
	p := accountKeyOf(fx.acc.ID)
	hidden, hiddenTruncated, err := fx.s.reachableAncestors(ctx, &trashedReader{accessReader: fx.s.store, trashed: dirs[5]}, p, n)
	if err != nil {
		t.Fatal(err)
	}
	topped, toppedTruncated, err := fx.s.reachableAncestors(ctx, &toppedReader{accessReader: fx.s.store, top: dirs[6]}, p, n)
	if err != nil {
		t.Fatal(err)
	}
	if hiddenTruncated || toppedTruncated {
		t.Errorf("truncated is %v below a trashed 65th, and %v for a chain whose top is the 64th; want false for both",
			hiddenTruncated, toppedTruncated)
	}
	if got, want := walkOf(hidden), walkOf(topped); !reflect.DeepEqual(got, want) {
		t.Errorf("the walk below a trashed 65th is %v, want the chain that ends at the 64th, %v", got, want)
	}
	if len(hidden) != 64 || hidden[63].node.ID != dirs[6] {
		t.Errorf("the walk below a trashed 65th has %d items; want 64, d7 the topmost", len(hidden))
	}
}

// TestAncestorsReadOneNodeAboveTheCapAndOnlyThere: the walk reads each ancestor once. Below the cap it reads nothing more:
// 11 reads for a file 10 directories down (ten directories and the root), 64 for one 63 down (the root the 64th). At the cap
// it reads one node more, the 65th, and none above it: 65 reads for a file 64 down and for one 70 down. A walk that stops at
// an unreachable parent reads that parent and nothing above it.
func TestAncestorsReadOneNodeAboveTheCapAndOnlyThere(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	ctx := context.Background()
	p := accountKeyOf(fx.acc.ID)
	var dirs10 []string
	var node10 *store.Node
	for _, c := range []struct {
		depth, reads, items int
		truncated           bool
	}{{10, 11, 11, false}, {63, 64, 64, false}, {64, 65, 64, true}, {70, 65, 64, true}} {
		n, dirs := fx.deepNode(t, c.depth)
		if c.depth == 10 {
			node10, dirs10 = n, dirs
		}
		rd := &nodeCountingReader{accessReader: fx.s.store}
		chain, truncated, err := fx.s.reachableAncestors(ctx, rd, p, n)
		if err != nil {
			t.Fatal(err)
		}
		if rd.nodes != c.reads {
			t.Errorf("depth %d: the walk read %d nodes, want %d", c.depth, rd.nodes, c.reads)
		}
		if len(chain) != c.items || truncated != c.truncated {
			t.Errorf("depth %d: %d items, truncated %v; want %d, %v", c.depth, len(chain), truncated, c.items, c.truncated)
		}
	}

	// d5 is trashed: the walk reads d10 to d6, which it reaches, and d5, which it does not, and stops there.
	rd := &nodeCountingReader{accessReader: &trashedReader{accessReader: fx.s.store, trashed: dirs10[4]}}
	chain, truncated, err := fx.s.reachableAncestors(ctx, rd, p, node10)
	if err != nil {
		t.Fatal(err)
	}
	if rd.nodes != 6 || len(chain) != 5 || truncated {
		t.Errorf("a walk stopped at d5 read %d nodes for %d items, truncated %v; want 6 reads, 5 items, not truncated",
			rd.nodes, len(chain), truncated)
	}
}

// TestAncestorsStoreErrorAboveTheCapIsAnError: the store fails reading the 65th ancestor. The walk answers that error, which
// the handler sends as a 500, and not the 64 items read before it with a truncated flag guessed from the failure.
func TestAncestorsStoreErrorAboveTheCapIsAnError(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	n, _ := fx.deepNode(t, 70)
	rd := &flakyReader{accessReader: fx.s.store, failAt: 65}
	chain, truncated, err := fx.s.reachableAncestors(context.Background(), rd, accountKeyOf(fx.acc.ID), n)
	if err == nil {
		t.Fatalf("the walk answered %d ancestors, truncated %v, and no error; a store error must be the answer", len(chain), truncated)
	}
	if code := apierr.From(err).Code; code != apierr.Internal {
		t.Errorf("the error is %s, want internal (a 500)", code)
	}
	if chain != nil || truncated {
		t.Errorf("the walk returned %d items, truncated %v, with the error", len(chain), truncated)
	}
}
