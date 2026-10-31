// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"reflect"
	"testing"
)

// The review fixes of slice j4 (review-j4.md): crossings count used bytes and are checked once per transaction, an
// edit of a limit or percent clears the marks it affects, and a mark is written only for an event someone subscribes to.

// inTx runs fn in one write transaction of the fixture's store.
func (q quotaFixture) inTx(t *testing.T, fn func(tx *Tx) error) {
	t.Helper()
	if err := q.s.Write(context.Background(), fn); err != nil {
		t.Fatal(err)
	}
}

// setEvents replaces the event list of the fixture's endpoint.
func (q quotaFixture) setEvents(t *testing.T, events ...string) {
	t.Helper()
	e, err := q.s.WebhookEndpoint(context.Background(), q.acc, q.ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.Events = events
	q.inTx(t, func(tx *Tx) error { return tx.UpdateWebhookEndpoint(context.Background(), e) })
}

// wantEvents checks the event types of the account, in order.
func wantEvents(t *testing.T, s *Store, accountID string, want ...string) {
	t.Helper()
	got := eventTypes(t, s, accountID)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
}

// TestUploadCompletionFiresOnce: a reservation is not usage. Creating the upload fires nothing, and its completion, which
// releases the reservation and counts the bytes in one transaction, fires one event.
func TestUploadCompletionFiresOnce(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	ctx := context.Background()
	q.add(t, 500)
	q.inTx(t, func(tx *Tx) error { return tx.AdjustUsage(ctx, q.acc, q.folder, UsageChange{Reserved: 400}) })
	wantEvents(t, q.s, q.acc)
	q.inTx(t, func(tx *Tx) error {
		if err := tx.AdjustUsage(ctx, q.acc, q.folder, UsageChange{Reserved: -400}); err != nil {
			return err
		}
		return tx.AdjustUsage(ctx, q.acc, q.folder, UsageChange{Bytes: 400})
	})
	wantEvents(t, q.s, q.acc, "quota.warning")
}

// TestReserveAndCancelFiresNothing: a reservation that is taken and then released says nothing.
func TestReserveAndCancelFiresNothing(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	ctx := context.Background()
	q.add(t, 500)
	q.inTx(t, func(tx *Tx) error { return tx.AdjustUsage(ctx, q.acc, q.folder, UsageChange{Reserved: 400}) })
	q.inTx(t, func(tx *Tx) error { return tx.AdjustUsage(ctx, q.acc, q.folder, UsageChange{Reserved: -400}) })
	wantEvents(t, q.s, q.acc)
}

// TestDipAndReturnInOneTransactionFiresNothing: a transaction that drops usage below a level and returns it before it
// commits fires nothing, and leaves the mark where it was.
func TestDipAndReturnInOneTransactionFiresNothing(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	ctx := context.Background()
	q.add(t, 850)
	wantEvents(t, q.s, q.acc, "quota.warning")
	q.inTx(t, func(tx *Tx) error {
		if err := tx.AdjustUsage(ctx, q.acc, q.folder, UsageChange{Bytes: -200}); err != nil {
			return err
		}
		return tx.AdjustUsage(ctx, q.acc, q.folder, UsageChange{Bytes: 200})
	})
	wantEvents(t, q.s, q.acc, "quota.warning")
	if q.marks(t) != 1 {
		t.Fatalf("marks = %d after the dip and return, want 1", q.marks(t))
	}
}

// TestQuotaRaiseLetsTheNextCrossingSend: the review's sequence. The edit sends nothing, and the next change that leaves
// the account above the level announces it.
func TestQuotaRaiseLetsTheNextCrossingSend(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	q.add(t, 850)
	acc, err := q.s.Account(context.Background(), q.acc)
	if err != nil {
		t.Fatal(err)
	}
	acc.QuotaBytes = 10000
	acc.UpdatedAt = Now()
	q.inTx(t, func(tx *Tx) error { return tx.UpdateAccount(context.Background(), acc) })
	wantEvents(t, q.s, q.acc, "quota.warning")
	q.add(t, 7500) // 8350 of 10000: 83 percent, a crossing of the level again
	wantEvents(t, q.s, q.acc, "quota.warning", "quota.warning")
}

// TestUserLimitRaiseLetsTheNextCrossingSend: the same for a user's limit on its personal folder.
func TestUserLimitRaiseLetsTheNextCrossingSend(t *testing.T) {
	q := newQuotaFixture(t, 0, "quota.warning")
	ctx := context.Background()
	u := newUserRow(t, q.s, q.acc, "ann@example.com", "")
	pf := newPersonalRow(t, q.s, q.acc, u.ID)
	limit := int64(100)
	u.QuotaBytes = &limit
	q.inTx(t, func(tx *Tx) error { return tx.UpdateUser(ctx, u) })
	q.inTx(t, func(tx *Tx) error { return tx.AdjustUsage(ctx, q.acc, pf.ID, UsageChange{Bytes: 85}) })
	wantEvents(t, q.s, q.acc, "quota.warning")

	raised := int64(1000)
	u.QuotaBytes = &raised
	q.inTx(t, func(tx *Tx) error { return tx.UpdateUser(ctx, u) })
	wantEvents(t, q.s, q.acc, "quota.warning")
	q.inTx(t, func(tx *Tx) error { return tx.AdjustUsage(ctx, q.acc, pf.ID, UsageChange{Bytes: 715}) }) // 800 of 1000
	wantEvents(t, q.s, q.acc, "quota.warning", "quota.warning")
}

// TestUserDefaultLimitRaiseLetsTheNextCrossingSend: a user whose limit follows the account default crosses the warning
// level; raising the default clears that user's mark, so the next change above the new level announces it again.
func TestUserDefaultLimitRaiseLetsTheNextCrossingSend(t *testing.T) {
	q := newQuotaFixture(t, 0, "quota.warning")
	ctx := context.Background()
	u := newUserRow(t, q.s, q.acc, "ann@example.com", "")
	pf := newPersonalRow(t, q.s, q.acc, u.ID)
	q.inTx(t, func(tx *Tx) error { return tx.SetUserQuotaDefault(ctx, q.acc, 100, Now()) })
	q.inTx(t, func(tx *Tx) error { return tx.AdjustUsage(ctx, q.acc, pf.ID, UsageChange{Bytes: 85}) })
	wantEvents(t, q.s, q.acc, "quota.warning")

	q.inTx(t, func(tx *Tx) error { return tx.SetUserQuotaDefault(ctx, q.acc, 1000, Now()) })
	q.inTx(t, func(tx *Tx) error { return tx.AdjustUsage(ctx, q.acc, pf.ID, UsageChange{Bytes: 715}) }) // 800 of 1000
	wantEvents(t, q.s, q.acc, "quota.warning", "quota.warning")
}

// TestPercentRaiseLetsTheNextCrossingSend: a raised warning level clears the warning mark, so the next change above the
// new level announces it.
func TestPercentRaiseLetsTheNextCrossingSend(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	q.add(t, 850)
	q.inTx(t, func(tx *Tx) error {
		return tx.SetQuotaWarningPercent(context.Background(), q.acc, 90, Now())
	})
	wantEvents(t, q.s, q.acc, "quota.warning")
	q.add(t, 70) // 920 of 1000: 92 percent, past the new level
	wantEvents(t, q.s, q.acc, "quota.warning", "quota.warning")
}

// TestWarningMarkNeedsAWarningSubscriber: an endpoint that subscribes to quota.exceeded only leaves no warning mark, so
// when it later subscribes to quota.warning, the next change above the level announces it.
func TestWarningMarkNeedsAWarningSubscriber(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.exceeded")
	q.add(t, 850)
	wantEvents(t, q.s, q.acc)
	if q.marks(t) != 0 {
		t.Fatalf("a warning mark was written for an event nobody subscribes to: %d marks", q.marks(t))
	}
	q.setEvents(t, "quota.exceeded", "quota.warning")
	q.add(t, 10)
	wantEvents(t, q.s, q.acc, "quota.warning")
	q.add(t, 10)
	wantEvents(t, q.s, q.acc, "quota.warning")
}

// TestSubscriberAddedWhileOverFiresOnceNextChange: subscribing while the account is already over the level sends nothing
// for the past; the next change fires once, and the one after does not.
func TestSubscriberAddedWhileOverFiresOnceNextChange(t *testing.T) {
	q := newQuotaFixture(t, 1000)
	q.add(t, 850)
	q.ep = newEndpointState(t, q.s, q.acc, "https://hook.example.com/late", WebhookActive, "quota.warning")
	wantEvents(t, q.s, q.acc)
	q.add(t, 10)
	wantEvents(t, q.s, q.acc, "quota.warning")
	q.add(t, 10)
	wantEvents(t, q.s, q.acc, "quota.warning")
}
