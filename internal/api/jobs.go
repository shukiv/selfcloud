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
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Target     string     `json:"target"` // what the job acts on: for node.copy, the new copy's root
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at"`
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
	writeJSON(w, http.StatusOK, toJobJSON(job))
	return nil
}
