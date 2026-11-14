// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exportCSV is a body as the export route writes one: a UTF-8 BOM, CRLF line ends and the ten columns.
var exportCSV = "time,actor_kind,actor_id,actor_name,action,target,outcome,ip,user_agent,details\r\n" +
	"2026-11-12T08:00:00.000Z,user," + testUser + ",Alice,file.read," + testFolder + ",ok,127.0.0.1,cli,\r\n" +
	"2026-11-11T08:00:00.000Z,account_key,,,folder.create," + testFolder + ",ok,127.0.0.1,cli,{\"name\":\"Team\"}\r\n"

func exportRoute() string { return accountRoute(testAcc) + "/audit/export" }

// TestAccountAuditSendsTheTimeWindow: --since and --until go out as RFC 3339 UTC instants, whether they were given as
// instants or as dates, and an offset is turned to UTC.
func TestAccountAuditSendsTheTimeWindow(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accountRoute(testAcc)+"/audit", page(""))
	cases := []struct {
		args  []string
		query string
	}{
		{[]string{"--since", "2026-11-01T00:00:00Z", "--until", "2026-11-02T00:00:00Z"},
			"since=2026-11-01T00%3A00%3A00Z&until=2026-11-02T00%3A00%3A00Z"},
		{[]string{"--since", "2026-11-01", "--until", "2026-11-02"},
			"since=2026-11-01T00%3A00%3A00Z&until=2026-11-02T00%3A00%3A00Z"},
		{[]string{"--since", "2026-11-01T02:00:00+02:00"}, "since=2026-11-01T00%3A00%3A00Z"},
		{[]string{"--until", "2026-11-02"}, "until=2026-11-02T00%3A00%3A00Z"},
		{[]string{"--since", "2026-11-01T00:00:00.500Z"}, "since=2026-11-01T00%3A00%3A00.5Z"},
		{[]string{"--action", "file.read", "--actor", testUser, "--since", "2026-11-01", "--until", "2026-11-02"},
			"action=file.read&actor=" + testUser + "&since=2026-11-01T00%3A00%3A00Z&until=2026-11-02T00%3A00%3A00Z"},
	}
	var want []string
	for _, tc := range cases {
		res := f.runAcc(AccountAudit, tc.args...)
		wantCode(t, res, 0)
		want = append(want, "GET "+accountRoute(testAcc)+"/audit?"+tc.query)
	}
	f.wantCalls(want...)
}

// TestAccountAuditRefusesBadTimes: a time that is neither RFC 3339 nor a date, and an until that is not after since,
// are usage errors that name the flag and send nothing.
func TestAccountAuditRefusesBadTimes(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	cases := []struct {
		args []string
		name string
	}{
		{[]string{"--since", "yesterday"}, "--since"},
		{[]string{"--since", "2026-11-1"}, "--since"},
		{[]string{"--since", "2026-11-01T00:00:00"}, "--since"},
		{[]string{"--until", "2026-13-01"}, "--until"},
		{[]string{"--until", "tomorrow"}, "--until"},
		{[]string{"--since", "2026-11-02", "--until", "2026-11-01"}, "--until"},
		{[]string{"--since", "2026-11-01", "--until", "2026-11-01"}, "--until"},
		{[]string{"--since", "2026-11-01T00:00:00Z", "--until", "2026-11-01T00:00:00Z"}, "--until"},
	}
	for _, tc := range cases {
		res := f.runAcc(AccountAudit, tc.args...)
		wantCode(t, res, 2)
		if !strings.Contains(res.stderr, tc.name) {
			t.Fatalf("%v: stderr %q does not name %s", tc.args, res.stderr, tc.name)
		}
	}
	f.wantNoCalls()
}

// TestAccountAuditExportSendsFilters: the export always asks for csv, and sends the action, actor and the time window
// it was given, as the list does.
func TestAccountAuditExportSendsFilters(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", exportRoute(), reply{status: http.StatusOK, body: exportCSV})
	res := f.runAcc(AccountAuditExport)
	wantCode(t, res, 0)
	res = f.runAcc(AccountAuditExport, "--since", "2026-11-01", "--until", "2026-11-02",
		"--action", "file.read", "--actor", testUser)
	wantCode(t, res, 0)
	f.wantCalls("GET "+exportRoute()+"?format=csv",
		"GET "+exportRoute()+"?action=file.read&actor="+testUser+"&format=csv"+
			"&since=2026-11-01T00%3A00%3A00Z&until=2026-11-02T00%3A00%3A00Z")
	f.wantBearer(testAccountKey)
}

// TestAccountAuditExportToStdout: without -o the body goes to standard output byte for byte as it arrives.
func TestAccountAuditExportToStdout(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", exportRoute(), reply{status: http.StatusOK, body: exportCSV})
	res := f.runAcc(AccountAuditExport)
	wantCode(t, res, 0)
	if res.stdout != exportCSV {
		t.Fatalf("stdout %q, want %q", res.stdout, exportCSV)
	}
}

