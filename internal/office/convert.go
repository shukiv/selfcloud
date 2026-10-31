// SPDX-License-Identifier: AGPL-3.0-or-later

// Package office converts documents through Collabora Online's conversion interface (design section 14, slice s3,
// decision 37). It is the one conversion client: previews, exports and, later, text extraction use it. No converter
// runs in the server (decision 10); the document goes to Collabora and its bytes come back.
//
// The client sends requests only to the scheme and host of office.discovery_url, on fixed paths. A URL from the
// discovery document or from a caller is never followed. Collabora's answers are checked for status, type and file
// signature, and their text is never kept: a failure is one of the sentinel errors below.
package office

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"
)

// Failures. Their text is the server's own. Collabora's status text, body and headers are not kept.
var (
	// ErrUnsupported is a format or a source extension outside the fixed lists. Nothing is sent.
	ErrUnsupported = errors.New("office: the format is not supported")
	// ErrRefused is a 4xx answer: Collabora will not convert this document.
	ErrRefused = errors.New("office: the converter refused the document")
	// ErrUnavailable is a 5xx answer other than 500 (500 is a refusal, see Convert), a connection failure, a reset, a body
	// that stopped early, or a capabilities document that cannot be had. It is an outage of the converter, not a judgement
	// about the document.
	ErrUnavailable = errors.New("office: the converter is unavailable")
	// ErrTooLarge is an answer over the output limit.
	ErrTooLarge = errors.New("office: the converted file is over the output limit")
	// ErrBadOutput is an answer whose type or file signature is not the format asked for.
	ErrBadOutput = errors.New("office: the converter answered with another type of file")
)

// format is one output the client asks for: the content type Collabora must answer with, and the bytes the file starts
// with. The magic values are the file signatures (PDF, PNG, and the ZIP container of the OOXML and ODF formats).
type format struct {
	ctype string
	magic []byte
}

// Formats is the fixed list of outputs. A caller names one of them; no other value reaches the URL.
var Formats = map[string]format{
	"pdf":  {"application/pdf", []byte("%PDF-")},
	"png":  {"image/png", []byte("\x89PNG\r\n\x1a\n")},
	"docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", []byte("PK\x03\x04")},
	"xlsx": {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", []byte("PK\x03\x04")},
	"pptx": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", []byte("PK\x03\x04")},
	"odt":  {"application/vnd.oasis.opendocument.text", []byte("PK\x03\x04")},
	"ods":  {"application/vnd.oasis.opendocument.spreadsheet", []byte("PK\x03\x04")},
	"odp":  {"application/vnd.oasis.opendocument.presentation", []byte("PK\x03\x04")},
}

// ContentType returns the content type of a format in the fixed list.
func ContentType(format string) (string, bool) {
	f, ok := Formats[format]
	return f.ctype, ok
}

// extPattern is a file extension as the server accepts it: lower-case letters and digits, short. It is checked before the
// extension is used in a multipart name.
var extPattern = regexp.MustCompile(`^[a-z0-9]{1,8}$`)

// Bounds on the client's own requests.
const (
	// capsTTL is how long a capabilities answer is kept before it is due again.
	capsTTL = time.Hour
	// capsRetryGap is the least time between two attempts to fetch the capabilities, whether they succeed or fail. A
	// refetch that is due waits for it, and a stale copy answers meanwhile.
	capsRetryGap = 30 * time.Second
	// maxCapsBytes is the most of the capabilities document that is read.
	maxCapsBytes = 64 << 10
	// capsPath is Collabora's capabilities document. It names the build (productVersionHash) and whether conversion is
	// available to this server.
	capsPath = "/hosting/capabilities"
	// convertPath is the conversion endpoint. The format is appended to it.
	convertPath = "/cool/convert-to/"
)

