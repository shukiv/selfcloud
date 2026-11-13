// SPDX-License-Identifier: AGPL-3.0-or-later

package signin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/profile"
)

// testToken is a device token of the shape the server makes. The fake server hands it out; no test makes a real one.
const testToken = "scd_TESTDEVICE_0123456789abcdefghijklmnopqrstuvwxyz"

// script is what the fake server answers. Polls are answered in order: an error code, or "" for the token. It records
// every request as a method, a path and the bearer credential it carried, and every body it wrote.
type script struct {
	polls   []string
	revoke  int
	folders int
	mu      sync.Mutex
	seen    []string
	bodies  strings.Builder
}

func newFake(t *testing.T, sc *script) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc.mu.Lock()
		sc.seen = append(sc.seen, r.Method+" "+r.URL.Path+" bearer="+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		sc.mu.Unlock()
		var body string
		status := http.StatusOK
		switch r.URL.Path {
		case "/api/v1/auth/cli/start":
			status = http.StatusCreated
			body = `{"device_code":"` + strings.Repeat("A", 43) + `","user_code":"BCDF-GHJK",` +
				`"verification_url":"` + "http://127.0.0.1/cli-approve" + `","interval":5,"expires_in":600}`
		case "/api/v1/auth/cli/poll":
			sc.mu.Lock()
			ans := sc.polls[0]
			if len(sc.polls) > 1 {
				sc.polls = sc.polls[1:]
			}
			sc.mu.Unlock()
			if ans == "" {
				body = `{"token":"` + testToken + `","expires_at":"2099-01-01T00:00:00Z"}`
			} else {
				status = http.StatusBadRequest
				body = `{"error":{"code":"` + ans + `","message":"the poll answer"}}`
			}
		case "/api/v1/auth/cli/revoke":
			status = sc.revoke
			if status != http.StatusNoContent {
				body = `{"error":{"code":"unauthenticated","message":"the token is not valid"}}`
			}
		case "/api/v1/folders":
			items := make([]string, 0, sc.folders)
			for range sc.folders {
				items = append(items, `{"id":"fld_x","name":"x"}`)
			}
			body = `{"items":[` + strings.Join(items, ",") + `]}`
		default:
			status = http.StatusNotFound
			body = `{"error":{"code":"not_found","message":"no such endpoint"}}`
		}
		sc.mu.Lock()
		sc.bodies.WriteString(body)
		sc.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusNoContent {
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// isolate points the profiles at a temporary directory, clears the environment the commands read, and makes the poll wait
// a no-op. The tests run one at a time, because the sleep and the environment are shared.
func isolate(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "profiles")
	t.Setenv("SELFCLOUD_PROFILES_DIR", dir)
	t.Setenv("SELFCLOUD_PROFILE", "")
	t.Setenv("SELFCLOUD_URL", "")
	t.Setenv("SELFCLOUD_TOKEN", "")
	saved := sleep
	sleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { sleep = saved })
	return dir
}

// saveProfile writes one profile straight into the profiles file, as an earlier login would have.
func saveProfile(t *testing.T, dir, name, kind, tok, url string) {
	t.Helper()
	s := profile.Open(dir)
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	f.Profiles[name] = profile.Profile{URL: url, Kind: kind, Token: tok}
	if f.Current == "" {
		f.Current = name
	}
	if err := s.Save(f); err != nil {
		t.Fatal(err)
	}
}

