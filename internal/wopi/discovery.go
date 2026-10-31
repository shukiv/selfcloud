// SPDX-License-Identifier: AGPL-3.0-or-later

// Package wopi reads Collabora's WOPI discovery document and checks the proof keys that Collabora signs its
// requests with (design section 10, decision 31). It has no store and no HTTP handler: the handlers live in the
// api package, which owns the rules for files.
package wopi

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Bounds on the discovery fetch. The URL is operator configuration, so it is trusted; the bounds still stop a
// misbehaving or hostile answer from holding a connection, or memory, for long.
const (
	// MaxDocumentBytes is the most of the discovery document that is read. Collabora's document is well under
	// this; a larger answer is refused.
	MaxDocumentBytes = 1 << 20
	// FetchTimeout is the longest one fetch may take, connect and body together.
	FetchTimeout = 10 * time.Second
	// RefreshEvery is how old a cached document may get before the next use fetches it again.
	RefreshEvery = time.Hour
	// RetryGap is the least time between two fetches that failed, and between the fetches a failed proof asks for.
	// A forged request cannot make Selfcloud fetch more often than this.
	RetryGap = time.Minute
)

// ErrUnavailable is returned when no discovery document is cached and the fetch cannot give one.
var ErrUnavailable = errors.New("wopi: discovery is not available")

// Document is what Selfcloud uses from the discovery document: the proof keys and the editor URLs by extension.
type Document struct {
	// Current verifies the proof signature of the current key. Old is the previous key; nil when there is none.
	Current, Old *rsa.PublicKey
	// actions maps a lower-case extension to its actions by name ("edit", "view"), each an editor URL template
	// with the placeholders still in it. Only URLs on the discovery host are kept.
	actions map[string]map[string]string
}

// Discovery fetches the document from one URL and keeps the last good copy. Its methods are safe for concurrent use.
type Discovery struct {
	url    string
	client *http.Client
	now    func() time.Time

	mu        sync.Mutex
	doc       *Document
	fetchedAt time.Time
	lastTry   time.Time
	busy      bool // a fetch is running; d.mu is not held during it
}

// NewDiscovery returns a Discovery for url. client may be nil, which gives a client with FetchTimeout. Whatever
// client is given, the copy used here refuses every redirect; tests pass a client that trusts their own server.
func NewDiscovery(rawURL string, client *http.Client, now func() time.Time) *Discovery {
	if client == nil {
		client = &http.Client{Timeout: FetchTimeout}
	}
	c := *client
	c.CheckRedirect = refuseRedirect
	if now == nil {
		now = time.Now
	}
	return &Discovery{url: rawURL, client: &c, now: now}
}

// refuseRedirect stops every redirect. Discovery is one fixed address; an answer that moves it is not followed.
func refuseRedirect(*http.Request, []*http.Request) error {
	return errors.New("wopi: discovery redirects are refused")
}

// Get returns the cached document, fetching it first when none is cached or the cache is older than RefreshEvery.
// A failed refresh keeps the old document. With no document at all, it returns ErrUnavailable; a fetch that failed
// less than RetryGap ago is not repeated.
func (d *Discovery) Get(ctx context.Context) (*Document, error) {
	d.mu.Lock()
	now := d.now()
	// A document older than RefreshEvery is still served while the last fetch is within RetryGap, so an outage
	// costs one fetch per RetryGap, not one per read.
	if d.doc != nil && (now.Sub(d.fetchedAt) < RefreshEvery || now.Sub(d.lastTry) < RetryGap) {
		doc := d.doc
		d.mu.Unlock()
		return doc, nil
	}
	if d.doc == nil && !d.lastTry.IsZero() && now.Sub(d.lastTry) < RetryGap {
		d.mu.Unlock()
		return nil, ErrUnavailable
	}
	return d.refresh(ctx) // releases d.mu
}

