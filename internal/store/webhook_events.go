// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// The outbox (design section 10, decision 27). An event is written in the transaction of the change it reports,
// so the two commit or fail together. Most changes reach it through InsertAudit, which maps the audit action of
// the change to its event (webhookAuditEvents). The few changes that an audit entry cannot describe on its own
// (a copy's new files, an upload's completion) call EmitEvent themselves.

// webhookAuditEvents maps an audit action to the event that the change it records reports. An action that is
// not here reports no event, and the guard test (webhook_guard_test.go) names each one. user.create and
// user.verify are handled apart, because a user created already verified reports both events.
var webhookAuditEvents = map[string]string{
	"user.create":          "user.created",
	"user.verify":          "user.verified",
	"node.create":          "file.created",
	"node.shortcut":        "file.created",
	"node.write":           "file.updated",
	"node.rename":          "file.updated",
	"node.version.restore": "file.updated",
	"node.move":            "file.moved",
	"node.trash":           "file.deleted",
	"node.restore":         "file.restored",
}

// subtreeAudits are the actions whose target may be a folder. Each file below such a folder changes too, so each
// one gets the event, or the folder gets one summary when there are too many (EmitTreeEvents). A folder itself is
// never reported one by one (decision 27). node.transfer is not here: it also covers a change of owner inside a
// team space, which moves nothing, so its move is reported by ownership.go where the move happens.
var subtreeAudits = map[string]bool{"node.move": true, "node.trash": true, "node.restore": true}

// WebhookFolderThreshold is the most files one change may report one by one. A change that touches more reports the
// folder once, as a summary with subtree true, and the receiver re-reads the folder through the API (decision 27).
// The count is read with a LIMIT one past it, so the threshold never loads a subtree into memory.
const WebhookFolderThreshold = 1000

// WebhookDeliveryLimit is the most deliveries one file-by-file report may write: the files times the active endpoints
// that subscribe to the type. Above it the change reports the folder once, so a change to 1,000 files and six
// endpoints is one summary, not 6,000 rows (decision 27). It tightens WebhookFolderThreshold only above five endpoints.
const WebhookDeliveryLimit = 5000

// WebhookNodeCap is the most nodes a change may walk and still report file by file. The walk that counts a tree
// stops one node past it, so a larger tree is never loaded, and it reports as a folder whatever its file count.
const WebhookNodeCap = 10000

// TreeReport is how a change to a tree reports one event type: nothing, one event per file, or one folder summary.
type TreeReport int

const (
	ReportNothing TreeReport = iota
	ReportEachFile
	ReportFolder
)

// WebhookPendingLimit is the most pending deliveries an endpoint may hold (decision 28). A change that would add one
// more does not fail: its delivery is recorded failed with last_error overflow, and the endpoint is disabled.
const WebhookPendingLimit = 100000

// webhookEndpointRef is an active endpoint as a fan-out sees it: its URL (for the audit origin) and its pending
// count, which the transaction moves as it queues deliveries.
type webhookEndpointRef struct {
	url        string
	pending    int
	generation int64
}

// webhookFanout is what one transaction needs to emit for one account: the active endpoints that subscribe to
// each event type, in ID order, the endpoints themselves, and the account's external ID. It is read at the first
// emission for the account in the transaction. Endpoints change only in their own transactions, so the read holds
// for this one, except for an endpoint this transaction disables, which is dropped from the fanout (overflow).
type webhookFanout struct {
	subs       map[string][]string
	endpoints  map[string]*webhookEndpointRef
	externalID *string
}

// fanout returns the fanout of accountID in tx, reading it on first use. The read asks the database every time, so
// an endpoint made by another process is seen. An account with no endpoint costs that one indexed query, which
// returns no rows.
func (tx *Tx) fanout(ctx context.Context, accountID string) (*webhookFanout, error) {
	if f, ok := tx.fanouts[accountID]; ok {
		return f, nil
	}
	f := &webhookFanout{subs: map[string][]string{}, endpoints: map[string]*webhookEndpointRef{}}
	if err := tx.activeSubscriptions(ctx, accountID, f); err != nil {
		return nil, err
	}
	if tx.fanouts == nil {
		tx.fanouts = map[string]*webhookFanout{}
	}
	tx.fanouts[accountID] = f
	return f, nil
}

