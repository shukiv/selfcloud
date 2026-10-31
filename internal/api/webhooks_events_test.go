// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"database/sql"
	"strings"
	"testing"
)

// outboxRow is one event as the outbox holds it.
type outboxRow struct{ Type, Payload string }

// outboxOf reads the outbox of accountID straight from the server's database, in the order the events
// were written.
func outboxOf(t *testing.T, s *Server, accountID string) []outboxRow {
	t.Helper()
	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT type, payload FROM webhook_events WHERE account_id = ? ORDER BY id", accountID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.Type, &r.Payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// countOf runs a query that counts rows, against the server's database.
func countOf(t *testing.T, s *Server, query string, args ...any) int {
	t.Helper()
	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// execOn runs one statement against the server's database.
func execOn(t *testing.T, s *Server, stmt string) {
	t.Helper()
	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(stmt); err != nil {
		t.Fatal(err)
	}
}

// expectNewEvents checks the types of the events written since the last call, and returns the count so far.
func expectNewEvents(t *testing.T, s *Server, accountID string, seen int, step string, want ...string) int {
	t.Helper()
	all := outboxOf(t, s, accountID)
	var got []string
	for _, r := range all[seen:] {
		got = append(got, r.Type)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s: events %v, want %v", step, got, want)
	}
	return len(all)
}

// Every file route reports its change, in the order the changes were made. A purge reports nothing.
func TestWebhookEventsFollowFileRoutes(t *testing.T) {
	fx := newWebhookFixture(t)
	fx.create(t, map[string]any{"url": "https://hooks.example.com/in",
		"events": []string{"file.created", "file.updated", "file.moved", "file.deleted", "file.restored"}})
	one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
	seen := 0

	a := putNode(t, fx.s, fx.key, one.ID, "/a.txt", "aaa")
	seen = expectNewEvents(t, fx.s, fx.acc.ID, seen, "create", "file.created")

	wantStatus(t, rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+a.ID+"/content", fx.key, "aaaa",
		map[string]string{"If-Match": a.ETag}), 200)
	seen = expectNewEvents(t, fx.s, fx.acc.ID, seen, "write", "file.updated")

	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+a.ID, fx.key, map[string]any{"name": "b.txt"}), 200)
	seen = expectNewEvents(t, fx.s, fx.acc.ID, seen, "rename", "file.updated")

	d := mkdirs(t, fx.s, fx.key, one.ID, "/d")
	seen = expectNewEvents(t, fx.s, fx.acc.ID, seen, "mkdir")
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+a.ID+"/move", fx.key, map[string]any{"parent": d.ID}), 200)
	seen = expectNewEvents(t, fx.s, fx.acc.ID, seen, "move", "file.moved")

	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+a.ID+"/copy", fx.key, map[string]any{"parent": d.ID, "name": "c.txt"}), 201)
	seen = expectNewEvents(t, fx.s, fx.acc.ID, seen, "copy", "file.created")

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+a.ID, fx.key, nil), 204)
	seen = expectNewEvents(t, fx.s, fx.acc.ID, seen, "trash", "file.deleted")

	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+a.ID+"/restore", fx.key, nil), 200)
	seen = expectNewEvents(t, fx.s, fx.acc.ID, seen, "restore", "file.restored")

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+a.ID, fx.key, nil), 204)
	seen = expectNewEvents(t, fx.s, fx.acc.ID, seen, "trash again", "file.deleted")

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+a.ID, fx.key, nil), 204)
	expectNewEvents(t, fx.s, fx.acc.ID, seen, "purge")
}

// A shortcut reports its own creation with kind shortcut, and its payload never names the file it points to.
func TestWebhookShortcutEventHoldsNoTarget(t *testing.T) {
	fx := newWebhookFixture(t)
	fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created"}})
	one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
	a := putNode(t, fx.s, fx.key, one.ID, "/target-file.txt", "x")
	dir := mkdirs(t, fx.s, fx.key, one.ID, "/dir")
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+dir.ID+"/shortcuts", fx.key, map[string]any{"target": a.ID}), 201)

	evs := outboxOf(t, fx.s, fx.acc.ID)
	if len(evs) == 0 {
		t.Fatal("the shortcut wrote no event")
	}
	last := evs[len(evs)-1]
	if last.Type != "file.created" || !strings.Contains(last.Payload, `"kind":"shortcut"`) {
		t.Fatalf("shortcut event %+v", last)
	}
	if strings.Contains(last.Payload, a.ID) {
		t.Fatalf("shortcut event names its target: %s", last.Payload)
	}
}

