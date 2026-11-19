// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/base64"
	"encoding/xml"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// davServer is a test server with the WebDAV mount on and an https public_url, so Basic credentials
// are accepted from any address.
func davServer(t *testing.T, mutate ...func(*config.Config)) *Server {
	t.Helper()
	base := []func(*config.Config){func(c *config.Config) {
		c.Dav.Enabled = true
		c.PublicURL = "https://drive.example.com"
	}}
	return newAuthServer(t, &mail.Recorder{}, append(base, mutate...)...)
}

// davFix is an account with one signed-in member and a write app password for the mount.
type davFix struct {
	s      *Server
	admin  string
	acc    accountJSON
	key    string
	u      userResponse
	sess   testSession
	secret string
}

func newDavFix(t *testing.T, mutate ...func(*config.Config)) *davFix {
	t.Helper()
	s := davServer(t, mutate...)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	u, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	created := makeAppPassword(t, s, sess, "mount", "write")
	return &davFix{s: s, admin: admin, acc: acc, key: key, u: u, sess: sess, secret: created.Secret}
}

// davBasic sends a request with the app password as a Basic credential. The username is ignored by
// the mount; the password is the app password.
func davBasic(t *testing.T, s *Server, method, path, secret, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:"+secret)))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// davEl is a generic XML node the mount tests decode into.
type davEl struct {
	XMLName xml.Name
	Text    string  `xml:",chardata"`
	Kids    []davEl `xml:",any"`
}

func (e davEl) find(local string) (davEl, bool) {
	for _, k := range e.Kids {
		if k.XMLName.Local == local {
			return k, true
		}
	}
	return davEl{}, false
}

func (e davEl) all(local string) []davEl {
	var out []davEl
	for _, k := range e.Kids {
		if k.XMLName.Local == local {
			out = append(out, k)
		}
	}
	return out
}

// davMultistatus asserts a 207 and decodes its body.
func davMultistatus(t *testing.T, rec *httptest.ResponseRecorder) davEl {
	t.Helper()
	wantStatus(t, rec, 207)
	var e davEl
	if err := xml.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("the 207 body is not valid XML: %v\n%s", err, rec.Body.String())
	}
	if e.XMLName.Local != "multistatus" {
		t.Fatalf("root = %v, want multistatus", e.XMLName)
	}
	return e
}

// davHrefs returns the href of every response in a multistatus.
func davHrefs(ms davEl) []string {
	var out []string
	for _, r := range ms.all("response") {
		h, _ := r.find("href")
		out = append(out, h.Text)
	}
	return out
}

// davProp reads one property's text from the 200 propstat of the response with the given href.
func davProp(t *testing.T, ms davEl, href, prop string) (string, bool) {
	t.Helper()
	for _, r := range ms.all("response") {
		h, _ := r.find("href")
		if h.Text != href {
			continue
		}
		for _, ps := range r.all("propstat") {
			st, _ := ps.find("status")
			if !strings.Contains(st.Text, "200") {
				continue
			}
			p, _ := ps.find("prop")
			if el, ok := p.find(prop); ok {
				return el.Text, true
			}
		}
	}
	return "", false
}

// personalFolderOf returns the signed-in user's personal folder from GET /folders.
func personalFolderOf(t *testing.T, s *Server, sess testSession) folderAccessJSON {
	t.Helper()
	list := decode[listResponse[folderAccessJSON]](t, callSession(t, s, sess, "GET", "/api/v1/folders", nil))
	for _, f := range list.Items {
		if f.Kind == "personal" {
			return f
		}
	}
	t.Fatal("no personal folder in GET /folders")
	return folderAccessJSON{}
}

func TestDAVOffAnswersNotFound(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.PublicURL = "https://drive.example.com" })
	for _, method := range []string{"PROPFIND", "OPTIONS", "GET"} {
		rec := rawCall(t, s, method, "/dav/", "", "", nil)
		wantStatus(t, rec, 404)
	}
}

