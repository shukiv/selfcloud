// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"time"
)

// jsonBodyTimeout is how long a JSON request body may take to arrive (sec1, decision 84). A body that stalls is ended
// rather than held open. Tests shorten it.
var jsonBodyTimeout = 15 * time.Second

// writeDeadlineWriter moves the write deadline of its response on for every write. It is for a response whose length is
// not known up front (a streamed zip or export), which cannot be sent with one deadline and does not use sendfile
// anyway. A download, whose length is known, sets one deadline before its copy instead (sec1, decision 84). No response
// streams today: every body is served from a file of known length (zips and exports included), so nothing uses this yet.
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
