// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// probeAt is the time of the probe entries the audit tests write directly, far from the entries the
// server makes while the test runs.
func probeAt(second int) time.Time {
	return time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(second) * time.Second)
}

// insertAuditRows writes audit entries directly, so a test can choose their exact times.
func insertAuditRows(t *testing.T, s *Server, entries ...*store.AuditEntry) {
	t.Helper()
	ctx := context.Background()
	if err := s.store.Write(ctx, func(tx *store.Tx) error {
		for _, e := range entries {
			if err := tx.InsertAudit(ctx, e); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func auditURL(accID, suffix string, q url.Values) string {
	u := "/api/v1/accounts/" + accID + "/audit"
	if suffix != "" {
		u += "/" + suffix
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// readExport checks the CSV framing (BOM and CRLF) and returns its records.
func readExport(t *testing.T, rec *httptest.ResponseRecorder) [][]string {
	t.Helper()
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatalf("export does not start with a UTF-8 BOM: %q", body[:min(len(body), 20)])
	}
	for i, c := range body {
		if c == '\n' && (i == 0 || body[i-1] != '\r') {
			t.Fatalf("export has a bare newline at byte %d", i)
		}
	}
	r := csv.NewReader(bytes.NewReader(body[3:]))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("reading the CSV export: %v\n%s", err, body)
	}
	return rows
}

var auditCSVHeader = []string{"time", "actor_kind", "actor_id", "actor_name", "action", "target",
	"outcome", "ip", "user_agent", "details"}

func csvTime(tm time.Time) string {
	return tm.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

func TestAuditSinceUntil(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	insertAuditRows(t, s,
		&store.AuditEntry{Time: probeAt(100), AccountID: acc.ID, ActorKind: "user", ActorID: "usr_a",
			Action: "probe.0", Target: "nod_0"},
		&store.AuditEntry{Time: probeAt(101), AccountID: acc.ID, ActorKind: "user", ActorID: "usr_a",
			Action: "probe.1", Target: "nod_1"},
		&store.AuditEntry{Time: probeAt(102), AccountID: acc.ID, ActorKind: "user", ActorID: "usr_a",
			Action: "probe.2", Target: "nod_2"},
	)
	actions := func(q url.Values) []string {
		t.Helper()
		rec := call(t, s, "GET", auditURL(acc.ID, "", q), key, nil)
		wantStatus(t, rec, 200)
		var out []string
		for _, e := range decode[listResponse[auditJSON]](t, rec).Items {
			out = append(out, e.Action)
		}
		return out
	}
	for _, tt := range []struct {
		name string
		q    url.Values
		want string
	}{
		{"since includes its instant", url.Values{"action": {"probe.1"}, "since": {probeAt(101).Format(time.RFC3339Nano)}}, "probe.1"},
		{"until leaves its own instant out", url.Values{"action": {"probe.1"}, "until": {probeAt(101).Format(time.RFC3339Nano)}}, ""},
		{"both bounds", url.Values{"action": {"probe.1"}, "since": {probeAt(101).Format(time.RFC3339Nano)}, "until": {probeAt(102).Format(time.RFC3339Nano)}}, "probe.1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.Join(actions(tt.q), ","); got != tt.want {
				t.Errorf("actions = %q, want %q", got, tt.want)
			}
		})
	}
	for _, tt := range []struct {
		name string
		q    url.Values
		want string
	}{
		{"bad since", url.Values{"since": {"yesterday"}}, "since must be an RFC 3339 time"},
		{"bad until", url.Values{"until": {"2026-01-01"}}, "until must be an RFC 3339 time"},
		{"until before since", url.Values{"since": {probeAt(102).Format(time.RFC3339Nano)}, "until": {probeAt(101).Format(time.RFC3339Nano)}}, "until must be after since"},
		{"until equal to since", url.Values{"since": {probeAt(101).Format(time.RFC3339Nano)}, "until": {probeAt(101).Format(time.RFC3339Nano)}}, "until must be after since"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if e := wantError(t, call(t, s, "GET", auditURL(acc.ID, "", tt.q), key, nil), 400, apierr.InvalidRequest); e.Message != tt.want {
				t.Errorf("message = %q, want %q", e.Message, tt.want)
			}
		})
	}
}

func TestAuditExportCSV(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	rec := call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "ann@example.com", "display_name": "Ann Admin", "verification": "link"})
	wantStatus(t, rec, 201)
	ann := decode[userResponse](t, rec)
	insertAuditRows(t, s,
		&store.AuditEntry{Time: probeAt(100), AccountID: acc.ID, ActorKind: "user", ActorID: ann.ID,
			Action: "probe.plain", Target: "nod_plain", IP: "192.0.2.1", UserAgent: "test",
			Details: map[string]any{"b": 1, "a": "x"}},
		&store.AuditEntry{Time: probeAt(101), AccountID: acc.ID, ActorKind: "user", ActorID: ann.ID,
			Action: "probe.formula", Target: "=cmd|calc", Details: map[string]any{"name": "@evil"}},
		&store.AuditEntry{Time: probeAt(102), AccountID: acc.ID, ActorKind: "account_key", ActorID: "key_a",
			Action: "probe.quotes", Target: "a,\"b\""},
	)
	q := url.Values{"since": {probeAt(100).Format(time.RFC3339Nano)},
		"until": {probeAt(103).Format(time.RFC3339Nano)}}
	rec = call(t, s, "GET", auditURL(acc.ID, "export", q), key, nil)
	wantStatus(t, rec, 200)
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	disp := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disp, `attachment; filename="audit-`+acc.ID+"-") || !strings.HasSuffix(disp, ".csv\"") &&
		!strings.Contains(disp, ".csv") {
		t.Errorf("Content-Disposition = %q", disp)
	}
	rows := readExport(t, rec)
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want a header and three entries:\n%v", len(rows), rows)
	}
	if got := rows[0]; strings.Join(got, ",") != strings.Join(auditCSVHeader, ",") {
		t.Errorf("header = %v, want %v", got, auditCSVHeader)
	}
	// Newest first: quotes, formula, plain.
	quotes := rows[1]
	if quotes[4] != "probe.quotes" || quotes[5] != `a,"b"` || quotes[1] != "account_key" || quotes[3] != "" {
		t.Errorf("quoted row = %v", quotes)
	}
	formula := rows[2]
	if formula[4] != "probe.formula" || formula[5] != "'=cmd|calc" {
		t.Errorf("formula target = %q, want it escaped", formula[5])
	}
	if formula[3] != "Ann Admin" {
		t.Errorf("actor_name = %q, want Ann Admin", formula[3])
	}
	if formula[0] != csvTime(probeAt(101)) {
		t.Errorf("time = %q, want %q", formula[0], csvTime(probeAt(101)))
	}
	plain := rows[3]
	if plain[2] != ann.ID || plain[6] != "ok" || plain[7] != "192.0.2.1" || plain[8] != "test" {
		t.Errorf("plain row = %v", plain)
	}
	if plain[9] != `{"a":"x","b":1}` {
		t.Errorf("details = %q, want compact JSON", plain[9])
	}
	// A cell that starts a formula is escaped wherever it is; the details cell starts with a brace.
	if formula[9] != `{"name":"@evil"}` {
		t.Errorf("formula details = %q", formula[9])
	}
	for _, row := range rows[1:] {
		for i, cell := range row {
			if cell == "" {
				continue
			}
			if bearer := strings.ContainsRune("=+-@\t\r", rune(cell[0])); bearer && !strings.HasPrefix(cell, "'") {
				t.Errorf("cell %d of %v starts with a formula or control character and is not escaped", i, row)
			}
		}
	}
}

