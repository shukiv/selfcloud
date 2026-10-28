// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// errDownloadRestricted is the refusal for a file whose no-download rule applies to the caller. The
// caller may still view it inline where the type allows; the message says so.
func errDownloadRestricted() error {
	return apierr.New(apierr.DownloadRestricted,
		"downloads of this item are turned off for your role; viewing it in the browser is still allowed")
}

// downloadRestricted reports whether the no-download rule refuses the caller the downloads of node n,
// which it reaches at role. The rule binds only a viewer or a commenter: contributors and above, and an
// account key, may always download. It deters, and does not secure: a viewer can still see the
// contents inline, and a file it has seen can be copied by other means.
func (s *Server) downloadRestricted(ctx context.Context, n *store.Node, role string) (bool, error) {
	if store.RoleRank(role) > store.RoleRank(store.RoleCommenter) {
		return false, nil
	}
	return s.store.NoDownloadApplies(ctx, n.AccountID, n.ID)
}

// inlineAllowed reports whether a content response for n may be inline. A restricted caller may take
// only inline responses, so an inline request for a type the browser may not show
// inline is refused rather than downgraded silently to a download.
func (s *Server) inlineAllowed(r *http.Request, n *store.Node, wantInline bool) bool {
	if !wantInline {
		return false
	}
	_, ok := s.inlinePolicy(r, n.MimeType)
	return ok
}

// noDownloadReader is what the no-download checks read: the store, or a write transaction.
type noDownloadReader interface {
	NoDownloadApplies(ctx context.Context, accountID, nodeID string) (bool, error)
	SubtreeHasNoDownload(ctx context.Context, accountID, nodeID string) (bool, error)
}

// copyRestricted reports whether the no-download rule refuses a copy of n by a caller at role. A
// viewer or commenter may not copy a node the rule covers, nor a subtree that holds one, since the copy
// would carry the restricted contents out. Contributors and above copy freely, and the copy keeps its
// flags, so the rule goes on applying to it.
func (s *Server) copyRestricted(ctx context.Context, rd noDownloadReader, n *store.Node, role string) (bool, error) {
	if store.RoleRank(role) > store.RoleRank(store.RoleCommenter) {
		return false, nil
	}
	covered, err := rd.NoDownloadApplies(ctx, n.AccountID, n.ID)
	if err != nil || covered {
		return covered, err
	}
	return rd.SubtreeHasNoDownload(ctx, n.AccountID, n.ID)
}