// Retry fetches the document again, unless the last fetch was less than RetryGap ago. It is called when a proof
// fails, so that a key Collabora has just rotated is picked up, while forged requests cannot make Selfcloud fetch
// more often than RetryGap. It returns the document that is cached afterwards, which may be the old one.
func (d *Discovery) Retry(ctx context.Context) (*Document, error) {
	d.mu.Lock()
	if !d.lastTry.IsZero() && d.now().Sub(d.lastTry) < RetryGap {
		doc := d.doc
		d.mu.Unlock()
		if doc == nil {
			return nil, ErrUnavailable
		}
		return doc, nil
	}
	return d.refresh(ctx) // releases d.mu
}

// refresh fetches the document with d.mu released, so a slow fetch does not hold up readers of the cache. While one
// fetch runs, another caller gets the cached document and does not start its own. A failure keeps the cached document.
// It is entered with d.mu held and returns with it released.
func (d *Discovery) refresh(ctx context.Context) (*Document, error) {
	if d.busy {
		doc := d.doc
		d.mu.Unlock()
		if doc == nil {
			return nil, ErrUnavailable
		}
		return doc, nil
	}
	d.busy = true
	d.lastTry = d.now()
	d.mu.Unlock()

	doc, err := d.fetch(ctx)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.busy = false
	if err == nil {
		d.doc, d.fetchedAt = doc, d.now()
		return doc, nil
	}
	if d.doc != nil {
		return d.doc, nil
	}
	return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
}

// fetch makes one request for the document and parses it. The body is read to MaxDocumentBytes and no further.
func (d *Discovery) fetch(ctx context.Context) (*Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxDocumentBytes {
		return nil, fmt.Errorf("discovery document is larger than %d bytes", MaxDocumentBytes)
	}
	return ParseDocument(body, d.url)
}

// xmlDiscovery is the part of the discovery XML that is read.
type xmlDiscovery struct {
	Zones []struct {
		Name string `xml:"name,attr"`
		Apps []struct {
			Actions []struct {
				Name   string `xml:"name,attr"`
				Ext    string `xml:"ext,attr"`
				URLSrc string `xml:"urlsrc,attr"`
			} `xml:"action"`
		} `xml:"app"`
	} `xml:"net-zone"`
	ProofKey struct {
		Modulus     string `xml:"modulus,attr"`
		Exponent    string `xml:"exponent,attr"`
		OldModulus  string `xml:"oldmodulus,attr"`
		OldExponent string `xml:"oldexponent,attr"`
	} `xml:"proof-key"`
}

// ParseDocument reads a discovery document fetched from discoveryURL. Only the first zone whose name starts with
// "external" is used, or the first zone when none does. An editor URL is kept only when its host is the discovery
// host: an action that points anywhere else is dropped, so a file is never opened through a foreign address.
// The current proof key is required; the old one is optional.
func ParseDocument(body []byte, discoveryURL string) (*Document, error) {
	var x xmlDiscovery
	if err := xml.Unmarshal(body, &x); err != nil {
		return nil, fmt.Errorf("discovery is not XML: %w", err)
	}
	cur, err := rsaKey(x.ProofKey.Modulus, x.ProofKey.Exponent)
	if err != nil {
		return nil, fmt.Errorf("discovery proof key: %w", err)
	}
	if cur == nil {
		return nil, errors.New("discovery has no proof key")
	}
	old, err := rsaKey(x.ProofKey.OldModulus, x.ProofKey.OldExponent)
	if err != nil {
		return nil, fmt.Errorf("discovery old proof key: %w", err)
	}
	doc := &Document{Current: cur, Old: old, actions: map[string]map[string]string{}}
	zone := -1
	for i, z := range x.Zones {
		if strings.HasPrefix(z.Name, "external") {
			zone = i
			break
		}
	}
	if zone < 0 && len(x.Zones) > 0 {
		zone = 0
	}
	if zone < 0 {
		return doc, nil
	}
	base, err := url.Parse(discoveryURL)
	if err != nil {
		return nil, err
	}
	for _, app := range x.Zones[zone].Apps {
		for _, a := range app.Actions {
			ext := strings.ToLower(a.Ext)
			if ext == "" || a.Name == "" || !sameHost(a.URLSrc, base) {
				continue
			}
			if doc.actions[ext] == nil {
				doc.actions[ext] = map[string]string{}
			}
			if _, dup := doc.actions[ext][a.Name]; !dup {
				doc.actions[ext][a.Name] = a.URLSrc
			}
		}
	}
	return doc, nil
}