// Options configure a Client.
type Options struct {
	// Concurrent is how many conversions run at once. A conversion waits for a slot with its context.
	Concurrent int
	// Timeout is the longest one conversion may take, from the request to the last byte.
	Timeout time.Duration
	// MaxOutput is the most bytes one conversion may return.
	MaxOutput int64
}

// Caps is what Collabora says of itself in its capabilities document.
type Caps struct {
	Available   bool   // convert-to is available to this server
	Version     string // productVersion
	VersionHash string // productVersionHash: the build
}

// Client converts documents. Its methods are safe for concurrent use.
type Client struct {
	base    string
	hc      *http.Client
	slots   chan struct{}
	timeout time.Duration
	maxOut  int64
	now     func() time.Time

	// mu guards the capabilities copy and its state. It is never held across a network call: a fetch runs with the lock
	// released, and while it runs (busy) other callers get the copy they have, or ErrUnavailable when there is none.
	mu      sync.Mutex
	caps    *Caps
	capsAt  time.Time // when caps was fetched
	capsTry time.Time // when the last fetch started, successful or not
	busy    bool
}

// New returns a Client for the Collabora server named by discoveryURL: its scheme and host, and no path. hc may be nil,
// which gives a client with the timeout. Whatever client is given, redirects are refused, as discovery refuses them.
func New(discoveryURL string, hc *http.Client, o Options) (*Client, error) {
	u, err := url.Parse(discoveryURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("office: the discovery URL must name a scheme and a host")
	}
	base := u.Scheme + "://" + u.Host
	if o.Concurrent < 1 {
		o.Concurrent = 1
	}
	if o.Timeout <= 0 {
		o.Timeout = time.Minute
	}
	if hc == nil {
		hc = &http.Client{}
	}
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errRedirect
	}
	return &Client{base: base, hc: &c, slots: make(chan struct{}, o.Concurrent), timeout: o.Timeout,
		maxOut: o.MaxOutput, now: time.Now}, nil
}

// errRedirect is returned by the client's redirect policy. A request that is redirected is refused, and not followed.
var errRedirect = errors.New("office: redirect refused")

// Convert converts the document read from src, whose size is size bytes and whose extension is ext, to format, and writes
// the result to dst. The request body is sent with a Content-Length computed from size, so it is not chunked. src must
// deliver exactly size bytes; a shorter source fails the request. The extension is the type Collabora is told the input is
// (the multipart file name is source.<ext>); the caller's file name never reaches Collabora. Nothing is sent for an
// unsupported format or extension. Errors are the sentinels above, or the error of dst or of ctx.
func (c *Client) Convert(ctx context.Context, src io.Reader, size int64, ext, format string, dst io.Writer) error {
	f, ok := Formats[format]
	if !ok || !extPattern.MatchString(ext) || size < 0 {
		return ErrUnsupported
	}
	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.slots }()

	// The multipart framing is written first, so its length is known: the part header, the file, and the closing boundary.
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	if _, err := mw.CreateFormFile("data", "source."+ext); err != nil {
		return ErrUnsupported
	}
	prefix := append([]byte(nil), head.Bytes()...)
	if err := mw.Close(); err != nil {
		return ErrUnsupported
	}
	suffix := append([]byte(nil), head.Bytes()[len(prefix):]...)
	body := io.MultiReader(bytes.NewReader(prefix), io.LimitReader(src, size), bytes.NewReader(suffix))
	length := int64(len(prefix)) + size + int64(len(suffix))

	rctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, c.base+convertPath+format, body)
	if err != nil {
		return ErrUnsupported
	}
	req.ContentLength = length
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errRedirect) {
			return ErrRefused
		}
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
		switch {
		case resp.StatusCode == http.StatusInternalServerError:
			// Live Collabora 26.04.5.2 answers 500 (X-Error-Kind: savefailed, no body) when it cannot save the document as the
			// format asked for, as with a spreadsheet asked for as a text document. Every 500 seen live is that refusal.
			return ErrRefused
		case resp.StatusCode >= 500:
			return ErrUnavailable
		}
		return ErrRefused
	}
	// Live, every format comes back as application/octet-stream with Content-Disposition, so the type is accepted in
	// either form and the file signature below decides what the bytes are.
	ct, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || (ct != f.ctype && ct != "application/octet-stream") {
		return ErrBadOutput
	}
	sig := make([]byte, len(f.magic))
	if _, err := io.ReadFull(resp.Body, sig); err != nil || !bytes.Equal(sig, f.magic) {
		return ErrBadOutput
	}
	if _, err := dst.Write(sig); err != nil {
		return err
	}
	// The output is read to maxOut+1 bytes and no further: one byte over is refused before dst sees it.
	n, err := io.Copy(dst, io.LimitReader(resp.Body, c.maxOut-int64(len(sig))+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, errorKind(err))
	}
	if int64(len(sig))+n > c.maxOut {
		return ErrTooLarge
	}
	return nil
}

