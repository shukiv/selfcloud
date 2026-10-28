// SPDX-License-Identifier: AGPL-3.0-or-later

package jobs

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
)

func TestRunPending(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "t.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	queue := func(id, kind string) {
		now := store.Now()
		err := st.Write(ctx, func(tx *store.Tx) error {
			return tx.InsertJob(ctx, &store.Job{ID: id, AccountID: "acc_1", Kind: kind,
				Status: store.JobQueued, CreatedAt: now, UpdatedAt: now})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	queue("job_ok", "ok")
	queue("job_fail", "fail")
	queue("job_panic", "panic")
	queue("job_unknown", "unknown")

	r := New(st, slog.New(slog.DiscardHandler))
	var ran []string
	r.Handle("ok", func(ctx context.Context, j *store.Job) error { ran = append(ran, j.ID); return nil })
	r.Handle("fail", func(ctx context.Context, j *store.Job) error { return errors.New("nope") })
	r.Handle("panic", func(ctx context.Context, j *store.Job) error { panic("boom") })

	if err := r.RunPending(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 {
		t.Errorf("ran = %v", ran)
	}
	for id, want := range map[string]string{
		"job_ok": store.JobSucceeded, "job_fail": store.JobFailed,
		"job_panic": store.JobFailed, "job_unknown": store.JobFailed,
	} {
		j, err := st.Job(ctx, id)
		if err != nil || j.Status != want {
			t.Errorf("%s: %+v, %v; want %s", id, j, err, want)
		}
	}
}