func TestDAVOptions(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	rec := davBasic(t, fx.s, "OPTIONS", "/dav/", fx.secret, "", nil)
	wantStatus(t, rec, 200)
	if got := rec.Header().Get("DAV"); got != "1" {
		t.Errorf("DAV = %q, want 1", got)
	}
	allow := rec.Header().Get("Allow")
	for _, m := range []string{"OPTIONS", "PROPFIND", "GET", "HEAD"} {
		if !strings.Contains(allow, m) {
			t.Errorf("Allow = %q, missing %s", allow, m)
		}
	}
}

func TestDAVAuthFailures(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)

	// No credential.
	rec := rawCall(t, fx.s, "PROPFIND", "/dav/", "", "", map[string]string{"Depth": "0"})
	wantStatus(t, rec, 401)
	if ch := rec.Header().Get("WWW-Authenticate"); !strings.Contains(ch, "Basic") {
		t.Errorf("WWW-Authenticate = %q, want a Basic challenge", ch)
	}
	// A wrong secret, and the real password, are refused.
	wantStatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", "scp_wrong", "", map[string]string{"Depth": "0"}), 401)
	wantStatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", testPassword, "", map[string]string{"Depth": "0"}), 401)
	// Keys are not mount credentials.
	wantStatus(t, rawCall(t, fx.s, "PROPFIND", "/dav/", fx.key, "", map[string]string{"Depth": "0"}), 401)
	wantStatus(t, rawCall(t, fx.s, "PROPFIND", "/dav/", fx.admin, "", map[string]string{"Depth": "0"}), 401)
	// A valid secret works.
	wantStatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", fx.secret, "", map[string]string{"Depth": "0"}), 207)
}

func TestDAVRevokedAndDeadCredentials(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	// Revoke the app password; its secret stops working.
	list := decode[listResponse[apwItem]](t, callSession(t, fx.s, fx.sess, "GET", apwPath, nil))
	wantStatus(t, callSession(t, fx.s, fx.sess, "DELETE", apwPath+"/"+list.Items[0].ID, nil), 204)
	wantStatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", fx.secret, "", map[string]string{"Depth": "0"}), 401)

	// A disabled user's app password is refused.
	fx2 := newDavFix(t)
	wantStatus(t, call(t, fx2.s, "PATCH", "/api/v1/accounts/"+fx2.acc.ID+"/users/"+fx2.u.ID, fx2.key,
		map[string]any{"status": "disabled"}), 200)
	wantStatus(t, davBasic(t, fx2.s, "PROPFIND", "/dav/", fx2.secret, "", map[string]string{"Depth": "0"}), 401)

	// A suspended account's app password is refused.
	fx3 := newDavFix(t)
	wantStatus(t, call(t, fx3.s, "PATCH", "/api/v1/accounts/"+fx3.acc.ID, fx3.admin,
		map[string]any{"status": "suspended"}), 200)
	wantStatus(t, davBasic(t, fx3.s, "PROPFIND", "/dav/", fx3.secret, "", map[string]string{"Depth": "0"}), 401)
}

func TestDAVMustChangeRefused(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	setMustChange(t, fx.s, fx.acc.ID, fx.u.ID, true)
	wantStatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", fx.secret, "", map[string]string{"Depth": "0"}), 401)
}

func TestDAVBasicNeedsHTTPSOrLoopback(t *testing.T) {
	t.Parallel()
	// http public_url: Basic is refused from a non-loopback address, accepted from loopback.
	s := newAuthServer(t, &mail.Recorder{}, davOn) // public_url stays http://localhost:8080
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	secret := makeAppPassword(t, s, sess, "mount", "read").Secret

	req := httptest.NewRequest("PROPFIND", "/dav/", strings.NewReader(""))
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:"+secret)))
	req.Header.Set("Depth", "0")
	req.RemoteAddr = "203.0.113.9:4000" // non-loopback
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, 401)

	req.RemoteAddr = "127.0.0.1:4000" // loopback
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, 207)
}

func TestDAVBearerTokens(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	// A delegated token acting as the user reads the mount.
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.u.ID, "all": true})
	wantStatus(t, rawCall(t, fx.s, "PROPFIND", "/dav/", tok, "", map[string]string{"Depth": "0"}), 207)
	// A device token reads it too.
	_, devTok := approvedToken(t, fx.s, fx.sess)
	wantStatus(t, rawCall(t, fx.s, "PROPFIND", "/dav/", devTok, "", map[string]string{"Depth": "0"}), 207)
	// A share-link credential is not a mount credential.
	wantStatus(t, rawCall(t, fx.s, "PROPFIND", "/dav/", "scl_notatoken", "", map[string]string{"Depth": "0"}), 401)
}

