// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"
)

// The one client every webhook connection uses (design section 10, decision 28). It resolves a host once for each
// connection, checks every address it gets, and dials a checked address directly, so no second lookup can return
// something else (DNS rebinding). It uses no proxy and follows no redirect, and it reads at most ReadLimit bytes of an
// answer.

// Limits of one attempt and of the client.
const (
	// Timeout is the whole of one attempt: the lookup, the connect, the handshake, the request and the answer.
	Timeout = 10 * time.Second
	// RegisterTimeout bounds the lookup that checks an endpoint URL when it is registered or changed.
	RegisterTimeout = 5 * time.Second
	// ReadLimit is how much of an answer is read and then discarded.
	ReadLimit = 4 << 10
	// MaxConns is the most connections the client holds to one host at a time.
	MaxConns = 8
)

// ErrRefused means an address of a host is refused. ErrResolve means a host has no address, or its lookup failed. The
// two are never told apart to a caller outside the worker: a refused registration says neither (decision 28).
var (
	ErrRefused = errors.New("webhook: address refused")
	ErrResolve = errors.New("webhook: host did not resolve")
)

// Resolver looks up the addresses of a host name. net.DefaultResolver satisfies it; tests supply their own.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// ClientOptions configure a Client.
type ClientOptions struct {
	Resolver  Resolver
	Allow     []netip.Prefix // networks the operator allows (webhooks.allow_networks)
	AllowHTTP bool           // webhooks.allow_http
	// RootCAs replaces the system roots for TLS. Nil in production; tests use it for a test server.
	RootCAs *x509.CertPool
}

// Client makes the webhook requests. Its zero value is not usable; build one with NewClient.
type Client struct {
	resolver  Resolver
	allow     []netip.Prefix
	allowHTTP bool
	dialer    net.Dialer
	http      *http.Client
}

// Result is the outcome of one request. Err is a fixed token ("" for a 2xx answer), never an error text, since an
// error text may hold the URL or the body. Code is the HTTP status, or 0 when there was none.
type Result struct {
	Code int
	Err  string
}

// NewClient returns a Client with the given options.
func NewClient(o ClientOptions) *Client {
	c := &Client{resolver: o.Resolver, allow: o.Allow, allowHTTP: o.AllowHTTP}
	t := &http.Transport{
		// Proxy is nil: HTTP_PROXY and its kin never carry a request past the check below.
		Proxy:               nil,
		DialContext:         c.dial,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: o.RootCAs},
		MaxConnsPerHost:     MaxConns,
		MaxIdleConnsPerHost: MaxConns,
		IdleConnTimeout:     30 * time.Second,
	}
	c.http = &http.Client{
		Transport: t,
		Timeout:   Timeout,
		// A redirect answer is the answer: a 3xx is a failure, and the target is not connected to.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c
}

// CheckURL checks the host of an endpoint URL at registration or change: every address it resolves to must be
// allowed. An IP literal is checked without a lookup. The lookup is bounded by RegisterTimeout.
func (c *Client) CheckURL(ctx context.Context, host string) error {
	ctx, cancel := context.WithTimeout(ctx, RegisterTimeout)
	defer cancel()
	_, err := c.checked(ctx, host)
	return err
}

// checked returns the addresses of host, all of them allowed. An IP literal is its own address. A name is looked up
// once, and a single refused address refuses the whole host, so a mixed answer cannot slip a private address past.
func (c *Client) checked(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		if Refused(a, c.allow) {
			return nil, ErrRefused
		}
		return []netip.Addr{a}, nil
	}
	if c.resolver == nil {
		return nil, ErrResolve
	}
	addrs, err := c.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return nil, ErrResolve
	}
	for _, a := range addrs {
		if Refused(a, c.allow) {
			return nil, ErrRefused
		}
	}
	return addrs, nil
}

// dial is the transport's dialer. It checks the host, then connects to a checked address by its IP. The transport
// still handshakes with the URL's host name, so TLS verifies the certificate for that name.
func (c *Client) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, ErrResolve
	}
	addrs, err := c.checked(ctx, host)
	if err != nil {
		return nil, err
	}
	var first error
	for _, a := range addrs {
		conn, err := c.dialer.DialContext(ctx, "tcp", net.JoinHostPort(a.String(), port))
		if err == nil {
			return conn, nil
		}
		if first == nil {
			first = err
		}
	}
	return nil, first
}

// Post sends body to rawURL as a POST with header, and reads the answer. The whole attempt is bounded by Timeout.
func (c *Client) Post(ctx context.Context, rawURL string, header http.Header, body []byte) Result {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	u, err := url.Parse(rawURL)
	if err != nil || !(u.Scheme == "https" || (u.Scheme == "http" && c.allowHTTP)) {
		return Result{Err: "url"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return Result{Err: "url"}
	}
	req.Header = header.Clone()
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{Err: classify(err)}
	}
	defer resp.Body.Close()
	io.CopyN(io.Discard, resp.Body, ReadLimit)
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return Result{Code: resp.StatusCode}
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return Result{Code: resp.StatusCode, Err: "redirect"}
	}
	return Result{Code: resp.StatusCode, Err: "status " + strconv.Itoa(resp.StatusCode)}
}

// classify maps a transport error to one of the fixed tokens. The error text is never used: it holds the URL.
func classify(err error) string {
	var (
		netErr  interface{ Timeout() bool }
		certErr x509.UnknownAuthorityError
		hostErr x509.HostnameError
		recErr  tls.RecordHeaderError
	)
	switch {
	case errors.Is(err, ErrRefused):
		return "address_refused"
	case errors.Is(err, ErrResolve):
		return "resolve"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &certErr), errors.As(err, &hostErr), errors.As(err, &recErr):
		return "tls"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	}
	return "connect"
}