// Payloads hold neither the file's contents, nor the endpoint's secret, nor a signed link.
func TestWebhookPayloadsHoldNoContentOrSecret(t *testing.T) {
	fx := newWebhookFixture(t)
	_, secret := fx.create(t, map[string]any{"url": "https://hooks.example.com/in",
		"events": []string{"file.created", "file.updated"}})
	one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
	a := putNode(t, fx.s, fx.key, one.ID, "/secret-body.txt", "TOP-SECRET-BODY-42")
	wantStatus(t, rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+a.ID+"/content", fx.key, "TOP-SECRET-BODY-43",
		map[string]string{"If-Match": a.ETag}), 200)

	evs := outboxOf(t, fx.s, fx.acc.ID)
	if len(evs) != 2 {
		t.Fatalf("events %+v, want two", evs)
	}
	for _, e := range evs {
		for _, leak := range []string{"TOP-SECRET", secret, "whsec_", "sig=", "/links/", "sc_acct_", "sc_"} {
			if strings.Contains(e.Payload, leak) {
				t.Fatalf("payload holds %q: %s", leak, e.Payload)
			}
		}
	}
}

// The outbox is written in the change's transaction: a failure of the event insert leaves no node, and
// once the failure is gone the same change writes its event.
func TestWebhookEventFailureRollsBackTheChange(t *testing.T) {
	fx := newWebhookFixture(t)
	fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created"}})
	one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
	nodesBefore := countOf(t, fx.s, "SELECT COUNT(*) FROM nodes WHERE account_id = ?", fx.acc.ID)

	execOn(t, fx.s, `CREATE TRIGGER refuse_events BEFORE INSERT ON webhook_events
		BEGIN SELECT RAISE(ABORT, 'forced event failure'); END`)
	rec := rawCall(t, fx.s, "POST", "/api/v1/folders/"+one.ID+"/files?"+q("/lost.txt"), fx.key, "lost", nil)
	if rec.Code < 500 {
		t.Fatalf("status %d with a refused event, want a server error", rec.Code)
	}
	if got := countOf(t, fx.s, "SELECT COUNT(*) FROM nodes WHERE account_id = ?", fx.acc.ID); got != nodesBefore {
		t.Fatalf("%d nodes after the refused event, want %d: the change was not rolled back", got, nodesBefore)
	}

	execOn(t, fx.s, "DROP TRIGGER refuse_events")
	putNode(t, fx.s, fx.key, one.ID, "/kept.txt", "kept")
	evs := outboxOf(t, fx.s, fx.acc.ID)
	if len(evs) != 1 || evs[0].Type != "file.created" {
		t.Fatalf("events after the failure is gone: %+v", evs)
	}
}

// An account's events reach only its own endpoints.
func TestWebhookEventsStayInTheirAccount(t *testing.T) {
	fx := newWebhookFixture(t)
	fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created"}})
	other, otherKey := newAccount(t, fx.s, adminKey(t, fx.s), "Other")
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+other.ID+"/webhooks", otherKey,
		map[string]any{"url": "https://other.example.com/in", "events": []string{"file.created"}})
	wantStatus(t, rec, 201)
	one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
	putNode(t, fx.s, fx.key, one.ID, "/a.txt", "a")

	if got := countOf(t, fx.s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = (SELECT id FROM webhook_endpoints WHERE account_id = ?)", fx.acc.ID); got != 1 {
		t.Fatalf("the account's own endpoint has %d deliveries, want 1 (a control)", got)
	}
	if got := len(outboxOf(t, fx.s, other.ID)); got != 0 {
		t.Fatalf("the other account has %d events for a change it did not make", got)
	}
	if got := countOf(t, fx.s, "SELECT COUNT(*) FROM webhook_deliveries WHERE account_id = ?", other.ID); got != 0 {
		t.Fatalf("the other account has %d deliveries", got)
	}
}

// A paused endpoint gets nothing for changes made while it is paused.
func TestWebhookPausedEndpointGetsNothing(t *testing.T) {
	fx := newWebhookFixture(t)
	hook, _ := fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created"}})
	one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
	putNode(t, fx.s, fx.key, one.ID, "/while-active.txt", "a")
	if got := countOf(t, fx.s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", hook); got != 1 {
		t.Fatalf("the active endpoint has %d deliveries, want 1", got)
	}
	wantStatus(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"state": "paused"}), 200)

	putNode(t, fx.s, fx.key, one.ID, "/while-paused.txt", "p")
	if got := countOf(t, fx.s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", hook); got != 1 {
		t.Fatalf("a paused endpoint has %d deliveries, want the 1 from before the pause", got)
	}
}

