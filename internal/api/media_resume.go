// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"math"
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// maxMediaSeconds bounds a position and a duration. A longer file is refused rather than stored.
const maxMediaSeconds = 10_000_000

// resumeWriteRate is the limiter of the writes of a place, per user (server.go sets its size).
const resumeWriteRate = "resume.write"

// resumeOwnCaller is the caller of the resume routes: the signed-in user, and no delegated token. A place in a file is the
// user's own, as the recent list is, so a token acting for the user has no part in it, and neither does an account key or
// an installation admin.
func resumeOwnCaller(p *auth.Principal) error {
	if err := userCaller(p, "a place in a media file"); err != nil {
		return err
	}
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token has no place in a media file; only the signed-in user does")
	}
	return nil
}

// resumeBody is the place in a file, in seconds of media time.
type resumeBody struct {
	PositionSeconds float64 `json:"position_seconds"`
	DurationSeconds float64 `json:"duration_seconds"`
}

// resumeAnswer is the place a user stopped at. With no place stored, both are 0 and updated_at is absent.
type resumeAnswer struct {
	PositionSeconds float64    `json:"position_seconds"`
	DurationSeconds float64    `json:"duration_seconds"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
}

// toMS checks one number of seconds and converts it to milliseconds. NaN, infinities, negative values and values past
// maxMediaSeconds are refused.
func toMS(name string, sec float64) (int64, error) {
	if math.IsNaN(sec) || math.IsInf(sec, 0) || sec < 0 || sec > maxMediaSeconds {
		return 0, apierr.Newf(apierr.InvalidRequest, "%s must be a number of seconds from 0 to %d", name, maxMediaSeconds)
	}
	return int64(math.Round(sec * 1000)), nil
}

// handleGetMediaResume answers the place the signed-in user stopped at in a file the user reaches.
func (s *Server) handleGetMediaResume(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := resumeOwnCaller(p); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	if _, _, err := nodeAccessWith(r.Context(), s.store, p, nodeID); err != nil {
		return err
	}
	got, found, err := s.store.MediaResumeOf(r.Context(), p.UserID, nodeID)
	if err != nil {
		return err
	}
	if !found {
		writeJSON(w, http.StatusOK, resumeAnswer{})
		return nil
	}
	at := got.UpdatedAt
	writeJSON(w, http.StatusOK, resumeAnswer{
		PositionSeconds: float64(got.PositionMS) / 1000,
		DurationSeconds: float64(got.DurationMS) / 1000,
		UpdatedAt:       &at,
	})
	return nil
}

// handlePutMediaResume stores the place the signed-in user stopped at in a file the user reaches. The position may not be
// past the duration when the duration is known (a duration of 0 is unknown, as a stream has none). A file is the only
// kind that has a place.
func (s *Server) handlePutMediaResume(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := resumeOwnCaller(p); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	// Charged before the body is read, so a flood of bad requests spends the same budget as good ones.
	if err := s.rateLimit(resumeWriteRate, p.UserID); err != nil {
		return err
	}
	var in resumeBody
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	pos, err := toMS("position_seconds", in.PositionSeconds)
	if err != nil {
		return err
	}
	dur, err := toMS("duration_seconds", in.DurationSeconds)
	if err != nil {
		return err
	}
	if dur > 0 && pos > dur {
		return apierr.New(apierr.InvalidRequest, "position_seconds may not be past duration_seconds")
	}
	ctx := r.Context()
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		// The reach is checked inside the write, so a grant revoked during the request stops the place.
		n, _, err := nodeAccessWith(ctx, tx, p, nodeID)
		if err != nil {
			return err
		}
		if n.Kind != store.KindFile {
			return nodeNotFound()
		}
		return tx.SetMediaResume(ctx, n.AccountID, p.UserID, nodeID, store.MediaResume{PositionMS: pos, DurationMS: dur}, store.Now())
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
