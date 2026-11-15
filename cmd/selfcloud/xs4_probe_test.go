// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// The external-sharing commands (slice xs4, decision 187) against the real API: the switch, a grant to an address of
// another account, its list and removal, and the shared-with list the second account's user reads.

// xs4Guest makes an active user of account acc with the given address, its control proved in the store or not.
func xs4Guest(t *testing.T, st *store.Store, acc, email string, proved bool) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	u := &store.User{ID: id.New("usr"), AccountID: acc, Email: email, EmailKey: email, DisplayName: "xs4 guest",
		Locale: "en", Role: store.RoleMember, Status: store.UserActive, CreatedAt: now, UpdatedAt: now, VerifiedAt: &now}
	if proved {
		u.EmailProvedKey, u.EmailProvedAt = email, &now
	}
	if err := st.Write(ctx, func(tx *store.Tx) error { return tx.InsertUser(ctx, u) }); err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// xs4Post posts a JSON body to the probe's server and reads the answer, failing the test on any error status.
func xs4Post(t *testing.T, base, path string, body any, cookie, csrf string) (*http.Response, []byte) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", "selfcloud_session="+cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Origin", "http://localhost:8080")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s: status %d, body %s", path, resp.StatusCode, answer)
	}
	return resp, answer
}

// xs4DeviceToken signs the user in at a terminal the way login does: a sign-in link of the account's key makes a
// browser session, the session approves a CLI sign-in, and the poll answers the device token.
func xs4DeviceToken(t *testing.T, p *fileProbe, acc, key, user string) string {
	t.Helper()
	prev := os.Getenv("SELFCLOUD_TOKEN")
	t.Setenv("SELFCLOUD_TOKEN", key)
	link := valueOf(t, p.do("user", "sign-in-link", "--account", acc, user), "link=")
	t.Setenv("SELFCLOUD_TOKEN", prev)
	i := strings.Index(link, "#token=")
	if i < 0 {
		t.Fatalf("the sign-in link %q carries no token", link)
	}
	resp, body := xs4Post(t, p.url, "/api/v1/auth/sign-in-link", map[string]any{"token": link[i+len("#token="):]}, "", "")
	cookie := ""
	for _, c := range resp.Cookies() {
		if c.Name == "selfcloud_session" {
			cookie = c.Value
		}
	}
	var signed struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(body, &signed); err != nil || cookie == "" || signed.CSRFToken == "" {
		t.Fatalf("the sign-in link made no session: %s", body)
	}
	_, body = xs4Post(t, p.url, "/api/v1/auth/cli/start", map[string]any{"client_name": "xs4 probe"}, "", "")
	var start struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}
	if err := json.Unmarshal(body, &start); err != nil || start.DeviceCode == "" {
		t.Fatalf("the CLI sign-in did not start: %s", body)
	}
	xs4Post(t, p.url, "/api/v1/auth/cli/approve", map[string]any{"user_code": start.UserCode}, cookie, signed.CSRFToken)
	_, body = xs4Post(t, p.url, "/api/v1/auth/cli/poll", map[string]any{"device_code": start.DeviceCode}, "", "")
	var poll struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &poll); err != nil || poll.Token == "" {
		t.Fatalf("the CLI sign-in answered no token: %s", body)
	}
	return poll.Token
}

