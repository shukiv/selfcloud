// SPDX-License-Identifier: AGPL-3.0-or-later

// Package wopitest is a fake Collabora Online for tests. It serves a WOPI discovery document over TLS, holds proof
// keys it can rotate, and signs requests the way Collabora signs them. It is not a substitute for a real editor: it
// shows what Selfcloud's checks accept and refuse, and nothing about Collabora's own behaviour.
package wopitest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/wopi"
)

// DiscoveryPath is the path Collabora serves discovery at, which office.discovery_url must end in.
const DiscoveryPath = "/hosting/discovery"

// capabilitiesPath and convertPrefix are the paths of the conversion interface (convert.go).
const (
	capabilitiesPath = "/hosting/capabilities"
	convertPrefix    = "/cool/convert-to"
)

// Collabora is a fake Collabora with its own TLS server.
type Collabora struct {
	srv *httptest.Server

	mu       sync.Mutex
	current  *rsa.PrivateKey
	previous *rsa.PrivateKey // the key before the last rotation; nil until one happens

	// failStatus, when not 0, is the status every discovery request answers with. Tests use it to make the
	// discovery fetch fail.
	failStatus atomic.Int32
	// onlyEdit, when set, leaves the view actions out of discovery, as a discovery document can for the types it
	// lists only an edit action for.
	onlyEdit atomic.Bool
	// hits counts the discovery requests received.
	hits atomic.Int32

	// conv holds the conversion endpoint's controls and record (convert.go).
	conv convertState
}

// New starts a fake Collabora with two fresh 2048-bit proof keys. It is closed when the test ends.
func New(t *testing.T) *Collabora {
	t.Helper()
	c := &Collabora{current: newKey(t)}
	c.srv = httptest.NewTLSServer(http.HandlerFunc(c.serve))
	t.Cleanup(c.srv.Close)
	return c
}

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// DiscoveryURL is the discovery URL to configure: the fake's TLS address and DiscoveryPath.
func (c *Collabora) DiscoveryURL() string { return c.srv.URL + DiscoveryPath }

// Origin is the fake's origin, scheme and host.
func (c *Collabora) Origin() string { return c.srv.URL }

// Client is an HTTP client that trusts the fake's certificate.
func (c *Collabora) Client() *http.Client { return c.srv.Client() }

// Hits is how many discovery requests the fake has answered.
func (c *Collabora) Hits() int { return int(c.hits.Load()) }

// FailDiscovery makes every discovery request answer status, until ResumeDiscovery.
func (c *Collabora) FailDiscovery(status int) { c.failStatus.Store(int32(status)) }

// OmitViewActions makes discovery list only the edit actions, so a type has no view action.
func (c *Collabora) OmitViewActions() { c.onlyEdit.Store(true) }

// ResumeDiscovery makes discovery answer normally again.
func (c *Collabora) ResumeDiscovery() { c.failStatus.Store(0) }

// Rotate makes a new current proof key, and keeps the old current key as the old one, as Collabora does when it
// rotates. A host that has not fetched discovery since then holds only the old key.
func (c *Collabora) Rotate(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.previous = c.current
	c.current = newKey(t)
}

// Sign returns the value of X-WOPI-Proof for a request with the given access token, URL (as Collabora sends it,
// query string included) and timestamp, signed with the current key. For the old key, use SignWith.
func (c *Collabora) Sign(token, url string, ts int64) string {
	c.mu.Lock()
	key := c.current
	c.mu.Unlock()
	return SignWith(key, token, url, ts)
}

// SignWith signs a request with key, as Collabora's proof does: RSA PKCS#1 v1.5 with SHA-256, over the expected
// proof, base64-encoded. Tests use it to sign with a key that is not the current one.
func SignWith(key *rsa.PrivateKey, token, url string, ts int64) string {
	digest := sha256.Sum256(wopi.ExpectedProof(token, url, ts))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		panic(fmt.Sprintf("wopitest: sign: %v", err))
	}
	return base64.StdEncoding.EncodeToString(sig)
}

