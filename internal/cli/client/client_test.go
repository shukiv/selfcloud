// SPDX-License-Identifier: AGPL-3.0-or-later

package client

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// secret is a credential-shaped value. No error, log line or output in these tests may contain it.
const secret = "sc_acct_SECRETVALUE0123456789"

// fakeClock records the waits the client asks for, instead of waiting.
type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (f *fakeClock) Now() time.Time { return f.now }

func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	f.sleeps = append(f.sleeps, d)
	return ctx.Err()
}

func newClient(t *testing.T, base string, mutate ...func(*Options)) *Client {
	t.Helper()
	o := Options{BaseURL: base, Credential: secret}
	for _, f := range mutate {
		f(&o)
	}
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustNotContainSecret(t *testing.T, what string, s string) {
	t.Helper()
	if strings.Contains(s, secret) {
		t.Errorf("%s contains the credential: %q", what, s)
	}
}

func TestBaseURLRules(t *testing.T) {
	t.Parallel()
	ok := []struct {
		raw      string
		insecure bool
	}{
		{"https://selfcloud.example", false},
		{"https://selfcloud.example/prefix/", false},
		{"http://127.0.0.1:8080", false},
		{"http://localhost:8080", false},
		{"http://[::1]:8080", false},
		{"http://selfcloud.example", true}, // --insecure-http
	}
	for _, c := range ok {
		if _, err := ParseBaseURL(c.raw, c.insecure); err != nil {
			t.Errorf("ParseBaseURL(%q, %v) = %v, want ok", c.raw, c.insecure, err)
		}
	}
	bad := []string{
		"http://selfcloud.example",       // plain http to a name, without --insecure-http
		"http://192.0.2.3",               // plain http to an address that is not loopback
		"ftp://selfcloud.example",        // another scheme
		"selfcloud.example",              // no scheme
		"https://",                       // no host
		"https://selfcloud.example/?a=1", // a query
		"https://selfcloud.example/#x",   // a fragment
	}
	for _, raw := range bad {
		if _, err := ParseBaseURL(raw, false); err == nil {
			t.Errorf("ParseBaseURL(%q) = ok, want a refusal", raw)
		}
	}
}

func TestURLWithCredentialIsRefusedAndNotEchoed(t *testing.T) {
	t.Parallel()
	raw := "https://admin:" + secret + "@selfcloud.example"
	_, err := ParseBaseURL(raw, false)
	if err == nil {
		t.Fatal("a URL with a user name and password was accepted")
	}
	mustNotContainSecret(t, "the refusal", err.Error())
}

func TestPathRules(t *testing.T) {
	t.Parallel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	for _, p := range []string{"api/v1/info", "/api/v1/../info", "/a/..", "/a?b=c", "/a#b"} {
		if _, err := c.Do(context.Background(), Request{Method: "GET", Path: p}); err == nil {
			t.Errorf("path %q was accepted", p)
		}
	}
	if calls != 0 {
		t.Errorf("a refused path still made %d request(s)", calls)
	}
}

func TestCredentialIsSentOnlyInTheAuthorizationHeader(t *testing.T) {
	t.Parallel()
	var gotAuth, gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotURL = r.URL.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info", Query: map[string][]string{"a": {"b"}}}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer "+secret {
		t.Errorf("Authorization = %q, want the Bearer credential", gotAuth)
	}
	mustNotContainSecret(t, "the request URL", gotURL)
}

func TestNoCredentialMeansNoAuthorizationHeader(t *testing.T) {
	t.Parallel()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, func(o *Options) { o.Credential = "" })
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want none", gotAuth)
	}
}

func TestTLSIsRefusedByDefault(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"})
	var te *TransportError
	if !errors.As(err, &te) || te.Kind != KindTLS {
		t.Fatalf("err = %v, want a TLS transport error", err)
	}
	mustNotContainSecret(t, "the TLS error", err.Error())
}

func TestCAFileAcceptsThePrivateRoot(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	c := newClient(t, srv.URL, func(o *Options) { o.CAFile = caPath })
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"}); err != nil {
		t.Fatalf("with --ca-file the self-signed server was refused: %v", err)
	}
}

func TestCAFileWithNoCertificateIsRefused(t *testing.T) {
	t.Parallel()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{BaseURL: "https://selfcloud.example", CAFile: caPath}); err == nil {
		t.Error("a CA file with no certificate was accepted")
	}
}