func TestExternalSharingThroughTheRealAPI(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	sharerKey := os.Getenv("SELFCLOUD_TOKEN")

	// The switch goes on through the command itself, and show prints it.
	if code, out, errOut := p.sharingRun("sharing", "policy", "set", "--account", p.acc, "--external-sharing", "true"); code != 0 ||
		!strings.Contains(out, "external_sharing=true\n") {
		t.Fatalf("external-sharing on: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if _, out, _ := p.sharingRun("sharing", "policy", "show", "--account", p.acc); !strings.Contains(out, "external_sharing=true\n") {
		t.Fatalf("policy show: %q", out)
	}

	// A second account, its switch on too (the shared-with list needs both), and a guest whose address is proved.
	code, out, errOut := runCmd(t, "admin", "create-account", "-name", "xs4-other")
	if code != 0 {
		t.Fatalf("create-account: exit %d, stderr %q", code, errOut)
	}
	otherAcc, otherKey := valueOf(t, out, "account_id="), valueOf(t, out, "account_key=")
	t.Setenv("SELFCLOUD_TOKEN", otherKey)
	if code, _, errOut := p.sharingRun("sharing", "policy", "set", "--account", otherAcc, "--external-sharing", "true"); code != 0 {
		t.Fatalf("the other account's switch: exit %d, stderr %q", code, errOut)
	}
	guestEmail := "xs4-guest@example.test"
	guest := xs4Guest(t, p.st, otherAcc, guestEmail, true)
	t.Setenv("SELFCLOUD_TOKEN", sharerKey)

	// Grant a node to the guest's address; the external list shows it.
	node := p.touch("/xs4-shared.txt")
	if code, out, errOut := p.sharingRun("share", "grant", node, "--external", guestEmail); code != 0 ||
		!strings.Contains(out, "email="+guestEmail+"\n") || !strings.Contains(out, "role=viewer\n") {
		t.Fatalf("grant: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if _, out, _ := p.sharingRun("share", "ls", node, "--external"); !strings.Contains(out, guestEmail) || !strings.Contains(out, "viewer") {
		t.Fatalf("share ls --external: %q", out)
	}

	// An address with a slash stays one path segment: it is granted, listed and removed like any other.
	slash := "xs4/slash@example.test"
	if code, _, errOut := p.sharingRun("share", "grant", node, "--external", slash); code != 0 {
		t.Fatalf("grant of a slash address: exit %d, stderr %q", code, errOut)
	}
	if _, out, _ := p.sharingRun("share", "ls", node, "--external"); !strings.Contains(out, slash) {
		t.Fatalf("share ls --external after the slash grant: %q", out)
	}
	if code, _, errOut := p.sharingRun("share", "revoke", node, "--external", slash, "--yes"); code != 0 {
		t.Fatalf("revoke of a slash address: exit %d, stderr %q", code, errOut)
	}
	if code, _, _ := p.sharingRun("share", "revoke", node, "--external", slash, "--yes"); code != 4 {
		t.Fatalf("a second revoke of the slash address: exit %d, want 4", code)
	}

	// A terminal's device token reads the list as a session does (decision 203): the proved guest sees the grant.
	guestToken := xs4DeviceToken(t, p, otherAcc, otherKey, guest)
	t.Setenv("SELFCLOUD_TOKEN", guestToken)
	code, out, errOut = p.sharingRun("shared", "--external")
	if code != 0 || !strings.Contains(out, node) || !strings.Contains(out, "viewer") {
		t.Fatalf("shared --external: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if strings.Contains(errOut, "not proved") || strings.Contains(errOut, "terminal sign-in") {
		t.Fatalf("a proved device token got a notice: %q", errOut)
	}
	if _, out, errOut := p.sharingRun("shared", "--external", "--json"); !strings.Contains(out, `"email_proved": true`) ||
		!strings.Contains(out, node) || strings.Contains(errOut, "terminal sign-in") {
		t.Fatalf("shared --external --json: stdout %q, stderr %q", out, errOut)
	}

	// An account key is refused, as for any refusal.
	t.Setenv("SELFCLOUD_TOKEN", sharerKey)
	if code, _, _ := p.sharingRun("shared", "--external"); code != 3 {
		t.Fatalf("shared --external with an account key: exit %d, want 3", code)
	}

	// The sharer removes the grant; the external list no longer shows it, and a second revoke is not found.
	if code, _, errOut := p.sharingRun("share", "revoke", node, "--external", guestEmail, "--yes"); code != 0 {
		t.Fatalf("revoke: exit %d, stderr %q", code, errOut)
	}
	if code, _, _ := p.sharingRun("share", "revoke", node, "--external", guestEmail, "--yes"); code != 4 {
		t.Fatalf("a second revoke: exit %d, want 4", code)
	}
	if _, out, _ := p.sharingRun("share", "ls", node, "--external"); strings.Contains(out, guestEmail) {
		t.Fatalf("share ls --external after the revoke: %q", out)
	}

	// A user whose address is not proved gets the notice, and an empty list.
	unproved := xs4Guest(t, p.st, otherAcc, "xs4-unproved@example.test", false)
	unprovedToken := xs4DeviceToken(t, p, otherAcc, otherKey, unproved)
	t.Setenv("SELFCLOUD_TOKEN", unprovedToken)
	code, out, errOut = p.sharingRun("shared", "--external")
	if code != 0 || !strings.Contains(errOut, "your email address is not proved") || strings.Contains(out, node) {
		t.Fatalf("shared --external unproved: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}