// Current returns the current key, for tests that sign with it directly.
func (c *Collabora) Current() *rsa.PrivateKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

// Previous returns the key before the last rotation, or nil.
func (c *Collabora) Previous() *rsa.PrivateKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.previous
}

// Ticks converts a time to the timestamp a WOPI request carries, the .NET tick count that wopi.TimestampFresh reads.
func Ticks(t time.Time) int64 {
	return t.UnixNano()/100 + wopi.TicksAtUnixEpoch
}

// serve answers discovery. Every other path is not found.
func (c *Collabora) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == capabilitiesPath || strings.HasPrefix(r.URL.Path, convertPrefix) {
		c.serveConvert(w, r)
		return
	}
	if r.URL.Path == "/redirect" {
		http.Redirect(w, r, DiscoveryPath, http.StatusFound)
		return
	}
	if r.URL.Path != DiscoveryPath {
		http.NotFound(w, r)
		return
	}
	c.hits.Add(1)
	if st := int(c.failStatus.Load()); st != 0 {
		http.Error(w, "fake discovery failure", st)
		return
	}
	c.mu.Lock()
	cur := c.current
	var old *rsa.PrivateKey
	if c.previous != nil {
		old = c.previous
	}
	c.mu.Unlock()
	w.Header().Set("Content-Type", "text/xml")
	fmt.Fprint(w, discoveryXML(c.srv.URL, cur, old, c.onlyEdit.Load(), c.ConvertListed()))
}

// discoveryXML is a discovery document with the actions the tests use: edit and view for docx, view for pdf, and an
// edit of odt whose URL is on another host, which Selfcloud must drop.
func discoveryXML(origin string, cur, old *rsa.PrivateKey, onlyEdit, listConvert bool) string {
	convert := func(line string) string {
		if !listConvert {
			return ""
		}
		return line
	}
	view := func(line string) string {
		if onlyEdit {
			return ""
		}
		return line
	}
	mod := func(k *rsa.PrivateKey) string {
		if k == nil {
			return ""
		}
		return base64.StdEncoding.EncodeToString(k.N.Bytes())
	}
	exp := func(k *rsa.PrivateKey) string {
		if k == nil {
			return ""
		}
		e := make([]byte, 0, 4)
		for v := k.E; v > 0; v >>= 8 {
			e = append([]byte{byte(v)}, e...)
		}
		return base64.StdEncoding.EncodeToString(e)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<wopi-discovery>
  <net-zone name="external-https">
    <app name="writer">
      <action name="edit" ext="docx" urlsrc="%[1]s/browser/dist/cool.html?&lt;rbs=1&amp;&gt;"/>
      %[8]s
      %[6]s
    </app>
    <app name="impress">
      %[7]s
    </app>
    <app name="foreign">
      <action name="edit" ext="odt" urlsrc="https://elsewhere.example/browser/dist/cool.html?"/>
      %[9]s
    </app>
  </net-zone>
  <proof-key value="" oldvalue="" modulus="%[2]s" exponent="%[3]s" oldmodulus="%[4]s" oldexponent="%[5]s"/>
</wopi-discovery>
`, origin, mod(cur), exp(cur), mod(old), exp(old),
		view(fmt.Sprintf(`<action name="view" ext="docx" urlsrc="%s/browser/dist/cool.html?"/>`, origin)),
		view(fmt.Sprintf(`<action name="view" ext="pdf" urlsrc="%s/browser/dist/view.html?"/>`, origin)),
		convert(fmt.Sprintf(`<action name="convert-to" ext="docx" urlsrc="%s/cool/convert-to"/>`, origin)),
		convert(`<action name="convert-to" ext="xlsx" urlsrc="https://elsewhere.example/cool/convert-to"/>`))
}

// Discovery returns a wopi.Discovery for the fake, which trusts its certificate.
func (c *Collabora) Discovery() *wopi.Discovery {
	return wopi.NewDiscovery(c.DiscoveryURL(), c.Client(), nil)
}