func TestInsecureSkipVerifyAcceptsTheServer(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, func(o *Options) { o.InsecureSkipVerify = true })
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"}); err != nil {
		t.Fatalf("with skip-verify the server was refused: %v", err)
	}
}

// TestTLSVerificationIsNotChangedByAProxy checks the transport's settings, not a live proxy: Go exempts loopback from the
// proxy environment, and the proxy variables are read once per process. The proxy is set, and verification stays as the
// options say.
func TestTLSVerificationIsNotChangedByAProxy(t *testing.T) {
	t.Parallel()
	for _, skip := range []bool{false, true} {
		c := newClient(t, "https://selfcloud.example", func(o *Options) { o.InsecureSkipVerify = skip })
		tr, ok := c.http.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("transport is %T", c.http.Transport)
		}
		if tr.Proxy == nil {
			t.Error("the transport does not use the proxy environment")
		}
		if tr.TLSClientConfig == nil || tr.TLSClientConfig.InsecureSkipVerify != skip {
			t.Errorf("InsecureSkipVerify = %v, want %v", tr.TLSClientConfig.InsecureSkipVerify, skip)
		}
		if tr.TLSClientConfig.MinVersion < 0x0303 {
			t.Errorf("MinVersion = %x, want TLS 1.2 or later", tr.TLSClientConfig.MinVersion)
		}
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	t.Parallel()
	var targetAuth string
	var targetCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls++
		targetAuth = r.Header.Get("Authorization")
	}))
	defer target.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v1/info", http.StatusFound)
	}))
	defer src.Close()
	c := newClient(t, src.URL)
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"}); err == nil {
		t.Fatal("a redirect was followed")
	}
	if targetCalls != 0 || targetAuth != "" {
		t.Errorf("the redirect target was called %d time(s) with Authorization %q", targetCalls, targetAuth)
	}
}

func TestHeaderTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, func(o *Options) { o.HeaderTimeout = 100 * time.Millisecond })
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"})
	var te *TransportError
	if !errors.As(err, &te) || te.Kind != KindTimeout {
		t.Fatalf("err = %v, want a timeout", err)
	}
	mustNotContainSecret(t, "the timeout error", err.Error())
}

func TestOverallTimeoutCoversTheBody(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, func(o *Options) { o.Timeout = 200 * time.Millisecond })
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"})
	var te *TransportError
	if !errors.As(err, &te) || te.Kind != KindTimeout {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

func TestTimeoutsAreSettable(t *testing.T) {
	t.Parallel()
	c := newClient(t, "https://selfcloud.example", func(o *Options) {
		o.ConnectTimeout = 7 * time.Second
		o.HeaderTimeout = 9 * time.Second
		o.Timeout = 11 * time.Second
	})
	tr := c.http.Transport.(*http.Transport)
	if tr.TLSHandshakeTimeout != 7*time.Second || tr.ResponseHeaderTimeout != 9*time.Second || c.http.Timeout != 11*time.Second {
		t.Errorf("timeouts = handshake %v, header %v, overall %v", tr.TLSHandshakeTimeout, tr.ResponseHeaderTimeout, c.http.Timeout)
	}
}

func TestUnreachableIsATransportError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()
	c := newClient(t, addr)
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"})
	var te *TransportError
	if !errors.As(err, &te) || te.Kind != KindUnreachable {
		t.Fatalf("err = %v, want unreachable", err)
	}
	mustNotContainSecret(t, "the unreachable error", err.Error())
	if strings.Contains(err.Error(), addr) {
		t.Errorf("the unreachable error names the address: %q", err.Error())
	}
}

func TestAPIErrorIsDecoded(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"the key is not valid"}}`)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"})
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want an API error", err)
	}
	if ae.Status != http.StatusUnauthorized || ae.Code != "unauthenticated" || ae.Message != "the key is not valid" {
		t.Errorf("APIError = %+v", ae)
	}
	mustNotContainSecret(t, "the API error", err.Error())
}

func TestNonJSONErrorBodyIsNotEchoed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "<html>stack trace with "+secret+"</html>")
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 500 || ae.Code != "" {
		t.Fatalf("err = %v, want a 500 with no code", err)
	}
	mustNotContainSecret(t, "the error", err.Error())
}

func TestFailingPathsNeverCarryTheCredential(t *testing.T) {
	t.Parallel()
	statuses := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError}
	for _, status := range statuses {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":{"code":"forbidden","message":"no"}}`)
		}))
		c := newClient(t, srv.URL)
		_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"})
		if err == nil {
			t.Fatalf("status %d: no error", status)
		}
		mustNotContainSecret(t, "the error", err.Error())
		srv.Close()
	}
}

