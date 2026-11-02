// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Audio tags (design section 14, slice x3, decision 56): the title, artist, album and duration of an audio file, read on the
// server by the derived-files pipeline, the same workers and queue as the thumbnails. An audio file's cover is its thumbnail
// (thumbnails.go); this file serves the text.
//
// Access is the same as for a thumbnail: nodeAccess runs on every call, so a caller who cannot read the file gets the same
// answer as for a file that is not there. A tags read is viewing, not downloading, so the no-download rule does not refuse it.
// A link visitor is refused by the route (link_route), as for a thumbnail; the listing gives a link visitor no tag values (withThumbnails, decision 24).
//
// The answer's state is "ready" with the values, "pending" while the read is queued (202 on the single route), or "none" when
// the file has no readable tags, or the reader refused it. Values are text: the client shows them as text, never as markup.

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/derived"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/thumb"
)

// tagsBatch is how many node IDs one batch request may name.
const tagsBatch = 100

// tagsJSON is an audio file's tags as the API shows them. Empty values are left out.
type tagsJSON struct {
	Title      string `json:"title,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// tagsAnswer is one answer: the state, and the values when the state is ready.
type tagsAnswer struct {
	State string `json:"state"`
	tagsJSON
}

// tagsEligible reports whether a node is an audio file whose tags are read. The check fails closed: a folder, a shortcut
// and a file of another type have no tags. Access is nodeAccess, which runs first.
func tagsEligible(kind, mimeType string) bool {
	return kind == store.KindFile && thumb.AudioType(mimeType)
}

// toTagsJSON copies the stored values into the API's shape.
func toTagsJSON(t store.AudioTags) tagsJSON {
	return tagsJSON{Title: t.Title, Artist: t.Artist, Album: t.Album, DurationMS: t.DurationMS}
}

// handleGetTags answers the tags of an audio file the caller may read: 200 with the state, 202 while the read is queued,
// 404 when the node is not an audio file, and 429 when the queue or the caller's budget is full. HEAD answers what a GET
// would find and queues nothing.
func (s *Server) handleGetTags(w http.ResponseWriter, r *http.Request) error {
	if !thumb.Supported {
		return apierr.New(apierr.Unavailable, "audio tags need the Linux server: the reader's limits are enforced there only")
	}
	n, _, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	if !tagsEligible(n.Kind, n.MimeType) {
		return notFound("tags")
	}
	if r.Method == http.MethodHead {
		ans, err := s.derivedPool.PeekTags(r.Context(), n.AccountID, n.BlobID)
		if err != nil {
			return err
		}
		if ans.State == "" {
			return notFound("tags")
		}
		return s.answerTags(w, r, n, ans)
	}
	p := principalOf(r)
	key := string(p.Kind) + ":" + p.ID
	ans, err := s.derivedPool.RequestTags(r.Context(), n.AccountID, n.BlobID,
		func() error { return s.rateLimit("tags.enqueue", key) })
	switch {
	case errors.Is(err, derived.ErrBusy):
		w.Header().Set("Retry-After", "5")
		return apierr.New(apierr.RateLimited, "the audio tags are busy; ask again shortly")
	case errors.Is(err, derived.ErrNoBlob):
		return notFound("tags")
	case err != nil:
		return err
	}
	return s.answerTags(w, r, n, ans)
}

// answerTags writes one answer of the single route.
func (s *Server) answerTags(w http.ResponseWriter, r *http.Request, n *store.Node, ans derived.Answer) error {
	switch ans.State {
	case store.DerivedReady:
		vals, err := s.store.AudioTagsFor(r.Context(), n.AccountID, []string{n.ID}, s.derivedPool.TagsConverter())
		if err != nil {
			return err
		}
		body := tagsAnswer{State: "none"}
		if t, ok := vals[n.ID]; ok {
			body = tagsAnswer{State: "ready", tagsJSON: toTagsJSON(t)}
		}
		w.Header().Set("Cache-Control", "private, no-cache")
		writeJSON(w, http.StatusOK, body)
		return nil
	case store.DerivedFailed:
		w.Header().Set("Cache-Control", "private, no-cache")
		writeJSON(w, http.StatusOK, tagsAnswer{State: "none"})
		return nil
	}
	w.Header().Set("Retry-After", "2")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, tagsAnswer{State: "pending"})
	return nil
}

// handleTagsBatch answers the tags of up to tagsBatch nodes in one request: GET /api/v1/media/tags?ids=nod_a,nod_b. A node the
// caller cannot read, one that is not an audio file, and one that does not exist are all left out of the answer, which is the
// same for each. A read not yet made is queued, so the answer is "pending" for it; the client asks again for those.
func (s *Server) handleTagsBatch(w http.ResponseWriter, r *http.Request) error {
	if !thumb.Supported {
		return apierr.New(apierr.Unavailable, "audio tags need the Linux server: the reader's limits are enforced there only")
	}
	ids, err := parseTagIDs(r.URL.Query().Get("ids"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	key := string(p.Kind) + ":" + p.ID
	// Each request walks up to tagsBatch nodes for access, which the read budget does not count. This limit counts the requests
	// themselves; a refused request is 429 for the whole request, and the web client backs off (decision 56).
	if err := s.rateLimit("tags.batch", key); err != nil {
		return err
	}
	// limited is set when the caller's budget of new reads runs out. The items it leaves are answered "pending" each, so a
	// long list is answered in part and the rest are asked for again; the batch itself is not refused (decision 56).
	limited := false
	admit := func() error {
		if err := s.rateLimit("tags.enqueue", key); err != nil {
			limited = true
			return err
		}
		return nil
	}
	items := map[string]tagsAnswer{}
	// The ready reads are looked up per account, so a listing of one account's files costs one query.
	ready := map[string][]string{}
	for _, nid := range ids {
		n, _, err := nodeAccessByID(s, r, nid)
		if err != nil || !tagsEligible(n.Kind, n.MimeType) {
			continue
		}
		ans, err := s.derivedPool.RequestTags(ctx, n.AccountID, n.BlobID, admit)
		switch {
		case err != nil && limited, errors.Is(err, derived.ErrBusy):
			items[nid] = tagsAnswer{State: "pending"}
		case errors.Is(err, derived.ErrNoBlob):
			continue
		case err != nil:
			return err
		case ans.State == store.DerivedReady:
			ready[n.AccountID] = append(ready[n.AccountID], nid)
		case ans.State == store.DerivedFailed:
			items[nid] = tagsAnswer{State: "none"}
		default:
			items[nid] = tagsAnswer{State: "pending"}
		}
	}
	for acc, nids := range ready {
		vals, err := s.store.AudioTagsFor(ctx, acc, nids, s.derivedPool.TagsConverter())
		if err != nil {
			return err
		}
		for _, nid := range nids {
			if t, ok := vals[nid]; ok {
				items[nid] = tagsAnswer{State: "ready", tagsJSON: toTagsJSON(t)}
			} else {
				items[nid] = tagsAnswer{State: "none"}
			}
		}
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

// nodeAccessByID is Server.nodeAccess for a node named by ID rather than by path, as the batch route needs.
func nodeAccessByID(s *Server, r *http.Request, nodeID string) (*store.Node, string, error) {
	if !id.Valid("nod", nodeID) {
		return nil, "", nodeNotFound()
	}
	return nodeAccessWith(r.Context(), s.store, principalOf(r), nodeID)
}

// parseTagIDs reads the ids of a batch: one to tagsBatch node IDs, separated by commas, each valid and each named once.
func parseTagIDs(raw string) ([]string, error) {
	if raw == "" {
		return nil, apierr.New(apierr.InvalidRequest, "ids must name one to 100 nodes")
	}
	parts := strings.Split(raw, ",")
	if len(parts) > tagsBatch {
		return nil, apierr.New(apierr.InvalidRequest, "ids must name one to 100 nodes")
	}
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		if !id.Valid("nod", p) || seen[p] {
			return nil, apierr.New(apierr.InvalidRequest, "ids must be distinct node IDs")
		}
		seen[p] = true
	}
	return parts, nil
}

// withTags sets the tags of the audio files among items that have been read. It is one query for each batch of nodes, as
// withThumbnails is. A link visitor gets none (decision 24).
func (s *Server) withTags(ctx context.Context, p *auth.Principal, accountID string, items ...*nodeJSON) error {
	if p == nil || p.Kind == auth.KindShareLink {
		return nil
	}
	var ids []string
	byID := map[string]*nodeJSON{}
	for _, it := range items {
		if tagsEligible(it.Kind, it.MimeType) {
			ids = append(ids, it.ID)
			byID[it.ID] = it
		}
	}
	for start := 0; start < len(ids); start += thumbBatch {
		end := min(start+thumbBatch, len(ids))
		vals, err := s.store.AudioTagsFor(ctx, accountID, ids[start:end], s.derivedPool.TagsConverter())
		if err != nil {
			return err
		}
		for nid, t := range vals {
			tj := toTagsJSON(t)
			byID[nid].Tags = &tj
		}
	}
	return nil
}
