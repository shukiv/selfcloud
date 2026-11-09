// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/api"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// recoveryFixture is a data folder with its configuration. The test seeds the database through its own handle, and each
// command opens its own handle on the same folder, as the commands do while the server runs.
type recoveryFixture struct {
	t   *testing.T
	cfg *config.Config
	st  *store.Store
	ctx context.Context
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	t.Setenv("SELFCLOUD_DATA_DIR", t.TempDir())
	cfg, err := config.Load("", os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, err := openStore(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &recoveryFixture{t: t, cfg: cfg, st: st, ctx: ctx}
}

// seedAccount inserts an active account.
func (f *recoveryFixture) seedAccount(name string) *store.Account {
	f.t.Helper()
	now := store.Now()
	acc := &store.Account{ID: id.New("acc"), Name: name, Status: store.AccountActive, CreatedAt: now, UpdatedAt: now}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error { return tx.InsertAccount(f.ctx, acc) }); err != nil {
		f.t.Fatal(err)
	}
	return acc
}

// seedUser inserts a user. An empty accountID makes an installation admin. An active user is verified and, when
// password is not empty, has that password; a pending user has none.
func (f *recoveryFixture) seedUser(accountID, email, role, status, password string) *store.User {
	f.t.Helper()
	now := store.Now()
	u := &store.User{ID: id.New("usr"), AccountID: accountID, Email: email, EmailKey: strings.ToLower(email),
		DisplayName: "Test", Locale: "en", Role: role, Status: status, CreatedAt: now, UpdatedAt: now}
	if status == store.UserActive {
		u.VerifiedAt = &now
	}
	if password != "" {
		hash, err := auth.HashPassword(f.ctx, password)
		if err != nil {
			f.t.Fatal(err)
		}
		u.PasswordHash = hash
	}
	err := f.st.Write(f.ctx, func(tx *store.Tx) error { return tx.InsertUser(f.ctx, u) })
	if err != nil {
		f.t.Fatal(err)
	}
	return u
}

// reload returns the stored user, so a test sees what a command wrote.
func (f *recoveryFixture) reload(u *store.User) *store.User {
	f.t.Helper()
	var got *store.User
	err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		var err error
		got, err = tx.UserByID(f.ctx, u.ID)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

// auditRows returns the audit entries of the account and of the installation.
func (f *recoveryFixture) auditRows(accountID string) []*store.AuditEntry {
	f.t.Helper()
	var out []*store.AuditEntry
	for _, acc := range []string{accountID, ""} {
		rows, _, err := f.st.AuditLog(f.ctx, acc, "", true, store.Page{Limit: 500})
		if err != nil {
			f.t.Fatal(err)
		}
		out = append(out, rows...)
	}
	return out
}

// assertNoSecret fails when an audit entry holds one of the secrets, in any field.
func (f *recoveryFixture) assertNoSecret(accountID string, secrets ...string) {
	f.t.Helper()
	for _, row := range f.auditRows(accountID) {
		b, _ := json.Marshal(row)
		for _, s := range secrets {
			if s != "" && bytes.Contains(b, []byte(s)) {
				f.t.Fatalf("audit row %s holds a secret", row.Action)
			}
		}
	}
}

// serve starts the API on the same data, as the server does, and returns its URL and a client with a cookie jar.
func (f *recoveryFixture) serve() (*httptest.Server, *http.Client) {
	f.t.Helper()
	srv, err := api.New(api.Options{Config: f.cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: f.st})
	if err != nil {
		f.t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	f.t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return ts, &http.Client{Jar: jar}
}

func postJSON(t *testing.T, c *http.Client, url string, body any) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := c.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// redeemReset completes a reset link through the real route, and returns the status.
func redeemReset(t *testing.T, ts *httptest.Server, c *http.Client, token, password string) int {
	t.Helper()
	code, _ := postJSON(t, c, ts.URL+"/api/v1/auth/password/reset", map[string]any{"token": token, "password": password})
	return code
}

// login signs in and returns the status and the user ID the answer names.
func login(t *testing.T, ts *httptest.Server, c *http.Client, email, password string) (int, string) {
	t.Helper()
	code, body := postJSON(t, c, ts.URL+"/api/v1/auth/login", map[string]any{"email": email, "password": password})
	var out struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	return code, out.User.ID
}

// kv reads the key=value lines of a command's output. A value may contain "=".
func kv(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			m[k] = v
		}
	}
	return m
}

// tokenOf returns the token in a link, which sits after "#token=".
func tokenOf(t *testing.T, link string) string {
	t.Helper()
	_, tok, ok := strings.Cut(link, "#token=")
	if !ok || tok == "" {
		t.Fatalf("link %q carries no token", link)
	}
	return tok
}

// withStdin makes the admin commands read from r for the rest of the test.
func withStdin(t *testing.T, r io.Reader) {
	t.Helper()
	old := adminStdin
	adminStdin = r
	t.Cleanup(func() { adminStdin = old })
}

// TestResetLinkRedeemsThroughTheRoute: a link for an installation admin and for an account user, named by ID or email,
// is redeemed by the real reset route once. The second use is refused, and the new password signs in as the same user.
func TestResetLinkRedeemsThroughTheRoute(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	admin := f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase one")
	member := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase two")
	ts, _ := f.serve()

	for _, tc := range []struct {
		name string
		u    *store.User
		ref  string
	}{
		{"installation admin by email", admin, "root@example.com"},
		{"account user by ID", member, member.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A client of its own: a session of the other subtest would make the reset ask to switch users (409).
			jar, _ := cookiejar.New(nil)
			c := &http.Client{Jar: jar}
			code, out, errOut := runCmd(t, "admin", "reset-password", "-user", tc.ref)
			if code != 0 {
				t.Fatalf("reset-password = %d %q %q", code, out, errOut)
			}
			kvs := kv(out)
			if kvs["user_id"] != tc.u.ID || kvs["method"] != "link" {
				t.Fatalf("output = %q; want user_id=%s method=link", out, tc.u.ID)
			}
			tok := tokenOf(t, kvs["link"])
			expires, err := time.Parse(time.RFC3339, kvs["expires"])
			if err != nil {
				t.Fatalf("expires %q: %v", kvs["expires"], err)
			}
			if d := time.Until(expires) - f.cfg.Users.ResetTTL.D(); d > time.Minute || d < -time.Minute {
				t.Errorf("expires %v is not the reset TTL from now", expires)
			}
			if got := redeemReset(t, ts, c, tok, "a new passphrase"); got != 200 {
				t.Fatalf("redeem = %d, want 200", got)
			}
			if got := redeemReset(t, ts, c, tok, "another passphrase"); got != 400 {
				t.Errorf("second redeem = %d, want 400", got)
			}
			if code, uid := login(t, ts, c, tc.u.Email, "a new passphrase"); code != 200 || uid != tc.u.ID {
				t.Errorf("sign-in after reset = %d user %q, want 200 user %s", code, uid, tc.u.ID)
			}
			f.assertNoSecret(acc.ID, tok, kvs["link"], "a new passphrase")
		})
	}
}

