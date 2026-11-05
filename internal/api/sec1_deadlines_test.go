// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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

// downloadFixture makes a file of size bytes and returns a live server, its key, and the file's node ID. The download
// settings are tuned by mutate.
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

// setSegment makes the download segments size bytes for one test, and restores them afterwards. Tests that call it do
// not run in parallel.
func setSegment(t *testing.T, size int64) {
	t.Helper()
	saved := downloadSegmentBytes
	downloadSegmentBytes = size
	t.Cleanup(func() { downloadSegmentBytes = saved })
}

// sec1 item 3, rework 2 (decision 84): a reader that reads part of a download and then stops is cut off within about one
// segment's deadline (download.timeout plus the segment over download.min_rate), not after the whole file's time.
// Before, one deadline covered the whole file: 12 MiB at 1 MiB/s here is about 12 s, and this test reads after 3 s.
func TestDownloadThatStopsBeingReadIsCutOffWithinASegment(t *testing.T) {
	setSegment(t, 1<<20)
	const size = 12 << 20
	srv, _, key, id := downloadFixture(t, size, func(c *config.Config) {
		c.Download.MinRate = config.ByteSize(1 << 20)
		c.Download.Timeout = config.Duration(200 * time.Millisecond)
	})
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /api/v1/nodes/%s/content HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nConnection: close\r\n\r\n", id, key)
	// The reader waits past one segment's deadline (about 1.2 s), and well short of the whole file's (about 12 s).
	time.Sleep(3 * time.Second)
	if err := conn.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	n, _ := io.Copy(io.Discard, conn)
	if n >= size {
		t.Errorf("a reader that stopped was sent the whole download (%d bytes): the deadline did not end it", n)
	}
	if took := time.Since(start); took > 6*time.Second {
		t.Errorf("the connection ended only after %v, after the reader resumed", took)
	}
}

// sec1 item 3, rework 2 (decision 84): a reader that keeps reading above download.min_rate completes the download. There
// is no absolute cap on the total time: each segment gets its own deadline as the reader moves on.
func TestDownloadCompletesForASlowReaderAboveTheMinimumRate(t *testing.T) {
	setSegment(t, 512<<10)
	const size = 2 << 20
	srv, _, key, id := downloadFixture(t, size, func(c *config.Config) {
		c.Download.MinRate = config.ByteSize(512 << 10)
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
	// About 2 MiB a second: above the minimum of 512 KiB a second, but not a full-speed read.
	buf := make([]byte, 64<<10)
	var n int64
	for {
		m, rerr := resp.Body.Read(buf)
		n += int64(m)
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			t.Fatalf("slow reader: read %d bytes, then %v", n, rerr)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if n != size {
		t.Errorf("slow reader got %d bytes, want %d", n, size)
	}
}

// sec1 item 3, rework 2 (decision 84): a single range and a multipart range both complete.
func TestDownloadRangesComplete(t *testing.T) {
	setSegment(t, 512<<10)
	const size = 2 << 20
	srv, _, key, id := downloadFixture(t, size, func(c *config.Config) {
		c.Download.MinRate = config.ByteSize(512 << 10)
		c.Download.Timeout = config.Duration(200 * time.Millisecond)
	})
	get := func(rng string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest("GET", srv.URL+"/api/v1/nodes/"+id+"/content", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Range", rng)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("range %q: %v after %d bytes", rng, err, len(body))
		}
		return resp, body
	}
	resp, body := get("bytes=100-1099999")
	if resp.StatusCode != http.StatusPartialContent || len(body) != 1099900 {
		t.Errorf("single range: status %d, %d bytes; want 206 and 1099900", resp.StatusCode, len(body))
	}
	resp, body = get("bytes=0-9,20-29,100000-100019")
	if resp.StatusCode != http.StatusPartialContent || len(body) < 30+20 {
		t.Errorf("multipart range: status %d, %d bytes; want 206 with the parts", resp.StatusCode, len(body))
	}
}

// sec1 item 3, rework 2 (decision 84): a whole file is sent by sendfile, one segment at a time. The response's ReadFrom
// receives the segments, each as a limited reader over the file, and that is the path net/http turns into sendfile. The
// recorder stands in for the connection's writer.
func TestDownloadSendsFileSegmentsThroughReadFrom(t *testing.T) {
	setSegment(t, 512<<10)
	s, key, id := downloadServer(t, 2<<20)
	rec := &readFromRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest("GET", "/api/v1/nodes/"+id+"/content", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.fileSegments != 4 {
		t.Errorf("file segments through the response's ReadFrom = %d (of %d calls), want 4", rec.fileSegments, rec.readFrom)
	}
}

// downloadServer is a server with one file of size bytes, for the copy-path test.
func downloadServer(t *testing.T, size int) (*Server, string, string) {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	node := putTyped(t, s, key, f.ID, "big.bin", "application/octet-stream", strings.Repeat("x", size))
	return s, key, node.ID
}

// readFromRecorder counts the ReadFrom calls made on it, and the calls whose source is a limited reader over a file. It
// copies as a plain writer would.
type readFromRecorder struct {
	*httptest.ResponseRecorder
	readFrom     int
	fileSegments int
}

func (r *readFromRecorder) ReadFrom(src io.Reader) (int64, error) {
	r.readFrom++
	if lr, ok := src.(*io.LimitedReader); ok {
		if _, ok := lr.R.(*os.File); ok {
			r.fileSegments++
		}
	}
	return io.Copy(struct{ io.Writer }{r.ResponseRecorder}, src)
}
