// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
)

// subtitleFormats are the subtitle files a video may have beside it, in the order they are offered.
var subtitleFormats = []string{"vtt", "srt"}

// subtitleJSON is one subtitle file that sits beside a file: the same name without the extension, and .vtt or .srt.
type subtitleJSON struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Format string `json:"format"`
}

// subtitleStem is the name of a file without its last extension. A name with no dot is its own stem.
func subtitleStem(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[:i]
	}
	return name
}

// handleListSubtitles lists the subtitle files beside a file the caller reaches. A subtitle the caller cannot reach is not
// listed; a subtitle the server will not serve (a file that may not be downloaded) is listed here and refused by its own
// content route, so the client shows no track for it.
func (s *Server) handleListSubtitles(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	ctx := r.Context()
	n, _, err := nodeAccessWith(ctx, s.store, p, nodeID)
	if err != nil {
		return err
	}
	out := []subtitleJSON{}
	if n.Kind != store.KindFile || n.ParentID == "" {
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
		return nil
	}
	stem := subtitleStem(n.Name)
	for _, ext := range subtitleFormats {
		_, key, err := names.Clean(stem + "." + ext)
		if err != nil {
			continue
		}
		sib, err := s.store.Child(ctx, n.AccountID, n.ParentID, key)
		if err != nil || sib.Kind != store.KindFile || sib.TrashedAt != nil {
			continue
		}
		if _, _, err := nodeAccessWith(ctx, s.store, p, sib.ID); err != nil {
			continue
		}
		out = append(out, subtitleJSON{ID: sib.ID, Name: sib.Name, Format: ext})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
	return nil
}
