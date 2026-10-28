package main

import (
	"strings"
	"testing"
)

// TestRecountUsagePrintsBeforeAndAfter: the command recounts one account, or every account, and
// prints the counters it found and the ones it wrote.
func TestRecountUsagePrintsBeforeAndAfter(t *testing.T) {
	t.Setenv("SELFCLOUD_DATA_DIR", t.TempDir())
	code, out, errOut := runCmd(t, "admin", "create-account", "-name", "Acme", "-quota", "100MiB")
	if code != 0 {
		t.Fatalf("create-account = %d %q %q", code, out, errOut)
	}
	var accID string
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "account_id="); ok {
			accID = v
		}
	}
	code, out, errOut = runCmd(t, "admin", "recount-usage", "-account", accID)
	if code != 0 {
		t.Fatalf("recount-usage = %d %q %q", code, out, errOut)
	}
	want := "account=" + accID + " used_bytes=0->0 used_files=0->0 reserved_bytes=0->0"
	if strings.TrimSpace(out) != want {
		t.Errorf("output = %q; want %q", out, want)
	}
	if code, _, _ := runCmd(t, "admin", "recount-usage", "-account", "acc_missing"); code != 2 {
		t.Errorf("malformed account exit = %d, want 2", code)
	}
	if code, out, _ := runCmd(t, "admin", "recount-usage"); code != 0 || !strings.Contains(out, accID) {
		t.Errorf("recount of every account = %d %q; want it to list %s", code, out, accID)
	}
}
