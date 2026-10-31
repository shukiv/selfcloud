// SPDX-License-Identifier: AGPL-3.0-or-later

package wopitest

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// convertState is the conversion endpoint's behaviour and record. Tests set the mode before a request, and read the record
// after it.
type convertState struct {
	mu          sync.Mutex
	mode        string        // "ok" (the default), "refuse", "fail", "wrongtype", "wrongmagic", "big", "redirect", "stall"
	size        int           // the output size in "ok" and "big"
	stall       time.Duration // how long "ok" waits before it answers
	capsOff     bool          // convert-to is not available
	capsHash    string        // productVersionHash
	hits        int           // conversion requests received
	redirected  int           // requests that followed a redirect into the fake
	lastFormat  string
	lastName    string // the file name of the "data" part, as Collabora received it
	inflight    int
	maxInflight int
	capsDown    bool // capabilities answer 503
	unlisted    bool // discovery lists no conversion action
}

// SetConvertMode sets what the conversion endpoint answers: "ok", "refuse" (400), "fail" (503), "wrongtype" (a text
// answer), "wrongmagic" (a PDF type with HTML bytes), "big" (more bytes than the size), "redirect" (302 to the fake),
// or "stall" (waits until the request is cancelled).
func (c *Collabora) SetConvertMode(mode string) {
	c.conv.mu.Lock()
	defer c.conv.mu.Unlock()
	c.conv.mode = mode
}

// SetConvertSize sets the output size of "ok" and "big" answers, in bytes.
func (c *Collabora) SetConvertSize(n int) {
	c.conv.mu.Lock()
	defer c.conv.mu.Unlock()
	c.conv.size = n
}

// SetConvertStall makes an "ok" answer wait for d before it is sent.
func (c *Collabora) SetConvertStall(d time.Duration) {
	c.conv.mu.Lock()
	defer c.conv.mu.Unlock()
	c.conv.stall = d
}

// SetCapabilities sets whether convert-to is available, and the build hash the capabilities document names.
func (c *Collabora) SetCapabilities(available bool, hash string) {
	c.conv.mu.Lock()
	defer c.conv.mu.Unlock()
	c.conv.capsOff = !available
	c.conv.capsHash = hash
}

// SetCapabilitiesDown makes the capabilities document answer 503, as a Collabora that is not up does.
func (c *Collabora) SetCapabilitiesDown(down bool) {
	c.conv.mu.Lock()
	defer c.conv.mu.Unlock()
	c.conv.capsDown = down
}

// SetConvertListed sets whether discovery lists any conversion action. A real discovery document may list none.
func (c *Collabora) SetConvertListed(listed bool) {
	c.conv.mu.Lock()
	defer c.conv.mu.Unlock()
	c.conv.unlisted = !listed
}

// ConvertListed reports whether discovery lists conversion actions.
func (c *Collabora) ConvertListed() bool {
	c.conv.mu.Lock()
	defer c.conv.mu.Unlock()
	return !c.conv.unlisted
}

// ConvertHits returns how many conversion requests the fake received.
func (c *Collabora) ConvertHits() int {
	c.conv.mu.Lock()
	defer c.conv.mu.Unlock()
	return c.conv.hits
}

// RedirectHits returns how many requests reached the redirect target. Selfcloud must never reach it.
func (c *Collabora) RedirectHits() int {
	c.conv.mu.Lock()
	defer c.conv.mu.Unlock()
	return c.conv.redirected
}

// LastConvert returns the format and the file name of the last conversion request.
func (c *Collabora) LastConvert() (format, name string) {
	c.conv.mu.Lock()
	defer c.conv.mu.Unlock()
	return c.conv.lastFormat, c.conv.lastName
}

// MaxInflight returns the most conversions the fake saw in progress at once.
func (c *Collabora) MaxInflight() int {
	c.conv.mu.Lock()
	defer c.conv.mu.Unlock()
	return c.conv.maxInflight
}