// activeSubscriptions fills f with the active endpoints of accountID, by the event type each subscribes to, and
// with the account's external ID. The account is read in the same statement, so a change pays one query for it.
func (tx *Tx) activeSubscriptions(ctx context.Context, accountID string, f *webhookFanout) error {
	rows, err := tx.q.QueryContext(ctx, `SELECT e.id, e.url, e.pending_count, e.generation, s.event, a.external_id FROM webhook_endpoints e
		JOIN webhook_endpoint_events s ON s.endpoint_id = e.id AND s.account_id = e.account_id
		JOIN accounts a ON a.id = e.account_id
		WHERE e.account_id = ? AND e.state = ? ORDER BY e.id, s.event`, accountID, WebhookActive)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var endpoint, url, event string
		var pending int
		var generation int64
		var ext sql.NullString
		if err := rows.Scan(&endpoint, &url, &pending, &generation, &event, &ext); err != nil {
			return err
		}
		if f.endpoints[endpoint] == nil {
			f.endpoints[endpoint] = &webhookEndpointRef{url: url, pending: pending, generation: generation}
		}
		f.subs[event] = append(f.subs[event], endpoint)
		if f.externalID == nil && ext.Valid {
			f.externalID = &ext.String
		}
	}
	return rows.Err()
}

// EmitEvent queues an event of accountID in tx: one webhook_events row, and one delivery for each active endpoint of
// the account that subscribes to eventType. An account with no such endpoint gets no row, so an event exists exactly
// when a delivery waits for it. The event commits or rolls back with tx, which is what ties it to its change. data is
// the event's data (package webhook); it is never read from the audit details.
//
// An endpoint at WebhookPendingLimit gets its delivery recorded as failed with last_error overflow, and is disabled
// with reason overflow in the same transaction (decision 28). The change does not fail.
func (tx *Tx) EmitEvent(ctx context.Context, accountID, eventType string, at time.Time, data any) error {
	f, err := tx.fanout(ctx, accountID)
	if err != nil {
		return err
	}
	endpoints := f.subs[eventType]
	if len(endpoints) == 0 {
		return nil
	}
	eventID := id.New("evt")
	body, err := webhook.Body(eventID, eventType, at, accountID, f.externalID, data)
	if err != nil {
		return err
	}
	res, err := tx.q.ExecContext(ctx, `INSERT INTO webhook_events (event_id, account_id, type, payload, created_at)
		VALUES (?, ?, ?, ?, ?)`, eventID, accountID, eventType, string(body), millis(at))
	if err != nil {
		return err
	}
	row, err := res.LastInsertId()
	if err != nil {
		return err
	}
	// The overflow check reads the maintained count, so it costs the same however many deliveries are pending.
	type delivery struct {
		ID         string `json:"id"`
		Endpoint   string `json:"endpoint"`
		State      string `json:"state"`
		LastError  string `json:"last_error"`
		Generation int64  `json:"generation"`
	}
	list := make([]delivery, 0, len(endpoints))
	var queued, overflowed []string
	for _, endpoint := range endpoints {
		ref := f.endpoints[endpoint]
		if ref.pending >= WebhookPendingLimit {
			overflowed = append(overflowed, endpoint)
			list = append(list, delivery{ID: id.New("dlv"), Endpoint: endpoint, State: "failed", LastError: "overflow", Generation: ref.generation})
			continue
		}
		ref.pending++
		queued = append(queued, endpoint)
		list = append(list, delivery{ID: id.New("dlv"), Endpoint: endpoint, State: "pending", Generation: ref.generation})
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	// One statement writes the deliveries. The IDs, endpoints, states and generations go in as one JSON array, which
	// json_each reads, so the statement count per event is constant, not one per subscriber.
	_, err = tx.q.ExecContext(ctx, `INSERT INTO webhook_deliveries (id, account_id, endpoint_id, event_row_id,
		state, attempts, next_attempt_at, last_error, finished_at, created_at, generation)
		SELECT json_extract(value, '$.id'), ?, json_extract(value, '$.endpoint'), ?, json_extract(value, '$.state'), 0, ?,
			json_extract(value, '$.last_error'), CASE WHEN json_extract(value, '$.state') = 'failed' THEN ? END, ?,
			json_extract(value, '$.generation')
		FROM json_each(?)`, accountID, row, millis(at), millis(at), millis(at), string(raw))
	if err != nil {
		return err
	}
	if err := tx.bumpPending(ctx, queued, 1); err != nil {
		return err
	}
	for _, endpoint := range overflowed {
		if err := tx.disableInTx(ctx, accountID, endpoint, WebhookDisabledOverflow, at, f.endpoints[endpoint].url); err != nil {
			return err
		}
		f.drop(endpoint)
	}
	return nil
}

// bumpPending adds delta to the pending count of each endpoint in endpoints. An empty list is a no-op.
func (tx *Tx) bumpPending(ctx context.Context, endpoints []string, delta int) error {
	if len(endpoints) == 0 {
		return nil
	}
	raw, err := json.Marshal(endpoints)
	if err != nil {
		return err
	}
	_, err = tx.q.ExecContext(ctx, `UPDATE webhook_endpoints SET pending_count = pending_count + ?
		WHERE id IN (SELECT value FROM json_each(?))`, delta, string(raw))
	return err
}

// drop removes an endpoint from f, for the rest of the transaction.
func (f *webhookFanout) drop(endpoint string) {
	delete(f.endpoints, endpoint)
	for ev, list := range f.subs {
		f.subs[ev] = slices.DeleteFunc(list, func(e string) bool { return e == endpoint })
	}
}

// EmitTestEvent queues one webhook.test event for one endpoint of accountID, in tx, and returns its event ID: the
// endpoint receives it whatever it subscribes to. The caller checks that the endpoint is active and has room. The
// event has an empty data object.
func (tx *Tx) EmitTestEvent(ctx context.Context, accountID, endpointID string, at time.Time) (string, error) {
	var ext sql.NullString
	if err := tx.q.QueryRowContext(ctx, "SELECT external_id FROM accounts WHERE id = ?", accountID).Scan(&ext); err != nil {
		return "", err
	}
	eventID := id.New("evt")
	body, err := webhook.Body(eventID, "webhook.test", at, accountID, nullableString(ext), struct{}{})
	if err != nil {
		return "", err
	}
	res, err := tx.q.ExecContext(ctx, `INSERT INTO webhook_events (event_id, account_id, type, payload, created_at)
		VALUES (?, ?, 'webhook.test', ?, ?)`, eventID, accountID, string(body), millis(at))
	if err != nil {
		return "", err
	}
	row, err := res.LastInsertId()
	if err != nil {
		return "", err
	}
	if _, err := tx.q.ExecContext(ctx, `INSERT INTO webhook_deliveries (id, account_id, endpoint_id, event_row_id,
		state, attempts, next_attempt_at, created_at, generation)
		VALUES (?, ?, ?, ?, 'pending', 0, ?, ?, (SELECT generation FROM webhook_endpoints WHERE id = ?))`,
		id.New("dlv"), accountID, endpointID, row, millis(at), millis(at), endpointID); err != nil {
		return "", err
	}
	if err := tx.bumpPending(ctx, []string{endpointID}, 1); err != nil {
		return "", err
	}
	return eventID, nil
}

func nullableString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

// countSubtree counts the nodes of the subtree of rootID, the root included, and its files (files and shortcuts,
// not folders). The walk stops at limit nodes, so a huge tree costs limit rows and no more, and the count of a tree
// over the limit is limit. Trashed nodes below the root are left out, as Subtree leaves them out.
func (tx *Tx) countSubtree(ctx context.Context, accountID, rootID string, limit int) (nodes, files int, err error) {
	err = tx.q.QueryRowContext(ctx, `WITH RECURSIVE sub(nid, kind) AS (
			SELECT id, kind FROM nodes WHERE account_id = ? AND id = ?
			UNION ALL
			SELECT c.id, c.kind FROM nodes c JOIN sub ON c.parent_id = sub.nid WHERE c.account_id = ? AND c.trashed_at IS NULL
			LIMIT ?
		)
		SELECT COUNT(*), COALESCE(SUM(kind <> 'dir'), 0) FROM sub`,
		accountID, rootID, accountID, limit).Scan(&nodes, &files)
	return nodes, files, err
}

// ReportFor decides how a change to a tree of nodes nodes and files files reports eventType to accountID. Nothing when
// no endpoint subscribes, or when the tree within the cap holds no file. Each file when the tree holds at most
// WebhookFolderThreshold files and the files times the subscribed endpoints stay within WebhookDeliveryLimit. A folder
// summary otherwise, and always above WebhookNodeCap nodes: a walk that stops at the cap cannot tell whether a file
// lies beyond it. Copies and the subtree emit both call it, so the rule is in one place.
func (tx *Tx) ReportFor(ctx context.Context, accountID, eventType string, nodes, files int) (TreeReport, error) {
	f, err := tx.fanout(ctx, accountID)
	if err != nil {
		return ReportNothing, err
	}
	endpoints := len(f.subs[eventType])
	switch {
	case endpoints == 0:
		return ReportNothing, nil
	case nodes > WebhookNodeCap:
		return ReportFolder, nil
	case files == 0:
		return ReportNothing, nil
	case files > WebhookFolderThreshold || files*endpoints > WebhookDeliveryLimit:
		return ReportFolder, nil
	}
	return ReportEachFile, nil
}

// EmitTreeEvents reports a change that touched the subtree of rootID, in tx, as ReportFor decides: nothing, each file
// with eventType, or the folder once with a summary that names no file. A folder inside the subtree is never reported.
func (tx *Tx) EmitTreeEvents(ctx context.Context, eventType, accountID, rootID string, at time.Time) error {
	f, err := tx.fanout(ctx, accountID)
	if err != nil {
		return err
	}
	if len(f.subs[eventType]) == 0 {
		return nil
	}
	nodes, files, err := tx.countSubtree(ctx, accountID, rootID, WebhookNodeCap+1)
	if err != nil {
		return err
	}
	report, err := tx.ReportFor(ctx, accountID, eventType, nodes, files)
	if err != nil {
		return err
	}
	switch report {
	case ReportNothing:
		return nil
	case ReportFolder:
		root, err := tx.Node(ctx, accountID, rootID)
		if err != nil {
			return fmt.Errorf("webhook summary for %s: %w", eventType, err)
		}
		return tx.EmitEvent(ctx, accountID, eventType, at, FolderEvent(root))
	}
	// The count proved the tree is within WebhookNodeCap, so this load is bounded.
	all, err := tx.Subtree(ctx, accountID, rootID)
	if err != nil {
		return err
	}
	for _, n := range all {
		if n.Kind == KindDir {
			continue
		}
		if err := tx.EmitEvent(ctx, accountID, eventType, at, FileEvent(n)); err != nil {
			return err
		}
	}
	return nil
}

// FileEvent is the data of a file event for n: the node as the change left it. It holds the node's own fields
// and never a shortcut's target.
func FileEvent(n *Node) webhook.File {
	return webhook.File{ID: n.ID, FolderID: n.FolderID, Kind: n.Kind, Size: n.Size, Name: n.Name, ParentID: n.ParentID}
}

// FolderEvent is the data of a folder summary for n: the folder that a change touched more files under than
// WebhookFolderThreshold allows. It names no file, and the receiver re-reads the folder through the API.
func FolderEvent(n *Node) webhook.Folder {
	return webhook.Folder{ID: n.ID, FolderID: n.FolderID, Kind: KindDir, ParentID: n.ParentID, Subtree: true}
}

// emitAudit reports the change that an audit entry records, if the action maps to an event. It runs in the
// transaction of the change, after the audit entry is written, so a rolled-back change reports nothing. Admin
// actions have no account and report nothing.
func (tx *Tx) emitAudit(ctx context.Context, e *AuditEntry) error {
	eventType, ok := webhookAuditEvents[e.Action]
	if !ok || e.AccountID == "" {
		return nil
	}
	at := e.Time
	if at.IsZero() {
		at = Now()
	}
	f, err := tx.fanout(ctx, e.AccountID)
	if err != nil {
		return err
	}
	if e.Action == "user.create" || e.Action == "user.verify" {
		return tx.emitUser(ctx, e, f, at)
	}
	if len(f.subs[eventType]) == 0 {
		return nil
	}
	n, err := tx.Node(ctx, e.AccountID, e.Target)
	if err != nil {
		// Every mapped action names a node that its change has just written. A missing one is a bug, and the change
		// must not commit without its event, so the error rolls the change back.
		return fmt.Errorf("webhook event for %s: %w", e.Action, err)
	}
	if n.Kind == KindDir {
		if !subtreeAudits[e.Action] {
			return nil
		}
		return tx.EmitTreeEvents(ctx, eventType, e.AccountID, n.ID, at)
	}
	return tx.EmitEvent(ctx, e.AccountID, eventType, at, FileEvent(n))
}

// emitUser reports a user's creation, and its verification when it was verified at once, or a verification.
func (tx *Tx) emitUser(ctx context.Context, e *AuditEntry, f *webhookFanout, at time.Time) error {
	if len(f.subs["user.created"])+len(f.subs["user.verified"]) == 0 {
		return nil
	}
	u, err := tx.User(ctx, e.AccountID, e.Target)
	if err != nil {
		return fmt.Errorf("webhook event for %s: %w", e.Action, err)
	}
	data := webhook.User{ID: u.ID, ExternalID: externalIDOf(u.ExternalID)}
	if e.Action == "user.create" {
		if err := tx.EmitEvent(ctx, e.AccountID, "user.created", at, data); err != nil {
			return err
		}
		if u.VerifiedAt == nil {
			return nil
		}
	}
	return tx.EmitEvent(ctx, e.AccountID, "user.verified", at, data)
}

// externalIDOf is the external ID as an event carries it: nil when the account or user has none.
func externalIDOf(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
