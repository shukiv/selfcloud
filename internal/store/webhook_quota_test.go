// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// quotaFixture is an account with a quota, one team folder and its root, and one active endpoint that subscribes to
// the given quota events. The limit is in bytes: the quota counts what usage holds.
type quotaFixture struct {
	s      *Store
	acc    string
	folder string
	ep     *WebhookEndpoint
}

func newQuotaFixture(t *testing.T, quota int64, events ...string) quotaFixture {
	t.Helper()
	s := openTest(t)
	acc := newAccountRow(t, s, "Quota")
	f := newFolderRow(t, s, acc, "Team", "team")
	if _, err := execWrite(s, "UPDATE accounts SET quota_bytes = ? WHERE id = ?", quota, acc); err != nil {
		t.Fatal(err)
	}
	var ep *WebhookEndpoint
	if len(events) > 0 {
		ep = newEndpointState(t, s, acc, "https://hook.example.com/quota", WebhookActive, events...)
	}
	return quotaFixture{s: s, acc: acc, folder: f.ID, ep: ep}
}

// execWrite runs one statement in the writer, for a test that sets up a row the API would not write.
func execWrite(s *Store, query string, args ...any) (sql.Result, error) {
	var res sql.Result
	err := s.Write(context.Background(), func(tx *Tx) error {
		var err error
		res, err = tx.q.ExecContext(context.Background(), query, args...)
		return err
	})
	return res, err
}

// add changes the account's usage by bytes in the team folder, in its own transaction, as an upload does.
func (q quotaFixture) add(t *testing.T, bytes int64) {
	t.Helper()
	if err := q.s.Write(context.Background(), func(tx *Tx) error {
		return tx.AdjustUsage(context.Background(), q.acc, q.folder, UsageChange{Bytes: bytes})
	}); err != nil {
		t.Fatal(err)
	}
}

// marks counts the quota marks of the account.
func (q quotaFixture) marks(t *testing.T) int {
	t.Helper()
	return countRows(t, q.s, "SELECT COUNT(*) FROM webhook_quota_marks WHERE account_id = ?", q.acc)
}

// watched is the account's quota_watched flag.
func (q quotaFixture) watched(t *testing.T) int {
	t.Helper()
	return countRows(t, q.s, "SELECT quota_watched FROM accounts WHERE id = ?", q.acc)
}