// magics are the file signatures the fake writes for each output, by format.
var magics = map[string]struct{ ctype, magic string }{
	"pdf":  {"application/pdf", "%PDF-1.4\n"},
	"png":  {"image/png", "\x89PNG\r\n\x1a\n"},
	"docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "PK\x03\x04"},
	"xlsx": {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "PK\x03\x04"},
	"pptx": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", "PK\x03\x04"},
	"odt":  {"application/vnd.oasis.opendocument.text", "PK\x03\x04"},
	"ods":  {"application/vnd.oasis.opendocument.spreadsheet", "PK\x03\x04"},
	"odp":  {"application/vnd.oasis.opendocument.presentation", "PK\x03\x04"},
}

// serveConvert answers the capabilities document, the conversion endpoint and the redirect target.
func (c *Collabora) serveConvert(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/cool/redirected" {
		c.conv.mu.Lock()
		c.conv.redirected++
		c.conv.mu.Unlock()
		http.Error(w, "reached", http.StatusOK)
		return
	}
	if r.URL.Path == capabilitiesPath {
		c.conv.mu.Lock()
		off, hash, down := c.conv.capsOff, c.conv.capsHash, c.conv.capsDown
		c.conv.mu.Unlock()
		if down {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"convert-to":{"available":%t,"endpoint":"/cool/convert-to"},"productVersion":"24.04.7.2","productVersionHash":%q}`,
			!off, hash)
		return
	}
	format := strings.TrimPrefix(r.URL.Path, convertPrefix+"/")
	if r.Method != http.MethodPost || format == r.URL.Path || strings.Contains(format, "/") {
		http.NotFound(w, r)
		return
	}
	name, err := dataPartName(r)
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	c.conv.mu.Lock()
	c.conv.hits++
	c.conv.lastFormat, c.conv.lastName = format, name
	c.conv.inflight++
	c.conv.maxInflight = max(c.conv.maxInflight, c.conv.inflight)
	mode, size, stall := c.conv.mode, c.conv.size, c.conv.stall
	c.conv.mu.Unlock()
	defer func() {
		c.conv.mu.Lock()
		c.conv.inflight--
		c.conv.mu.Unlock()
	}()
	if mode == "" {
		mode = "ok"
	}
	// The body is read in full first, as Collabora reads an upload, so a stall does not hold a half-read body.
	_, _ = io.Copy(io.Discard, r.Body)
	out, ok := magics[format]
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch mode {
	case "refuse":
		http.Error(w, "SECRET-CONVERTER-TEXT for "+name, http.StatusBadRequest)
	case "fail":
		http.Error(w, "SECRET-CONVERTER-TEXT overloaded", http.StatusServiceUnavailable)
	case "redirect":
		http.Redirect(w, r, c.srv.URL+"/cool/redirected", http.StatusFound)
	case "stall":
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	case "wrongtype":
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "not the format asked for")
	case "wrongmagic":
		w.Header().Set("Content-Type", out.ctype)
		io.WriteString(w, "<html>not a file</html>")
	case "big":
		w.Header().Set("Content-Type", out.ctype)
		w.Write(fileBytes(out.magic, size))
	default: // "ok"
		if stall > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(stall):
			}
		}
		w.Header().Set("Content-Type", out.ctype)
		w.Write(fileBytes(out.magic, size))
	}
}

// fileBytes returns a file of n bytes that starts with magic. A size under the magic's length gives the magic whole.
func fileBytes(magic string, n int) []byte {
	if n < len(magic) {
		n = len(magic)
	}
	b := bytes.Repeat([]byte{'x'}, n)
	copy(b, magic)
	return b
}

// dataPartName reads the multipart body and returns the file name of its "data" part. It reads the whole body.
func dataPartName(r *http.Request) (string, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", err
	}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return "", fmt.Errorf("no data part")
		}
		if err != nil {
			return "", err
		}
		if p.FormName() == "data" {
			name := p.FileName()
			_, _ = io.Copy(io.Discard, p)
			return name, nil
		}
		_, _ = io.Copy(io.Discard, p)
	}
}
