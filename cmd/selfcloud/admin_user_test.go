package main

import (
	"strings"
	"testing"
)

func TestCreateAdminPrintsLink(t *testing.T) {
	t.Setenv("SELFCLOUD_DATA_DIR", t.TempDir())

	code, out, errOut := runCmd(t, "admin", "create-admin", "-email", "ann@example.com", "-name", "Ann")
	if code != 0 {
		t.Fatalf("create-admin = %d %q %q", code, out, errOut)
	}
	var link, token string
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "link="); ok {
			link = v
		}
		if v, ok := strings.CutPrefix(line, "token="); ok {
			token = v
		}
	}
	// The link is the verify template with the token in its fragment, as for any user.
	if !strings.Contains(link, "/verify#token=") || !strings.HasSuffix(link, "#token="+token) || token == "" {
		t.Errorf("link %q does not carry token %q", link, token)
	}

	// The same email again is refused, and a missing or malformed email is a usage error.
	if code, _, _ := runCmd(t, "admin", "create-admin", "-email", "ann@example.com"); code != 1 {
		t.Errorf("duplicate exit = %d, want 1", code)
	}
	if code, _, _ := runCmd(t, "admin", "create-admin"); code != 2 {
		t.Errorf("no email exit = %d, want 2", code)
	}
	if code, _, _ := runCmd(t, "admin", "create-admin", "-email", "Ann <ann@example.com>"); code != 2 {
		t.Errorf("display name exit = %d, want 2", code)
	}
}