// TestResetLinkVoidsEarlierLinks: a second link voids the first, so only the newest works.
func TestResetLinkVoidsEarlierLinks(t *testing.T) {
	f := newRecoveryFixture(t)
	f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase one")
	ts, c := f.serve()

	_, first, _ := runCmd(t, "admin", "reset-password", "-user", "root@example.com")
	_, second, _ := runCmd(t, "admin", "reset-password", "-user", "root@example.com")
	if redeemReset(t, ts, c, tokenOf(t, kv(first)["link"]), "a new passphrase") != 400 {
		t.Error("the earlier link still redeems")
	}
	if got := redeemReset(t, ts, c, tokenOf(t, kv(second)["link"]), "a new passphrase"); got != 200 {
		t.Errorf("the newest link = %d, want 200", got)
	}
}

// TestResetAmbiguousEmailListsTheIDs: an email that two users have is refused with exit 2, and both IDs and accounts
// are listed.
func TestResetAmbiguousEmailListsTheIDs(t *testing.T) {
	f := newRecoveryFixture(t)
	a := f.seedAccount("Acme")
	b := f.seedAccount("Beta")
	u1 := f.seedUser(a.ID, "shared@example.com", store.RoleMember, store.UserActive, "old passphrase")
	u2 := f.seedUser(b.ID, "shared@example.com", store.RoleMember, store.UserActive, "old passphrase")
	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", "shared@example.com")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (%q)", code, errOut)
	}
	for _, want := range []string{u1.ID, u2.ID, a.ID, b.ID} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q does not name %s", errOut, want)
		}
	}
	if strings.Contains(out, "link=") {
		t.Error("a refused reset printed a link")
	}
}

