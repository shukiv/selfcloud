// SPDX-License-Identifier: AGPL-3.0-or-later

// Package jobs runs background work queued in the store, one job at a time.
//
// A job is queued in the same transaction as the change that needs it, so it is never lost.
// A job left running when the process stops is run again on the next start, so every handler
// must be safe to repeat.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
)

// pollInterval is how often the queue is checked when nothing wakes the runner.
const pollInterval = 30 * time.Second

// Handler does one job.
type Handler func(ctx context.Context, job *store.Job) error

// Runner runs queued jobs.
type Runner struct {
	store    *store.Store
	log      *slog.Logger
	handlers map[string]Handler
	wake     chan struct{}
}

// New returns a Runner with no handlers.
func New(st *store.Store, log *slog.Logger) *Runner {
	return &Runner{store: st, log: log, handlers: map[string]Handler{}, wake: make(chan struct{}, 1)}
}

// Handle registers the handler for a kind of job. Call it before Run.
func (r *Runner) Handle(kind string, h Handler) { r.handlers[kind] = h }

// Wake tells the runner a job was queued.
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run requeues jobs a previous process left running, then runs jobs until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	if n, err := r.store.RequeueRunningJobs(ctx); err != nil {
		r.log.Error("cannot requeue interrupted jobs", "error", err)
	} else if n > 0 {
		r.log.Info("requeued interrupted jobs", "count", n)
	}
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		if err := r.RunPending(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("job queue", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-t.C:
		}
	}
}

// RunPending runs queued jobs until the queue is empty or ctx is cancelled.
func (r *Runner) RunPending(ctx context.Context) error {
	for ctx.Err() == nil {
		job, err := r.store.ClaimJob(ctx)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		jobErr := r.run(ctx, job)
		if ctx.Err() != nil {
			// Interrupted by shutdown: leave it running, so the next start runs it again.
			return ctx.Err()
		}
		if jobErr != nil {
			r.log.Error("job failed", "job", job.ID, "kind", job.Kind, "error", jobErr)
		} else {
			r.log.Info("job done", "job", job.ID, "kind", job.Kind)
		}
		if err := r.store.FinishJob(context.WithoutCancel(ctx), job.ID, jobErr); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (r *Runner) run(ctx context.Context, job *store.Job) (err error) {
	h, ok := r.handlers[job.Kind]
	if !ok {
		return fmt.Errorf("no handler for job kind %q", job.Kind)
	}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return h(ctx, job)
}
