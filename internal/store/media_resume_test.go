// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"
)

// TestMediaResumeRoundTrip: a stored place is read back, a later write replaces it, and a file with none reads as not found.
func TestMediaResumeRoundTrip(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	if _, found, err := s.MediaResumeOf(ctx, "usr_b", "nod_n"); err != nil || found {
		t.Fatalf("no place stored: found = %v, err = %v; want not found", found, err)
	}
	at := time.Unix(80_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.SetMediaResume(ctx, "acc_n", "usr_b", "nod_n", MediaResume{PositionMS: 1500, DurationMS: 9000}, at)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.SetMediaResume(ctx, "acc_n", "usr_b", "nod_n", MediaResume{PositionMS: 4200, DurationMS: 9000}, at.Add(time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.MediaResumeOf(ctx, "usr_b", "nod_n")
	if err != nil || !found {
		t.Fatalf("read back: found = %v, err = %v", found, err)
	}
	if got.PositionMS != 4200 || got.DurationMS != 9000 || !got.UpdatedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("read back %+v, want the later write (4200 of 9000, a minute on)", got)
	}
}

// TestMediaResumeCascades: a place goes with its user, its file or its account, as the other per-user rows do.
func TestMediaResumeCascades(t *testing.T) {
	cases := map[string]string{
		"user":    "DELETE FROM users WHERE id = 'usr_b'",
		"node":    "DELETE FROM nodes WHERE id = 'nod_n'",
		"account": "DELETE FROM accounts WHERE id = 'acc_n'",
	}
	for name, del := range cases {
		t.Run(name, func(t *testing.T) {
			s := openTest(t)
			seedNotify(t, s)
			ctx := context.Background()
			if err := s.Write(ctx, func(tx *Tx) error {
				return tx.SetMediaResume(ctx, "acc_n", "usr_b", "nod_n", MediaResume{PositionMS: 1, DurationMS: 2}, time.Unix(1, 0))
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.w.ExecContext(ctx, del); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_resume").Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("%s delete left %d media_resume rows; want 0", name, n)
			}
		})
	}
}