// loadProfiles reads the profiles file.
func loadProfiles(t *testing.T, dir string) profile.File {
	t.Helper()
	f, err := profile.Open(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// noToken fails the test if the token appears in any of the outputs.
func noToken(t *testing.T, outs ...*bytes.Buffer) {
	t.Helper()
	for _, o := range outs {
		if strings.Contains(o.String(), testToken) || strings.Contains(o.String(), "TESTDEVICE") {
			t.Errorf("an output holds the device token: %q", o.String())
		}
	}
}

func TestLoginSavesTheDeviceToken(t *testing.T) {
	dir := isolate(t)
	sc := &script{polls: []string{"authorization_pending", "slow_down", ""}}
	srv := newFake(t, sc)
	var out, errOut bytes.Buffer
	if code := Login([]string{"--url", srv.URL, "--profile", "work"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), errOut.String())
	}
	f := loadProfiles(t, dir)
	p, ok := f.Profiles["work"]
	if !ok || p.Kind != profile.KindDevice || p.Token != testToken || p.URL != srv.URL {
		t.Fatalf("profile work = %+v, ok %v; want the device token at the server's address", p, ok)
	}
	if f.Current != "work" {
		t.Errorf("current profile = %q, want work", f.Current)
	}
	if got := strings.Count(strings.Join(sc.seen, "\n"), "POST /api/v1/auth/cli/poll"); got != 3 {
		t.Errorf("polls = %d, want 3 (pending, slow down, then the token)", got)
	}
	if !strings.Contains(errOut.String(), "BCDF-GHJK") || !strings.Contains(errOut.String(), "/cli-approve") {
		t.Errorf("the code and the page are not shown on standard error: %q", errOut.String())
	}
	if !strings.Contains(out.String(), "signed in: profile work (kind device)") {
		t.Errorf("summary = %q", out.String())
	}
	noToken(t, &out, &errOut)
}

// The --json answer names the profile, the address, the kind and the expiry, and no token.
func TestLoginJSONCarriesNoToken(t *testing.T) {
	dir := isolate(t)
	srv := newFake(t, &script{polls: []string{""}})
	var out, errOut bytes.Buffer
	if code := Login([]string{"--url", srv.URL, "--profile", "work", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v: %q", err, out.String())
	}
	for _, k := range []string{"profile", "url", "kind", "expires_at"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("the JSON has no %q: %v", k, doc)
		}
	}
	if _, ok := doc["token"]; ok {
		t.Error("the JSON carries a token field")
	}
	noToken(t, &out, &errOut)
	if _, ok := loadProfiles(t, dir).Profiles["work"]; !ok {
		t.Error("the profile was not saved")
	}
}

// An expired sign-in ends the command with exit 3 and no profile: the person must start again.
func TestLoginEndsWhenTheSignInExpires(t *testing.T) {
	dir := isolate(t)
	srv := newFake(t, &script{polls: []string{"authorization_pending", "expired_token"}})
	var out, errOut bytes.Buffer
	if code := Login([]string{"--url", srv.URL, "--profile", "work"}, &out, &errOut); code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
	if _, ok := loadProfiles(t, dir).Profiles["work"]; ok {
		t.Error("a profile was saved for an ended sign-in")
	}
	if !strings.Contains(errOut.String(), "run login again") {
		t.Errorf("the message does not say what to do: %q", errOut.String())
	}
	noToken(t, &out, &errOut)
}

// login refuses to overwrite a profile, and says what to do; it makes no request first.
func TestLoginRefusesAnExistingProfile(t *testing.T) {
	dir := isolate(t)
	saveProfile(t, dir, "work", profile.KindAccount, "sc_acct_existing_key_value_0123456789abcdefghijk", "https://old.example")
	sc := &script{polls: []string{""}}
	srv := newFake(t, sc)
	var out, errOut bytes.Buffer
	if code := Login([]string{"--url", srv.URL, "--profile", "work"}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if len(sc.seen) != 0 {
		t.Errorf("requests were made for a refused login: %v", sc.seen)
	}
	if !strings.Contains(errOut.String(), "profile rm") {
		t.Errorf("the refusal does not say how to proceed: %q", errOut.String())
	}
}

func TestLoginNeedsAnAddress(t *testing.T) {
	isolate(t)
	var out, errOut bytes.Buffer
	if code := Login(nil, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

// A plain http address that is not loopback is refused before anything is sent (the client's own rule).
func TestLoginRefusesPlainHTTPOffLoopback(t *testing.T) {
	dir := isolate(t)
	var out, errOut bytes.Buffer
	if code := Login([]string{"--url", "http://192.0.2.10", "--profile", "work"}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut.String())
	}
	if _, ok := loadProfiles(t, dir).Profiles["work"]; ok {
		t.Error("a profile was saved")
	}
}

// logout ends the token on the server, with the token as the bearer, and removes the profile.
func TestLogoutEndsTheTokenAndRemovesTheProfile(t *testing.T) {
	dir := isolate(t)
	sc := &script{revoke: http.StatusNoContent}
	srv := newFake(t, sc)
	saveProfile(t, dir, "work", profile.KindDevice, testToken, srv.URL)
	var out, errOut bytes.Buffer
	if code := Logout([]string{"--profile", "work"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	want := "POST /api/v1/auth/cli/revoke bearer=" + testToken
	if len(sc.seen) != 1 || sc.seen[0] != want {
		t.Errorf("requests = %v, want the one revoke with the token", sc.seen)
	}
	if _, ok := loadProfiles(t, dir).Profiles["work"]; ok {
		t.Error("the profile is still there")
	}
	if !strings.Contains(out.String(), "signed out") {
		t.Errorf("summary = %q", out.String())
	}
	noToken(t, &out, &errOut)
}

// A server that cannot be reached keeps the token, so logout can be run again.
func TestLogoutKeepsTheTokenWhenTheServerCannotBeReached(t *testing.T) {
	dir := isolate(t)
	srv := newFake(t, &script{revoke: http.StatusNoContent})
	saveProfile(t, dir, "work", profile.KindDevice, testToken, srv.URL)
	srv.Close()
	var out, errOut bytes.Buffer
	if code := Logout([]string{"--profile", "work"}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if p, ok := loadProfiles(t, dir).Profiles["work"]; !ok || p.Token != testToken {
		t.Error("the token was removed although the server did not end it")
	}
	if !strings.Contains(errOut.String(), "kept") {
		t.Errorf("the message does not say the token is kept: %q", errOut.String())
	}
	noToken(t, &out, &errOut)
}

// A token the server already refuses is removed locally, and the answer says the server had ended it.
func TestLogoutRemovesAnEndedToken(t *testing.T) {
	dir := isolate(t)
	srv := newFake(t, &script{revoke: http.StatusUnauthorized})
	saveProfile(t, dir, "work", profile.KindDevice, testToken, srv.URL)
	var out, errOut bytes.Buffer
	if code := Logout([]string{"--profile", "work"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if _, ok := loadProfiles(t, dir).Profiles["work"]; ok {
		t.Error("the profile is still there")
	}
	if !strings.Contains(out.String(), "already ended") {
		t.Errorf("summary = %q", out.String())
	}
	noToken(t, &out, &errOut)
}

// logout signs out a device token only. A key is refused before any request, and the profile stays.
func TestLogoutRefusesAKey(t *testing.T) {
	dir := isolate(t)
	sc := &script{revoke: http.StatusNoContent}
	srv := newFake(t, sc)
	const key = "sc_acct_key_value_0123456789abcdefghijklmnopqrstuvwxyz"
	saveProfile(t, dir, "work", profile.KindAccount, key, srv.URL)
	var out, errOut bytes.Buffer
	if code := Logout([]string{"--profile", "work"}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if len(sc.seen) != 0 {
		t.Errorf("a request was made for a key: %v", sc.seen)
	}
	if _, ok := loadProfiles(t, dir).Profiles["work"]; !ok {
		t.Error("the key's profile was removed")
	}
	if strings.Contains(errOut.String(), key) || strings.Contains(out.String(), key) {
		t.Error("the key appears in an output")
	}
}

// whoami reads the folders the token reaches and prints the count; it prints no token.
func TestWhoamiCountsTheFolders(t *testing.T) {
	dir := isolate(t)
	sc := &script{folders: 2}
	srv := newFake(t, sc)
	saveProfile(t, dir, "work", profile.KindDevice, testToken, srv.URL)
	var out, errOut bytes.Buffer
	if code := Whoami([]string{"--profile", "work"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "folders") || !strings.Contains(out.String(), "2") {
		t.Errorf("output = %q", out.String())
	}
	if len(sc.seen) != 1 || sc.seen[0] != "GET /api/v1/folders bearer="+testToken {
		t.Errorf("requests = %v", sc.seen)
	}
	noToken(t, &out, &errOut)
}

func TestWhoamiJSON(t *testing.T) {
	dir := isolate(t)
	srv := newFake(t, &script{folders: 3})
	saveProfile(t, dir, "work", profile.KindDevice, testToken, srv.URL)
	var out, errOut bytes.Buffer
	if code := Whoami([]string{"--profile", "work", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var doc struct {
		Profile string `json:"profile"`
		Kind    string `json:"kind"`
		Folders int    `json:"folders"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if doc.Profile != "work" || doc.Kind != profile.KindDevice || doc.Folders != 3 {
		t.Errorf("document = %+v", doc)
	}
	noToken(t, &out, &errOut)
}

// login sends no credential at all. An environment token and a stored key are both present here, and neither reaches the
// server: the sign-in is not made with a key, and the new address is not given the old key (decision 125).
func TestLoginSendsNoCredential(t *testing.T) {
	dir := isolate(t)
	const key = "sc_acct_key_value_0123456789abcdefghijklmnopqrstuvwxyz"
	t.Setenv("SELFCLOUD_TOKEN", key)
	saveProfile(t, dir, "old", profile.KindAccount, key, "https://old.example")
	sc := &script{polls: []string{""}}
	srv := newFake(t, sc)
	var out, errOut bytes.Buffer
	if code := Login([]string{"--url", srv.URL, "--profile", "work"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, line := range sc.seen {
		if !strings.HasSuffix(line, "bearer=") {
			t.Errorf("a sign-in request carried a credential: %q", line)
		}
	}
	if len(sc.seen) != 2 {
		t.Errorf("requests = %v, want the start and one poll", sc.seen)
	}
	if !strings.Contains(errOut.String(), "SELFCLOUD_TOKEN is set; login ignores it") {
		t.Errorf("the environment token is not reported: %q", errOut.String())
	}
	noToken(t, &out, &errOut)
	if strings.Contains(errOut.String()+out.String(), key) {
		t.Error("the environment key appears in an output")
	}
}

// login does not read a credential from a file either: it signs a new device in, so there is no key to read.
func TestLoginRefusesTokenFile(t *testing.T) {
	isolate(t)
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte("sc_acct_key_value_0123456789abcdefghijklmnopqrstuvwxyz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := &script{polls: []string{""}}
	srv := newFake(t, sc)
	var out, errOut bytes.Buffer
	if code := Login([]string{"--url", srv.URL, "--token-file", file}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if len(sc.seen) != 0 {
		t.Errorf("requests were made with a token file given: %v", sc.seen)
	}
}

// logout and whoami send the profile's token to the profile's own address only. An address given on the command line
// that is not the profile's is refused before any request.
func TestProfileCommandsNeverSendToAnotherAddress(t *testing.T) {
	dir := isolate(t)
	sc := &script{revoke: http.StatusNoContent, folders: 1}
	srv := newFake(t, sc)
	saveProfile(t, dir, "work", profile.KindDevice, testToken, srv.URL)
	for name, run := range map[string]func([]string, *bytes.Buffer, *bytes.Buffer) int{
		"logout": func(a []string, o, e *bytes.Buffer) int { return Logout(a, o, e) },
		"whoami": func(a []string, o, e *bytes.Buffer) int { return Whoami(a, o, e) },
	} {
		var out, errOut bytes.Buffer
		if code := run([]string{"--profile", "work", "--url", "http://127.0.0.1:1"}, &out, &errOut); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
		noToken(t, &out, &errOut)
	}
	if len(sc.seen) != 0 {
		t.Errorf("a request was made to the profile's address with the other address given: %v", sc.seen)
	}
	if _, ok := loadProfiles(t, dir).Profiles["work"]; !ok {
		t.Error("the profile was removed by a refused command")
	}
}

// logout revokes the profile's own device token, not the one in the environment, and only at the profile's address.
func TestLogoutUsesTheProfilesOwnToken(t *testing.T) {
	dir := isolate(t)
	const envToken = "scd_ENVIRONMENT_0123456789abcdefghijklmnopqrstuvwxyz"
	t.Setenv("SELFCLOUD_TOKEN", envToken)
	sc := &script{revoke: http.StatusNoContent}
	srv := newFake(t, sc)
	saveProfile(t, dir, "work", profile.KindDevice, testToken, srv.URL)
	var out, errOut bytes.Buffer
	if code := Logout([]string{"--profile", "work"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if len(sc.seen) != 1 || sc.seen[0] != "POST /api/v1/auth/cli/revoke bearer="+testToken {
		t.Errorf("requests = %v, want one revoke with the profile's token", sc.seen)
	}
	if strings.Contains(strings.Join(sc.seen, "\n"), "ENVIRONMENT") {
		t.Error("the environment's token was sent")
	}
}

// The failing paths, run with the token known, name no token: an expired sign-in, an unreachable server, and a whoami the
// server refuses.
func TestFailingPathsNameNoToken(t *testing.T) {
	dir := isolate(t)
	srv := newFake(t, &script{polls: []string{"expired_token"}, revoke: http.StatusNoContent})
	saveProfile(t, dir, "work", profile.KindDevice, testToken, srv.URL)
	var out, errOut bytes.Buffer
	Login([]string{"--url", srv.URL, "--profile", "other"}, &out, &errOut)
	noToken(t, &out, &errOut)
	out.Reset()
	errOut.Reset()
	Whoami([]string{"--profile", "missing"}, &out, &errOut)
	noToken(t, &out, &errOut)
	out.Reset()
	errOut.Reset()
	srv.Close()
	Logout([]string{"--profile", "work"}, &out, &errOut)
	noToken(t, &out, &errOut)
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("a failed logout wrote to stdout: %q", out.String())
	}
}
