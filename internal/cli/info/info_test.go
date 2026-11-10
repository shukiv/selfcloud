// SPDX-License-Identifier: AGPL-3.0-or-later

package info

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const secret = "sc_acct_SECRETVALUE0123456789"

// body is a fixed answer, so the output is the same on every run.
const body = `{"name":"SelfCloud","version":"1.2.3","public_url":"https://drive.example","web_interface":true,` +
	`"brand":{"name":"SelfCloud","accent":"#2563eb","logo_url":null,"artwork":true},"password_min_length":8,` +
	`"limits":{"max_file_size":107374182400,"max_chunk_size":67108864},"sharing_max_expiry_days":30,` +
	`"features":["office_convert"]}`

// isolate gives the test a private profiles directory and clears the variables the resolution reads.
func isolate(t *testing.T) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "profiles")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SELFCLOUD_PROFILES_DIR", dir)
	t.Setenv("SELFCLOUD_PROFILE", "")
	t.Setenv("SELFCLOUD_URL", "")
	t.Setenv("SELFCLOUD_TOKEN", "")
}

func golden(t *testing.T, name string, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs.\n--- got\n%s--- want\n%s", name, got, want)
	}
}

func fakeServer(t *testing.T, status int, reply string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestInfoPrintsKeyValues(t *testing.T) {
	isolate(t)
	srv := fakeServer(t, http.StatusOK, body)
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", srv.URL}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, stderr %q", code, errOut.String())
	}
	golden(t, "info.golden", out.String())
}

func TestInfoJSONIsTheAPIsDocument(t *testing.T) {
	isolate(t)
	srv := fakeServer(t, http.StatusOK, body)
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", srv.URL, "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, stderr %q", code, errOut.String())
	}
	golden(t, "info.json.golden", out.String())
}

func TestInfoNeedsNoCredential(t *testing.T) {
	isolate(t)
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", srv.URL}, &out, &errOut); code != 0 || gotAuth != "" {
		t.Errorf("exit = %d, Authorization = %q; want 0 and none", code, gotAuth)
	}
}

func TestInfoRefusesArguments(t *testing.T) {
	isolate(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"extra"}, &out, &errOut); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestInfoNotJSONIsAFailure(t *testing.T) {
	isolate(t)
	srv := fakeServer(t, http.StatusOK, "<html>")
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", srv.URL, "--json"}, &out, &errOut); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", out.String())
	}
}

// TestInfoFailuresNeverShowTheCredential runs the failing paths with a known key in a token file: a 401, a 500, a
// timeout, a TLS refusal, a refused connection, and an address with the key in it. Nothing printed may hold the key.
func TestInfoFailuresNeverShowTheCredential(t *testing.T) {
	isolate(t)
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tokenPath, 0o600); err != nil {
		t.Fatal(err)
	}

	unauth := fakeServer(t, http.StatusUnauthorized, `{"error":{"code":"unauthenticated","message":"the key is not valid"}}`)
	broken := fakeServer(t, http.StatusInternalServerError, "boom "+secret)
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer tls.Close()
	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	goneURL := gone.URL
	gone.Close()

	cases := []struct {
		name string
		args []string
		exit int
	}{
		{"401", []string{"--url", unauth.URL, "--token-file", tokenPath}, 3},
		{"500", []string{"--url", broken.URL, "--token-file", tokenPath}, 1},
		{"timeout", []string{"--url", slow.URL, "--token-file", tokenPath, "--header-timeout", "100ms"}, 1},
		{"tls", []string{"--url", tls.URL, "--token-file", tokenPath}, 1},
		{"refused", []string{"--url", goneURL, "--token-file", tokenPath}, 1},
		{"bad address", []string{"--url", "https://admin:" + secret + "@x.example", "--token-file", tokenPath}, 2},
		{"bad flag", []string{"--" + secret}, 2},
		{"bad timeout", []string{"--url", unauth.URL, "--timeout", secret}, 2},
	}
	for _, c := range cases {
		for _, asJSON := range []bool{false, true} {
			args := append([]string{}, c.args...)
			if asJSON {
				args = append(args, "--json")
			}
			var out, errOut bytes.Buffer
			code := Run(args, &out, &errOut)
			if code != c.exit {
				t.Errorf("%s (json %v): exit = %d, want %d; stderr %q", c.name, asJSON, code, c.exit, errOut.String())
			}
			if strings.Contains(out.String(), secret) || strings.Contains(errOut.String(), secret) {
				t.Errorf("%s (json %v): the credential was printed: stdout %q, stderr %q", c.name, asJSON, out.String(), errOut.String())
			}
		}
	}
}

func TestInfoJSONErrorIsTheEnvelope(t *testing.T) {
	isolate(t)
	srv := fakeServer(t, http.StatusUnauthorized, `{"error":{"code":"unauthenticated","message":"the key is not valid"}}`)
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", srv.URL, "--json"}, &out, &errOut); code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	want := `{"error":{"code":"unauthenticated","message":"the key is not valid (HTTP 401)"}}` + "\n"
	if errOut.String() != want {
		t.Errorf("stderr = %q, want %q", errOut.String(), want)
	}
}

// TestInfoHeaderTimeoutIsSettable: a server that answers after the header limit gets exit 1 at once.
func TestInfoHeaderTimeoutIsSettable(t *testing.T) {
	isolate(t)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	defer close(release) // runs first, so the handler has returned before srv.Close waits for it
	var out, errOut bytes.Buffer
	start := time.Now()
	code := Run([]string{"--url", srv.URL, "--header-timeout", "100ms"}, &out, &errOut)
	if code != 1 || time.Since(start) > 3*time.Second {
		t.Errorf("exit = %d after %v; want 1 well before the server's answer", code, time.Since(start))
	}
}
