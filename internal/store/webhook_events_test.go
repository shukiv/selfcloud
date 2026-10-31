// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// eventRow is one event as the outbox holds it.
type eventRow struct{ Type, Payload string }

// eventsOf returns the events of accountID in the order they were written.
func eventsOf(t *testing.T, s *Store, accountID string) []eventRow {
	t.Helper()
	rows, err := s.r.QueryContext(context.Background(),
		"SELECT type, payload FROM webhook_events WHERE account_id = ? ORDER BY id", accountID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []eventRow
	for rows.Next() {
		var e eventRow
		if err := rows.Scan(&e.Type, &e.Payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// eventTypes is eventsOf reduced to the types, for comparing sequences.
func eventTypes(t *testing.T, s *Store, accountID string) []string {
	t.Helper()
	var out []string
	for _, e := range eventsOf(t, s, accountID) {
		out = append(out, e.Type)
	}
	return out
}

// newEndpointState is newEndpointRow with a state. A disabled endpoint gets the reason "failing".
func newEndpointState(t testing.TB, s *Store, accountID, url, state string, events ...string) *WebhookEndpoint {
	t.Helper()
	now := Now()
	e := &WebhookEndpoint{ID: id.New("whk"), AccountID: accountID, URL: url, Events: events, State: state,
		SecretEnc: []byte("sealed-bytes-for-test"), CreatedBy: "key_test", CreatedAt: now, UpdatedAt: now}
	if state == WebhookDisabled {
		e.DisabledReason = "failing"
	}
	if err := s.Write(context.Background(), func(tx *Tx) error {
		return tx.InsertWebhookEndpoint(context.Background(), e)
	}); err != nil {
		t.Fatal(err)
	}
	return e
}

// auditNode writes a node of kind under parent in folder, and in the same transaction audits it under
// action, the way a handler does. fn, when set, runs after the audit and may fail the transaction.
func auditNode(t *testing.T, s *Store, accountID, folderID, parentID, name, kind, action string,
	fn func(tx *Tx) error) *Node {
	t.Helper()
	n := &Node{ID: id.New("nod"), AccountID: accountID, FolderID: folderID, ParentID: parentID, Name: name,
		NameKey: strings.ToLower(name), Kind: kind, Size: 5, BlobID: "blb_test", ETag: `"e"`,
		CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: "usr_test", ModifiedBy: "usr_test"}
	if kind == KindDir {
		n.Size, n.BlobID, n.ETag = 0, "", ""
	}
	err := s.Write(context.Background(), func(tx *Tx) error {
		if err := tx.InsertNode(context.Background(), n); err != nil {
			return err
		}
		if err := tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: accountID,
			ActorKind: "account_key", ActorID: "key_test", Action: action, Target: n.ID, Folders: []string{folderID}}); err != nil {
			return err
		}
		if fn != nil {
			return fn(tx)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// webhookFixtureStore is an account with one team folder and its root node.
type webhookFixtureStore struct {
	acc    string
	folder *Folder
	root   *Node
}

func newWebhookFixtureStore(t *testing.T, s *Store) webhookFixtureStore {
	t.Helper()
	acc := newAccountRow(t, s, "Acme")
	f := newFolderRow(t, s, acc, "Team", "team")
	var root *Node
	if err := s.Write(context.Background(), func(tx *Tx) error {
		var err error
		root, err = tx.RootNode(context.Background(), acc, f.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return webhookFixtureStore{acc: acc, folder: f, root: root}
}

// The event of a file's creation is written in the change's transaction, with one pending delivery for
// the active endpoint that subscribes to it.
func TestAuditWritesFileCreatedWithDelivery(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	ep := newEndpointState(t, s, fx.acc, "https://hooks.example.com/in", WebhookActive, "file.created")

	n := auditNode(t, s, fx.acc, fx.folder.ID, fx.root.ID, "a.txt", KindFile, "node.create", nil)

	evs := eventsOf(t, s, fx.acc)
	if len(evs) != 1 || evs[0].Type != "file.created" {
		t.Fatalf("events %+v, want one file.created", evs)
	}
	if !strings.Contains(evs[0].Payload, `"id":"`+n.ID+`"`) || !strings.Contains(evs[0].Payload, `"kind":"file"`) {
		t.Fatalf("payload %s", evs[0].Payload)
	}
	if got := countRows(t, s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ? AND state = 'pending'", ep.ID); got != 1 {
		t.Fatalf("deliveries for the endpoint = %d, want 1", got)
	}
}

// A change that rolls back takes its event with it: no node, no event, no delivery.
func TestRolledBackChangeLeavesNoEvent(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	ep := newEndpointState(t, s, fx.acc, "https://hooks.example.com/in", WebhookActive, "file.created")

	n := &Node{ID: id.New("nod"), AccountID: fx.acc, FolderID: fx.folder.ID, ParentID: fx.root.ID, Name: "b.txt",
		NameKey: "b.txt", Kind: KindFile, Size: 5, BlobID: "blb_test", ETag: `"e"`, CreatedAt: Now(), ModifiedAt: Now(),
		CreatedBy: "usr_test", ModifiedBy: "usr_test"}
	forced := errors.New("forced failure after the audit entry")
	err := s.Write(context.Background(), func(tx *Tx) error {
		if err := tx.InsertNode(context.Background(), n); err != nil {
			return err
		}
		if err := tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
			ActorID: "key_test", Action: "node.create", Target: n.ID, Folders: []string{fx.folder.ID}}); err != nil {
			return err
		}
		return forced
	})
	if !errors.Is(err, forced) {
		t.Fatalf("Write returned %v, want the forced failure", err)
	}
	if _, err := s.Node(context.Background(), fx.acc, n.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the node exists after rollback: %v", err)
	}
	if got := len(eventsOf(t, s, fx.acc)); got != 0 {
		t.Fatalf("%d events after rollback, want 0", got)
	}
	if got := countRows(t, s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", ep.ID); got != 0 {
		t.Fatalf("%d deliveries after rollback, want 0", got)
	}
}

// A real database refusal of the event insert (a trigger that raises) rolls back the change it belongs to.
func TestEventInsertFailureRollsBackChange(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	newEndpointState(t, s, fx.acc, "https://hooks.example.com/in", WebhookActive, "file.created")
	if _, err := s.w.ExecContext(context.Background(), `CREATE TRIGGER refuse_events BEFORE INSERT ON webhook_events
		BEGIN SELECT RAISE(ABORT, 'forced event failure'); END`); err != nil {
		t.Fatal(err)
	}

	n := &Node{ID: id.New("nod"), AccountID: fx.acc, FolderID: fx.folder.ID, ParentID: fx.root.ID, Name: "c.txt",
		NameKey: "c.txt", Kind: KindFile, Size: 5, BlobID: "blb_test", ETag: `"e"`, CreatedAt: Now(), ModifiedAt: Now(),
		CreatedBy: "usr_test", ModifiedBy: "usr_test"}
	err := s.Write(context.Background(), func(tx *Tx) error {
		if err := tx.InsertNode(context.Background(), n); err != nil {
			return err
		}
		return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
			ActorID: "key_test", Action: "node.create", Target: n.ID, Folders: []string{fx.folder.ID}})
	})
	if err == nil || !strings.Contains(err.Error(), "forced event failure") {
		t.Fatalf("Write returned %v, want the event failure", err)
	}
	if _, err := s.Node(context.Background(), fx.acc, n.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the node exists though its event was refused: %v", err)
	}
}

// Only active endpoints that subscribe to the type get a delivery. Paused, disabled and unsubscribed
// endpoints get none.
func TestOnlyActiveSubscribedEndpointsGetDeliveries(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	want := newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.created")
	paused := newEndpointState(t, s, fx.acc, "https://b.example.com/in", WebhookPaused, "file.created")
	disabled := newEndpointState(t, s, fx.acc, "https://c.example.com/in", WebhookDisabled, "file.created")
	other := newEndpointState(t, s, fx.acc, "https://d.example.com/in", WebhookActive, "user.created")

	auditNode(t, s, fx.acc, fx.folder.ID, fx.root.ID, "d.txt", KindFile, "node.create", nil)

	if got := eventTypes(t, s, fx.acc); len(got) != 1 {
		t.Fatalf("events %v, want one", got)
	}
	for _, c := range []struct {
		name string
		ep   *WebhookEndpoint
		want int
	}{{"active subscriber", want, 1}, {"paused", paused, 0}, {"disabled", disabled, 0}, {"unsubscribed", other, 0}} {
		if got := countRows(t, s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", c.ep.ID); got != c.want {
			t.Errorf("%s: %d deliveries, want %d", c.name, got, c.want)
		}
	}
}

// An account whose endpoints subscribe to nothing that changed gets no event row at all.
func TestNoSubscriberNoEventRow(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "user.created")

	auditNode(t, s, fx.acc, fx.folder.ID, fx.root.ID, "e.txt", KindFile, "node.create", nil)

	if got := len(eventsOf(t, s, fx.acc)); got != 0 {
		t.Fatalf("%d events for an endpoint that did not subscribe, want 0", got)
	}
}

// An account without endpoints never gets an event, however many changes it makes.
func TestAccountWithoutEndpointsGetsNoEvents(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	for i := 0; i < 5; i++ {
		auditNode(t, s, fx.acc, fx.folder.ID, fx.root.ID, "f"+string(rune('a'+i))+".txt", KindFile, "node.create", nil)
	}
	if got := countRows(t, s, "SELECT COUNT(*) FROM webhook_events WHERE account_id = ?", fx.acc); got != 0 {
		t.Fatalf("%d event rows with no endpoints, want 0", got)
	}
}

// Events of one account never reach another account's endpoints.
func TestEventsStayInTheirAccount(t *testing.T) {
	s := openTest(t)
	a := newWebhookFixtureStore(t, s)
	b := newWebhookFixtureStore(t, s)
	epA := newEndpointState(t, s, a.acc, "https://a.example.com/in", WebhookActive, "file.created")
	epB := newEndpointState(t, s, b.acc, "https://b.example.com/in", WebhookActive, "file.created")

	auditNode(t, s, a.acc, a.folder.ID, a.root.ID, "g.txt", KindFile, "node.create", nil)

	if got := countRows(t, s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", epB.ID); got != 0 {
		t.Fatalf("account B's endpoint got %d deliveries for account A's file", got)
	}
	if got := countRows(t, s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", epA.ID); got != 1 {
		t.Fatalf("account A's endpoint got %d deliveries, want 1", got)
	}
}

// One delivery per endpoint per event: two subscribers get one each, and the schema refuses a second row
// for the same endpoint and event.
func TestOneDeliveryPerEndpointPerEvent(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	e1 := newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.created", "file.updated")
	e2 := newEndpointState(t, s, fx.acc, "https://b.example.com/in", WebhookActive, "file.created")

	auditNode(t, s, fx.acc, fx.folder.ID, fx.root.ID, "h.txt", KindFile, "node.create", nil)

	if got := countRows(t, s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", e1.ID); got != 1 {
		t.Fatalf("first endpoint: %d deliveries, want 1", got)
	}
	if got := countRows(t, s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", e2.ID); got != 1 {
		t.Fatalf("second endpoint: %d deliveries, want 1", got)
	}
	evID := countRows(t, s, "SELECT MAX(id) FROM webhook_events WHERE account_id = ?", fx.acc)
	_, err := s.w.ExecContext(context.Background(), `INSERT INTO webhook_deliveries (id, account_id, endpoint_id, event_row_id,
		state, attempts, next_attempt_at, created_at) VALUES (?, ?, ?, ?, 'pending', 0, 0, 0)`,
		id.New("dlv"), fx.acc, e1.ID, evID)
	if err == nil {
		t.Fatal("a second delivery for the same endpoint and event was accepted")
	}
}

// Trashing a folder writes file.deleted for each file below it, and none for the folder.
func TestTrashingAFolderEmitsEachFile(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.created", "file.deleted")
	dir := auditNode(t, s, fx.acc, fx.folder.ID, fx.root.ID, "dir", KindDir, "node.create", nil)
	auditNode(t, s, fx.acc, fx.folder.ID, dir.ID, "f1.txt", KindFile, "node.create", nil)
	auditNode(t, s, fx.acc, fx.folder.ID, dir.ID, "f2.txt", KindFile, "node.create", nil)
	sub := auditNode(t, s, fx.acc, fx.folder.ID, dir.ID, "sub", KindDir, "node.create", nil)
	auditNode(t, s, fx.acc, fx.folder.ID, sub.ID, "f3.txt", KindFile, "node.create", nil)
	before := len(eventsOf(t, s, fx.acc))

	if err := s.Write(context.Background(), func(tx *Tx) error {
		if err := tx.TrashNode(context.Background(), fx.acc, dir.ID, "usr_test", Now()); err != nil {
			return err
		}
		return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
			ActorID: "key_test", Action: "node.trash", Target: dir.ID, Folders: []string{fx.folder.ID}})
	}); err != nil {
		t.Fatal(err)
	}

	got := eventTypes(t, s, fx.acc)[before:]
	if len(got) != 3 {
		t.Fatalf("trash emitted %v, want file.deleted for three files", got)
	}
	for _, typ := range got {
		if typ != "file.deleted" {
			t.Fatalf("trash emitted %v, want only file.deleted", got)
		}
	}
}

// Moving a folder writes file.moved for each file below it, since each file's row changes folder.
func TestMovingAFolderEmitsEachFile(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	other := newFolderRow(t, s, fx.acc, "Other", "other")
	var otherRoot *Node
	if err := s.Write(context.Background(), func(tx *Tx) error {
		var err error
		otherRoot, err = tx.RootNode(context.Background(), fx.acc, other.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.moved")
	dir := auditNode(t, s, fx.acc, fx.folder.ID, fx.root.ID, "dir", KindDir, "node.create", nil)
	auditNode(t, s, fx.acc, fx.folder.ID, dir.ID, "m1.txt", KindFile, "node.create", nil)
	auditNode(t, s, fx.acc, fx.folder.ID, dir.ID, "m2.txt", KindFile, "node.create", nil)

	if err := s.Write(context.Background(), func(tx *Tx) error {
		dir.FolderID, dir.ParentID = other.ID, otherRoot.ID
		if err := tx.UpdateNode(context.Background(), dir); err != nil {
			return err
		}
		if err := tx.MoveSubtree(context.Background(), fx.acc, dir.ID, fx.folder.ID, other.ID); err != nil {
			return err
		}
		return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
			ActorID: "key_test", Action: "node.move", Target: dir.ID, Folders: []string{fx.folder.ID, other.ID}})
	}); err != nil {
		t.Fatal(err)
	}

	got := eventTypes(t, s, fx.acc)
	if len(got) != 2 || got[0] != "file.moved" || got[1] != "file.moved" {
		t.Fatalf("events %v, want file.moved for each of the two files", got)
	}
}

// A restore from the trash reports file.restored, and a purge reports nothing: the trash already reported the file
// as deleted, and a purge is not on the event list (decision 27).
func TestRestoreReportsAndPurgeEmitsNothing(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.created", "file.deleted", "file.restored")
	f := auditNode(t, s, fx.acc, fx.folder.ID, fx.root.ID, "r.txt", KindFile, "node.create", nil)
	if err := s.Write(context.Background(), func(tx *Tx) error {
		if err := tx.TrashNode(context.Background(), fx.acc, f.ID, "usr_test", Now()); err != nil {
			return err
		}
		return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
			ActorID: "key_test", Action: "node.trash", Target: f.ID, Folders: []string{fx.folder.ID}})
	}); err != nil {
		t.Fatal(err)
	}
	before := len(eventsOf(t, s, fx.acc))

	if err := s.Write(context.Background(), func(tx *Tx) error {
		if err := tx.Untrash(context.Background(), fx.acc, f.ID); err != nil {
			return err
		}
		return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
			ActorID: "key_test", Action: "node.restore", Target: f.ID, Folders: []string{fx.folder.ID}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(context.Background(), func(tx *Tx) error {
		if _, _, err := tx.PurgeNode(context.Background(), fx.acc, f.ID); err != nil {
			return err
		}
		return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
			ActorID: "key_test", Action: "node.purge", Target: f.ID, Folders: []string{fx.folder.ID}})
	}); err != nil {
		t.Fatal(err)
	}
	evs := eventsOf(t, s, fx.acc)
	if len(evs) != before+1 || evs[len(evs)-1].Type != "file.restored" {
		t.Fatalf("restore wrote %d events, want one file.restored", len(evs)-before)
	}
	if got := len(eventsOf(t, s, fx.acc)); got != before+1 {
		t.Fatalf("purge wrote events, want none")
	}
}

// A user created already verified gets user.created and then user.verified. A pending user gets only the
// first, and a verification later gets user.verified.
func TestUserEvents(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "user.created", "user.verified")
	now := Now()
	pending := &User{ID: id.New("usr"), AccountID: fx.acc, ExternalID: "ext-p", Email: "p@example.com", EmailKey: "p@example.com",
		Locale: "en", Role: RoleMember, Status: UserPending, CreatedAt: now, UpdatedAt: now}
	verified := &User{ID: id.New("usr"), AccountID: fx.acc, Email: "v@example.com", EmailKey: "v@example.com", Locale: "en",
		Role: RoleMember, Status: UserActive, VerifiedAt: &now, CreatedAt: now, UpdatedAt: now}

	for _, u := range []*User{pending, verified} {
		u := u
		if err := s.Write(context.Background(), func(tx *Tx) error {
			if err := tx.InsertUser(context.Background(), u); err != nil {
				return err
			}
			return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
				ActorID: "key_test", Action: "user.create", Target: u.ID})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := eventTypes(t, s, fx.acc); strings.Join(got, ",") != "user.created,user.created,user.verified" {
		t.Fatalf("after two creations: %v", got)
	}
	if err := s.Write(context.Background(), func(tx *Tx) error {
		return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "user",
			ActorID: pending.ID, Action: "user.verify", Target: pending.ID})
	}); err != nil {
		t.Fatal(err)
	}
	evs := eventsOf(t, s, fx.acc)
	if last := evs[len(evs)-1]; last.Type != "user.verified" || !strings.Contains(last.Payload, `"id":"`+pending.ID+`"`) ||
		!strings.Contains(last.Payload, `"external_id":"ext-p"`) || strings.Contains(last.Payload, "p@example.com") {
		t.Fatalf("verification event %+v", last)
	}
}

