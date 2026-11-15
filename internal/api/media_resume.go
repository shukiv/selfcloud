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

// playedItem is a media file in the played list, with the place the caller stopped at.
type playedItem struct {
	nodeJSON
	PositionSeconds float64   `json:"position_seconds"`
	DurationSeconds float64   `json:"duration_seconds"`
	PlayedAt        time.Time `json:"played_at"`
	// Place is the name of the item's parent, when the caller reaches the parent (placeNames).
	Place string `json:"place,omitempty"`
}

// playedFilterParams reads the played list's filter through the list filter, with only video and audio as types: a
// place sits in a media file, so any other type value, another valid group included, is invalid_request naming type.
func playedFilterParams(r *http.Request) (store.ListFilter, error) {
	f, err := listFilterParams(r)
	if err != nil {
		return store.ListFilter{}, err
	}
	if f.Kind != "" || (f.Group != "" && f.Group != "video" && f.Group != "audio") {
		return store.ListFilter{}, apierr.New(apierr.InvalidRequest, "type must be video or audio")
	}
	return f, nil
}

// playedUnfinishedParam reads ?unfinished=: absent is false, true is true, and any other value is invalid_request.
func playedUnfinishedParam(r *http.Request) (bool, error) {
	vals := r.URL.Query()["unfinished"]
	if len(vals) == 0 {
		return false, nil
	}
	if len(vals) > 1 || vals[0] != "true" {
		return false, apierr.New(apierr.InvalidRequest, "unfinished must be true or absent")
	}
	return true, nil
}

// handleListPlayed lists the media files the signed-in user stopped at, the latest place first. As for the recent list,
// a place whose node the caller no longer reaches or that is in the trash is kept in the database and not listed, so a
// page can come back shorter.
func (s *Server) handleListPlayed(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := resumeOwnCaller(p); err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	filter, err := playedFilterParams(r)
	if err != nil {
		return err
	}
	unfinished, err := playedUnfinishedParam(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	rows, err := s.store.PlayedPage(ctx, p.UserID, filter, unfinished, page)
	if err != nil {
		return storeErr(err)
	}
	ids := make([]string, len(rows))
	for i, rw := range rows {
		ids[i] = rw.NodeID
	}
	reach, err := reachRoles(ctx, s.store, p, ids)
	if err != nil {
		return err
	}
	nodes, err := s.store.NodesByID(ctx, p.AccountID, ids)
	if err != nil {
		return err
	}
	var items []playedItem
	for _, rw := range rows {
		n, role := nodes[rw.NodeID], reach[rw.NodeID]
		if n == nil || role == "" {
			continue
		}
		j := toNodeJSON(n)
		j.Role = role
		items = append(items, playedItem{nodeJSON: j,
			PositionSeconds: float64(rw.PositionMS) / 1000,
			DurationSeconds: float64(rw.DurationMS) / 1000,
			PlayedAt:        rw.UpdatedAt})
	}
	kept := make([]*nodeJSON, len(items))
	for i := range items {
		kept[i] = &items[i].nodeJSON
	}
	places, err := s.placesOfItems(ctx, p, kept)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].Place = places[i]
	}
	if err := s.withTargets(ctx, p, kept...); err != nil {
		return err
	}
	if err := s.withStarred(r, p.UserID, kept); err != nil {
		return err
	}
	out := listResponse[playedItem]{Items: []playedItem{}}
	out.Items = append(out.Items, items...)
	if len(rows) == page.Limit {
		out.NextCursor = store.PlayedCursorOf(rows[len(rows)-1], filter, unfinished)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