func TestAuditExportPagesThroughTheLog(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	entries := make([]*store.AuditEntry, 0, 2500)
	for i := 0; i < 2500; i++ {
		entries = append(entries, &store.AuditEntry{Time: probeAt(i), AccountID: acc.ID,
			ActorKind: "user", ActorID: "usr_a", Action: "probe.page", Target: "nod_" + itoa(i)})
	}
	insertAuditRows(t, s, entries...)
	q := url.Values{"action": {"probe.page"}, "since": {probeAt(0).Format(time.RFC3339Nano)},
		"until": {probeAt(2500).Format(time.RFC3339Nano)}}
	rec := call(t, s, "GET", auditURL(acc.ID, "export", q), key, nil)
	wantStatus(t, rec, 200)
	rows := readExport(t, rec)
	if len(rows) != 2501 {
		t.Fatalf("rows = %d, want a header and 2500 entries", len(rows))
	}
	if rows[1][0] != csvTime(probeAt(2499)) || rows[2500][0] != csvTime(probeAt(0)) {
		t.Errorf("first %q last %q, want the newest and oldest probe times", rows[1][0], rows[2500][0])
	}
}

func TestAuditExportAbortsWhenAPageFails(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	entries := make([]*store.AuditEntry, 0, 2500)
	for i := 0; i < 2500; i++ {
		entries = append(entries, &store.AuditEntry{Time: probeAt(i), AccountID: acc.ID,
			ActorKind: "user", ActorID: "usr_a", Action: "probe.page", Target: "nod_" + itoa(i)})
	}
	insertAuditRows(t, s, entries...)
	var log lockedBuffer
	s.log = slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s.exportBeforePage = func(page int) error {
		if page == 1 {
			return errors.New("the second page is unreadable")
		}
		return nil
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	q := url.Values{"action": {"probe.page"}, "since": {probeAt(0).Format(time.RFC3339Nano)},
		"until": {probeAt(2500).Format(time.RFC3339Nano)}}
	req, err := http.NewRequest("GET", srv.URL+auditURL(acc.ID, "export", q), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("the export ended cleanly after a page read failed; the client would save a short CSV")
	}
	_ = resp.Body.Close()
	srv.Close()

	lines := strings.Split(log.String(), "\n")
	named := 0
	for _, line := range lines {
		if strings.Contains(line, "level=ERROR") && strings.Contains(line, "account="+acc.ID) {
			named++
		}
	}
	if named != 1 {
		t.Fatalf("log = %q, want one error line naming account %s", log.String(), acc.ID)
	}
}

func TestAuditExportRefusals(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, otherKey := newAccount(t, s, admin, "Other")
	insertAuditRows(t, s, &store.AuditEntry{Time: probeAt(100), AccountID: acc.ID,
		ActorKind: "user", ActorID: "usr_a", Action: "probe.any", Target: "nod_1"})
	for _, tt := range []struct {
		name   string
		path   string
		key    string
		status int
		code   apierr.Code
		msg    string
	}{
		{"another account's key", auditURL(acc.ID, "export", nil), otherKey, 404, apierr.NotFound, ""},
		{"only csv", auditURL(acc.ID, "export", url.Values{"format": {"json"}}), key, 400, apierr.InvalidRequest, "format must be csv"},
		{"bad since", auditURL(acc.ID, "export", url.Values{"since": {"yesterday"}}), key, 400, apierr.InvalidRequest, "since must be an RFC 3339 time"},
		{"bad until", auditURL(acc.ID, "export", url.Values{"until": {"2026-01-01"}}), key, 400, apierr.InvalidRequest, "until must be an RFC 3339 time"},
		{"empty window", auditURL(acc.ID, "export", url.Values{
			"since": {probeAt(102).Format(time.RFC3339Nano)}, "until": {probeAt(102).Format(time.RFC3339Nano)}}),
			key, 400, apierr.InvalidRequest, "until must be after since"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := call(t, s, "GET", tt.path, tt.key, nil)
			e := wantError(t, rec, tt.status, tt.code)
			if tt.msg != "" && e.Message != tt.msg {
				t.Errorf("message = %q, want %q", e.Message, tt.msg)
			}
			if bytes.HasPrefix(rec.Body.Bytes(), []byte{0xef, 0xbb, 0xbf}) {
				t.Error("a refused export wrote a CSV body")
			}
		})
	}
}

