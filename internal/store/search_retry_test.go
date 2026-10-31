// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// TestRetriedJobWaitsThenRunsOrIsWoken: a job put back for a retry is not claimed before it is due, and queued search work
// brings it forward (WakeJobs), so a backlog does not wait out a long retry for new work.
func TestRetriedJobWaitsThenRuns(t *testing.T) {
	s, _ := searchFixture(t)
	ctx := context.Background()
	job := &Job{ID: id.New("job"), Kind: SearchJobKind, Status: JobQueued, CreatedAt: Now(), UpdatedAt: Now()}
	write(t, s, func(tx *Tx) error { return tx.InsertJob(ctx, job) })
	claimed, err := s.ClaimJob(ctx)
	if err != nil || claimed.ID != job.ID {
		t.Fatalf("claim = %v, %v; want the job", claimed, err)
	}
	if err := s.RetryJob(ctx, job.ID, time.Hour, "disk busy"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimJob(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim during the retry wait = %v; want nothing due", err)
	}
	// New search work arrives: the waiting job is due at once.
	write(t, s, func(tx *Tx) error { return tx.WakeJobs(ctx, SearchJobKind) })
	again, err := s.ClaimJob(ctx)
	if err != nil || again.ID != job.ID {
		t.Fatalf("claim after the wake = %v, %v; want the retried job", again, err)
	}
}
