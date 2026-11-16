// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// maxBookmarksPerFile is how many bookmarks one user may keep in one file. The 201st is refused with conflict and
// reason limit, as a playlist past its cap is.
const maxBookmarksPerFile = 200

// maxBookmarkLabelRunes bounds a label after trimming. An empty label is allowed; a longer one is invalid_request.
const maxBookmarkLabelRunes = 100

// bookmarkWriteRate is the limiter of the bookmark writes, per user (server.go sets its size).
const bookmarkWriteRate = "bookmarks.write"

// bookmarkOwnCaller is the caller of the bookmark routes: the signed-in user, and no delegated token. A bookmark in a
// file is the user's own, as the place a user stopped at is, so a token acting for the user has no part in it, and
// neither does an account key or an installation admin.
func bookmarkOwnCaller(p *auth.Principal) error {
	if err := userCaller(p, "a bookmark in a media file"); err != nil {
		return err
	}
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token has no bookmark in a media file; only the signed-in user does")
	}
	return nil
}

// bookmarkCreateBody is the body of a create: a place in seconds of media time, and an optional label.
type bookmarkCreateBody struct {
	PositionSeconds float64 `json:"position_seconds"`
	Label           string  `json:"label"`
}

// bookmarkLabelBody is the body of a relabel.
type bookmarkLabelBody struct {
	Label string `json:"label"`
}

// bookmarkJSON is one bookmark as the routes answer it. The position is seconds of media time; created_at is RFC 3339.
type bookmarkJSON struct {
	ID              string    `json:"id"`
	PositionSeconds float64   `json:"position_seconds"`
	Label           string    `json:"label"`
	CreatedAt       time.Time `json:"created_at"`
}

type bookmarkListJSON struct {
	Bookmarks []bookmarkJSON `json:"bookmarks"`
}

func bookmarkJSONOf(b store.MediaBookmark) bookmarkJSON {
	return bookmarkJSON{ID: b.ID, PositionSeconds: float64(b.PositionMS) / 1000, Label: b.Label, CreatedAt: b.CreatedAt}
}

// bookmarkLabel trims a label and checks it: 0 to 100 characters (runes), with no control characters. An empty label is
// allowed. A refusal names the field.
func bookmarkLabel(raw string) (string, error) {
	label := strings.TrimSpace(raw)
	if utf8.RuneCountInString(label) > maxBookmarkLabelRunes {
		return "", apierr.Newf(apierr.InvalidRequest, "label must be from 0 to %d characters", maxBookmarkLabelRunes)
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return "", apierr.New(apierr.InvalidRequest, "label may not hold control characters")
		}
	}
	return label, nil
}

// bookmarkNode is the node a bookmark write acts on: the node the caller reaches, read inside the write so a grant
// revoked during the request stops the write, and a file only. A folder or a shortcut is not_found, as the resume
// place refuses them; nodeAccessWith has already refused a shortcut to an outsider.
func bookmarkNode(ctx context.Context, tx *store.Tx, p *auth.Principal, nodeID string) (*store.Node, error) {
	n, _, err := nodeAccessWith(ctx, tx, p, nodeID)
	if err != nil {
		return nil, err
	}
	if n.Kind != store.KindFile {
		return nil, nodeNotFound()
	}
	return n, nil
}

// handleListMediaBookmarks answers the bookmarks the signed-in user marked in a file the user reaches, in order. The
// list is the user's own: another user's bookmarks are never read. An empty list is [] and never null.
func (s *Server) handleListMediaBookmarks(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := bookmarkOwnCaller(p); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	if _, _, err := nodeAccessWith(r.Context(), s.store, p, nodeID); err != nil {
		return err
	}
	rows, err := s.store.MediaBookmarksOf(r.Context(), p.UserID, nodeID)
	if err != nil {
		return err
	}
	out := bookmarkListJSON{Bookmarks: make([]bookmarkJSON, 0, len(rows))}
	for _, b := range rows {
		out.Bookmarks = append(out.Bookmarks, bookmarkJSONOf(b))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleCreateMediaBookmark marks a place in a file the signed-in user reaches. The position is seconds of media time
// through toMS; the label is trimmed and bounded. A user past maxBookmarksPerFile in the file is refused with conflict
// and reason limit. The reach and the cap are checked inside the write. The bookmark is not audited, as the resume
// place is not.
func (s *Server) handleCreateMediaBookmark(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := bookmarkOwnCaller(p); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	// Charged before the body is read, so a flood of bad requests spends the same budget as good ones.
	if err := s.rateLimit(bookmarkWriteRate, p.UserID); err != nil {
		return err
	}
	var in bookmarkCreateBody
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	pos, err := toMS("position_seconds", in.PositionSeconds)
	if err != nil {
		return err
	}
	label, err := bookmarkLabel(in.Label)
	if err != nil {
		return err
	}
	ctx := r.Context()
	at := store.Now()
	b := store.MediaBookmark{ID: id.New("bmk"), PositionMS: pos, Label: label, CreatedAt: at}
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		n, err := bookmarkNode(ctx, tx, p, nodeID)
		if err != nil {
			return err
		}
		existing, err := tx.MediaBookmarksIn(ctx, p.UserID, nodeID)
		if err != nil {
			return err
		}
		if len(existing) >= maxBookmarksPerFile {
			return apierr.Newf(apierr.Conflict, "you may keep at most %d bookmarks in a file; delete some first", maxBookmarksPerFile).
				With("reason", "limit").With("limit", maxBookmarksPerFile)
		}
		return tx.AddMediaBookmark(ctx, n.AccountID, p.UserID, nodeID, b, at)
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, bookmarkJSONOf(b))
	return nil
}

// handleRelabelMediaBookmark sets the label of one of the signed-in user's bookmarks in a file the user reaches. A
// bookmark of another user, of another file, or one that does not exist is not_found. The reach is checked inside the
// write.
func (s *Server) handleRelabelMediaBookmark(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := bookmarkOwnCaller(p); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	bookmarkID := r.PathValue("bmk")
	if !id.Valid("bmk", bookmarkID) {
		return notFound("bookmark")
	}
	if err := s.rateLimit(bookmarkWriteRate, p.UserID); err != nil {
		return err
	}
	var in bookmarkLabelBody
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	label, err := bookmarkLabel(in.Label)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var got store.MediaBookmark
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := bookmarkNode(ctx, tx, p, nodeID); err != nil {
			return err
		}
		b, found, err := tx.RelabelMediaBookmark(ctx, p.UserID, nodeID, bookmarkID, label)
		if err != nil {
			return err
		}
		if !found {
			return notFound("bookmark")
		}
		got = b
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, bookmarkJSONOf(got))
	return nil
}

// handleDeleteMediaBookmark removes one of the signed-in user's bookmarks in a file the user reaches. A bookmark of
// another user, of another file, or one that does not exist is not_found. The reach is checked inside the write.
func (s *Server) handleDeleteMediaBookmark(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := bookmarkOwnCaller(p); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	bookmarkID := r.PathValue("bmk")
	if !id.Valid("bmk", bookmarkID) {
		return notFound("bookmark")
	}
	if err := s.rateLimit(bookmarkWriteRate, p.UserID); err != nil {
		return err
	}
	ctx := r.Context()
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := bookmarkNode(ctx, tx, p, nodeID); err != nil {
			return err
		}
		found, err := tx.DeleteMediaBookmark(ctx, p.UserID, nodeID, bookmarkID)
		if err != nil {
			return err
		}
		if !found {
			return notFound("bookmark")
		}
		return nil
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