func TestBodyLimit(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 1<<20)
		for i := 0; i <= MaxResponseBytes>>20; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"}); err == nil {
		t.Error("a response over the limit was read whole")
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	t.Parallel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	clock := &fakeClock{now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	c := newClient(t, srv.URL, func(o *Options) { o.Now = clock.Now; o.Sleep = clock.Sleep })
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(clock.sleeps) != 1 || clock.sleeps[0] != 2*time.Second {
		t.Errorf("calls = %d, sleeps = %v; want 2 calls and one 2s wait", calls, clock.sleeps)
	}
}

func TestRetryAfterHTTPDate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", now.Add(5*time.Second).Format(http.TimeFormat))
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	clock := &fakeClock{now: now}
	c := newClient(t, srv.URL, func(o *Options) { o.Now = clock.Now; o.Sleep = clock.Sleep })
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"}); err != nil {
		t.Fatal(err)
	}
	if len(clock.sleeps) != 1 || clock.sleeps[0] != 5*time.Second {
		t.Errorf("sleeps = %v, want one 5s wait", clock.sleeps)
	}
}

func TestRetryAfterCapIsNotWaited(t *testing.T) {
	t.Parallel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "99999999999999999999")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	clock := &fakeClock{now: time.Now()}
	c := newClient(t, srv.URL, func(o *Options) { o.Now = clock.Now; o.Sleep = clock.Sleep; o.MaxRetryAfter = 30 * time.Second })
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusTooManyRequests || ae.RetryAfter <= 30*time.Second {
		t.Fatalf("err = %v, want a 429 that reports its long Retry-After", err)
	}
	if calls != 1 || len(clock.sleeps) != 0 {
		t.Errorf("calls = %d, sleeps = %v; a Retry-After over the cap must not be waited", calls, clock.sleeps)
	}
}

func TestRetryAfterInvalidAndPastValues(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		header string
		want   time.Duration
	}{
		{"soon", time.Second}, // unparseable: the default backoff
		{"-5", time.Second},   // negative: the default backoff
		{now.Add(-time.Minute).Format(http.TimeFormat), 0}, // a date in the past: no wait
	}
	for _, c := range cases {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				w.Header().Set("Retry-After", c.header)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		clock := &fakeClock{now: now}
		cl := newClient(t, srv.URL, func(o *Options) { o.Now = clock.Now; o.Sleep = clock.Sleep })
		if _, err := cl.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"}); err != nil {
			t.Fatalf("Retry-After %q: %v", c.header, err)
		}
		if len(clock.sleeps) != 1 || clock.sleeps[0] != c.want {
			t.Errorf("Retry-After %q: sleeps = %v, want [%v]", c.header, clock.sleeps, c.want)
		}
		srv.Close()
	}
}

func TestNoRetryForUnsafeMethods(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		clock := &fakeClock{now: time.Now()}
		c := newClient(t, srv.URL, func(o *Options) { o.Now = clock.Now; o.Sleep = clock.Sleep })
		if _, err := c.Do(context.Background(), Request{Method: method, Path: "/api/v1/x", Body: []byte("{}")}); err == nil {
			t.Errorf("%s: no error", method)
		}
		if calls != 1 || len(clock.sleeps) != 0 {
			t.Errorf("%s: calls = %d, sleeps = %v; an unsafe method must not be repeated", method, calls, clock.sleeps)
		}
		srv.Close()
	}
}

func TestRetryIsBounded(t *testing.T) {
	t.Parallel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	clock := &fakeClock{now: time.Now()}
	c := newClient(t, srv.URL, func(o *Options) { o.Now = clock.Now; o.Sleep = clock.Sleep })
	_, err := c.Do(context.Background(), Request{Method: "HEAD", Path: "/api/v1/info"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusTooManyRequests {
		t.Fatalf("err = %v", err)
	}
	if calls != maxAttempts || len(clock.sleeps) != maxAttempts-1 {
		t.Errorf("calls = %d, sleeps = %d; want %d and %d", calls, len(clock.sleeps), maxAttempts, maxAttempts-1)
	}
}

func TestLoopbackIsOnlyTheLoopback(t *testing.T) {
	t.Parallel()
	for host, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "localhost": true, "LOCALHOST": true,
		"192.0.2.1": false, "selfcloud.example": false, "0.0.0.0": false,
	} {
		if got := IsLoopback(host); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}
