// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

type jobJSON struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Target string `json:"target"` // what the job acts on: for node.copy, the new copy's root
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	// error_code and error_reason name why a zip job failed, for the interface to translate; error is the server's text.
	ErrorCode   string     `json:"error_code,omitempty"`
	ErrorReason string     `json:"error_reason,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

func toJobJSON(j *store.Job) jobJSON {
	return jobJSON{ID: j.ID, Kind: j.Kind, Target: j.Target, Status: j.Status, Error: j.Error, CreatedAt: j.CreatedAt,
		UpdatedAt: j.UpdatedAt, FinishedAt: j.FinishedAt}
}

// queuedBy reports whether p is the caller a job runs for, as its payload records it: the same
// account, kind and ID. A member, or a browser with a delegated token, can then follow a copy it
// started without managing the account. A token sees nothing outside its folders, so a token may
// read only a job queued under a token whose folders are all in its own list: the copy's source
// and destination were in those folders.
//
// An all-reachable token names no folders, so the subset rule cannot be checked for it here. The
// rule is therefore strict on both sides:
//   - a job queued by an all-reachable token is read only by an all-reachable token of the same
//     user with the same node scope (an exact match, so a confined token does not read an
//     unconfined job, and the reverse);
//   - a job queued by a folder-list token is read only by a folder-list token, under the subset and
//     node rules above; an all-reachable token does not read it, because that would need the
//     user's reach, which this check does not compute.
func queuedBy(job *store.Job, p *auth.Principal) bool {
	if p == nil || job.Payload == "" || p.AccountID != job.AccountID {
		return false
	}
	var pl struct {
		Actor *actorPayload `json:"actor"`
	}
	if json.Unmarshal([]byte(job.Payload), &pl) != nil || pl.Actor == nil {
		return false
	}
	if pl.Actor.Kind != string(p.Kind) || pl.Actor.ID != p.ID {
		return false
	}
	if p.Token == nil {
		return true
	}
	if pl.Actor.Token == nil {
		return false
	}
	if p.Token.All || pl.Actor.Token.All {
		return p.Token.All && pl.Actor.Token.All && p.Token.Node == pl.Actor.Token.Node
	}
	for _, f := range pl.Actor.Token.Folders {
		if !slices.Contains(p.Token.Folders, f) {
			return false
		}
	}
	// A token confined to a directory reads only the jobs queued under the same confinement.
	if p.Token.Node != "" && p.Token.Node != pl.Actor.Token.Node {
		return false
	}
	return true
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) error {
	job, err := s.store.Job(r.Context(), r.PathValue("job"))
	if errors.Is(err, store.ErrNotFound) {
		return notFound("job")
	}
	if err != nil {
		return err
	}
	if p := principalOf(r); !p.ManagesAccount(job.AccountID) && !queuedBy(job, p) {
		return notFound("job")
	}
	out := toJobJSON(job)
	if job.Kind == jobZip && job.Status == store.JobFailed {
		z, err := s.store.Zip(r.Context(), job.AccountID, job.Target)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if z != nil {
			out.ErrorCode, out.ErrorReason = z.ErrorCode, z.ErrorReason
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