// sameHost reports whether the editor URL src is on the discovery URL's host (scheme and host, case aside).
func sameHost(src string, base *url.URL) bool {
	u, err := url.Parse(src)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host)
}

// rsaKey decodes a proof key from its base64 modulus and exponent (the non-.NET form of the discovery document).
// Both empty means no key; one empty, or a malformed value, is an error.
func rsaKey(modulus, exponent string) (*rsa.PublicKey, error) {
	if modulus == "" && exponent == "" {
		return nil, nil
	}
	if modulus == "" || exponent == "" {
		return nil, errors.New("modulus and exponent must both be set")
	}
	n, err := base64.StdEncoding.DecodeString(modulus)
	if err != nil {
		return nil, err
	}
	e, err := base64.StdEncoding.DecodeString(exponent)
	if err != nil {
		return nil, err
	}
	ev := new(big.Int).SetBytes(e)
	// An exponent that does not fit a machine int, or is below 3, is not a usable RSA key.
	if !ev.IsInt64() || ev.Int64() < 3 || ev.Int64() > 1<<31 {
		return nil, errors.New("exponent out of range")
	}
	nv := new(big.Int).SetBytes(n)
	if nv.BitLen() < 1024 {
		return nil, errors.New("modulus is too short")
	}
	return &rsa.PublicKey{N: nv, E: int(ev.Int64())}, nil
}

// placeholder matches an optional parameter that Collabora puts in an editor URL template, such as <rbs=1&>. It is
// removed; the template's fixed part is kept.
var placeholder = regexp.MustCompile(`<[^>]*>`)

// EditorURL returns the editor URL for extension ext in the given mode ("edit" or "view"), with WOPISrc set to
// wopiSrc. It reports false when the document has no such action for the extension.
func (doc *Document) EditorURL(ext, mode, wopiSrc string) (string, bool) {
	if doc == nil {
		return "", false
	}
	tmpl, ok := doc.actions[strings.ToLower(ext)][mode]
	if !ok {
		return "", false
	}
	base := placeholder.ReplaceAllString(tmpl, "")
	// A template ends in "?" or "&" when it has no parameters left, or after a removed placeholder; that needs no
	// separator. Otherwise the parameter joins with "&" when there is a query, and with "?" when there is none.
	sep := "?"
	switch {
	case strings.HasSuffix(base, "?"), strings.HasSuffix(base, "&"):
		sep = ""
	case strings.Contains(base, "?"):
		sep = "&"
	}
	return base + sep + "WOPISrc=" + url.QueryEscape(wopiSrc), true
}

// Openable reports whether the document has an action in mode ("edit" or "view") for extension ext.
func (doc *Document) Openable(ext, mode string) bool {
	if doc == nil {
		return false
	}
	_, ok := doc.actions[strings.ToLower(ext)][mode]
	return ok
}

// ConvertibleFrom reports whether extension ext is a type the converter takes as a source (decision 37). Live Collabora
// 26.04.5.2 lists no convert-to action in its discovery at all, so the answer is the edit action: the types Collabora
// opens for editing on the discovery host. The action is read as the parser reads every action, so one on another host
// does not count. Whether the converter is available is the capabilities' business, not this document's.
func (doc *Document) ConvertibleFrom(ext string) bool {
	if doc == nil {
		return false
	}
	_, ok := doc.actions[strings.ToLower(ext)]["edit"]
	return ok
}
