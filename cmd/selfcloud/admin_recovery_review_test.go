// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
)

// setStatus changes a user's status directly in the database, for the states a command cannot reach by itself.
func (f *recoveryFixture) setStatus(u *store.User, status string) {
	f.t.Helper()
	err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		cur, err := tx.UserByID(f.ctx, u.ID)
		if err != nil {
			return err
		}
		cur.Status = status
		return tx.UpdateUser(f.ctx, cur)
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

// authedRequest sends a request with a bearer token and returns the status and the body.
func authedRequest(t *testing.T, method, url, token string, body any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// inTempAlphabet reports whether every character of s is in tempAlphabet.
func inTempAlphabet(s string) bool {
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(tempAlphabet, s[i]) < 0 {
			return false
		}
	}
	return true
}

// TestEnableRefusesANeverVerifiedUser: a disabled user who never verified is not enabled by reset-password -enable or by
// enable. The refusal points to admin verify, and the user stays disabled.
func TestEnableRefusesANeverVerifiedUser(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "new@example.com", store.RoleMember, store.UserDisabled, "")

	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID, "-enable")
	if code != 1 || !strings.Contains(errOut, "admin verify") || strings.Contains(out, "link=") {
		t.Fatalf("reset -enable of a never-verified user = %d %q %q; want 1 pointing to admin verify", code, out, errOut)
	}
	if f.reload(u).Status != store.UserDisabled {
		t.Fatal("a refused reset changed the status")
	}
	if code, _, errOut := runCmd(t, "admin", "enable", "-user", u.ID); code != 1 || !strings.Contains(errOut, "admin verify") {
		t.Fatalf("enable of a never-verified user = %d %q; want 1 pointing to admin verify", code, errOut)
	}
	if f.reload(u).Status != store.UserDisabled {
		t.Fatal("a refused enable changed the status")
	}
}

// TestVerifyDisabledNeverVerified: verify refuses a disabled user who never verified unless -enable is given. With
// -enable the user goes back to pending, never active, and the link works through the verify route, which activates it.
// A user who verified once and was disabled is still sent to admin enable.
func TestVerifyDisabledNeverVerified(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "new@example.com", store.RoleMember, store.UserDisabled, "")

	if code, _, errOut := runCmd(t, "admin", "verify", "-user", u.ID); code != 1 || !strings.Contains(errOut, "-enable") {
		t.Fatalf("verify of a disabled never-verified user = %d %q; want 1 naming -enable", code, errOut)
	}
	if f.reload(u).Status != store.UserDisabled {
		t.Fatal("a refused verify changed the status")
	}
	code, out, errOut := runCmd(t, "admin", "verify", "-user", u.ID, "-enable")
	if code != 0 {
		t.Fatalf("verify -enable = %d %q %q", code, out, errOut)
	}
	if got := f.reload(u); got.Status != store.UserPending || got.VerifiedAt != nil {
		t.Fatalf("status = %s, verified = %v after verify -enable; want pending and not verified", got.Status, got.VerifiedAt != nil)
	}
	ts, c := f.serve()
	if code, _ := postJSON(t, c, ts.URL+"/api/v1/auth/verify", map[string]any{"token": tokenOf(t, kv(out)["link"]),
		"password": "a passphrase"}); code != 200 {
		t.Fatalf("the verify route = %d, want 200", code)
	}
	if got := f.reload(u); got.Status != store.UserActive {
		t.Fatalf("status = %s after the verify route, want active", got.Status)
	}

	// Verified once, then disabled: the way back is admin enable, not verify.
	v := f.seedUser(acc.ID, "old@example.com", store.RoleMember, store.UserActive, "")
	f.setStatus(v, store.UserDisabled)
	if code, _, errOut := runCmd(t, "admin", "verify", "-user", v.ID, "-enable"); code != 1 || !strings.Contains(errOut, "enable") {
		t.Fatalf("verify of a verified, disabled user = %d %q; want 1 naming admin enable", code, errOut)
	}
}

// TestPasswordStdinRefusesATerminal: on a terminal the password would echo, so -password-stdin is refused with exit 2
// before anything changes. A character device stands in for the terminal.
func TestPasswordStdinRefusesATerminal(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	ts, c := f.serve()
	dev, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	withStdin(t, dev)
	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID, "-method", "temporary", "-password-stdin", "-yes")
	if code != 2 || !strings.Contains(errOut, "pipe or redirect") || strings.Contains(out, "password=") {
		t.Fatalf("-password-stdin on a terminal = %d %q %q; want 2 naming pipe or redirect", code, out, errOut)
	}
	if code, _ := login(t, ts, c, u.Email, "old passphrase"); code != 200 {
		t.Errorf("the old password no longer signs in: %d", code)
	}
}