// A shortcut's event names the shortcut and its kind, never the node it points to.
func TestShortcutEventHoldsNoTarget(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.created")
	target := auditNode(t, s, fx.acc, fx.folder.ID, fx.root.ID, "target-name.txt", KindFile, "node.create", nil)
	var sc *Node
	if err := s.Write(context.Background(), func(tx *Tx) error {
		sc = &Node{ID: id.New("nod"), AccountID: fx.acc, FolderID: fx.folder.ID, ParentID: fx.root.ID, Name: "link",
			NameKey: "link", Kind: KindShortcut, TargetID: target.ID, CreatedAt: Now(), ModifiedAt: Now(),
			CreatedBy: "usr_test", ModifiedBy: "usr_test"}
		if err := tx.InsertNode(context.Background(), sc); err != nil {
			return err
		}
		return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
			ActorID: "key_test", Action: "node.shortcut", Target: sc.ID, Folders: []string{fx.folder.ID}})
	}); err != nil {
		t.Fatal(err)
	}
	evs := eventsOf(t, s, fx.acc)
	if len(evs) == 0 {
		t.Fatal("the shortcut wrote no event")
	}
	last := evs[len(evs)-1]
	if !strings.Contains(last.Payload, `"kind":"shortcut"`) || !strings.Contains(last.Payload, `"id":"`+sc.ID+`"`) {
		t.Fatalf("shortcut event %s", last.Payload)
	}
	if strings.Contains(last.Payload, target.ID) {
		t.Fatalf("shortcut event names its target: %s", last.Payload)
	}
}