// errorKind names the kind of a read error without its text, which may come from the server.
func errorKind(err error) string {
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	return "read"
}

// Capabilities returns Collabora's capabilities document. A good answer is kept and used for an hour. When it is due, a
// fetch is made, at most once every capsRetryGap, and never with the lock held. A failed fetch keeps the stale copy, which
// answers while Collabora is down. With no copy at all, a caller gets ErrUnavailable.
func (c *Client) Capabilities(ctx context.Context) (Caps, error) {
	c.mu.Lock()
	now := c.now()
	if c.caps != nil && now.Sub(c.capsAt) < capsTTL {
		caps := *c.caps
		c.mu.Unlock()
		return caps, nil
	}
	if c.busy || now.Sub(c.capsTry) < capsRetryGap {
		caps, ok := c.copyLocked()
		c.mu.Unlock()
		if ok {
			return caps, nil
		}
		return Caps{}, ErrUnavailable
	}
	c.busy = true
	c.capsTry = now
	c.mu.Unlock()

	fetched, err := c.fetchCaps(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.busy = false
	if err == nil {
		c.caps, c.capsAt = &fetched, c.now()
		return fetched, nil
	}
	if caps, ok := c.copyLocked(); ok {
		return caps, nil
	}
	return Caps{}, ErrUnavailable
}

// copyLocked returns the stored capabilities, when there is one. The caller holds c.mu.
func (c *Client) copyLocked() (Caps, bool) {
	if c.caps == nil {
		return Caps{}, false
	}
	return *c.caps, true
}

// fetchCaps makes one request for the capabilities document and reads its three fields.
func (c *Client) fetchCaps(ctx context.Context) (Caps, error) {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, c.base+capsPath, nil)
	if err != nil {
		return Caps{}, ErrUnavailable
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return Caps{}, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Caps{}, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCapsBytes+1))
	if err != nil || len(body) > maxCapsBytes {
		return Caps{}, ErrUnavailable
	}
	var doc struct {
		ConvertTo struct {
			Available bool `json:"available"`
		} `json:"convert-to"`
		ProductVersion     string `json:"productVersion"`
		ProductVersionHash string `json:"productVersionHash"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return Caps{}, ErrUnavailable
	}
	return Caps{Available: doc.ConvertTo.Available, Version: doc.ProductVersion, VersionHash: doc.ProductVersionHash}, nil
}

// formatsVersion names the fixed list and the client's request shape. A change to either makes new converter keys.
const formatsVersion = "office-convert-1"

// ConverterKey is the converter key of the rows a build of Collabora makes under limits. It names the build (the
// capabilities' productVersionHash and productVersion), the client's version, and the limits that decide a refusal, so
// a new build or a new limit makes new rows (design decision 37).
func ConverterKey(caps Caps, limits string) string {
	sum := sha256.Sum256([]byte(formatsVersion + "|" + caps.VersionHash + "|" + caps.Version + "|" + limits))
	return formatsVersion + "+" + hex.EncodeToString(sum[:6])
}
