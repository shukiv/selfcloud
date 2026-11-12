// SPDX-License-Identifier: AGPL-3.0-or-later

package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// patterned returns n bytes of a pattern that is not all one byte, so a shifted or a short copy shows.
func patterned(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/251)
	}
	return b
}

func TestExtraHeadersAreSent(t *testing.T) {
	t.Parallel()
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	h := http.Header{}
	h.Set("Tus-Resumable", "1.0.0")
	h.Set("Upload-Offset", "42")
	if _, err := c.Do(context.Background(), Request{Method: "HEAD", Path: "/api/v1/uploads/x", Header: h}); err != nil {
		t.Fatal(err)
	}
	if got.Get("Tus-Resumable") != "1.0.0" || got.Get("Upload-Offset") != "42" {
		t.Errorf("headers = %v, want the two extra headers", got)
	}
	if got.Get("Authorization") != "Bearer "+secret {
		t.Errorf("Authorization = %q, want the client's own", got.Get("Authorization"))
	}
}

// TestExtraHeadersMayNotCarryACredential: the credential goes only where the client puts it. A request that tries to
// set Authorization, Proxy-Authorization, Cookie or Host through its extra headers is refused before anything is sent,
// and the refusal does not repeat the value.
func TestExtraHeadersMayNotCarryACredential(t *testing.T) {
	t.Parallel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	c := newClient(t, srv.URL)
	for _, name := range []string{"Authorization", "authorization", "Proxy-Authorization", "Cookie", "Host"} {
		h := http.Header{}
		h[name] = []string{"Bearer " + secret}
		_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info", Header: h})
		if err == nil {
			t.Errorf("%s: the request was sent", name)
			continue
		}
		mustNotContainSecret(t, name+" refusal", err.Error())
		_, err = c.Stream(context.Background(), Request{Method: "GET", Path: "/api/v1/info", Header: h}, time.Second)
		if err == nil {
			t.Errorf("%s: the stream was sent", name)
		}
	}
	if calls != 0 {
		t.Errorf("the server was called %d time(s)", calls)
	}
}