// TestResetRefusesPendingUsers: a pending user is refused, and the message points to admin verify.
func TestResetRefusesPendingUsers(t *testing.T) {
	f := newRecoveryFixture(t)
	f.seedUser("", "new@example.com", store.RoleAdmin, store.UserPending, "")
	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", "new@example.com")
	if code != 1 || !strings.Contains(errOut, "admin verify") || strings.Contains(out, "link=") {
		t.Fatalf("pending reset = %d %q %q; want 1 pointing to admin verify", code, out, errOut)
	}
}

// TestResetDisabledNeedsEnable: a disabled user is refused without -enable and unchanged. With -enable the user is
// active, the link works, and the audit row says enabled.
func TestResetDisabledNeedsEnable(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserDisabled, "old passphrase")
	ts, c := f.serve()

	if code, _, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID); code != 1 || !strings.Contains(errOut, "-enable") {
		t.Fatalf("disabled reset without -enable = %d %q; want 1 naming -enable", code, errOut)
	}
	if got := f.reload(u); got.Status != store.UserDisabled {
		t.Fatalf("status = %s after a refused reset", got.Status)
	}
	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID, "-enable")
	if code != 0 {
		t.Fatalf("reset -enable = %d %q %q", code, out, errOut)
	}
	if got := f.reload(u); got.Status != store.UserActive {
		t.Fatalf("status = %s, want active", got.Status)
	}
	if got := redeemReset(t, ts, c, tokenOf(t, kv(out)["link"]), "a new passphrase"); got != 200 {
		t.Fatalf("redeem after -enable = %d, want 200", got)
	}
	found := false
	for _, row := range f.auditRows(acc.ID) {
		if row.Action == "user.password_reset" && row.Target == u.ID && row.Details["enabled"] == true {
			found = true
		}
	}
	if !found {
		t.Error("the reset audit row does not say enabled")
	}
}

// TestTemporaryPasswordIsGeneratedAndEndsEverything: the generated password is printed once, is 24 characters from the
// unambiguous alphabet, and sets MustChangePassword. Old sessions and tokens end. Sign-in still succeeds, and a route
// other than the must-change ones is refused with password_change_required until the change.
func TestTemporaryPasswordIsGeneratedAndEndsEverything(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	ts, oldClient := f.serve()
	if code, _ := login(t, ts, oldClient, u.Email, "old passphrase"); code != 200 {
		t.Fatalf("first sign-in = %d", code)
	}

	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID, "-method", "temporary", "-yes")
	if code != 0 {
		t.Fatalf("temporary = %d %q %q", code, out, errOut)
	}
	pw := kv(out)["password"]
	if len(pw) != 24 || strings.ContainsAny(pw, "0O1lI") || strings.Count(out, pw) != 1 || strings.Contains(errOut, pw) {
		t.Fatalf("password %q: want 24 characters from the unambiguous alphabet, printed once", pw)
	}
	if got := f.reload(u); !got.MustChangePassword {
		t.Error("MustChangePassword is not set")
	}
	if _, err := f.st.UserTokensNotBefore(f.ctx, acc.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	resp0, err := oldClient.Get(ts.URL + "/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	resp0.Body.Close()
	if resp0.StatusCode != 401 {
		t.Errorf("the old session = %d, want 401", resp0.StatusCode)
	}

	newClient := &http.Client{}
	jar, _ := cookiejar.New(nil)
	newClient.Jar = jar
	if code, uid := login(t, ts, newClient, u.Email, pw); code != 200 || uid != u.ID {
		t.Fatalf("sign-in with the temporary password = %d, want 200 for %s", code, u.ID)
	}
	resp, err := newClient.Get(ts.URL + "/api/v1/me/storage/largest")
	if err != nil {
		t.Fatal(err)
	}
	body := new(bytes.Buffer)
	body.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 403 || !strings.Contains(body.String(), "password_change_required") {
		t.Errorf("a normal route = %d %s; want 403 password_change_required", resp.StatusCode, body)
	}
	f.assertNoSecret(acc.ID, pw)
}

// TestTemporaryNeedsYesWhenNotATerminal: without -yes and a terminal, the temporary method is refused and nothing
// changes.
func TestTemporaryNeedsYesWhenNotATerminal(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	ts, c := f.serve()
	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID, "-method", "temporary")
	if code != 2 || strings.Contains(out, "password=") || !strings.Contains(errOut, "-yes") {
		t.Fatalf("temporary without -yes = %d %q %q; want 2 naming -yes", code, out, errOut)
	}
	if code, _ := login(t, ts, c, u.Email, "old passphrase"); code != 200 {
		t.Errorf("the old password no longer signs in: %d", code)
	}
	if f.reload(u).MustChangePassword {
		t.Error("a refused reset set MustChangePassword")
	}
}

