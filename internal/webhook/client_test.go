// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeResolver answers names from a table. A name mapped to nil does not resolve. Every lookup is counted, so a test can
// see how many times a name was resolved.
type fakeResolver struct {
	hosts map[string][]netip.Addr
	calls atomic.Int32
}

func (r *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.calls.Add(1)
	a, ok := r.hosts[host]
	if !ok || len(a) == 0 {
		return nil, errors.New("no such host")
	}
	return a, nil
}

var (
	loopback = netip.MustParseAddr("127.0.0.1")
	private  = netip.MustParseAddr("10.0.0.5")
	public   = netip.MustParseAddr("93.184.216.34")
)

// loopbackOnly is a client that may reach 127.0.0.1 and nothing else in the table, by the name "receiver.test".
func loopbackOnly(t *testing.T, hosts map[string][]netip.Addr, roots *x509.CertPool) (*Client, *fakeResolver) {
	t.Helper()
	if hosts == nil {
		hosts = map[string][]netip.Addr{}
	}
	r := &fakeResolver{hosts: hosts}
	c := NewClient(ClientOptions{Resolver: r, Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		AllowHTTP: true, RootCAs: roots})
	return c, r
}

// A name that resolves to a public address first and a private one second is refused: one refused address refuses the
// whole host, so the second answer cannot be dialled.
func TestMixedAnswerIsRefused(t *testing.T) {
	t.Parallel()
	c, _ := loopbackOnly(t, map[string][]netip.Addr{"mixed.test": {public, private}}, nil)
	res := c.Post(context.Background(), "http://mixed.test/in", http.Header{}, []byte("{}"))
	if res.Err != "address_refused" || res.Code != 0 {
		t.Fatalf("result = %+v, want address_refused with no status", res)
	}
	if err := c.CheckURL(context.Background(), "mixed.test"); err != ErrRefused {
		t.Fatalf("CheckURL = %v, want ErrRefused", err)
	}
}

// Rebinding: a name that is public when the endpoint is registered and private at delivery is refused at delivery. The
// delivery looks the name up once, and dials the address it checked.
func TestRebindingRefusedAtDelivery(t *testing.T) {
	t.Parallel()
	hosts := map[string][]netip.Addr{"rebind.test": {public}}
	c, r := loopbackOnly(t, hosts, nil)
	if err := c.CheckURL(context.Background(), "rebind.test"); err != nil {
		t.Fatalf("registration of a public name: %v", err)
	}
	hosts["rebind.test"] = []netip.Addr{private}
	before := r.calls.Load()
	res := c.Post(context.Background(), "http://rebind.test/in", http.Header{}, []byte("{}"))
	if res.Err != "address_refused" {
		t.Fatalf("delivery after rebinding = %+v, want address_refused", res)
	}
	if n := r.calls.Load() - before; n != 1 {
		t.Fatalf("the delivery looked the name up %d times, want once", n)
	}
}

// Each address of the deny table, given as a URL host, is refused at once, before any connection.
func TestDeniedHostLiteralsRefused(t *testing.T) {
	t.Parallel()
	c, _ := loopbackOnly(t, nil, nil)
	for _, host := range []string{"10.0.0.1", "169.254.169.254", "[::1]", "[fe80::1]", "[fd00::1]", "[::ffff:10.0.0.1]",
		"[64:ff9b::a00:1]", "[2002:0a00:0001::]", "100.64.0.1", "224.0.0.1", "0.0.0.0", "[::]"} {
		res := c.Post(context.Background(), "https://"+host+"/in", http.Header{}, nil)
		if res.Err != "address_refused" {
			t.Errorf("%s: result %+v, want address_refused", host, res)
		}
		if err := c.CheckURL(context.Background(), strings.Trim(host, "[]")); err != ErrRefused {
			t.Errorf("%s: CheckURL = %v, want ErrRefused", host, err)
		}
	}
}

