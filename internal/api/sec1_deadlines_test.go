// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// sec1 item 3 (decision 84): a JSON body must arrive within a deadline. A client that sends its body a byte at a time
// held the connection and its goroutine for as long as it liked. Here the body stalls after a few bytes, and the server
// must end the request.
func TestJSONBodyThatStallsIsEnded(t *testing.T) {
	saved := jsonBodyTimeout
	jsonBodyTimeout = 300 * time.Millisecond
	t.Cleanup(func() { jsonBodyTimeout = saved })

	s := newAuthServer(t, nil)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	head := "POST /api/v1/auth/login HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\n" +
		"Content-Length: 1000\r\n\r\n{\"email\""
	if _, err := io.WriteString(conn, head); err != nil {
		t.Fatal(err)
	}
	// The body never completes. The server must answer or close within a few seconds; if it waits, the read times out.
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the server still waits for the rest of a stalled JSON body after 5 s")
	}
	if err == nil {
		resp.Body.Close()
	}
}

// sec1 item 3 (decision 84): a download's writes extend the write deadline, so a reader that stops ends the response
// and a reader that keeps reading does not. The deadline is set on each write, not once for the whole response.
func TestDownloadWritesExtendTheWriteDeadline(t *testing.T) {
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	w := newWriteDeadlineWriter(rec, 50*time.Millisecond)
	for i := 0; i < 3; i++ {
		if _, err := w.Write([]byte("chunk")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(rec.deadlines) != 3 {
		t.Fatalf("write deadlines set = %d, want one per write", len(rec.deadlines))
	}
	for i := 1; i < len(rec.deadlines); i++ {
		if !rec.deadlines[i].After(rec.deadlines[i-1]) {
			t.Errorf("deadline %d (%v) does not move past deadline %d (%v)", i, rec.deadlines[i], i-1, rec.deadlines[i-1])
		}
	}
	if got := rec.deadlines[0].Sub(time.Now()); got > 60*time.Millisecond || got < 0 {
		t.Errorf("first deadline is %v away, want about the timeout (50ms)", got)
	}
}

// downloadFixture makes a file of size bytes and returns a live server, its key, and the file's node ID. The download
// deadline is tuned by mutate.
func downloadFixture(t *testing.T, size int, mutate func(*config.Config)) (*httptest.Server, *Server, string, string) {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{}, mutate)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	node := putTyped(t, s, key, f.ID, "big.bin", "application/octet-stream", strings.Repeat("x", size))
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, s, key, node.ID
}

// sec1 item 3, rework (decision 84): a download's write deadline is its base time plus its bytes over min_rate, set once
// before the copy. A reader that stops is cut off after about that time; the copy itself is not chunked.
func TestDownloadThatStopsBeingReadIsCutOff(t *testing.T) {
	const size = 12 << 20
	srv, _, key, id := downloadFixture(t, size, func(c *config.Config) {
		c.Download.MinRate = config.ByteSize(8 << 20)
		c.Download.Timeout = config.Duration(200 * time.Millisecond)
	})
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /api/v1/nodes/%s/content HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nConnection: close\r\n\r\n", id, key)
	// The reader waits longer than the computed deadline (about 1.7 s) before it reads.
	time.Sleep(3 * time.Second)
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, _ := io.Copy(io.Discard, conn)
	if n >= size {
		t.Errorf("a reader that stopped was sent the whole download (%d bytes); the write deadline did not end it", n)
	}
}

// sec1 item 3, rework (decision 84): a reader that keeps up gets the whole download.
func TestDownloadCompletesForAReaderThatKeepsUp(t *testing.T) {
	const size = 12 << 20
	srv, _, key, id := downloadFixture(t, size, func(c *config.Config) {
		c.Download.MinRate = config.ByteSize(8 << 20)
		c.Download.Timeout = config.Duration(200 * time.Millisecond)
	})
	req, err := http.NewRequest("GET", srv.URL+"/api/v1/nodes/"+id+"/content", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil || n != size {
		t.Errorf("download read %d bytes, err %v; want %d", n, err, size)
	}
}

// sec1 item 3, rework (decision 84): the download goes straight to the response, so net/http's ReadFrom (the sendfile
// path for a file) is reached. The recorder below stands in for the response, and the middleware passes ReadFrom
// through, so the call reaching it shows the copy was not chunked by a wrapper.
func TestDownloadCopyGoesThroughTheResponsesReadFrom(t *testing.T) {
	s, key, id := downloadServer(t)
	rec := &readFromRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest("GET", "/api/v1/nodes/"+id+"/content", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.readFrom != 1 {
		t.Errorf("the response's ReadFrom was called %d times, want 1 (sendfile is not reached)", rec.readFrom)
	}
}

// downloadServer is a server with one small file, for the copy-path test.
func downloadServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	node := putTyped(t, s, key, f.ID, "small.bin", "application/octet-stream", "some bytes")
	return s, key, node.ID
}

// readFromRecorder counts the ReadFrom calls made on it, and copies as a plain writer would.
type readFromRecorder struct {
	*httptest.ResponseRecorder
	readFrom int
}

func (r *readFromRecorder) ReadFrom(src io.Reader) (int64, error) {
	r.readFrom++
	return io.Copy(struct{ io.Writer }{r.ResponseRecorder}, src)
}

// deadlineRecorder is a test writer that records the write deadlines it is given, as a real connection's writer does.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadlines = append(d.deadlines, t)
	return nil
}