// TestTemporaryStdinIsCheckedAgainstTheRules: -password-stdin takes one line, checked with the web's rules. A bad value
// is a usage error and changes nothing. A good one is used, and only "password=set" is printed.
func TestTemporaryStdinIsCheckedAgainstTheRules(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	ts, c := f.serve()

	for _, in := range []string{"short\n", "\n", ""} {
		withStdin(t, strings.NewReader(in))
		if code, _, _ := runCmd(t, "admin", "reset-password", "-user", u.ID, "-method", "temporary", "-password-stdin", "-yes"); code != 2 {
			t.Errorf("stdin %q = %d, want 2", in, code)
		}
	}
	if code, _ := login(t, ts, c, u.Email, "old passphrase"); code != 200 {
		t.Fatal("a refused stdin password changed the password")
	}

	withStdin(t, strings.NewReader("a sufficiently long passphrase\n"))
	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID, "-method", "temporary", "-password-stdin", "-yes")
	if code != 0 || kv(out)["password"] != "set" || strings.Contains(out, "sufficiently") || strings.Contains(errOut, "sufficiently") {
		t.Fatalf("stdin reset = %d %q %q", code, out, errOut)
	}
	if code, _ := login(t, ts, c, u.Email, "a sufficiently long passphrase"); code != 200 {
		t.Errorf("sign-in with the stdin password = %d, want 200", code)
	}
	f.assertNoSecret(acc.ID, "sufficiently long passphrase")
}