// A name the stub does not hold does not resolve, and the check says so with the same error a denied address gets.
func TestUnresolvedNameIsRefused(t *testing.T) {
	t.Parallel()
	c, _ := loopbackOnly(t, nil, nil)
	if err := c.CheckURL(context.Background(), "nothing.test"); err != ErrResolve {
		t.Fatalf("CheckURL = %v, want ErrResolve", err)
	}
	res := c.Post(context.Background(), "http://nothing.test/in", http.Header{}, nil)
	if res.Err != "resolve" {
		t.Fatalf("result = %+v, want resolve", res)
	}
}

// The lookup at registration is bounded: a resolver that never answers is given up after RegisterTimeout.
func TestRegistrationLookupIsBounded(t *testing.T) {
	t.Parallel()
	c := NewClient(ClientOptions{Resolver: blockingResolver{}})
	start := time.Now()
	err := c.CheckURL(context.Background(), "slow.test")
	if err != ErrResolve {
		t.Fatalf("CheckURL = %v, want ErrResolve", err)
	}
	if d := time.Since(start); d < RegisterTimeout || d > RegisterTimeout+2*time.Second {
		t.Fatalf("the lookup took %v, want about %v", d, RegisterTimeout)
	}
}

type blockingResolver struct{}

func (blockingResolver) LookupNetIP(ctx context.Context, _, _ string) ([]netip.Addr, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// A redirect is the answer. The target is not connected to, so the receiver never sees the second request.
func TestRedirectIsAFailure(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "http://10.0.0.1/elsewhere", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	c, _ := loopbackOnly(t, map[string][]netip.Addr{"receiver.test": {loopback}}, nil)
	res := c.Post(context.Background(), "http://receiver.test:"+portOf(t, srv)+"/in", http.Header{}, []byte("{}"))
	if res.Code != http.StatusFound || res.Err != "redirect" {
		t.Fatalf("result = %+v, want a 302 failure", res)
	}
	if hits.Load() != 1 {
		t.Fatalf("the receiver saw %d requests, want 1", hits.Load())
	}
}

// Only the first ReadLimit bytes of an answer are read. An answer that streams on is cut off, and its status is kept.
func TestAnswerBodyIsCapped(t *testing.T) {
	t.Parallel()
	var written atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1024)
		for i := 0; i < 1<<20; i++ { // up to 1 GiB, if nothing stops it
			n, err := w.Write(chunk)
			written.Add(int64(n))
			if err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	c, _ := loopbackOnly(t, map[string][]netip.Addr{"receiver.test": {loopback}}, nil)
	start := time.Now()
	res := c.Post(context.Background(), "http://receiver.test:"+portOf(t, srv)+"/in", http.Header{}, nil)
	if res.Code != http.StatusOK || res.Err != "" {
		t.Fatalf("result = %+v, want a 200", res)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the answer took %v; the body was not cut off", d)
	}
}

// A server that accepts and then never answers is cut off by the attempt's deadline, and the failure is "timeout".
func TestStalledAnswerTimesOut(t *testing.T) {
	t.Parallel()
	stall := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-stall
	}))
	t.Cleanup(func() { close(stall); srv.Close() })
	c, _ := loopbackOnly(t, map[string][]netip.Addr{"receiver.test": {loopback}}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res := c.Post(ctx, "http://receiver.test:"+portOf(t, srv)+"/in", http.Header{}, nil)
	if res.Err != "timeout" {
		t.Fatalf("result = %+v, want timeout", res)
	}
}

// The attempt's deadline is ten seconds, and the client's timeout is the same.
func TestAttemptDeadline(t *testing.T) {
	t.Parallel()
	if Timeout != 10*time.Second || MaxConns != 8 || ReadLimit != 4<<10 {
		t.Fatalf("limits are %v, %d, %d; the design says 10 s, 8 connections and 4 KiB", Timeout, MaxConns, ReadLimit)
	}
}