func TestAuditExportRecordsItself(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	insertAuditRows(t, s, &store.AuditEntry{Time: probeAt(100), AccountID: acc.ID,
		ActorKind: "user", ActorID: "usr_a", Action: "probe.any", Target: "nod_1"})
	since := probeAt(100).Format(time.RFC3339)
	until := probeAt(101).Format(time.RFC3339)
	q := url.Values{"action": {"probe.any"}, "since": {since}, "until": {until}}
	wantStatus(t, call(t, s, "GET", auditURL(acc.ID, "export", q), key, nil), 200)
	rec := call(t, s, "GET", auditURL(acc.ID, "", url.Values{"action": {"audit.exported"}}), key, nil)
	wantStatus(t, rec, 200)
	items := decode[listResponse[auditJSON]](t, rec).Items
	if len(items) != 1 {
		t.Fatalf("audit.exported rows = %d, want 1", len(items))
	}
	got := items[0]
	if got.Target != acc.ID {
		t.Errorf("target = %q, want the account %q", got.Target, acc.ID)
	}
	if got.Details["format"] != "csv" || got.Details["action"] != "probe.any" ||
		got.Details["since"] != since || got.Details["until"] != until {
		t.Errorf("details = %v", got.Details)
	}
	if _, ok := got.Details["actor"]; ok {
		t.Errorf("details name an actor that was not filtered on: %v", got.Details)
	}
}

