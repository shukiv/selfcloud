// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

func newEndpointRow(t testing.TB, s *Store, accountID, url string, events ...string) *WebhookEndpoint {
	t.Helper()
	now := Now()
	e := &WebhookEndpoint{ID: id.New("whk"), AccountID: accountID, URL: url, Events: events, State: WebhookActive,
		SecretEnc: []byte("sealed-bytes-for-test"), CreatedBy: "key_test", CreatedAt: now, UpdatedAt: now}
	if err := s.Write(context.Background(), func(tx *Tx) error {
		return tx.InsertWebhookEndpoint(context.Background(), e)
	}); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestWebhookEndpointRoundTripWithEvents(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	a := newAccountRow(t, s, "A")
	e := newEndpointRow(t, s, a, "https://hooks.example.com/in", "file.created", "user.created")

	got, err := s.WebhookEndpoint(ctx, a, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != e.URL || got.State != WebhookActive || !slices.Equal(got.Events, []string{"file.created", "user.created"}) {
		t.Fatalf("read back %+v", got)
	}
	if string(got.SecretEnc) != "sealed-bytes-for-test" {
		t.Fatalf("secret blob = %q", got.SecretEnc)
	}

	list, next, err := s.WebhookEndpoints(ctx, a, Page{Limit: 10})
	if err != nil || next != "" || len(list) != 1 || list[0].ID != e.ID {
		t.Fatalf("list = %v, next %q, err %v", list, next, err)
	}
	if !slices.Equal(list[0].Events, []string{"file.created", "user.created"}) {
		t.Fatalf("list events = %v", list[0].Events)
	}
}

// Another account's endpoints are not found, listed, or changed: every query names the account.
func TestWebhookEndpointIsOnlyReachableByItsAccount(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	e := newEndpointRow(t, s, a, "https://a.example.com/hook", "file.created")

	if _, err := s.WebhookEndpoint(ctx, b, e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read by B: err = %v, want ErrNotFound", err)
	}
	list, _, err := s.WebhookEndpoints(ctx, b, Page{Limit: 10})
	if err != nil || len(list) != 0 {
		t.Fatalf("B lists %d endpoints of A (err %v)", len(list), err)
	}
	err = s.Write(ctx, func(tx *Tx) error {
		if err := tx.DeleteWebhookEndpoint(ctx, b, e.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete by B: err = %v, want ErrNotFound", err)
		}
		e2 := *e
		e2.AccountID = b
		if err := tx.UpdateWebhookEndpoint(ctx, &e2); !errors.Is(err, ErrNotFound) {
			t.Errorf("update as B: err = %v, want ErrNotFound", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WebhookEndpoint(ctx, a, e.ID); err != nil {
		t.Fatalf("A lost its endpoint: %v", err)
	}
}

func TestWebhookEndpointUpdateReplacesEvents(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	a := newAccountRow(t, s, "A")
	e := newEndpointRow(t, s, a, "https://a.example.com/hook", "file.created", "file.deleted")
	err := s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.WebhookEndpoint(ctx, a, e.ID)
		if err != nil {
			return err
		}
		cur.Events = []string{"file.moved"}
		cur.State = WebhookPaused
		cur.UpdatedAt = Now()
		return tx.UpdateWebhookEndpoint(ctx, cur)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.WebhookEndpoint(ctx, a, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Events, []string{"file.moved"}) || got.State != WebhookPaused {
		t.Fatalf("after update: events %v, state %s", got.Events, got.State)
	}
}

func TestWebhookDeleteEndpointRemovesEvents(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	a := newAccountRow(t, s, "A")
	e := newEndpointRow(t, s, a, "https://a.example.com/hook", "file.created")
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteWebhookEndpoint(ctx, a, e.ID) }); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_endpoint_events WHERE endpoint_id = ?", e.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d event rows survive the endpoint", n)
	}
}

// Deleting the account removes its endpoints and their events.
func TestWebhookAccountDeleteCascades(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	a := newAccountRow(t, s, "A")
	newEndpointRow(t, s, a, "https://a.example.com/hook", "file.created")
	if _, err := s.w.ExecContext(ctx, "DELETE FROM accounts WHERE id = ?", a); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"webhook_endpoints", "webhook_endpoint_events"} {
		var n int
		if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s: %d rows survive the account", table, n)
		}
	}
}

// The database itself refuses what the code also refuses: an unknown event, a state that needs a reason
// it lacks, and an event row naming an endpoint of another account.
func TestWebhookSchemaRefusesBadRows(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	e := newEndpointRow(t, s, a, "https://a.example.com/hook", "file.created")
	for name, q := range map[string]string{
		"unknown event": "INSERT INTO webhook_endpoint_events (endpoint_id, account_id, event) VALUES (?, ?, 'user.deleted')",
		"other account": "INSERT INTO webhook_endpoint_events (endpoint_id, account_id, event) VALUES (?, ?, 'file.moved')",
	} {
		acc := a
		if name == "other account" {
			acc = b
		}
		if _, err := s.w.ExecContext(ctx, q, e.ID, acc); err == nil {
			t.Errorf("%s: the row was accepted", name)
		}
	}
	if _, err := s.w.ExecContext(ctx,
		`UPDATE webhook_endpoints SET state = 'disabled' WHERE id = ?`, e.ID); err == nil {
		t.Error("disabled without a reason was accepted")
	}
}