// TestStdinPasswordMustBeTypeable: a stdin password must be UTF-8 with no control characters, so a browser can type it.
// A password of other characters, such as Hebrew, is accepted.
func TestStdinPasswordMustBeTypeable(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	ts, c := f.serve()
	for _, in := range []string{
		"pass\tword is long",
		"password\x01 is long",
		"password\x7f is long",
		"pass\x00word is long",
		"pass\xffword is long",
	} {
		withStdin(t, strings.NewReader(in+"\n"))
		if code, _, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID, "-method", "temporary",
			"-password-stdin", "-yes"); code != 2 {
			t.Errorf("stdin %q = %d %q, want 2", in, code, errOut)
		}
	}
	if code, _ := login(t, ts, c, u.Email, "old passphrase"); code != 200 {
		t.Fatal("a refused stdin password changed the password")
	}
	const hebrew = "סיסמה ארוכה מאוד"
	withStdin(t, strings.NewReader(hebrew+"\n"))
	if code, out, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID, "-method", "temporary",
		"-password-stdin", "-yes"); code != 0 {
		t.Fatalf("a typeable password = %d %q %q", code, out, errOut)
	}
	if code, _ := login(t, ts, c, u.Email, hebrew); code != 200 {
		t.Errorf("sign-in with the typeable password = %d, want 200", code)
	}
}

// TestDisableRefusesTheLastAccountOwner: the last active owner of an account is refused, as the API's disable refuses
// it. The refusal comes before the prompt, so no -yes is needed to see it. A second owner lets the first go.
func TestDisableRefusesTheLastAccountOwner(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	owner := f.seedUser(acc.ID, "owner@example.com", store.RoleOwner, store.UserActive, "old passphrase")
	if code, _, errOut := runCmd(t, "admin", "disable", "-user", owner.ID, "-yes"); code != 1 || !strings.Contains(errOut, "owner") {
		t.Fatalf("disable of the last owner = %d %q; want 1 naming the owner", code, errOut)
	}
	if f.reload(owner).Status != store.UserActive {
		t.Fatal("the last owner was disabled")
	}
	if code, _, _ := runCmd(t, "admin", "disable", "-user", owner.ID); code != 1 {
		t.Errorf("disable of the last owner without -yes = %d, want 1 (refused before the prompt)", code)
	}
	second := f.seedUser(acc.ID, "second@example.com", store.RoleOwner, store.UserActive, "old passphrase")
	if code, _, errOut := runCmd(t, "admin", "disable", "-user", owner.ID, "-yes"); code != 0 {
		t.Fatalf("disable with a second owner = %d %q", code, errOut)
	}
	if code, _, _ := runCmd(t, "admin", "disable", "-user", second.ID, "-yes"); code != 1 {
		t.Errorf("disable of the new last owner = %d, want 1", code)
	}
}

// TestLastAdminIsRefusedBeforeAPrompt: the last installation admin is refused with exit 1 even without -yes, so the
// command does not ask for a confirmation it would then refuse.
func TestLastAdminIsRefusedBeforeAPrompt(t *testing.T) {
	f := newRecoveryFixture(t)
	root := f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	if code, _, errOut := runCmd(t, "admin", "disable", "-user", root.ID); code != 1 || !strings.Contains(errOut, "last") {
		t.Fatalf("disable of the last admin without -yes = %d %q; want 1 naming the last admin", code, errOut)
	}
}

// TestTemporaryVoidsAnOutstandingLink: a temporary reset voids the reset link already out, so the link redeems with 400.
func TestTemporaryVoidsAnOutstandingLink(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	ts, c := f.serve()
	_, linkOut, _ := runCmd(t, "admin", "reset-password", "-user", u.ID)
	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID, "-method", "temporary", "-yes")
	if code != 0 {
		t.Fatalf("temporary = %d %q %q", code, out, errOut)
	}
	if got := redeemReset(t, ts, c, tokenOf(t, kv(linkOut)["link"]), "a new passphrase"); got != 400 {
		t.Errorf("the link out before the temporary reset = %d, want 400", got)
	}
	if code, _ := login(t, ts, c, u.Email, kv(out)["password"]); code != 200 {
		t.Errorf("sign-in with the temporary password = %d, want 200", code)
	}
}

// TestMixedCaseEmailResolves: an email typed with capitals names the user it names in lower case.
func TestMixedCaseEmailResolves(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", "Ann@Example.COM")
	if code != 0 || kv(out)["user_id"] != u.ID {
		t.Fatalf("mixed-case reset = %d %q %q; want user %s", code, out, errOut, u.ID)
	}
}

