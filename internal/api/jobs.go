package api

import (
	"errors"
	"net/http"
	"time"

	"selfcloud/internal/store"
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

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) error {
	job, err := s.store.Job(r.Context(), r.PathValue("job"))
	if errors.Is(err, store.ErrNotFound) {
		return notFound("job")
	}
	if err != nil {
		return err
	}
	if !principalOf(r).ManagesAccount(job.AccountID) {
		return notFound("job")
	}
	writeJSON(w, http.StatusOK, toJobJSON(job))
	return nil
}
