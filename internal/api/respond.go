// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// errorBody is the JSON shape of every error response.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code      apierr.Code    `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // the client may have gone; nothing useful to do
}

// writeError renders err as the JSON error envelope. Errors that are not *apierr.Error become
// a generic internal error; their cause is logged with the request ID, never sent.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	e := apierr.From(err)
	info := infoFrom(r.Context())
	reqID := ""
	if info != nil {
		reqID = info.id
		info.err = e
		if info.wroteHeader() {
			// Part of a response has gone out; a JSON error now would corrupt it.
			s.log.Error("error after the response started", "request_id", reqID, "error", e)
			return
		}
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(e.RetryAfter.Seconds()))))
	}
	w.Header().Set("Cache-Control", "no-store")
	// JSON errors are never rendered, but a browser that sniffed one, or was sent it as a
	// document, must not run anything in it.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	writeJSON(w, e.Code.Status(), errorBody{Error: errorDetail{
		Code:      e.Code,
		Message:   e.Message,
		Details:   e.Details,
		RequestID: reqID,
	}})
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) error {
	return apierr.New(apierr.NotFound, "no such endpoint")
}