// TestTempAlphabet: the alphabet has 56 different characters, none of them 0, O, 1, l, I or o, and a generated password
// uses only those characters.
func TestTempAlphabet(t *testing.T) {
	if len(tempAlphabet) != 56 {
		t.Fatalf("alphabet has %d characters, want 56", len(tempAlphabet))
	}
	seen := map[byte]bool{}
	for i := 0; i < len(tempAlphabet); i++ {
		c := tempAlphabet[i]
		if seen[c] {
			t.Errorf("alphabet repeats %q", c)
		}
		seen[c] = true
		if strings.IndexByte("0O1lIo", c) >= 0 {
			t.Errorf("alphabet holds the ambiguous character %q", c)
		}
	}
	if len(seen) != 56 {
		t.Errorf("alphabet has %d different characters, want 56", len(seen))
	}
	pw, err := generatePassword(5000)
	if err != nil || len(pw) != 5000 || !inTempAlphabet(pw) {
		t.Fatalf("generated password: %d characters, err %v, all in the alphabet %v", len(pw), err, inTempAlphabet(pw))
	}
}

// TestTemporaryEndsDelegatedTokens: a delegated token that acts as the user stops working when a temporary reset ends the
// user's tokens.
func TestTemporaryEndsDelegatedTokens(t *testing.T) {
	f := newRecoveryFixture(t)
	code, out, errOut := runCmd(t, "admin", "create-account", "-name", "Acme")
	if code != 0 {
		t.Fatalf("create-account = %d %q", code, errOut)
	}
	acc, key := kv(out)["account_id"], kv(out)["account_key"]
	u := f.seedUser(acc, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	ts, _ := f.serve()

	status, body := authedRequest(t, "POST", ts.URL+"/api/v1/accounts/"+acc+"/tokens", key, map[string]any{"user": u.ID, "all": true})
	if status != 201 {
		t.Fatalf("mint = %d %s", status, body)
	}
	var minted struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &minted); err != nil || minted.Token == "" {
		t.Fatalf("no token in %s", body)
	}
	if status, body := authedRequest(t, "GET", ts.URL+"/api/v1/folders", minted.Token, nil); status != 200 {
		t.Fatalf("the token before the reset = %d %s, want 200", status, body)
	}
	if code, _, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID, "-method", "temporary", "-yes"); code != 0 {
		t.Fatalf("temporary = %d %q", code, errOut)
	}
	if status, _ := authedRequest(t, "GET", ts.URL+"/api/v1/folders", minted.Token, nil); status != 401 {
		t.Errorf("the token after the temporary reset = %d, want 401", status)
	}
}

// TestLinkFileRefusesASymlink: -link-file never follows a symbolic link. An existing target keeps its content, and a
// dangling link creates nothing at its target.
func TestLinkFileRefusesASymlink(t *testing.T) {
	f := newRecoveryFixture(t)
	f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	dir := t.TempDir()

	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "existing.txt")
	if err := os.Symlink(target, existing); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", "root@example.com", "-link-file", existing)
	if code != 1 || strings.Contains(out, "link=") {
		t.Fatalf("link file that is a symlink = %d %q %q; want 1", code, out, errOut)
	}
	if got, _ := os.ReadFile(target); string(got) != "keep me" {
		t.Fatalf("the symlink's target changed to %q", got)
	}

	absent := filepath.Join(dir, "absent.txt")
	dangling := filepath.Join(dir, "dangling.txt")
	if err := os.Symlink(absent, dangling); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runCmd(t, "admin", "reset-password", "-user", "root@example.com", "-link-file", dangling); code != 1 {
		t.Fatalf("dangling link file = %d, want 1", code)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("a dangling link file created its target")
	}
}

// TestLinkIssueAuditIsStaged: the audit row of an issued link says stage=issued, and the output names expires_at.
func TestLinkIssueAuditIsStaged(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	pending := f.seedUser(acc.ID, "new@example.com", store.RoleMember, store.UserPending, "")

	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID)
	if code != 0 || kv(out)["expires_at"] == "" || kv(out)["expires"] != "" {
		t.Fatalf("reset output = %d %q %q; want expires_at and no expires", code, out, errOut)
	}
	code, out, errOut = runCmd(t, "admin", "verify", "-user", pending.ID)
	if code != 0 || kv(out)["expires_at"] == "" {
		t.Fatalf("verify output = %d %q %q; want expires_at", code, out, errOut)
	}
	var reset, verify bool
	for _, row := range f.auditRows(acc.ID) {
		if row.Action == "user.password_reset" && row.Target == u.ID && row.Details["stage"] == "issued" {
			reset = true
		}
		if row.Action == "user.verification_issued" && row.Target == pending.ID && row.Details["stage"] == "issued" {
			verify = true
		}
	}
	if !reset || !verify {
		t.Errorf("audit rows staged issued: reset=%v verify=%v", reset, verify)
	}
}
