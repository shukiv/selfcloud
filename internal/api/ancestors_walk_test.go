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
