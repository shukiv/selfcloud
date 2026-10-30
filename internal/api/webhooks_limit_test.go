// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// decodeErrorDetails returns the details of an error answer.
func decodeErrorDetails(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
	return body.Error.Details
}

// fillEndpoints adds n endpoints to accountID directly through the store, so a limit test need not make
// them one request at a time.
func fillEndpoints(t *testing.T, fx *webhookFixture, accountID string, n int) {
	t.Helper()
	ctx := context.Background()
	for range n {
		now := store.Now()
		e := &store.WebhookEndpoint{ID: id.New("whk"), AccountID: accountID, URL: "https://hooks.example.com/fill",
			Events: []string{"file.created"}, State: store.WebhookActive, SecretEnc: []byte("filler"),
			CreatedBy: "key_test", CreatedAt: now, UpdatedAt: now}
		if err := fx.s.store.Write(ctx, func(tx *store.Tx) error { return tx.InsertWebhookEndpoint(ctx, e) }); err != nil {
			t.Fatal(err)
		}
	}
}

// TestWebhookEndpointLimitPerAccount: an account holds at most 100 endpoints. The 101st create is a conflict
// with reason limit. The limit is per account: another account can still create.
func TestWebhookEndpointLimitPerAccount(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	fillEndpoints(t, fx, fx.acc.ID, webhookEndpointLimit)
	rec := call(t, fx.s, "POST", fx.base(), fx.key, map[string]any{
		"url": "https://hooks.example.com/over", "events": []string{"file.created"}})
	wantError(t, rec, 409, apierr.Conflict)
	if got := decodeErrorDetails(t, rec)["reason"]; got != "limit" {
		t.Fatalf("reason = %v, want limit: %s", got, rec.Body)
	}

	admin := adminKey(t, fx.s)
	other, otherKey := newAccount(t, fx.s, admin, "Other")
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+other.ID+"/webhooks", otherKey, map[string]any{
		"url": "https://hooks.example.com/other", "events": []string{"file.created"}}), 201)
}

// TestWebhookEndpointLimitHoldsUnderConcurrentCreates: with one slot left, several creates at once admit
// exactly one. The count is read in the same write as the insert, so no two can both pass.
func TestWebhookEndpointLimitHoldsUnderConcurrentCreates(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	fillEndpoints(t, fx, fx.acc.ID, webhookEndpointLimit-1)
	const racers = 6
	codes := make(chan int, racers)
	var wg sync.WaitGroup
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := jsonRequest(t, "POST", fx.base(), map[string]any{
				"url": "https://hooks.example.com/race", "events": []string{"file.created"}})
			req.Header.Set("Authorization", "Bearer "+fx.key)
			rec := httptest.NewRecorder()
			fx.s.Handler().ServeHTTP(rec, req)
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)
	created, refused := 0, 0
	for c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			refused++
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if created != 1 || refused != racers-1 {
		t.Fatalf("created %d, refused %d; want 1 and %d", created, refused, racers-1)
	}
	list, _, err := fx.s.store.WebhookEndpoints(context.Background(), fx.acc.ID, store.Page{Limit: 500})
	if err != nil || len(list) != webhookEndpointLimit {
		t.Fatalf("the account holds %d endpoints (err %v), want %d", len(list), err, webhookEndpointLimit)
	}
}