// TestAccountAuditExportToFile: with -o the body lands in the file, mode 0600, and no temporary file is left beside it.
func TestAccountAuditExportToFile(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", exportRoute(), reply{status: http.StatusOK, body: exportCSV})
	dir := t.TempDir()
	dest := filepath.Join(dir, "audit.csv")
	res := f.runAcc(AccountAuditExport, "-o", dest)
	wantCode(t, res, 0)
	if res.stdout != "" {
		t.Fatalf("stdout %q, want nothing beside the file", res.stdout)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != exportCSV {
		t.Fatalf("the file holds %q, want %q", got, exportCSV)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file's mode is %v, want 0600", fi.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "audit.csv" {
		t.Fatalf("the directory holds %v, want only audit.csv", entries)
	}
}

// TestAccountAuditExportRefusesAnExistingFile: an existing file is refused before any request, and --force replaces it.
func TestAccountAuditExportRefusesAnExistingFile(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", exportRoute(), reply{status: http.StatusOK, body: exportCSV})
	dir := t.TempDir()
	dest := filepath.Join(dir, "audit.csv")
	if err := os.WriteFile(dest, []byte("old bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := f.runAcc(AccountAuditExport, "-o", dest)
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, "--force") {
		t.Fatalf("stderr %q does not name --force", res.stderr)
	}
	f.wantNoCalls()
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "old bytes" {
		t.Fatalf("the existing file changed: %q, %v", got, err)
	}
	res = f.runAcc(AccountAuditExport, "-o", dest, "--force")
	wantCode(t, res, 0)
	got, err = os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != exportCSV {
		t.Fatalf("the file holds %q, want %q", got, exportCSV)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("the replaced file's mode is %v, want 0600", fi.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("a temporary file was left: %v", entries)
	}
	// A directory at the destination is not a file to replace.
	res = f.runAcc(AccountAuditExport, "-o", dir, "--force")
	wantCode(t, res, 2)
}

// TestAccountAuditExportCutShort: a body that ends early is a failure; with -o neither the file nor a temporary file
// is left, and the message is plain on standard error.
func TestAccountAuditExportCutShort(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", exportRoute(), reply{status: http.StatusOK, body: exportCSV, cutShort: true})
	dir := t.TempDir()
	dest := filepath.Join(dir, "audit.csv")
	res := f.runAcc(AccountAuditExport, "-o", dest)
	wantCode(t, res, 1)
	if res.stderr == "" || strings.HasPrefix(res.stderr, "{") {
		t.Fatalf("stderr %q is not a plain message", res.stderr)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("the file exists after a cut body: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a temporary file was left: %v", entries)
	}
	// The same failure on standard output.
	res = f.runAcc(AccountAuditExport)
	wantCode(t, res, 1)
	if res.stderr == "" {
		t.Fatal("a cut body on standard output said nothing")
	}
}

// TestAccountAuditExportRateLimited: a 429 prints the server's message and the wait, exits non-zero and is not retried.
func TestAccountAuditExportRateLimited(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	rep := apiErr(http.StatusTooManyRequests, "rate_limited", "an export of this account's audit log is already running")
	rep.retryAfter = "5"
	f.on("GET", exportRoute(), rep)
	dir := t.TempDir()
	dest := filepath.Join(dir, "audit.csv")
	res := f.runAcc(AccountAuditExport, "-o", dest)
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "already running") || !strings.Contains(res.stderr, "5 seconds") {
		t.Fatalf("stderr %q lacks the message or the wait", res.stderr)
	}
	f.wantCalls("GET " + exportRoute() + "?format=csv")
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("a refused export wrote the file: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a temporary file was left: %v", entries)
	}
}

// TestAccountAuditExportRefusals: --json, an extra argument, over-long filters and bad times are usage errors that
// send nothing.
func TestAccountAuditExportRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	long := strings.Repeat("a", 101)
	cases := [][]string{
		{"--json"},
		{"extra"},
		{"--action", long},
		{"--actor", long},
		{"--since", "yesterday"},
		{"--until", "2026-11-01T00:00:00"},
		{"--since", "2026-11-02", "--until", "2026-11-01"},
		{"--since", "2026-11-01", "--until", "2026-11-01"},
	}
	for _, args := range cases {
		res := f.runAcc(AccountAuditExport, args...)
		wantCode(t, res, 2)
	}
	res := f.runAcc(AccountAuditExport, "--json")
	if !strings.Contains(res.stderr, "--json") {
		t.Fatalf("stderr %q does not name --json", res.stderr)
	}
	f.wantNoCalls()
}
