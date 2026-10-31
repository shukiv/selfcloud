// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// The delivery log and the two actions on a delivery or an endpoint (design section 9, slice j3): the log an account
// reads, the retry of a failed delivery, and the test event. An owner reads the log. Only the account key retries
// and tests, as it manages endpoints (decision 26). A token is refused on every route.

import (
	"errors"
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// recentDeliveries is how many deliveries an endpoint's detail shows.
const recentDeliveries = 5

// deliveryJSON is one row of an endpoint's delivery log. It holds the event's ID and type, never its payload, and the
// last error as a fixed token (never a URL or a body).
type deliveryJSON struct {
	ID            string     `json:"id"`
	EventID       string     `json:"event_id"`
	Type          string     `json:"type"`
	State         string     `json:"state"`
	Attempts      int        `json:"attempts"`
	LastCode      *int       `json:"last_code"`
	LastError     *string    `json:"last_error"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	CreatedAt     time.Time  `json:"created_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

// webhookDetail is one endpoint with its newest deliveries.
type webhookDetail struct {
	webhookJSON
	RecentDeliveries []deliveryJSON `json:"recent_deliveries"`
}

func deliveryOf(d *store.WebhookDelivery) deliveryJSON {
	out := deliveryJSON{ID: d.ID, EventID: d.EventID, Type: d.EventType, State: d.State, Attempts: d.Attempts,
		LastCode: d.LastCode, NextAttemptAt: d.NextAttemptAt, CreatedAt: d.CreatedAt, FinishedAt: d.FinishedAt}
	if d.LastError != "" {
		out.LastError = &d.LastError
	}
	return out
}

func deliveriesJSON(list []*store.WebhookDelivery) []deliveryJSON {
	out := make([]deliveryJSON, 0, len(list))
	for _, d := range list {
		out = append(out, deliveryOf(d))
	}
	return out
}

func (s *Server) handleListWebhookDeliveries(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.webhookReader(r)
	if err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	e, err := s.store.WebhookEndpoint(ctx, acc.ID, r.PathValue("hook"))
	if errors.Is(err, store.ErrNotFound) {
		return notFound("webhook")
	}
	if err != nil {
		return err
	}
	list, next, err := s.store.WebhookDeliveries(ctx, acc.ID, e.ID, page)
	if err != nil {
		return storeErr(err)
	}
	writeJSON(w, http.StatusOK, listResponse[deliveryJSON]{Items: deliveriesJSON(list), NextCursor: next})
	return nil
}

// handleRetryWebhookDelivery queues a failed delivery again. It is due at once, with a fresh 24-hour window. A
// delivery that is not failed is a conflict, and so is a queue at the pending limit or a disabled endpoint, whose
// owner must first change its URL or rotate its secret.
func (s *Server) handleRetryWebhookDelivery(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.webhookWriter(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	at := store.Now()
	s.atWrite()
	var d *store.WebhookDelivery
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := s.webhookOf(r, tx, acc.ID)
		if err != nil {
			return err
		}
		found, err := tx.WebhookDelivery(ctx, acc.ID, cur.ID, r.PathValue("dlv"))
		if errors.Is(err, store.ErrNotFound) {
			return notFound("delivery")
		}
		if err != nil {
			return err
		}
		if cur.State == store.WebhookDisabled {
			return apierr.New(apierr.Conflict,
				"a disabled webhook has no delivery to retry; change its URL or rotate its secret first").With("reason", "state")
		}
		if found.State != "failed" {
			return apierr.New(apierr.Conflict, "only a failed delivery is retried").With("reason", "state")
		}
		pending, err := tx.WebhookPending(ctx, acc.ID, cur.ID)
		if err != nil {
			return err
		}
		if pending >= store.WebhookPendingLimit {
			return apierr.New(apierr.Conflict, "the webhook's queue is full").With("reason", "limit")
		}
		if err := tx.RequeueWebhookDelivery(ctx, acc.ID, cur.ID, found.ID, at); err != nil {
			return err
		}
		if err := tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "webhook.retry", found.ID,
			map[string]any{"url": urlOrigin(cur.URL)})); err != nil {
			return err
		}
		found.State = "pending"
		found.NextAttemptAt = at
		found.FinishedAt = nil
		d = found
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, deliveryOf(d))
	return nil
}

// handleTestWebhook queues a webhook.test event for one endpoint, whatever events it subscribes to. The endpoint must
// be active: a paused or disabled one would hold the test until it is switched on.
func (s *Server) handleTestWebhook(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.webhookWriter(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	at := store.Now()
	s.atWrite()
	var eventID string
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := s.webhookOf(r, tx, acc.ID)
		if err != nil {
			return err
		}
		if cur.State != store.WebhookActive {
			return apierr.New(apierr.Conflict, "only an active webhook gets a test event").With("reason", "state")
		}
		pending, err := tx.WebhookPending(ctx, acc.ID, cur.ID)
		if err != nil {
			return err
		}
		if pending >= store.WebhookPendingLimit {
			return apierr.New(apierr.Conflict, "the webhook's queue is full").With("reason", "limit")
		}
		if eventID, err = tx.EmitTestEvent(ctx, acc.ID, cur.ID, at); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "webhook.test", cur.ID,
			map[string]any{"url": urlOrigin(cur.URL)}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"event_id": eventID})
	return nil
}