// The payload is built from the node row, not from the audit details: a visitor's name and email that a
// drop upload records in its details never reach a payload.
func TestPayloadIgnoresAuditDetails(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.created")

	auditNodeDetails(t, s, fx.acc, fx.folder.ID, fx.root.ID, "drop.txt", map[string]any{"via": "share_link",
		"uploader_name": "Visitor", "uploader_email": "visitor@example.com", "from": map[string]any{"parent": "LEAK-MARKER-FROM"}})

	evs := eventsOf(t, s, fx.acc)
	if len(evs) != 1 {
		t.Fatalf("events %+v", evs)
	}
	for _, leak := range []string{"visitor@example.com", "Visitor", "uploader", "LEAK-MARKER-FROM"} {
		if strings.Contains(evs[0].Payload, leak) {
			t.Fatalf("payload holds %q: %s", leak, evs[0].Payload)
		}
	}
}

// auditNodeDetails is auditNode with details on the audit entry.
func auditNodeDetails(t *testing.T, s *Store, accountID, folderID, parentID, name string, details map[string]any) {
	t.Helper()
	n := &Node{ID: id.New("nod"), AccountID: accountID, FolderID: folderID, ParentID: parentID, Name: name,
		NameKey: name, Kind: KindFile, Size: 5, BlobID: "blb_test", ETag: `"e"`, CreatedAt: Now(), ModifiedAt: Now(),
		CreatedBy: "usr_test", ModifiedBy: "usr_test"}
	if err := s.Write(context.Background(), func(tx *Tx) error {
		if err := tx.InsertNode(context.Background(), n); err != nil {
			return err
		}
		return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: accountID, ActorKind: "system",
			Action: "node.create", Target: n.ID, Folders: []string{folderID}, Details: details})
	}); err != nil {
		t.Fatal(err)
	}
}

// An action outside the event list writes no event, even for an account subscribed to everything.
func TestUnlistedActionsEmitNothing(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, webhookAllEvents()...)
	for _, action := range []string{"folder.create", "node.sharing", "node.appearance", "node.version.update", "link.create"} {
		if err := s.Write(context.Background(), func(tx *Tx) error {
			return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
				ActorID: "key_test", Action: action, Target: fx.folder.ID, Folders: []string{fx.folder.ID}})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(eventsOf(t, s, fx.acc)); got != 0 {
		t.Fatalf("%d events for unlisted actions, want 0", got)
	}
}

// webhookAllEvents is every event type an endpoint may subscribe to.
func webhookAllEvents() []string {
	return []string{"file.created", "file.updated", "file.deleted", "file.moved", "upload.completed",
		"quota.warning", "quota.exceeded", "user.created", "user.verified", "webhook.test"}
}
