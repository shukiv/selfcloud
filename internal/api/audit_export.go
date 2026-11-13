// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// auditFilterFrom reads the action, actor, since and until parameters shared by the audit list and
// its export. A time is an RFC 3339 instant; since includes it and until leaves it out.
func auditFilterFrom(q url.Values) (store.AuditFilter, error) {
	f := store.AuditFilter{Action: q.Get("action"), ActorID: q.Get("actor")}
	if len(f.Action) > 100 || len(f.ActorID) > 100 {
		return f, apierr.New(apierr.InvalidRequest, "action and actor may be at most 100 characters")
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return f, apierr.New(apierr.InvalidRequest, "since must be an RFC 3339 time")
		}
		f.Since = t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return f, apierr.New(apierr.InvalidRequest, "until must be an RFC 3339 time")
		}
		f.Until = t
	}
	if !f.Since.IsZero() && !f.Until.IsZero() && !f.Until.After(f.Since) {
		return f, apierr.New(apierr.InvalidRequest, "until must be after since")
	}
	return f, nil
}

// auditExportDetails records only the filters the export was asked for.
func auditExportDetails(f store.AuditFilter) map[string]any {
	details := map[string]any{"format": "csv"}
	if !f.Since.IsZero() {
		details["since"] = f.Since.UTC().Format(time.RFC3339)
	}
	if !f.Until.IsZero() {
		details["until"] = f.Until.UTC().Format(time.RFC3339)
	}
	if f.Action != "" {
		details["action"] = f.Action
	}
	if f.ActorID != "" {
		details["actor"] = f.ActorID
	}
	return details
}

// beginExport claims the account's one export slot. It reports false when an export is already
// running for the account.
func (s *Server) beginExport(accountID string) bool {
	s.exportMu.Lock()
	defer s.exportMu.Unlock()
	if s.exporting[accountID] {
		return false
	}
	if s.exporting == nil {
		s.exporting = map[string]bool{}
	}
	s.exporting[accountID] = true
	return true
}

// endExport frees the account's export slot.
func (s *Server) endExport(accountID string) {
	s.exportMu.Lock()
	delete(s.exporting, accountID)
	s.exportMu.Unlock()
}

var auditCSVColumns = []string{"time", "actor_kind", "actor_id", "actor_name", "action", "target",
	"outcome", "ip", "user_agent", "details"}