func TestDAVAuthFailedRateLimit(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	hdr := map[string]string{"Depth": "0"}
	// Thirty failed sign-ins spend the per-address budget; the thirty-first is refused with 429.
	for i := 0; i < 30; i++ {
		wantStatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", "scp_wrong", "", hdr), 401)
	}
	wantStatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", "scp_wrong", "", hdr), 429)
	// The person's web sign-in budget is untouched: a correct login still works.
	wantStatus(t, login(t, fx.s, "ann@example.com", testPassword, ""), 200)
}

func TestDAVPropfindRoot(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	// Depth 0: only the root.
	ms := davMultistatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", fx.secret, "", map[string]string{"Depth": "0"}))
	hrefs := davHrefs(ms)
	if len(hrefs) != 1 || hrefs[0] != "/dav/" {
		t.Fatalf("Depth 0 hrefs = %v, want [/dav/]", hrefs)
	}
	if name, ok := davProp(t, ms, "/dav/", "displayname"); !ok || name != "Selfcloud" {
		t.Errorf("root displayname = %q, want Selfcloud", name)
	}
	// Depth 1: the root and its top-level entries (My files and Shared with me at least).
	ms = davMultistatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", fx.secret, "", map[string]string{"Depth": "1"}))
	hrefs = davHrefs(ms)
	var hasMyFiles, hasShared bool
	for _, h := range hrefs {
		if strings.Contains(h, "My%20files") {
			hasMyFiles = true
		}
		if strings.Contains(h, "Shared%20with%20me") {
			hasShared = true
		}
	}
	if !hasMyFiles || !hasShared {
		t.Errorf("Depth 1 hrefs = %v, want My files and Shared with me", hrefs)
	}
}

func TestDAVDepthRefused(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	for _, depth := range []string{"infinity", "2", ""} {
		hdr := map[string]string{}
		if depth != "" {
			hdr["Depth"] = depth
		}
		rec := davBasic(t, fx.s, "PROPFIND", "/dav/", fx.secret, "", hdr)
		wantStatus(t, rec, 403)
		var e davEl
		if err := xml.Unmarshal(rec.Body.Bytes(), &e); err != nil {
			t.Fatalf("depth %q: body is not XML: %v", depth, err)
		}
		if _, ok := e.find("propfind-finite-depth"); !ok {
			t.Errorf("depth %q: no propfind-finite-depth in %s", depth, rec.Body.String())
		}
	}
}

func TestDAVPropfindBodyTooLarge(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	body := `<D:propfind xmlns:D="DAV:"><D:prop>` + strings.Repeat("<D:x/>", 70000) + `</D:prop></D:propfind>`
	rec := davBasic(t, fx.s, "PROPFIND", "/dav/", fx.secret, body, map[string]string{"Depth": "0"})
	wantStatus(t, rec, 413)
}

func TestDAVFilePropertiesAndDownload(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	pf := personalFolderOf(t, fx.s, fx.sess)
	nodeOf(t, writeFile(t, fx.s, fx.key, pf.ID, "hello.txt", "hello world"))

	href := "/dav/My%20files/hello.txt"
	ms := davMultistatus(t, davBasic(t, fx.s, "PROPFIND", href, fx.secret, "", map[string]string{"Depth": "0"}))
	if got, ok := davProp(t, ms, href, "getcontentlength"); !ok || got != "11" {
		t.Errorf("getcontentlength = %q, want 11", got)
	}
	if got, ok := davProp(t, ms, href, "displayname"); !ok || got != "hello.txt" {
		t.Errorf("displayname = %q", got)
	}
	if got, ok := davProp(t, ms, href, "getetag"); !ok || got == "" {
		t.Errorf("getetag = %q, want the file's ETag", got)
	}
	if _, ok := davProp(t, ms, href, "getlastmodified"); !ok {
		t.Error("no getlastmodified")
	}
	if _, ok := davProp(t, ms, href, "creationdate"); !ok {
		t.Error("no creationdate")
	}
	// A file's resourcetype is not a collection.
	for _, r := range ms.all("response") {
		for _, ps := range r.all("propstat") {
			p, _ := ps.find("prop")
			if rt, ok := p.find("resourcetype"); ok {
				if _, isCol := rt.find("collection"); isCol {
					t.Error("a file is marked as a collection")
				}
			}
		}
	}

	// GET returns the bytes as an attachment; HEAD returns no body.
	rec := davBasic(t, fx.s, "GET", href, fx.secret, "", nil)
	wantStatus(t, rec, 200)
	if rec.Body.String() != "hello world" {
		t.Errorf("GET body = %q", rec.Body.String())
	}
	if disp := rec.Header().Get("Content-Disposition"); !strings.Contains(disp, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", disp)
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Error("no Accept-Ranges: bytes")
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag on GET")
	}
	rec = davBasic(t, fx.s, "HEAD", href, fx.secret, "", nil)
	wantStatus(t, rec, 200)
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD has a body: %q", rec.Body.String())
	}
}