// TLS verifies the certificate for the URL's name, while the connection goes to the checked address. The name is
// example.com, which the test certificate holds; the address is loopback.
func TestTLSVerifiesTheURLName(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	c, _ := loopbackOnly(t, map[string][]netip.Addr{"example.com": {loopback}}, roots)
	port := portOf(t, srv)
	if res := c.Post(context.Background(), "https://example.com:"+port+"/in", http.Header{}, nil); res.Code != http.StatusNoContent {
		t.Fatalf("a matching name: %+v", res)
	}
	// The same server under another name: the certificate does not match it, and the failure is a TLS one.
	c2, _ := loopbackOnly(t, map[string][]netip.Addr{"other.test": {loopback}}, roots)
	if res := c2.Post(context.Background(), "https://other.test:"+port+"/in", http.Header{}, nil); res.Err != "tls" {
		t.Fatalf("a name the certificate does not hold: %+v, want tls", res)
	}
}

// An error text can hold the URL, which carries a token. A connection refused on a closed port returns a fixed token,
// and neither the token nor the URL appears in it.
func TestTransportErrorNeverCarriesTheURL(t *testing.T) {
	t.Parallel()
	// Port 1 is refused. A port picked by listening and closing can be reused by another server, and then this would not fail.
	c, _ := loopbackOnly(t, map[string][]netip.Addr{"receiver.test": {loopback}}, nil)
	raw := "http://receiver.test:1/services/PATHTOKEN?token=QUERYTOKEN"
	res := c.Post(context.Background(), raw, http.Header{}, nil)
	if res.Err != "connect" {
		t.Fatalf("result = %+v, want connect", res)
	}
	if strings.Contains(res.Err, "TOKEN") || strings.Contains(res.Err, "receiver") {
		t.Fatalf("the failure text carries the URL: %q", res.Err)
	}
}

// A URL that is not https (or http with the operator's allowance) is not connected to at all.
func TestSchemeIsChecked(t *testing.T) {
	t.Parallel()
	c := NewClient(ClientOptions{Resolver: &fakeResolver{}})
	for _, raw := range []string{"http://hooks.test/in", "ftp://hooks.test/in", "gopher://x/"} {
		if res := c.Post(context.Background(), raw, http.Header{}, nil); res.Err != "url" {
			t.Errorf("%s: %+v, want url", raw, res)
		}
	}
}

// Proxies are never used: the transport has none, so a proxy variable in the environment carries nothing past the check.
// The test sets the variable to a closed port; a request that used it would fail.
func TestProxyEnvironmentIsIgnored(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	c, _ := loopbackOnly(t, map[string][]netip.Addr{"receiver.test": {loopback}}, nil)
	if res := c.Post(context.Background(), "http://receiver.test:"+portOf(t, srv)+"/in", http.Header{}, nil); res.Code != http.StatusAccepted {
		t.Fatalf("the request did not reach the receiver: %+v", res)
	}
	if c.http.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("the transport has a proxy")
	}
}

// The status classes: 2xx is success, a 4xx or 5xx is a failure that carries its code, and 410 is returned as such.
func TestStatusClasses(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(atoiStatus(r.URL.Path))
	}))
	t.Cleanup(srv.Close)
	c, _ := loopbackOnly(t, map[string][]netip.Addr{"receiver.test": {loopback}}, nil)
	base := "http://receiver.test:" + portOf(t, srv)
	want := map[string]Result{
		"/200": {Code: 200}, "/204": {Code: 204}, "/410": {Code: 410, Err: "status 410"},
		"/503": {Code: 503, Err: "status 503"},
	}
	for path, w := range want {
		if got := c.Post(context.Background(), base+path, http.Header{}, nil); got != w {
			t.Errorf("%s: %+v, want %+v", path, got, w)
		}
	}
}

// --- small helpers ---

func portOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func atoiStatus(path string) int {
	code := 0
	for _, c := range strings.TrimPrefix(path, "/") {
		code = code*10 + int(c-'0')
	}
	return code
}