// TestLinkFileIsNewOwnerOnlyAndNeverOverwritten: -link-file writes the link to a new file (mode 0600) instead of stdout,
// and a refused second run leaves the file and its link alone.
func TestLinkFileIsNewOwnerOnlyAndNeverOverwritten(t *testing.T) {
	f := newRecoveryFixture(t)
	f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	ts, c := f.serve()
	path := filepath.Join(t.TempDir(), "link.txt")

	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", "root@example.com", "-link-file", path)
	if code != 0 || strings.Contains(out, "link=") || kv(out)["link_file"] != path {
		t.Fatalf("link-file run = %d %q %q", code, out, errOut)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("link file mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	before, _ := os.ReadFile(path)

	if code, _, _ := runCmd(t, "admin", "reset-password", "-user", "root@example.com", "-link-file", path); code != 1 {
		t.Fatalf("second run into an existing file = %d, want 1", code)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("the existing file was changed")
	}
	if got := redeemReset(t, ts, c, tokenOf(t, strings.TrimSpace(string(after))), "a new passphrase"); got != 200 {
		t.Errorf("the link in the file = %d, want 200 (the refused run must not void it)", got)
	}
}

// TestResetTTLBounds: -ttl takes 5m to 168h, and only for the link method.
func TestResetTTLBounds(t *testing.T) {
	f := newRecoveryFixture(t)
	f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	for _, ttl := range []string{"4m", "169h", "nonsense", "-1h"} {
		if code, _, _ := runCmd(t, "admin", "reset-password", "-user", "root@example.com", "-ttl", ttl); code != 2 {
			t.Errorf("-ttl %s = %d, want 2", ttl, code)
		}
	}
	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", "root@example.com", "-ttl", "5m")
	if code != 0 {
		t.Fatalf("-ttl 5m = %d %q", code, errOut)
	}
	expires, _ := time.Parse(time.RFC3339, kv(out)["expires"])
	if d := time.Until(expires) - 5*time.Minute; d > time.Minute || d < -time.Minute {
		t.Errorf("-ttl 5m expires %v", expires)
	}
	if code, _, _ := runCmd(t, "admin", "reset-password", "-user", "root@example.com", "-method", "temporary", "-ttl", "1h", "-yes"); code != 2 {
		t.Errorf("-ttl with temporary = %d, want 2", code)
	}
}

// TestResetFlagValidation: bad usage is exit 2 and changes nothing.
func TestResetFlagValidation(t *testing.T) {
	f := newRecoveryFixture(t)
	f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	path := filepath.Join(t.TempDir(), "x")
	withStdin(t, strings.NewReader("a sufficiently long passphrase\n"))
	for _, args := range [][]string{
		{},
		{"-user", "notanemail"},
		{"-user", "usr_bad"},
		{"-user", "root@example.com", "-method", "sms"},
		{"-user", "root@example.com", "-password-stdin"},
		{"-user", "root@example.com", "-method", "temporary", "-link-file", path, "-yes"},
		{"-user", "root@example.com", "-method", "temporary", "-password-stdin", "-link-file", path, "-yes"},
	} {
		if code, _, _ := runCmd(t, append([]string{"admin", "reset-password"}, args...)...); code != 2 {
			t.Errorf("reset-password %v = %d, want 2", args, code)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a refused command created the link file")
	}
}

// TestVerifyIssuesALinkThatTheRouteAccepts: verify gives a pending user a fresh link the verify route accepts, and an
// earlier link is void. An active user is refused.
func TestVerifyIssuesALinkThatTheRouteAccepts(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserPending, "")
	ts, c := f.serve()

	_, first, _ := runCmd(t, "admin", "verify", "-user", "ann@example.com")
	code, out, errOut := runCmd(t, "admin", "verify", "-user", "ann@example.com")
	if code != 0 {
		t.Fatalf("verify = %d %q %q", code, out, errOut)
	}
	if kv(out)["user_id"] != u.ID {
		t.Fatalf("output %q does not name %s", out, u.ID)
	}
	if code, _ := postJSON(t, c, ts.URL+"/api/v1/auth/verify", map[string]any{"token": tokenOf(t, kv(first)["link"]),
		"password": "a passphrase"}); code != 400 {
		t.Errorf("the earlier verify link = %d, want 400", code)
	}
	if code, _ := postJSON(t, c, ts.URL+"/api/v1/auth/verify", map[string]any{"token": tokenOf(t, kv(out)["link"]),
		"password": "a passphrase"}); code != 200 {
		t.Fatalf("the verify route = %d, want 200", code)
	}
	if got := f.reload(u); got.Status != store.UserActive {
		t.Errorf("status = %s after verify, want active", got.Status)
	}
	if code, _, _ := runCmd(t, "admin", "verify", "-user", "ann@example.com"); code != 1 {
		t.Errorf("verify of an active user = %d, want 1", code)
	}
	f.assertNoSecret(acc.ID, tokenOf(t, kv(out)["link"]), kv(out)["link"])
}

// TestUsersTableAndFilters: a tab-separated table with the listed columns and no secret, narrowed by each filter.
func TestUsersTableAndFilters(t *testing.T) {
	f := newRecoveryFixture(t)
	a := f.seedAccount("Acme")
	b := f.seedAccount("Beta")
	f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	f.seedUser(a.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	f.seedUser(a.ID, "bob@example.com", store.RoleMember, store.UserDisabled, "old passphrase")
	f.seedUser(b.ID, "cy@example.com", store.RoleOwner, store.UserPending, "")

	code, out, errOut := runCmd(t, "admin", "users")
	if code != 0 {
		t.Fatalf("users = %d %q", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if lines[0] != "id\temail\trole\tstatus\taccount\tcreated\tmust_change" {
		t.Fatalf("header = %q", lines[0])
	}
	if len(lines) != 5 {
		t.Fatalf("rows = %d, want 4 users", len(lines)-1)
	}
	for _, l := range lines[1:] {
		if n := strings.Count(l, "\t"); n != 6 {
			t.Errorf("row %q has %d tabs, want 6", l, n)
		}
	}
	if strings.Contains(out, "$argon2") {
		t.Error("the table holds a password hash")
	}
	rowsOf := func(args ...string) []string {
		t.Helper()
		code, out, errOut := runCmd(t, append([]string{"admin", "users"}, args...)...)
		if code != 0 {
			t.Fatalf("users %v = %d %q", args, code, errOut)
		}
		var rows []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
			rows = append(rows, l)
		}
		return rows
	}
	if n := len(rowsOf("-account", a.ID)); n != 2 {
		t.Errorf("-account rows = %d, want 2", n)
	}
	if rows := rowsOf("-admins"); len(rows) != 1 || !strings.Contains(rows[0], "root@example.com") || !strings.HasSuffix(rows[0], "\tfalse") {
		t.Errorf("-admins = %q", rows)
	}
	if rows := rowsOf("-status", "disabled"); len(rows) != 1 || !strings.Contains(rows[0], "bob@example.com") {
		t.Errorf("-status disabled = %q", rows)
	}
	if rows := rowsOf("-email", "ANN@example.com"); len(rows) != 1 || !strings.Contains(rows[0], "ann@example.com") {
		t.Errorf("-email = %q", rows)
	}
	if code, _, _ := runCmd(t, "admin", "users", "-admins", "-account", a.ID); code != 2 {
		t.Errorf("-admins with -account = %d, want 2", code)
	}
	for _, args := range [][]string{{"-status", "bogus"}, {"-account", "bad"}, {"-email", "nope"}} {
		if code, _, _ := runCmd(t, append([]string{"admin", "users"}, args...)...); code != 2 {
			t.Errorf("users %v = %d, want 2", args, code)
		}
	}
}

// TestDisableAndEnable: disable needs -yes, ends the sessions and stops sign-in. Enable restores it. Pending and
// already-set users are refused, and both commands are audited.
func TestDisableAndEnable(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	pending := f.seedUser(acc.ID, "new@example.com", store.RoleMember, store.UserPending, "")
	ts, c := f.serve()
	if code, _ := login(t, ts, c, u.Email, "old passphrase"); code != 200 {
		t.Fatal("sign-in before disable")
	}

	if code, _, _ := runCmd(t, "admin", "disable", "-user", u.ID); code != 2 {
		t.Fatalf("disable without -yes = %d, want 2", code)
	}
	if f.reload(u).Status != store.UserActive {
		t.Fatal("a refused disable changed the status")
	}
	if code, _, errOut := runCmd(t, "admin", "disable", "-user", u.ID, "-yes"); code != 0 {
		t.Fatalf("disable -yes = %d %q", code, errOut)
	}
	if f.reload(u).Status != store.UserDisabled {
		t.Fatal("status is not disabled")
	}
	if code, _ := login(t, ts, c, u.Email, "old passphrase"); code == 200 {
		t.Error("a disabled user signed in")
	}
	// The session the user had before the disable is ended too; c still holds its cookie.
	resp, err := c.Get(ts.URL + "/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("the session from before the disable = %d, want 401", resp.StatusCode)
	}
	if code, _, _ := runCmd(t, "admin", "disable", "-user", u.ID, "-yes"); code != 1 {
		t.Errorf("disable of a disabled user = %d, want 1", code)
	}
	if code, _, _ := runCmd(t, "admin", "enable", "-user", pending.ID); code != 1 {
		t.Errorf("enable of a pending user = %d, want 1", code)
	}
	if code, _, _ := runCmd(t, "admin", "enable", "-user", u.ID); code != 0 {
		t.Fatalf("enable = %d", code)
	}
	if f.reload(u).Status != store.UserActive {
		t.Fatal("status is not active after enable")
	}
	if code, _, _ := runCmd(t, "admin", "enable", "-user", u.ID); code != 1 {
		t.Errorf("enable of an active user = %d, want 1", code)
	}
	if code, uid := login(t, ts, c, u.Email, "old passphrase"); code != 200 || uid != u.ID {
		t.Errorf("sign-in after enable = %d, want 200", code)
	}
	var disabled, enabled bool
	for _, row := range f.auditRows(acc.ID) {
		if row.Target == u.ID && row.Action == "user.disable" && row.Details["via"] == "cli" {
			disabled = true
		}
		if row.Target == u.ID && row.Action == "user.enable" && row.Details["via"] == "cli" {
			enabled = true
		}
	}
	if !disabled || !enabled {
		t.Errorf("audit rows: disable=%v enable=%v", disabled, enabled)
	}
}

// TestLastInstallationAdminCannotBeDisabled: the last active installation admin is refused, and a second one lets the
// first go.
func TestLastInstallationAdminCannotBeDisabled(t *testing.T) {
	f := newRecoveryFixture(t)
	first := f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	if code, _, errOut := runCmd(t, "admin", "disable", "-user", first.ID, "-yes"); code != 1 || !strings.Contains(errOut, "last") {
		t.Fatalf("disable of the last admin = %d %q; want 1 naming the last admin", code, errOut)
	}
	if f.reload(first).Status != store.UserActive {
		t.Fatal("the last admin was disabled")
	}
	second := f.seedUser("", "second@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	if code, _, errOut := runCmd(t, "admin", "disable", "-user", first.ID, "-yes"); code != 0 {
		t.Fatalf("disable with another admin = %d %q", code, errOut)
	}
	if code, _, _ := runCmd(t, "admin", "disable", "-user", second.ID, "-yes"); code != 1 {
		t.Errorf("disable of the new last admin = %d, want 1", code)
	}
}

// TestEndSessionsEndsSessionsTokensAndImpersonations: end-sessions needs -yes, ends the browser session, and the
// impersonation of the user, and is audited.
func TestEndSessionsEndsSessionsTokensAndImpersonations(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	admin := f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	ts, c := f.serve()
	if code, _ := login(t, ts, c, u.Email, "old passphrase"); code != 200 {
		t.Fatal("sign-in")
	}
	_, hash := auth.NewToken()
	now := store.Now()
	imp := &store.Session{IDHash: hash, UserID: u.ID, AccountID: acc.ID, CSRFToken: "x", CreatedAt: now,
		LastSeenAt: now, ExpiresAt: now.Add(time.Hour), ImpersonatorID: admin.ID}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error { return tx.InsertSession(f.ctx, imp) }); err != nil {
		t.Fatal(err)
	}

	if code, _, _ := runCmd(t, "admin", "end-sessions", "-user", u.ID); code != 2 {
		t.Fatalf("end-sessions without -yes = %d, want 2", code)
	}
	if code, _, errOut := runCmd(t, "admin", "end-sessions", "-user", u.ID, "-yes"); code != 0 {
		t.Fatalf("end-sessions = %d %q", code, errOut)
	}
	if _, err := f.st.SessionByHash(f.ctx, hash); err == nil {
		t.Error("the impersonation survived end-sessions")
	}
	resp, err := c.Get(ts.URL + "/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("the ended session = %d, want 401", resp.StatusCode)
	}
	var audited bool
	for _, row := range f.auditRows(acc.ID) {
		if row.Action == "user.sessions_ended" && row.Target == u.ID && row.Details["via"] == "cli" {
			audited = true
		}
	}
	if !audited {
		t.Error("no user.sessions_ended audit row with via=cli")
	}
}

// TestAdminUsageListsTheCommands: the usage text names every command of this slice.
func TestAdminUsageListsTheCommands(t *testing.T) {
	_, out, _ := runCmd(t, "admin", "help")
	for _, cmd := range []string{"reset-password", "verify", "users", "enable", "disable", "end-sessions"} {
		if !strings.Contains(out, "  "+cmd) {
			t.Errorf("usage does not list %s", cmd)
		}
	}
}