func TestDAVPropByNameAndPropname(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	pf := personalFolderOf(t, fx.s, fx.sess)
	writeFile(t, fx.s, fx.key, pf.ID, "f.txt", "data")
	href := "/dav/My%20files/f.txt"

	// A known and an unknown property: a 200 propstat and a 404 propstat.
	body := `<D:propfind xmlns:D="DAV:"><D:prop><D:displayname/><D:bogus/></D:prop></D:propfind>`
	ms := davMultistatus(t, davBasic(t, fx.s, "PROPFIND", href, fx.secret, body, map[string]string{"Depth": "0"}))
	var ok200, ok404 bool
	for _, r := range ms.all("response") {
		for _, ps := range r.all("propstat") {
			st, _ := ps.find("status")
			p, _ := ps.find("prop")
			if strings.Contains(st.Text, "200") {
				if _, ok := p.find("displayname"); ok {
					ok200 = true
				}
			}
			if strings.Contains(st.Text, "404") {
				if _, ok := p.find("bogus"); ok {
					ok404 = true
				}
			}
		}
	}
	if !ok200 || !ok404 {
		t.Errorf("prop by name: 200=%v 404=%v\n%s", ok200, ok404, ms)
	}

	// propname lists the applicable property names with empty values.
	body = `<D:propfind xmlns:D="DAV:"><D:propname/></D:propfind>`
	ms = davMultistatus(t, davBasic(t, fx.s, "PROPFIND", href, fx.secret, body, map[string]string{"Depth": "0"}))
	if _, ok := davProp(t, ms, href, "displayname"); !ok {
		t.Error("propname does not list displayname")
	}
}

func TestDAVMethodNotAllowed(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	// GET on a collection, and every write method, are 405 with the read-only Allow.
	for _, c := range []struct{ method, path string }{
		{"GET", "/dav/"},
		{"GET", "/dav/My%20files/"},
		{"PUT", "/dav/My%20files/x.txt"},
		{"DELETE", "/dav/My%20files/"},
		{"POST", "/dav/"},
		{"MKCOL", "/dav/new/"},
		{"PROPPATCH", "/dav/"},
		{"COPY", "/dav/"},
		{"MOVE", "/dav/"},
		{"LOCK", "/dav/"},
	} {
		rec := davBasic(t, fx.s, c.method, c.path, fx.secret, "", nil)
		wantStatus(t, rec, 405)
		if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "PROPFIND") {
			t.Errorf("%s %s: Allow = %q", c.method, c.path, allow)
		}
	}
}

func TestDAVNotFoundAndUnreachable(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	wantStatus(t, davBasic(t, fx.s, "GET", "/dav/My%20files/nope.txt", fx.secret, "", nil), 404)
	wantStatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/No%20such%20space/", fx.secret, "", map[string]string{"Depth": "0"}), 404)
	// A dot segment does not escape the mount.
	wantStatus(t, davBasic(t, fx.s, "GET", "/dav/My%20files/..%2F..%2Fetc", fx.secret, "", nil), 404)
}