// csvCell keeps a spreadsheet from running a value as a formula.
func csvCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// handleAuditExport streams the account's audit log as CSV, newest first, with the same visibility
// and filters as the list.
func (s *Server) handleAuditExport(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	if format := q.Get("format"); format != "" && format != "csv" {
		return apierr.New(apierr.InvalidRequest, "format must be csv")
	}
	filter, err := auditFilterFrom(q)
	if err != nil {
		return err
	}
	if !s.beginExport(acc.ID) {
		busy := apierr.New(apierr.RateLimited, "an export of this account's audit log is already running")
		busy.RetryAfter = 5 * time.Second
		return busy
	}
	defer s.endExport(acc.ID)

	entry := s.auditEntry(r, acc.ID, "audit.exported", acc.ID, auditExportDetails(filter), "")
	if err := s.store.Write(r.Context(), func(tx *store.Tx) error {
		return tx.InsertAudit(r.Context(), entry)
	}); err != nil {
		return storeErr(err)
	}

	p := principalOf(r)
	seeAll := p.Kind == auth.KindAccountKey
	if s.exportBeforePage != nil {
		if err := s.exportBeforePage(0); err != nil {
			return storeErr(err)
		}
	}
	page, next, err := s.store.AuditLogFiltered(r.Context(), acc.ID, p.UserID, seeAll, filter,
		store.Page{Limit: 1000})
	if err != nil {
		return storeErr(err)
	}

	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	name := "audit-" + acc.ID + "-" + store.Now().UTC().Format("20060102-150405") + ".csv"
	h.Set("Content-Disposition", contentDisposition("attachment", name))

	controller := http.NewResponseController(w)
	// Every write gets its own deadline: a client that stops reading does not hold the export slot
	// forever.
	setDeadline := func() {
		_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
	}
	setDeadline()
	rows := 0
	if _, err := w.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
		s.auditExportFailed(r, acc.ID, rows, err)
		return nil
	}
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	if err := cw.Write(auditCSVColumns); err != nil {
		s.auditExportFailed(r, acc.ID, rows, err)
		return nil
	}
	names := map[string]string{}
	for pageNo := 0; ; pageNo++ {
		items := make([]auditJSON, 0, len(page))
		for _, e := range page {
			items = append(items, auditJSON{
				ID: "0", Time: e.Time, Actor: actorJSON{e.ActorKind, e.ActorID}, Action: e.Action,
				Target: e.Target, IP: e.IP, UserAgent: e.UserAgent, Outcome: e.Outcome, Details: e.Details,
			})
		}
		s.nameActors(r.Context(), items)
		for i, e := range page {
			details := ""
			if len(items[i].Details) > 0 {
				details = compactJSON(items[i].Details)
			}
			row := []string{
				e.Time.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
				e.ActorKind, e.ActorID, s.exportActorName(r, acc.ID, &items[i], names),
				e.Action, e.Target, e.Outcome, e.IP, e.UserAgent, details,
			}
			for j := range row {
				row[j] = csvCell(row[j])
			}
			setDeadline()
			if err := cw.Write(row); err != nil {
				s.auditExportFailed(r, acc.ID, rows, err)
				return nil
			}
			rows++
		}
		cw.Flush()
		_ = controller.Flush()
		if err := cw.Error(); err != nil {
			s.auditExportFailed(r, acc.ID, rows, err)
			return nil
		}
		if next == "" {
			return nil
		}
		if s.exportBeforePage != nil {
			if err := s.exportBeforePage(pageNo + 1); err != nil {
				s.auditExportFailed(r, acc.ID, rows, err)
				return nil
			}
		}
		setDeadline()
		page, next, err = s.store.AuditLogFiltered(r.Context(), acc.ID, p.UserID, seeAll, filter,
			store.Page{Limit: 1000, Cursor: next})
		if err != nil {
			s.auditExportFailed(r, acc.ID, rows, err)
			return nil
		}
	}
}

// auditExportFailed handles an error after the CSV headers are out. The answer can no longer be a
// JSON error: a short CSV would look complete, so the connection is cut and the browser marks the
// download as failed. A client that went away is not a server failure and is not logged at error
// level.
func (s *Server) auditExportFailed(r *http.Request, accountID string, rows int, err error) {
	if auditExportClientGone(r, err) {
		return
	}
	s.log.Error("cannot stream the audit export", "account", accountID, "rows", rows, "error", err)
	panic(http.ErrAbortHandler)
}

// auditExportClientGone reports whether an export error is the client going away: the request's own
// context was canceled, or the connection was closed under the writer.
func auditExportClientGone(r *http.Request, err error) bool {
	if r.Context().Err() != nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// exportActorName names a user row's actor: the name the list gives, else the account user's display
// name or e-mail, else an installation admin's.
func (s *Server) exportActorName(r *http.Request, accountID string, item *auditJSON, names map[string]string) string {
	if item.Actor.Kind != "user" || item.Actor.ID == "" {
		return ""
	}
	if name, _ := item.Details["actor_name"].(string); name != "" {
		return name
	}
	if name, ok := names[item.Actor.ID]; ok {
		return name
	}
	name := ""
	if u, err := s.store.User(r.Context(), accountID, item.Actor.ID); err == nil {
		name = u.DisplayName
		if name == "" {
			name = u.Email
		}
	} else if u, err := s.store.User(r.Context(), "", item.Actor.ID); err == nil {
		name = u.DisplayName
		if name == "" {
			name = u.Email
		}
	}
	names[item.Actor.ID] = name
	return name
}

// compactJSON marshals details for one CSV cell.
func compactJSON(details map[string]any) string {
	b, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	return string(b)
}
