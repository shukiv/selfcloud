// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func newAccount(id, ext string, created time.Time) *Account {
	return &Account{ID: id, ExternalID: ext, Name: "Name " + id, Status: AccountActive,
		CreatedAt: created, UpdatedAt: created}
}

func TestAccounts(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := Now()
	err := s.Write(ctx, func(tx *Tx) error {
		for i := range 5 {
			ext := ""
			if i == 2 {
				ext = "client-2"
			}
			if err := tx.InsertAccount(ctx, newAccount(fmt.Sprintf("acc_%d", i), ext, base.Add(time.Duration(i)*time.Second))); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = s.Write(ctx, func(tx *Tx) error { return tx.InsertAccount(ctx, newAccount("acc_x", "client-2", base)) })
	if !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate external_id: %v, want ErrConflict", err)
	}

	got, err := s.Account(ctx, "acc_2")
	if err != nil || got.ExternalID != "client-2" || !got.CreatedAt.Equal(base.Add(2*time.Second)) {
		t.Errorf("Account = %+v, %v", got, err)
	}
	if _, err := s.Account(ctx, "acc_none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing account: %v", err)
	}

	var ids []string
	cursor := ""
	for {
		page, next, err := s.Accounts(ctx, "", Page{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range page {
			ids = append(ids, a.ID)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if fmt.Sprint(ids) != "[acc_0 acc_1 acc_2 acc_3 acc_4]" {
		t.Errorf("paged listing = %v", ids)
	}

	only, _, err := s.Accounts(ctx, "client-2", Page{Limit: 10})
	if err != nil || len(only) != 1 || only[0].ID != "acc_2" {
		t.Errorf("filter by external_id = %v, %v", only, err)
	}
	if _, _, err := s.Accounts(ctx, "", Page{Limit: 2, Cursor: "!!"}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("bad cursor: %v", err)
	}
}

func TestDeleteAccountCascadesToKeys(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := Now()
	err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertAccount(ctx, newAccount("acc_1", "", now)); err != nil {
			return err
		}
		return tx.InsertAPIKey(ctx, &APIKey{ID: "key_1", AccountID: "acc_1", Name: "k", Prefix: "p",
			Hash: []byte("h"), CreatedAt: now})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteAccount(ctx, "acc_1") }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.APIKeyByHash(ctx, []byte("h")); !errors.Is(err, ErrNotFound) {
		t.Errorf("key survived its account: %v", err)
	}
}

func TestAPIKeys(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := Now()
	err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertAPIKey(ctx, &APIKey{ID: "key_admin", Name: "admin", Prefix: "p", Hash: []byte("a"), CreatedAt: now})
	})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := s.APIKeys(ctx, "")
	if err != nil || len(keys) != 1 || keys[0].AccountID != "" {
		t.Fatalf("admin keys = %v, %v", keys, err)
	}
	revoke := func(acc, id string, at time.Time) error {
		return s.Write(ctx, func(tx *Tx) error { return tx.RevokeAPIKey(ctx, acc, id, at) })
	}
	if err := revoke("acc_other", "key_admin", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoking an admin key through an account: %v", err)
	}
	if err := revoke("", "key_admin", now); err != nil {
		t.Fatal(err)
	}
	if err := revoke("", "key_admin", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	k, _ := s.APIKeyByHash(ctx, []byte("a"))
	if k.RevokedAt == nil || !k.RevokedAt.Equal(now) {
		t.Errorf("revoked_at = %v, want the first revocation %v", k.RevokedAt, now)
	}
}

func TestAuditLog(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	err := s.Write(ctx, func(tx *Tx) error {
		for i := range 5 {
			e := &AuditEntry{Time: Now(), AccountID: "acc_1", ActorKind: "system", ActorID: "test",
				Action: fmt.Sprintf("a.%d", i), Details: map[string]any{"n": i}}
			if err := tx.InsertAudit(ctx, e); err != nil {
				return err
			}
		}
		return tx.InsertAudit(ctx, &AuditEntry{Time: Now(), AccountID: "acc_2", ActorKind: "system", ActorID: "t", Action: "other"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	cursor := ""
	for {
		page, next, err := s.AuditLog(ctx, "acc_1", "", true, Page{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page {
			actions = append(actions, e.Action)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if fmt.Sprint(actions) != "[a.4 a.3 a.2 a.1 a.0]" {
		t.Errorf("audit pages = %v", actions)
	}
}

func TestJobs(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.ClaimJob(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim on an empty queue: %v", err)
	}
	now := Now()
	err := s.Write(ctx, func(tx *Tx) error {
		for i, id := range []string{"job_1", "job_2"} {
			at := now.Add(time.Duration(i) * time.Millisecond)
			if err := tx.InsertJob(ctx, &Job{ID: id, AccountID: "acc_1", Kind: "k", Status: JobQueued,
				CreatedAt: at, UpdatedAt: at}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.ClaimJob(ctx)
	if err != nil || j.ID != "job_1" || j.Status != JobRunning {
		t.Fatalf("claim = %+v, %v", j, err)
	}
	if n, err := s.RequeueRunningJobs(ctx); err != nil || n != 1 {
		t.Fatalf("requeue = %d, %v", n, err)
	}
	j, _ = s.ClaimJob(ctx)
	if j.ID != "job_1" {
		t.Fatalf("requeued job not claimed first: %s", j.ID)
	}
	if err := s.FinishJob(ctx, j.ID, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Job(ctx, "job_1")
	if got.Status != JobFailed || got.Error != "boom" || got.FinishedAt == nil {
		t.Errorf("finished job = %+v", got)
	}
}