// A file in a user's My files reaches the endpoint the account key made (decision 18).
func TestWebhookPersonalFileReachesAccountKeyEndpoint(t *testing.T) {
	fx := newWebhookFixture(t)
	hook, _ := fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created"}})
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": "mine@example.com", "verification": "none"})
	wantStatus(t, rec, 201)
	u := decode[userResponse](t, rec)
	pf, err := fx.s.store.PersonalFolder(t.Context(), fx.acc.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	putNode(t, fx.s, fx.key, pf.ID, "/mine.txt", "mine")

	evs := outboxOf(t, fx.s, fx.acc.ID)
	var files int
	for _, e := range evs {
		if e.Type == "file.created" && strings.Contains(e.Payload, `"folder_id":"`+pf.ID+`"`) {
			files++
		}
	}
	if files != 1 {
		t.Fatalf("events %+v: want one file.created in My files", evs)
	}
	if got := countOf(t, fx.s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", hook); got != 1 {
		t.Fatalf("the account key's endpoint has %d deliveries", got)
	}
}

// A copy of a folder reports each file it makes, and not the folder.
func TestWebhookCopyReportsEachCreatedFile(t *testing.T) {
	fx := newWebhookFixture(t)
	fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created"}})
	one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
	src := mkdirs(t, fx.s, fx.key, one.ID, "/src")
	putNode(t, fx.s, fx.key, one.ID, "/src/a.txt", "a")
	putNode(t, fx.s, fx.key, one.ID, "/src/b.txt", "b")
	mkdirs(t, fx.s, fx.key, one.ID, "/src/sub")
	putNode(t, fx.s, fx.key, one.ID, "/src/sub/c.txt", "c")
	before := len(outboxOf(t, fx.s, fx.acc.ID))

	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+src.ID+"/copy", fx.key, map[string]any{"name": "copied"}), 201)

	evs := outboxOf(t, fx.s, fx.acc.ID)[before:]
	if len(evs) != 3 {
		t.Fatalf("copy wrote %d events, want one per file (3)", len(evs))
	}
	for _, e := range evs {
		if e.Type != "file.created" || strings.Contains(e.Payload, `"kind":"dir"`) {
			t.Fatalf("copy event %+v", e)
		}
	}
}

// An upload reports the file it made, and then upload.completed.
func TestWebhookUploadReportsCompletion(t *testing.T) {
	s, key, accID, f := uploadSetup(t)
	rec := call(t, s, "POST", "/api/v1/accounts/"+accID+"/webhooks", key,
		map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created", "upload.completed"}})
	wantStatus(t, rec, 201)
	data := "upload-body"
	upl := uploadID(t, createUpload(t, s, key, int64(len(data)), md("folder", f.ID, "name", "up.txt", "sha256", digest(data))))
	wantStatus(t, piece(t, s, key, upl, 0, data, nil), 204)

	evs := outboxOf(t, s, accID)
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	if strings.Join(types, ",") != "file.created,upload.completed" {
		t.Fatalf("events %v, want file.created then upload.completed", types)
	}
	if !strings.Contains(evs[1].Payload, `"upload_id":"`+upl+`"`) {
		t.Fatalf("upload.completed names no upload: %s", evs[1].Payload)
	}
}

// Creating a user reports user.created, and user.verified too when the user is verified at once.
func TestWebhookUserEvents(t *testing.T) {
	fx := newWebhookFixture(t)
	fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"user.created", "user.verified"}})
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": "now@example.com", "verification": "none"}), 201)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": "later@example.com", "verification": "link"}), 201)

	evs := outboxOf(t, fx.s, fx.acc.ID)
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	if strings.Join(types, ",") != "user.created,user.verified,user.created" {
		t.Fatalf("events %v", types)
	}
	for _, e := range evs {
		if strings.Contains(e.Payload, "@example.com") {
			t.Fatalf("user event holds an email: %s", e.Payload)
		}
	}
}

// An ownership transfer reports a move only when the item moves into the new owner's My files. Inside a team
// space it changes the owner alone, and reports nothing.
func TestWebhookOwnershipTransferReportsOnlyAMove(t *testing.T) {
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/webhooks", fx.key,
		map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.moved"}}), 201)
	fx.personalFolder(t, fx.bobSess)
	annPF := fx.personalFolder(t, fx.annSess)
	docs := fx.sessionDir(t, fx.annSess, annPF, "/docs")
	fx.sessionFile(t, fx.annSess, annPF, "/docs/a.txt", "alpha")

	before := len(outboxOf(t, fx.s, fx.acc.ID))
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+docs.ID+"/transfer-ownership",
		map[string]any{"to": fx.bob.ID}), 200)
	moved := outboxOf(t, fx.s, fx.acc.ID)[before:]
	if len(moved) != 1 || moved[0].Type != "file.moved" || !strings.Contains(moved[0].Payload, `"kind":"file"`) {
		t.Fatalf("personal transfer events %+v, want one file.moved for the file", moved)
	}

	fx.grant(t, fx.c.ID, fx.ann.ID, "editor")
	mine := fx.sessionFile(t, fx.annSess, fx.fld.ID, "/a/c/ann.txt", "mine")
	before = len(outboxOf(t, fx.s, fx.acc.ID))
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+mine.ID+"/transfer-ownership",
		map[string]any{"to": fx.bob.ID}), 200)
	if got := len(outboxOf(t, fx.s, fx.acc.ID)) - before; got != 0 {
		t.Fatalf("team-space transfer wrote %d events, want none: the item did not move", got)
	}
}
