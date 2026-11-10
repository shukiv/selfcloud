// SPDX-License-Identifier: AGPL-3.0-or-later

// Package client is the HTTP client every remote command uses. It owns the rules for the server address (https, or
// loopback http), TLS verification, the three timeouts, the one retry policy and the decoding of API errors. The
// credential goes only in the Authorization header; it never reaches a URL, a message or a log line.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Kinds of transport failure, the JSON code a command reports for each.
const (
	KindUnreachable = "unreachable"
	KindTLS         = "tls"
	KindTimeout     = "timeout"
	KindFailed      = "failed"
)

// MaxResponseBytes bounds one answer. The remote commands that move files read pieces, not whole files, so the bound
// stays small.
const MaxResponseBytes = 16 << 20

const (
	// maxAttempts is one request and two repeats.
	maxAttempts = 3
	// defaultBackoff is the wait before a repeat when the server gives no usable Retry-After.
	defaultBackoff = time.Second
	userAgent      = "selfcloud-cli"
)

// Options configure a client. Zero durations take the defaults named on each field.
type Options struct {
	// BaseURL is the server's address, https://host[:port][/prefix]. http is accepted only for a loopback host, or with
	// InsecureHTTP.
	BaseURL string
	// Credential is sent as Bearer. It may be empty for a public route.
	Credential string
	// CAFile is a PEM file of roots to trust in addition to the system's.
	CAFile string
	// InsecureSkipVerify turns off the certificate check. The caller warns on each use.
	InsecureSkipVerify bool
	// InsecureHTTP allows plain http to a host that is not loopback.
	InsecureHTTP bool
	// ConnectTimeout bounds the dial and the TLS handshake. Default 10 s.
	ConnectTimeout time.Duration
	// HeaderTimeout bounds the wait for the response headers. Default 30 s.
	HeaderTimeout time.Duration
	// Timeout bounds one whole request, its body included. Default 2 min.
	Timeout time.Duration
	// MaxRetryAfter is the longest wait the client will honour. A longer Retry-After is not waited; the error carries it.
	// Default 30 s.
	MaxRetryAfter time.Duration
	// Now and Sleep exist for tests. They default to the clock and a timer that stops with the context.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

// Client makes requests to one server.
type Client struct {
	base          *url.URL
	cred          string
	http          *http.Client
	now           func() time.Time
	sleep         func(context.Context, time.Duration) error
	maxRetryAfter time.Duration
}

// Request is one call. Path is an absolute path on the server, with no query, no fragment and no ".." segment.
type Request struct {
	Method      string
	Path        string
	Query       url.Values
	Accept      string
	ContentType string
	Body        []byte
}

// Response is an answer with a 2xx status. Its body has been read whole, up to MaxResponseBytes.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// APIError is an answer with a non-2xx status. Code and Message come from the API's error envelope, when it has one.
type APIError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration // the server's Retry-After, when it gave one
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// TransportError is a failure before an answer: the server could not be reached, the certificate was refused, a timeout
// passed, or the request failed some other way. Message never names the address or the credential.
type TransportError struct {
	Kind    string
	Message string
}

func (e *TransportError) Error() string { return e.Message }

// ParseBaseURL checks a server address. It refuses a user name or password in the address (a credential there would
// reach error messages), a query, a fragment, a missing host and any scheme but https, and plain http unless the host is
// loopback or insecureHTTP is set. It returns the address with any trailing slash removed.
func ParseBaseURL(raw string, insecureHTTP bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("the server address is not a valid URL")
	}
	if u.User != nil {
		return nil, errors.New("the server address must not contain a user name or a password; use a credential instead")
	}
	if u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errors.New("the server address must not contain a query or a fragment")
	}
	if u.Hostname() == "" {
		return nil, errors.New("the server address needs a host")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !IsLoopback(u.Hostname()) && !insecureHTTP {
			return nil, errors.New("plain http is allowed only for a loopback address; use https, or --insecure-http " +
				"for a server you trust on a network you trust")
		}
	default:
		return nil, errors.New("the server address must start with https://")
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u, nil
}

// IsLoopback reports whether host names the loopback interface.
func IsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// LoadCAPool reads a PEM file of roots and returns the system's roots with them added.
func LoadCAPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read the CA file")
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("the CA file holds no PEM certificate")
	}
	return pool, nil
}