// blockingWriter holds the first write of an export until the test lets it go.
type blockingWriter struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (w *blockingWriter) Header() http.Header { return http.Header{} }

func (w *blockingWriter) WriteHeader(int) {}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return len(p), nil
}

func (w *blockingWriter) Flush() {}

func (w *blockingWriter) SetWriteDeadline(time.Time) error { return nil }

func TestAuditExportOneAtATime(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	w := &blockingWriter{started: make(chan struct{}), release: make(chan struct{})}
	req := httptest.NewRequest("GET", auditURL(acc.ID, "export", nil), nil)
	req.Header.Set("Authorization", "Bearer "+key)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Handler().ServeHTTP(w, req)
	}()
	<-w.started
	rec := call(t, s, "GET", auditURL(acc.ID, "export", nil), key, nil)
	e := wantError(t, rec, 429, apierr.RateLimited)
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want 5", got)
	}
	if e.Message != "an export of this account's audit log is already running" {
		t.Errorf("message = %q", e.Message)
	}
	close(w.release)
	<-done
	// The account is free again.
	wantStatus(t, call(t, s, "GET", auditURL(acc.ID, "export", nil), key, nil), 200)
}

func TestAuditExportVisibility(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key,
		map[string]any{"role": "owner"}), 200)
	insertAuditRows(t, fx.s,
		&store.AuditEntry{Time: probeAt(100), AccountID: fx.acc.ID, ActorKind: "user", ActorID: fx.ann.ID,
			Action: "probe.public", Target: "nod_public"},
		&store.AuditEntry{Time: probeAt(101), AccountID: fx.acc.ID, ActorKind: "user", ActorID: fx.ann.ID,
			Action: "probe.ann", Target: "nod_ann", PrivateUserID: fx.ann.ID},
		&store.AuditEntry{Time: probeAt(102), AccountID: fx.acc.ID, ActorKind: "user", ActorID: fx.bob.ID,
			Action: "probe.bob", Target: "nod_bob", PrivateUserID: fx.bob.ID},
	)
	q := url.Values{"since": {probeAt(100).Format(time.RFC3339Nano)},
		"until": {probeAt(103).Format(time.RFC3339Nano)}}
	actions := func(rec *httptest.ResponseRecorder) []string {
		t.Helper()
		wantStatus(t, rec, 200)
		var out []string
		for _, row := range readExport(t, rec)[1:] {
			out = append(out, row[4])
		}
		return out
	}
	if got := actions(callSession(t, fx.s, fx.annSess, "GET", auditURL(fx.acc.ID, "export", q), nil)); strings.Join(got, ",") != "probe.ann,probe.public" {
		t.Errorf("owner export actions = %v, want the public entry and its own private one", got)
	}
	if got := actions(call(t, fx.s, "GET", auditURL(fx.acc.ID, "export", q), fx.key, nil)); strings.Join(got, ",") != "probe.bob,probe.ann,probe.public" {
		t.Errorf("account key export actions = %v, want every entry", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
