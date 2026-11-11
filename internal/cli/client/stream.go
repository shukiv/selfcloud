// SPDX-License-Identifier: AGPL-3.0-or-later

package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// StreamResponse is a 2xx answer whose body is read as it arrives. Body must be closed.
type StreamResponse struct {
	Status int
	Header http.Header
	Body   io.ReadCloser
}

// Stream makes one request of a transfer: a piece of an upload, or a range of a download. It is never repeated. The
// client's overall timeout does not apply, because a large piece on a slow link can take longer than any fixed limit:
// the transfer ends instead when no byte has moved for stall, in either direction. The connect and response-header
// timeouts still apply, and redirects are not followed. An answer that is not 2xx comes back as an *APIError, with its
// body read and closed.
func (c *Client) Stream(ctx context.Context, r Request, stall time.Duration) (*StreamResponse, error) {
	if err := checkRequest(r); err != nil {
		return nil, err
	}
	if stall <= 0 {
		return nil, errors.New("a transfer needs a stall limit above zero")
	}
	ctx, cancel := context.WithCancel(ctx)
	w := newWatch(stall, cancel)
	end := func() {
		w.stop()
		cancel()
	}
	var body io.Reader
	switch {
	case r.Upload != nil && r.UploadLength > 0:
		body = &watchedUpload{r: r.Upload, w: w, left: r.UploadLength}
	case r.Body != nil:
		body = bytes.NewReader(r.Body)
	}
	u := c.target(r)
	req, err := c.newHTTPRequest(ctx, methodOf(r), &u, r, body)
	if err != nil {
		end()
		return nil, err
	}
	if r.Upload != nil {
		// The declared length goes out as Content-Length, so the server can refuse a piece before it reads it.
		req.ContentLength = r.UploadLength
		if r.UploadLength == 0 {
			req.Body = http.NoBody
		}
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		end()
		return nil, w.classify(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, rerr := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
		_ = resp.Body.Close()
		end()
		if rerr != nil {
			return nil, w.classify(rerr)
		}
		return nil, decodeAPIError(&Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: data})
	}
	w.kick()
	return &StreamResponse{Status: resp.StatusCode, Header: resp.Header.Clone(),
		Body: &watchedBody{rc: resp.Body, w: w, end: end}}, nil
}

// watch ends a transfer that has stopped moving: its timer cancels the request when no byte has moved for limit. Each
// byte read or sent restarts it.
type watch struct {
	mu    sync.Mutex
	limit time.Duration
	timer *time.Timer
	fired bool
}

func newWatch(limit time.Duration, cancel context.CancelFunc) *watch {
	w := &watch{limit: limit}
	w.timer = time.AfterFunc(limit, func() {
		w.mu.Lock()
		w.fired = true
		w.mu.Unlock()
		cancel()
	})
	return w
}

// kick restarts the limit after progress.
func (w *watch) kick() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.fired {
		w.timer.Reset(w.limit)
	}
}

// pause stops the timer while the server works on a piece that has been sent whole: the response-header timeout bounds
// that wait.
func (w *watch) pause() { w.timer.Stop() }

func (w *watch) stop() { w.timer.Stop() }

// classify names a stall as one, and any other failure as classify does.
func (w *watch) classify(err error) error {
	w.mu.Lock()
	fired := w.fired
	w.mu.Unlock()
	if fired {
		return &TransportError{Kind: KindTimeout, Message: fmt.Sprintf("the transfer stalled: no byte moved for %s", w.limit)}
	}
	return classify(err)
}

// watchedUpload is a request body that restarts the watch as it is read, and pauses it once its declared length is out.
type watchedUpload struct {
	r    io.Reader
	w    *watch
	left int64
}

func (u *watchedUpload) Read(p []byte) (int, error) {
	n, err := u.r.Read(p)
	if n > 0 {
		u.w.kick()
		u.left -= int64(n)
	}
	if u.left <= 0 || err == io.EOF {
		u.w.pause()
	}
	return n, err
}

// watchedBody is an answer's body that restarts the watch as it is read. A read that fails is classified, so a stall
// reads as one. Close stops the watch and ends the request.
type watchedBody struct {
	rc  io.ReadCloser
	w   *watch
	end func()
}

func (b *watchedBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.w.kick()
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, b.w.classify(err)
	}
	return n, err
}

func (b *watchedBody) Close() error {
	err := b.rc.Close()
	b.end()
	return err
}