// TestStreamReadsPastTheAnswerLimit: Do reads at most MaxResponseBytes; a stream hands the body over to be copied, so
// a range larger than that limit arrives whole.
func TestStreamReadsPastTheAnswerLimit(t *testing.T) {
	t.Parallel()
	data := patterned(MaxResponseBytes + 3<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	resp, err := c.Stream(context.Background(), Request{Method: "GET", Path: "/api/v1/nodes/x/content"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	h := sha256.New()
	n, err := io.Copy(h, resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if want := sha256.Sum256(data); n != int64(len(data)) || !bytes.Equal(h.Sum(nil), want[:]) {
		t.Errorf("read %d bytes with another digest, want %d", n, len(data))
	}
}

// TestStreamSendsTheUploadWithItsLength: the upload body is read from the reader as it is sent, with the declared
// length, so the server can refuse a piece by its Content-Length before it reads it.
func TestStreamSendsTheUploadWithItsLength(t *testing.T) {
	t.Parallel()
	data := patterned(3 << 20)
	var gotLen int64
	var gotSum [32]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLen = r.ContentLength
		b, _ := io.ReadAll(r.Body)
		gotSum = sha256.Sum256(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	resp, err := c.Stream(context.Background(), Request{Method: "PATCH", Path: "/api/v1/uploads/x",
		Upload: io.NopCloser(bytes.NewReader(data)), UploadLength: int64(len(data))}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Status != http.StatusNoContent {
		t.Errorf("status = %d", resp.Status)
	}
	if gotLen != int64(len(data)) || gotSum != sha256.Sum256(data) {
		t.Errorf("the server got %d bytes, or another digest; want %d", gotLen, len(data))
	}
}

// TestStreamStallEndsTheTransfer: a body that stops moving ends after the stall limit with a timeout, while the whole
// request takes no part: a slow body that keeps moving outlives the client's overall timeout.
func TestStreamStallEndsTheTransfer(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			for i := 0; i < 6; i++ {
				_, _ = w.Write([]byte("x"))
				w.(http.Flusher).Flush()
				time.Sleep(60 * time.Millisecond)
			}
			return
		}
		_, _ = w.Write([]byte("x"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, func(o *Options) { o.Timeout = 100 * time.Millisecond })

	resp, err := c.Stream(context.Background(), Request{Method: "GET", Path: "/slow"}, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(b) != "xxxxxx" {
		t.Errorf("a slow, moving body: %q, %v; want all of it", b, err)
	}

	resp, err = c.Stream(context.Background(), Request{Method: "GET", Path: "/stuck"}, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	var te *TransportError
	if !errors.As(err, &te) || te.Kind != KindTimeout {
		t.Fatalf("a stuck body: err = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the stall took %v to end", d)
	}
	mustNotContainSecret(t, "the stall error", err.Error())
}

// tailStallConn is a connection whose writes block, once the upload's last byte has been read from its source, until the
// connection is closed: a link that stalls while the transport still holds the end of the piece.
type tailStallConn struct {
	net.Conn
	exhausted *atomic.Bool
	closed    chan struct{}
	once      sync.Once
}

func (c *tailStallConn) Write(p []byte) (int, error) {
	if c.exhausted.Load() {
		<-c.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}

func (c *tailStallConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// endMarkReader reads from data and sets exhausted when the last byte has been handed out.
type endMarkReader struct {
	r         *bytes.Reader
	exhausted *atomic.Bool
}

func (e *endMarkReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if e.r.Len() == 0 {
		e.exhausted.Store(true)
	}
	return n, err
}

// stallAtTheEnd makes the client's connections stall once exhausted is set.
func stallAtTheEnd(t *testing.T, c *Client, exhausted *atomic.Bool) {
	t.Helper()
	tr := c.stream.Transport.(*http.Transport)
	dial := tr.DialContext
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &tailStallConn{Conn: conn, exhausted: exhausted, closed: make(chan struct{})}, nil
	}
}

// TestStreamStallCoversTheEndOfAnUploadPiece: when the link stalls after the last byte has been read from the file but
// before it has been written out, the transfer ends within the longer of the stall limit and the header limit, and is
// not left to TCP's own give-up time. It is not cut short either: the limit is the longer of the two.
func TestStreamStallCoversTheEndOfAnUploadPiece(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	const stall, header = 200 * time.Millisecond, 700 * time.Millisecond
	c := newClient(t, srv.URL, func(o *Options) { o.HeaderTimeout = header })
	var exhausted atomic.Bool
	stallAtTheEnd(t, c, &exhausted)
	data := patterned(64 << 10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := c.Stream(ctx, Request{Method: "PATCH", Path: "/api/v1/uploads/x",
		Upload: &endMarkReader{r: bytes.NewReader(data), exhausted: &exhausted}, UploadLength: int64(len(data))}, stall)
	d := time.Since(start)
	var te *TransportError
	if !errors.As(err, &te) || te.Kind != KindTimeout || !strings.Contains(te.Message, "stalled") {
		t.Fatalf("err = %v, want the transfer's own stall timeout", err)
	}
	if d < header-100*time.Millisecond || d > 3*time.Second {
		t.Errorf("the stalled tail ended after %v, want about %v", d, header)
	}
}

// TestStreamServerWorkAfterTheLastByteIsNotCutShort: a server that takes longer than the stall limit, but not longer than
// the header limit, to answer a piece it has read whole (hashing, a sync) still gets its answer.
func TestStreamServerWorkAfterTheLastByteIsNotCutShort(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, func(o *Options) { o.HeaderTimeout = 3 * time.Second })
	data := patterned(64 << 10)
	resp, err := c.Stream(context.Background(), Request{Method: "PATCH", Path: "/api/v1/uploads/x",
		Upload: bytes.NewReader(data), UploadLength: int64(len(data))}, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("a slow answer after the whole piece: %v", err)
	}
	resp.Body.Close()
}

// TestStreamErrorIsDecoded: an answer that is not 2xx is the API's error, its body read and closed.
func TestStreamErrorIsDecoded(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = io.WriteString(w, `{"error":{"code":"upload_expired","message":"the upload expired; start it again"}}`)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	_, err := c.Stream(context.Background(), Request{Method: "PATCH", Path: "/api/v1/uploads/x"}, time.Second)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusGone || ae.Code != "upload_expired" {
		t.Fatalf("err = %v, want the API's upload_expired", err)
	}
}

// TestAPIErrorCarriesTheDetails: the error's details come through, so a transfer can read the state an upload waits in
// or the offset the server holds. They are data for the command, not part of the message.
func TestAPIErrorCarriesTheDetails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":{"code":"conflict","message":"the name is taken",`+
			`"details":{"state":"awaiting_commit","upload_id":"upl_x","offset":42}}}`)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	_, err := c.Stream(context.Background(), Request{Method: "PATCH", Path: "/api/v1/uploads/x"}, time.Second)
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("err %v is not an APIError", err)
	}
	if ae.Details["state"] != "awaiting_commit" || ae.Details["offset"] != float64(42) {
		t.Errorf("Details = %v", ae.Details)
	}
	if strings.Contains(ae.Error(), "awaiting_commit") {
		t.Errorf("the message carries the details: %q", ae.Error())
	}
}

// TestStreamDoesNotFollowRedirects: a redirect is the answer, and its target is never called, so the credential never
// reaches another origin.
func TestStreamDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	var targetCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
	defer target.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v1/nodes/x/content", http.StatusFound)
	}))
	defer src.Close()
	c := newClient(t, src.URL)
	if _, err := c.Stream(context.Background(), Request{Method: "GET", Path: "/api/v1/nodes/x/content"}, time.Second); err == nil {
		t.Fatal("a redirect was followed")
	}
	if targetCalls != 0 {
		t.Errorf("the redirect target was called %d time(s)", targetCalls)
	}
}

// TestWithCredentialChangesOnlyTheCopy: a visitor's credential is swapped on a copy; the original keeps its own.
func TestWithCredentialChangesOnlyTheCopy(t *testing.T) {
	t.Parallel()
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, func(o *Options) { o.Credential = "" })
	v := c.WithCredential("scl_visitor")
	for _, cl := range []*Client{c, v} {
		if _, err := cl.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/info"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0] != "" || got[1] != "Bearer scl_visitor" {
		t.Errorf("Authorization headers = %q, want none and then the visitor's", got)
	}
}

// TestDoRefusesAStreamedBody: Do may repeat a request, and a reader cannot be read twice, so Do refuses one.
func TestDoRefusesAStreamedBody(t *testing.T) {
	t.Parallel()
	c := newClient(t, "https://selfcloud.example")
	_, err := c.Do(context.Background(), Request{Method: "PATCH", Path: "/x", Upload: io.NopCloser(strings.NewReader("a")),
		UploadLength: 1})
	if err == nil {
		t.Fatal("Do took a streamed body")
	}
}