// setState changes the state of the fixture's endpoint, as the endpoint routes do.
func (q quotaFixture) setState(t *testing.T, state string) {
	t.Helper()
	e, err := q.s.WebhookEndpoint(context.Background(), q.acc, q.ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.State = state
	if err := q.s.Write(context.Background(), func(tx *Tx) error { return tx.UpdateWebhookEndpoint(context.Background(), e) }); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaWarningIsSentOncePerCrossing(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning", "quota.exceeded")
	q.add(t, 700)
	if got := eventTypes(t, q.s, q.acc); len(got) != 0 {
		t.Fatalf("at 70 percent: events %v, want none", got)
	}
	q.add(t, 100) // 800: the warning level
	q.add(t, 50)  // 850: still past it, no second warning
	if got, want := eventTypes(t, q.s, q.acc), []string{"quota.warning"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	q.add(t, 200) // 1050: the exceeded level, once
	q.add(t, 10)
	if got, want := eventTypes(t, q.s, q.acc), []string{"quota.warning", "quota.exceeded"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	if n := q.marks(t); n != 2 {
		t.Fatalf("marks = %d, want 2 (warning and exceeded)", n)
	}
}

func TestDroppingBelowTheLevelLetsTheNextCrossingSend(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	q.add(t, 850) // warning
	q.add(t, -200)
	if n := q.marks(t); n != 0 {
		t.Fatalf("after dropping to 650 (65 percent): marks = %d, want 0", n)
	}
	q.add(t, 200) // 850 again: a new crossing
	if got, want := eventTypes(t, q.s, q.acc), []string{"quota.warning", "quota.warning"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
}

func TestOneChangePastBothLevelsSendsBoth(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning", "quota.exceeded")
	q.add(t, 1100)
	if got, want := eventTypes(t, q.s, q.acc), []string{"quota.warning", "quota.exceeded"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	if n := q.marks(t); n != 2 {
		t.Fatalf("marks = %d, want 2", n)
	}
}

// A reservation is not usage, so it does not move a level (review finding 6). Refusals still count it: see the quota
// checks in internal/api.
func TestReservationsDoNotCountTowardTheLevel(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	if err := q.s.Write(context.Background(), func(tx *Tx) error {
		return tx.AdjustUsage(context.Background(), q.acc, q.folder, UsageChange{Reserved: 800})
	}); err != nil {
		t.Fatal(err)
	}
	if got := eventTypes(t, q.s, q.acc); len(got) != 0 {
		t.Fatalf("a reservation of 800 of 1000 sent %v, want nothing", got)
	}
}

func TestOnlySubscribedTypesAreSent(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.exceeded")
	q.add(t, 900) // past the warning level: marked, but nobody subscribes to it
	if got := eventTypes(t, q.s, q.acc); len(got) != 0 {
		t.Fatalf("events %v, want none", got)
	}
	q.add(t, 200)
	if got, want := eventTypes(t, q.s, q.acc), []string{"quota.exceeded"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
}

func TestAccountNobodyWatchesWritesNoMarksAndNoEvents(t *testing.T) {
	q := newQuotaFixture(t, 1000)
	q.add(t, 990)
	q.add(t, 10)
	if q.watched(t) != 0 || q.marks(t) != 0 || len(eventTypes(t, q.s, q.acc)) != 0 {
		t.Fatalf("unwatched: flag %d, marks %d, events %v; want none", q.watched(t), q.marks(t), eventTypes(t, q.s, q.acc))
	}
}

func TestSubscribingLaterAnnouncesTheNextChangeAboveALevel(t *testing.T) {
	q := newQuotaFixture(t, 1000)
	q.add(t, 900) // above the level, nobody watching: no event, no mark
	q.ep = newEndpointState(t, q.s, q.acc, "https://hook.example.com/late", WebhookActive, "quota.warning")
	if q.watched(t) != 1 {
		t.Fatalf("after subscribing: flag %d, want 1", q.watched(t))
	}
	if got := eventTypes(t, q.s, q.acc); len(got) != 0 {
		t.Fatalf("subscribing sends nothing for the past: events %v", got)
	}
	q.add(t, 10) // the next change that leaves the account above the level announces it
	if got, want := eventTypes(t, q.s, q.acc), []string{"quota.warning"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
}

func TestPausingTheLastWatcherClearsMarksAndResumingStartsAgain(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	q.add(t, 850)
	if q.marks(t) != 1 {
		t.Fatalf("marks = %d, want 1", q.marks(t))
	}
	q.setState(t, WebhookPaused)
	if q.watched(t) != 0 || q.marks(t) != 0 {
		t.Fatalf("paused: flag %d, marks %d; want 0, 0", q.watched(t), q.marks(t))
	}
	q.add(t, 100) // unwatched: no event, and no mark for it later
	q.setState(t, WebhookActive)
	if got, want := eventTypes(t, q.s, q.acc), []string{"quota.warning"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v, want %v (the first crossing only)", got, want)
	}
	q.add(t, 10)
	if got, want := eventTypes(t, q.s, q.acc), []string{"quota.warning", "quota.warning"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after resuming, events %v, want %v", got, want)
	}
}

func TestDeletingTheLastWatcherClearsMarks(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	q.add(t, 850)
	if err := q.s.Write(context.Background(), func(tx *Tx) error {
		return tx.DeleteWebhookEndpoint(context.Background(), q.acc, q.ep.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if q.watched(t) != 0 || q.marks(t) != 0 {
		t.Fatalf("deleted: flag %d, marks %d; want 0, 0", q.watched(t), q.marks(t))
	}
}

func TestAnOverflowThatDisablesTheLastWatcherLeavesNoMark(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	// The endpoint is at the cap, so the warning it would get is recorded failed and the endpoint is disabled.
	if _, err := execWrite(q.s, "UPDATE webhook_endpoints SET pending_count = ? WHERE id = ?",
		WebhookPendingLimit, q.ep.ID); err != nil {
		t.Fatal(err)
	}
	q.add(t, 850)
	e, err := q.s.WebhookEndpoint(context.Background(), q.acc, q.ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.State != WebhookDisabled || e.DisabledReason != WebhookDisabledOverflow {
		t.Fatalf("endpoint %s/%s, want disabled/overflow", e.State, e.DisabledReason)
	}
	if q.watched(t) != 0 || q.marks(t) != 0 {
		t.Fatalf("after the overflow: flag %d, marks %d; want 0, 0", q.watched(t), q.marks(t))
	}
}

func TestQuotaEventCarriesNoFileName(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	// A named file is in the account when the crossing happens.
	if err := q.s.Write(context.Background(), func(tx *Tx) error {
		root, err := tx.RootNode(context.Background(), q.acc, q.folder)
		if err != nil {
			return err
		}
		n := &Node{ID: "nod_quotafile", AccountID: q.acc, FolderID: q.folder, ParentID: root.ID, Name: "payroll-2026.xlsx",
			NameKey: "payroll-2026.xlsx", Kind: KindFile, Size: 5, BlobID: "blb_x", ETag: `"e"`, CreatedAt: Now(), ModifiedAt: Now()}
		return tx.InsertNode(context.Background(), n)
	}); err != nil {
		t.Fatal(err)
	}
	q.add(t, 850)
	evs := eventsOf(t, q.s, q.acc)
	if len(evs) != 1 || evs[0].Type != "quota.warning" {
		t.Fatalf("events %+v, want one quota.warning", evs)
	}
	if strings.Contains(evs[0].Payload, "payroll") || strings.Contains(evs[0].Payload, "nod_quotafile") {
		t.Fatalf("payload names a file: %s", evs[0].Payload)
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(evs[0].Payload), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"scope": "account", "id": q.acc, "percent": float64(80), "used_bytes": float64(850),
		"limit_bytes": float64(1000)}
	if !reflect.DeepEqual(body.Data, want) {
		t.Fatalf("data = %v, want %v", body.Data, want)
	}
}

func TestPersonalFolderUserLimitIsAUserScope(t *testing.T) {
	q := newQuotaFixture(t, 0, "quota.warning")
	u := newUserRow(t, q.s, q.acc, "ann@example.com", "")
	pf := newPersonalRow(t, q.s, q.acc, u.ID)
	if _, err := execWrite(q.s, "UPDATE users SET quota_bytes = 100 WHERE id = ?", u.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.s.Write(context.Background(), func(tx *Tx) error {
		return tx.AdjustUsage(context.Background(), q.acc, pf.ID, UsageChange{Bytes: 85})
	}); err != nil {
		t.Fatal(err)
	}
	evs := eventsOf(t, q.s, q.acc)
	if len(evs) != 1 || evs[0].Type != "quota.warning" {
		t.Fatalf("events %+v, want one quota.warning", evs)
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(evs[0].Payload), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data["scope"] != "user" || body.Data["id"] != u.ID || body.Data["limit_bytes"] != float64(100) {
		t.Fatalf("data = %v, want the user's scope", body.Data)
	}
	// The team folder does not touch the user's limit, and the account has no quota to cross.
	q.add(t, 500)
	if n := len(eventsOf(t, q.s, q.acc)); n != 1 {
		t.Fatalf("a team change reached the user limit: %d events", n)
	}
	// Moving the user's files out of the personal folder drops the user below the level, so the mark goes.
	if err := q.s.Write(context.Background(), func(tx *Tx) error {
		return tx.MoveUsage(context.Background(), q.acc, pf.ID, q.folder, UsageChange{Bytes: 85})
	}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, q.s, "SELECT COUNT(*) FROM webhook_quota_marks WHERE account_id = ? AND scope = 'user'", q.acc); n != 0 {
		t.Fatalf("user marks = %d after the move, want 0", n)
	}
}

func TestQuotaWarningPercentIsTheAccountsSetting(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	if p := countRows(t, q.s, "SELECT quota_warning_percent FROM accounts WHERE id = ?", q.acc); p != WebhookQuotaWarningDefault {
		t.Fatalf("default percent = %d, want %d", p, WebhookQuotaWarningDefault)
	}
	if err := q.s.Write(context.Background(), func(tx *Tx) error {
		return tx.SetQuotaWarningPercent(context.Background(), q.acc, 50, Now())
	}); err != nil {
		t.Fatal(err)
	}
	q.add(t, 500) // 50 percent: the new level
	if got, want := eventTypes(t, q.s, q.acc), []string{"quota.warning"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
}

func TestQuotaWatchIsNotSharedAcrossAccounts(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	other := newAccountRow(t, q.s, "Other")
	if _, err := execWrite(q.s, "UPDATE accounts SET quota_bytes = 1000 WHERE id = ?", other); err != nil {
		t.Fatal(err)
	}
	q.add(t, 900)
	if n := len(eventsOf(t, q.s, other)); n != 0 {
		t.Fatalf("another account got %d events", n)
	}
	if q.watched(t) != 1 {
		t.Fatalf("this account's flag = %d, want 1", q.watched(t))
	}
	if n := countRows(t, q.s, "SELECT quota_watched FROM accounts WHERE id = ?", other); n != 0 {
		t.Fatalf("the other account is watched (%d)", n)
	}
}

func TestPruneQuotaMarksRemovesMarksThatNoLongerHold(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	q.add(t, 850)
	// A recount-like correction moves the counters without a usage change, so the mark is stale.
	if _, err := execWrite(q.s, "UPDATE accounts SET used_bytes = 100 WHERE id = ?", q.acc); err != nil {
		t.Fatal(err)
	}
	n, err := q.s.PruneQuotaMarks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || q.marks(t) != 0 {
		t.Fatalf("pruned %d, marks left %d; want 1 and 0", n, q.marks(t))
	}
}

func TestPruneQuotaMarksKeepsAMarkThatStillHolds(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	q.add(t, 850)
	n, err := q.s.PruneQuotaMarks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || q.marks(t) != 1 {
		t.Fatalf("a holding mark: pruned %d, marks left %d; want 0 and 1", n, q.marks(t))
	}
}

func TestPruneQuotaMarksRemovesMarksOfAnUnwatchedAccount(t *testing.T) {
	q := newQuotaFixture(t, 1000, "quota.warning")
	q.add(t, 850)
	if _, err := execWrite(q.s, "UPDATE accounts SET quota_watched = 0 WHERE id = ?", q.acc); err != nil {
		t.Fatal(err)
	}
	if _, err := q.s.PruneQuotaMarks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if q.marks(t) != 0 {
		t.Fatalf("an unwatched account keeps %d marks", q.marks(t))
	}
}