// New builds a client. It fails when the address or the CA file is refused.
func New(o Options) (*Client, error) {
	base, err := ParseBaseURL(o.BaseURL, o.InsecureHTTP)
	if err != nil {
		return nil, err
	}
	if o.ConnectTimeout <= 0 {
		o.ConnectTimeout = 10 * time.Second
	}
	if o.HeaderTimeout <= 0 {
		o.HeaderTimeout = 30 * time.Second
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Minute
	}
	if o.MaxRetryAfter <= 0 {
		o.MaxRetryAfter = 30 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = sleepFor
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: o.InsecureSkipVerify} // #nosec G402 -- only when the caller asks
	if o.CAFile != "" {
		pool, err := LoadCAPool(o.CAFile)
		if err != nil {
			return nil, err
		}
		tlsCfg.RootCAs = pool
	}
	dialer := &net.Dialer{Timeout: o.ConnectTimeout, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       tlsCfg,
		TLSHandshakeTimeout:   o.ConnectTimeout,
		ResponseHeaderTimeout: o.HeaderTimeout,
		ForceAttemptHTTP2:     true,
	}
	return &Client{
		base: base,
		cred: o.Credential,
		http: &http.Client{
			Transport: tr,
			Timeout:   o.Timeout,
			// A redirect is not followed: the credential would go to the new address, and an https to http
			// redirect would send it in clear. The answer is the redirect itself, which is an error.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now:           o.Now,
		sleep:         o.Sleep,
		maxRetryAfter: o.MaxRetryAfter,
	}, nil
}

// BaseURL returns the server's address. It carries no credential.
func (c *Client) BaseURL() string { return c.base.String() }

// Do makes the request. A 429 or 503 to a GET or HEAD is repeated, up to maxAttempts, after the server's Retry-After
// (seconds or an HTTP date), or after a second when it gives none. A wait longer than MaxRetryAfter is not made: the
// error returns at once with the Retry-After in it. Other methods are never repeated.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	if err := checkPath(r.Path); err != nil {
		return nil, err
	}
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	target := *c.base
	target.Path = c.base.Path + r.Path
	target.RawPath = ""
	target.RawQuery = r.Query.Encode()
	for attempt := 1; ; attempt++ {
		resp, err := c.send(ctx, method, &target, r)
		if err != nil {
			return nil, classify(err)
		}
		if resp.Status >= 200 && resp.Status < 300 {
			return resp, nil
		}
		apiErr := decodeAPIError(resp)
		if attempt >= maxAttempts || !repeatable(method) || !retryableStatus(resp.Status) {
			return nil, apiErr
		}
		wait, ok := retryAfter(resp.Header, c.now())
		if !ok {
			wait = defaultBackoff
		} else {
			apiErr.RetryAfter = wait
		}
		if wait > c.maxRetryAfter {
			return nil, apiErr
		}
		if err := c.sleep(ctx, wait); err != nil {
			return nil, classify(err)
		}
	}
}

func (c *Client) send(ctx context.Context, method string, u *url.URL, r Request) (*Response, error) {
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if r.Accept != "" {
		req.Header.Set("Accept", r.Accept)
	}
	if r.ContentType != "" {
		req.Header.Set("Content-Type", r.ContentType)
	}
	if c.cred != "" {
		req.Header.Set("Authorization", "Bearer "+c.cred)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxResponseBytes {
		return nil, &TransportError{Kind: KindFailed, Message: "the server's answer is larger than the client reads"}
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: data}, nil
}

// classify turns a failure from net/http into a TransportError. It reads the cause only, so the URL, which carries no
// credential, is not repeated in the message.
func classify(err error) error {
	var te *TransportError
	if errors.As(err, &te) {
		return te
	}
	inner := err
	var ue *url.Error
	if errors.As(err, &ue) {
		inner = ue.Err
	}
	var cve *tls.CertificateVerificationError
	var ua x509.UnknownAuthorityError
	var he x509.HostnameError
	var ci x509.CertificateInvalidError
	if errors.As(inner, &cve) || errors.As(inner, &ua) || errors.As(inner, &he) || errors.As(inner, &ci) {
		return &TransportError{Kind: KindTLS, Message: "the server's TLS certificate could not be verified; " +
			"use --ca-file for a private root, or --insecure-skip-verify for a test server only"}
	}
	var ne net.Error
	if errors.As(inner, &ne) && ne.Timeout() {
		return &TransportError{Kind: KindTimeout, Message: "the server did not answer in time"}
	}
	if errors.Is(inner, context.Canceled) {
		return &TransportError{Kind: KindFailed, Message: "the request was cancelled"}
	}
	var oe *net.OpError
	var de *net.DNSError
	if errors.As(inner, &oe) || errors.As(inner, &de) {
		return &TransportError{Kind: KindUnreachable, Message: "cannot reach the server"}
	}
	return &TransportError{Kind: KindFailed, Message: "the request failed: " + inner.Error()}
}

func decodeAPIError(resp *Response) *APIError {
	e := &APIError{Status: resp.Status}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(resp.Body, &env) == nil {
		e.Code = env.Error.Code
		e.Message = env.Error.Message
	}
	switch {
	case e.Message != "":
	case resp.Status >= 300 && resp.Status < 400:
		e.Message = fmt.Sprintf("the server redirected the request (HTTP %d); the client does not follow redirects, "+
			"so give the final address", resp.Status)
	default:
		e.Message = "the server gave an error without a message"
	}
	return e
}

func repeatable(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
}

// retryAfter reads Retry-After as delay seconds or an HTTP date. It reports false when the header is absent or cannot be
// read. A date in the past is no wait.
func retryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(v, "-") {
		// Too large to hold: a wait far beyond any cap, so it is reported, not made.
		return time.Duration(math.MaxInt64), true
	}
	if err == nil {
		if n < 0 {
			return 0, false
		}
		if n > math.MaxInt64/int64(time.Second) {
			return time.Duration(math.MaxInt64), true
		}
		return time.Duration(n) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// checkPath refuses a path that is not absolute, that carries a query or fragment, or that climbs with "..".
func checkPath(p string) error {
	if !strings.HasPrefix(p, "/") {
		return errors.New("the request path must start with /")
	}
	if strings.ContainsAny(p, "?#\\") {
		return errors.New("the request path must not contain a query, a fragment or a backslash")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return errors.New("the request path must not contain ..")
		}
	}
	return nil
}

func sleepFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
