// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// PutUserInfo (design section 10, decision 87). The editor sends a string about the person it is open for, which the host
// keeps and returns in CheckFileInfo as UserInfo. The string belongs to the user: it is stored per user, not per file, it
// is read back only for that user, and the row goes with the user (migration 0047).
//
// The specification gives the string a limit of 1024 ASCII characters. This host allows 1024 bytes, which is the same for
// ASCII and stricter for any other text, and the string must be valid UTF-8, since the store holds text. An answer for a
// longer string is 400, a code the specification does not list for this operation.

import (
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// wopiUserInfoMax is the longest UserInfo the host keeps, in bytes.
const wopiUserInfoMax = 1024

// handleWOPIPutUserInfo stores the body as the caller's UserInfo. A browser-origin token may send it, read-only ones too: the
// string is the person's own, not the file's. A token minted by a delegated key may not, since the key is not the person
// (decision 87): that answer is 501, as the other operations this host refuses for a caller.
func (s *Server) handleWOPIPutUserInfo(w http.ResponseWriter, r *http.Request) error {
	g, err := s.wopiRequest(w, r)
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	if g.p.UserID == "" {
		// Only a person opens the editor (decision 31), so a token without a user has no UserInfo to keep.
		s.wopiFail(w, r, notFound("node"))
		return nil
	}
	if g.p.Token != nil && g.p.Token.MintedBy != "" {
		if info := infoFrom(r.Context()); info != nil {
			info.clientFault = true
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotImplemented)
		return nil
	}
	// One byte more than the limit is read, so a longer body is seen as longer without reading the rest of it.
	body, err := io.ReadAll(io.LimitReader(r.Body, wopiUserInfoMax+1))
	if err != nil {
		s.wopiFail(w, r, apierr.Wrap(apierr.InvalidRequest, "the request body could not be read", err))
		return nil
	}
	if len(body) > wopiUserInfoMax {
		s.wopiFail(w, r, apierr.New(apierr.InvalidRequest, "UserInfo may be at most 1024 characters"))
		return nil
	}
	if !utf8.Valid(body) {
		s.wopiFail(w, r, apierr.New(apierr.InvalidRequest, "UserInfo must be valid UTF-8"))
		return nil
	}
	ctx := r.Context()
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		// The token is checked again inside the write, as every WOPI write is.
		if _, _, err := s.authenticateWOPI(ctx, tx, g.token, s.store.Clock()); err != nil {
			return err
		}
		return tx.PutWOPIUserInfo(ctx, g.node.AccountID, g.p.UserID, string(body), s.store.Clock())
	})
	if err != nil {
		s.wopiFail(w, r, err)
		return nil
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	return nil
}
