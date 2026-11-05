// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"time"
)

// jsonBodyTimeout is how long a JSON request body may take to arrive (sec1, decision 84). A body that stalls is ended
// rather than held open. Tests shorten it.
var jsonBodyTimeout = 15 * time.Second

// downloadWriteTimeout is how long a download may wait for its reader to take the next write (sec1, decision 84). Each
// write moves the deadline on, so a reader that keeps reading is not cut off, and one that stops is.
const downloadWriteTimeout = 30 * time.Second

// writeDeadlineWriter moves the write deadline of its response on for every write. It deliberately has no ReadFrom: a
// sendfile copy of a large file would run under one deadline and be cut off from a slow reader, so every chunk goes
// through Write. That gives up the kernel's sendfile path for downloads (sec1, decision 84).
type writeDeadlineWriter struct {
	http.ResponseWriter
	timeout time.Duration
}

// newWriteDeadlineWriter wraps w so that each write extends its write deadline by timeout.
func newWriteDeadlineWriter(w http.ResponseWriter, timeout time.Duration) *writeDeadlineWriter {
	return &writeDeadlineWriter{ResponseWriter: w, timeout: timeout}
}

// Write extends the write deadline to now plus the timeout, then writes p.
func (d *writeDeadlineWriter) Write(p []byte) (int, error) {
	_ = http.NewResponseController(d.ResponseWriter).SetWriteDeadline(time.Now().Add(d.timeout))
	return d.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (d *writeDeadlineWriter) Unwrap() http.ResponseWriter { return d.ResponseWriter }