func TestDAVNoDownloadRule(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	pf := personalFolderOf(t, fx.s, fx.sess)
	node := nodeOf(t, writeFile(t, fx.s, fx.key, pf.ID, "secret.txt", "hidden bytes"))
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+node.ID, fx.key, map[string]any{"no_download": true}), 200)

	href := "/dav/My%20files/secret.txt"
	// The write app password caps at editor, which may download.
	wantStatus(t, davBasic(t, fx.s, "GET", href, fx.secret, "", nil), 200)
	// A read app password caps at viewer, which the no-download rule refuses.
	readSecret := makeAppPassword(t, fx.s, fx.sess, "read mount", "read").Secret
	rec := davBasic(t, fx.s, "GET", href, readSecret, "", nil)
	wantStatus(t, rec, 403)
	// PROPFIND still lists the file's metadata for the reader.
	wantStatus(t, davBasic(t, fx.s, "PROPFIND", href, readSecret, "", map[string]string{"Depth": "0"}), 207)
}

func TestDAVTeamSpaceAndQuota(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	// A team space the member can read.
	fld := newFolder(t, fx.s, fx.key, fx.acc.ID, "Reports")
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+fld.ID+"/grants/"+fx.u.ID, fx.key,
		map[string]any{"level": "read"}), 200)
	writeFile(t, fx.s, fx.key, fld.ID, "q.txt", "quarterly")

	ms := davMultistatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", fx.secret, "", map[string]string{"Depth": "1"}))
	var spaceHref string
	for _, h := range davHrefs(ms) {
		if strings.Contains(h, "Reports") {
			spaceHref = h
		}
	}
	if spaceHref == "" {
		t.Fatalf("the team space is not listed: %v", davHrefs(ms))
	}
	// The space root carries quota properties.
	if _, ok := davProp(t, ms, spaceHref, "quota-used-bytes"); !ok {
		t.Error("the space root has no quota-used-bytes")
	}
	// Its child is reachable by path.
	wantStatus(t, davBasic(t, fx.s, "GET", spaceHref+"q.txt", fx.secret, "", nil), 200)
}

func TestDAVSharedWithMe(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	// Bob shares a file from his own personal folder with Ann.
	_, bobSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "bob@example.com")
	bobPf := personalFolderOf(t, fx.s, bobSess)
	node := nodeOf(t, writeFile(t, fx.s, fx.key, bobPf.ID, "shared.txt", "from bob"))
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+node.ID+"/permissions/"+fx.u.ID, fx.key,
		map[string]any{"role": "viewer"}), 200)

	ms := davMultistatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/Shared%20with%20me/", fx.secret, "", map[string]string{"Depth": "1"}))
	var found bool
	for _, h := range davHrefs(ms) {
		if strings.Contains(h, "shared.txt") {
			found = true
		}
	}
	if !found {
		t.Errorf("Shared with me does not list the shared file: %v", davHrefs(ms))
	}
}

func TestDAVSameNameSuffix(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	// A team space named "My files" collides with the personal folder's display name. Ann reaches it by a grant.
	fld := newFolder(t, fx.s, fx.key, fx.acc.ID, "My files")
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+fld.ID+"/grants/"+fx.u.ID, fx.key,
		map[string]any{"level": "read"}), 200)
	ms := davMultistatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", fx.secret, "", map[string]string{"Depth": "1"}))
	var base, suffixed bool
	for _, r := range ms.all("response") {
		for _, ps := range r.all("propstat") {
			p, _ := ps.find("prop")
			if dn, ok := p.find("displayname"); ok {
				switch dn.Text {
				case "My files":
					base = true
				case "My files (2)":
					suffixed = true
				}
			}
		}
	}
	if !base || !suffixed {
		t.Errorf("same-name entries: base=%v suffixed=%v; want one of each", base, suffixed)
	}
}

func TestDAVHiddenSpaceExcluded(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	fld := newFolder(t, fx.s, fx.key, fx.acc.ID, "Hidden")
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+fld.ID+"/grants/"+fx.u.ID, fx.key,
		map[string]any{"level": "read"}), 200)
	// Ann hides the space.
	wantStatus(t, callSession(t, fx.s, fx.sess, "PUT", "/api/v1/folders/"+fld.ID+"/hidden", nil), 204)
	ms := davMultistatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", fx.secret, "", map[string]string{"Depth": "1"}))
	for _, h := range davHrefs(ms) {
		if strings.Contains(h, "Hidden") {
			t.Errorf("the hidden space is listed: %v", davHrefs(ms))
		}
	}
}

