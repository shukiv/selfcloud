// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// deadlineRecorder is a test writer that records the write deadlines it is given, as a real connection's writer does.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadlines = append(d.deadlines, t)
	return nil
}
