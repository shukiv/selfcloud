// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
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
	chain, err := fx.s.reachableAncestors(ctx, rd, accountKeyOf(fx.acc.ID), n)
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
	chain, err := fx.s.reachableAncestors(ctx, rd, accountKeyOf(fx.acc.ID), n)
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