func TestDAVShortcutNotListedOrReachable(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	pf := personalFolderOf(t, fx.s, fx.sess)
	target := nodeOf(t, writeFile(t, fx.s, fx.key, pf.ID, "target.txt", "data"))
	// A shortcut to the file, in the personal folder's root.
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+pf.RootNodeID+"/shortcuts", fx.key,
		map[string]any{"name": "link.txt", "target": target.ID})
	wantStatus(t, rec, 201)
	ms := davMultistatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/My%20files/", fx.secret, "", map[string]string{"Depth": "1"}))
	for _, h := range davHrefs(ms) {
		if strings.Contains(h, "link.txt") {
			t.Errorf("the shortcut is listed: %v", davHrefs(ms))
		}
	}
	wantStatus(t, davBasic(t, fx.s, "GET", "/dav/My%20files/link.txt", fx.secret, "", nil), 404)
}

func TestDAVTokenFolderScope(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	fld := newFolder(t, fx.s, fx.key, fx.acc.ID, "Scoped")
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+fld.ID+"/grants/"+fx.u.ID, fx.key,
		map[string]any{"level": "read"}), 200)
	// A token confined to the one folder lists only it (plus the virtual Shared with me).
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.u.ID, "folders": []string{fld.ID}})
	ms := davMultistatus(t, rawCall(t, fx.s, "PROPFIND", "/dav/", tok, "", map[string]string{"Depth": "1"}))
	var hasScoped, hasMyFiles bool
	for _, h := range davHrefs(ms) {
		if strings.Contains(h, "Scoped") {
			hasScoped = true
		}
		if strings.Contains(h, "My%20files") {
			hasMyFiles = true
		}
	}
	if !hasScoped || hasMyFiles {
		t.Errorf("folder-scoped token: Scoped=%v MyFiles=%v; want only Scoped", hasScoped, hasMyFiles)
	}
}

func TestDAVReadRateLimit(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	// Shrink the read budget so the test spends it quickly; the handler must charge it per request.
	fx.s.limits["dav.read"] = newLimiter(3)
	for i := 0; i < 3; i++ {
		wantStatus(t, davBasic(t, fx.s, "OPTIONS", "/dav/", fx.secret, "", nil), 200)
	}
	wantStatus(t, davBasic(t, fx.s, "OPTIONS", "/dav/", fx.secret, "", nil), 429)
}

func TestDAVLastUsedTouched(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	wantStatus(t, davBasic(t, fx.s, "PROPFIND", "/dav/", fx.secret, "", map[string]string{"Depth": "0"}), 207)
	list := decode[listResponse[apwItem]](t, callSession(t, fx.s, fx.sess, "GET", apwPath, nil))
	if len(list.Items) != 1 || list.Items[0].LastUsedAt == nil {
		t.Fatalf("last_used_at was not recorded: %+v", list.Items)
	}
}

func TestDAVNotCorsAndNotWeb(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t, func(c *config.Config) { c.CORS.AllowedOrigins = []string{"https://app.example.com"} })
	// A cross-origin preflight to the mount is not answered by CORS.
	req := httptest.NewRequest("OPTIONS", "/dav/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "PROPFIND")
	rec := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("the mount answers CORS preflights; it must be left alone")
	}
	// The web interface never serves under /dav/: an unauthenticated GET is the mount's 401, not HTML.
	rec = rawCall(t, fx.s, "GET", "/dav/", "", "", nil)
	wantStatus(t, rec, 401)
	if strings.Contains(rec.Body.String(), "<!doctype html") || strings.Contains(rec.Body.String(), "<!DOCTYPE html") {
		t.Error("the web interface answered under /dav/")
	}
}

func TestAppPasswordSecretRefusedOnAPI(t *testing.T) {
	t.Parallel()
	fx := newDavFix(t)
	// The mount credential is not an API credential.
	wantStatus(t, rawCall(t, fx.s, "GET", "/api/v1/me", fx.secret, "", nil), 401)
}
