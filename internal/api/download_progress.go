// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"io"
	"net/http"
	"os"
	"time"
)

// downloadSegmentBytes is how much of a download is sent by one sendfile call under one write deadline (sec1, decision
// 84). The deadline follows the progress: before each segment it is set to download.timeout plus the segment over
// download.min_rate, so a reader that stops ends the download within one segment's time. A variable so a test can make
// the segments small.
var downloadSegmentBytes int64 = 8 << 20

// serveDownload sends a file as a download, under the progress deadlines of download.timeout and download.min_rate
// (sec1, decision 84). Every route that sends a file to be downloaded uses it: a single file, a zip, an export.
func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request, name string, mod time.Time, content io.ReadSeeker) {
	pw := newProgressWriter(w, s.cfg.Download.Timeout.D(), int64(s.cfg.Download.MinRate))
	http.ServeContent(pw, r, name, mod, content)
}

// progressWriter is the response of a download. A copy from the file is sent in segments, each under its own write
// deadline, through the response's own ReadFrom, so the kernel's sendfile path is used (sec1, decision 84). Any other
// copy, such as the parts of a multipart range (which come through a pipe), sets the deadline before each chunk.
type progressWriter struct {
	http.ResponseWriter
	timeout time.Duration
	minRate int64
}

// newProgressWriter wraps a download's response. timeout is download.timeout, minRate is download.min_rate in bytes a
// second.
func newProgressWriter(w http.ResponseWriter, timeout time.Duration, minRate int64) *progressWriter {
	return &progressWriter{ResponseWriter: w, timeout: timeout, minRate: max(minRate, 1)}
}

// setDeadline moves the write deadline on to now plus the timeout plus n bytes over the minimum rate.
func (p *progressWriter) setDeadline(n int64) {
	d := p.timeout + time.Duration(float64(n)/float64(p.minRate)*float64(time.Second))
	_ = http.NewResponseController(p.ResponseWriter).SetWriteDeadline(time.Now().Add(d))
}

// Unwrap lets http.ResponseController reach the real writer, for the deadline and for Flush.
func (p *progressWriter) Unwrap() http.ResponseWriter { return p.ResponseWriter }

// ReadFrom sends src. When src is the rest of a file (a single range, or the whole file), it sends it a segment at a
// time, each through the response's ReadFrom with its own deadline. Otherwise it copies chunk by chunk, each chunk under
// a deadline of its own.
func (p *progressWriter) ReadFrom(src io.Reader) (int64, error) {
	lr, ok := src.(*io.LimitedReader)
	if !ok {
		return p.copyChunked(src)
	}
	f, ok := lr.R.(*os.File)
	if !ok {
		return p.copyChunked(src)
	}
	rf, hasReadFrom := p.ResponseWriter.(io.ReaderFrom)
	var total int64
	for lr.N > 0 {
		seg := min(lr.N, downloadSegmentBytes)
		p.setDeadline(seg)
		part := &io.LimitedReader{R: f, N: seg}
		var (
			n   int64
			err error
		)
		if hasReadFrom {
			n, err = rf.ReadFrom(part)
		} else {
			n, err = io.Copy(writerOnly{p.ResponseWriter}, part)
		}
		total += n
		lr.N -= n
		if err != nil || n < seg {
			return total, err
		}
	}
	return total, nil
}

// copyChunked copies src, setting a deadline before each chunk it writes.
func (p *progressWriter) copyChunked(src io.Reader) (int64, error) {
	return io.Copy(writerOnly{chunkDeadlines{p}}, src)
}

// chunkDeadlines sets the write deadline before each write, for the copies that have no known length.
type chunkDeadlines struct{ p *progressWriter }

func (c chunkDeadlines) Write(b []byte) (int, error) {
	c.p.setDeadline(int64(len(b)))
	return c.p.ResponseWriter.Write(b)
}
