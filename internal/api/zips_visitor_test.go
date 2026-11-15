// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// anyoneZipFixture is newAnyoneFixture with a configuration the test chooses, so the visitor limits can be set small.
func anyoneZipFixture(t *testing.T, mutate ...func(*config.Config)) *anyoneFixture {
	t.Helper()
	fx := zipFixture(t, mutate...)
	fx.s.guard.slowdown = 20 * time.Millisecond
	fx.settings(t, fx.fld.ID, map[string]any{"allow_links": true})
	setPolicy(t, fx.s, fx.key, fx.acc.ID, true)
	return &anyoneFixture{linkFixture: &linkFixture{sharingFixture: fx, root: fx.rootOf(t, fx.fld.ID)}}
}

// visitorZip asks for a zip as a link visitor and wants it accepted. It returns the job.
func visitorZip(t *testing.T, visitor asCaller, nodes ...string) jobJSON {
	t.Helper()
	rec := visitor("POST", "/api/v1/zips", map[string]any{"nodes": nodes})
	wantStatus(t, rec, 202)
	return decode[jobJSON](t, rec)
}

// visitorZipLink asks for the signed download link of a visitor's zip and wants it accepted.
func visitorZipLink(t *testing.T, visitor asCaller, zipID string) (string, time.Time) {
	t.Helper()
	rec := visitor("POST", "/api/v1/zips/"+zipID+"/links", nil)
	wantStatus(t, rec, 200)
	out := decode[struct {
		URL       string    `json:"url"`
		ExpiresAt time.Time `json:"expires_at"`
	}](t, rec)
	if out.URL == "" {
		t.Fatal("the zip link answer carries no url")
	}
	return out.URL, out.ExpiresAt
}

// linkPath is the path of a signed link URL, so a test can GET it on the test server.
func linkPath(t *testing.T, rawURL string) string {
	t.Helper()
	i := strings.Index(rawURL, linkPrefix)
	if i < 0 {
		t.Fatalf("the url %q has no link path", rawURL)
	}
	return rawURL[i:]
}

// getLink GETs a signed link with no credential, as a browser follows it.
func getLink(t *testing.T, s *Server, rawURL string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return rawCall(t, s, "GET", linkPath(t, rawURL), "", "", hdr)
}

// TestZipVisitorMakesAndDownloads: a visitor of an anyone link makes a zip of the link's folder, and downloads it
// through the signed zip link. The zip is the link's, not a person's, and the download needs no credential.
func TestZipVisitorMakesAndDownloads(t *testing.T) {
	fx := anyoneZipFixture(t)
	link, secret := fx.makeAnyone(t, fx.b.ID, nil)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	job := visitorZip(t, visitor, fx.b.ID)
	if j := runZip(t, fx.sharingFixture, job.ID); j.Status != "succeeded" {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	z, err := fx.s.store.Zip(context.Background(), fx.acc.ID, job.Target)
	if err != nil {
		t.Fatal(err)
	}
	if z.CreatedKind != string(auth.KindShareLink) || z.CreatedBy != link.ID || z.CreatedCred != "" {
		t.Errorf("zip maker = %q %q cred %q, want the link %q with no credential", z.CreatedKind, z.CreatedBy, z.CreatedCred, link.ID)
	}
	url, expires := visitorZipLink(t, visitor, job.Target)
	if !expires.After(time.Now()) {
		t.Errorf("the zip link expires at %v, in the past", expires)
	}
	rec := getLink(t, fx.s, url, nil)
	wantStatus(t, rec, 200)
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "b.zip") {
		t.Errorf("disposition %q is not an attachment named after the root", cd)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content type %q", ct)
	}
	got := zipEntries(t, rec.Body.Bytes())
	if got["b/file.txt"] != "deep" {
		t.Errorf("entries %v, want the link's file", got)
	}
	// A Range resumes the download, as the maker's GET does.
	full := rec.Body.Bytes()
	part := getLink(t, fx.s, url, map[string]string{"Range": "bytes=5-"})
	wantStatus(t, part, 206)
	if string(part.Body.Bytes()) != string(full[5:]) {
		t.Error("the ranged body is not the rest of the zip")
	}
	// The signed link reads no cookie: another member's session changes nothing.
	withCookie := rawSession(t, fx.s, fx.bobSess, "GET", linkPath(t, url), "", nil)
	wantStatus(t, withCookie, 200)
}

// TestZipVisitorReusesOneZipPerLink: one link has one zip. A second credential of the same link asking for the same
// roots gets the same job back; asking for other roots while the zip lives is refused.
func TestZipVisitorReusesOneZipPerLink(t *testing.T) {
	fx := anyoneZipFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	v1 := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	v2 := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	first := visitorZip(t, v1, fx.b.ID)
	again := visitorZip(t, v2, fx.b.ID)
	if again.ID != first.ID || again.Target != first.Target {
		t.Errorf("a second credential of the link made a new job %s, want the reused %s", again.ID, first.ID)
	}
	// Another root set the link still reaches (a child of its node) while its zip lives is refused: one zip per link.
	rec := v1("POST", "/api/v1/zips", map[string]any{"nodes": []string{fx.deep.ID}})
	wantStatus(t, rec, 409)
	if !strings.Contains(rec.Body.String(), "too_many_zips_link") {
		t.Errorf("refusal %s does not name too_many_zips_link", rec.Body.String())
	}
}

// TestZipVisitorRootsAreTheLinks: a visitor zips only what its link reaches. A root outside the link is not_found,
// and another link's visitor does not reach this link's zip.
func TestZipVisitorRootsAreTheLinks(t *testing.T) {
	fx := anyoneZipFixture(t)
	_, secretB := fx.makeAnyone(t, fx.b.ID, nil)
	_, secretC := fx.makeAnyone(t, fx.c.ID, nil)
	vB := byCred(t, fx.s, fx.visitAnyone(t, secretB, ""))
	vC := byCred(t, fx.s, fx.visitAnyone(t, secretC, ""))
	// A root outside the link's node is not there for the visitor.
	wantStatus(t, vB("POST", "/api/v1/zips", map[string]any{"nodes": []string{fx.c.ID}}), 404)
	job := visitorZip(t, vB, fx.b.ID)
	runZip(t, fx.sharingFixture, job.ID)
	// The other link's visitor is not the maker: the zip link route answers not_found.
	wantStatus(t, vC("POST", "/api/v1/zips/"+job.Target+"/links", nil), 404)
}

// TestZipVisitorRestrictedRoot: the no-download rule refuses a visitor's zip, as it refuses a member's.
func TestZipVisitorRestrictedRoot(t *testing.T) {
	fx := anyoneZipFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.b.ID, fx.key, map[string]any{"no_download": true}), 200)
	rec := visitor("POST", "/api/v1/zips", map[string]any{"nodes": []string{fx.b.ID}})
	wantStatus(t, rec, 403)
	if !strings.Contains(rec.Body.String(), "download_restricted") {
		t.Errorf("refusal %s does not name download_restricted", rec.Body.String())
	}
}

// TestZipVisitorLinkDiesMidJob: a link that ends between the request and the job leaves no zip. An expired link and
// the account's switch turned off both fail the job; nothing is published.
func TestZipVisitorLinkDiesMidJob(t *testing.T) {
	for _, name := range []string{"expired", "switched off"} {
		t.Run(name, func(t *testing.T) {
			fx := anyoneZipFixture(t)
			link, secret := fx.makeAnyone(t, fx.b.ID, nil)
			visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
			job := visitorZip(t, visitor, fx.b.ID)
			if name == "expired" {
				fx.setExpiry(t, link.ID, time.Now().Add(-time.Hour))
			} else {
				setPolicy(t, fx.s, fx.key, fx.acc.ID, false)
			}
			j := runZip(t, fx.sharingFixture, job.ID)
			if j.Status != "failed" {
				t.Fatalf("job %s %q, want failed once the link is dead", j.Status, j.Error)
			}
			z, err := fx.s.store.Zip(context.Background(), fx.acc.ID, job.Target)
			if err != nil {
				t.Fatal(err)
			}
			if z.State == store.ZipReady {
				t.Error("a zip of a dead link was published")
			}
			path, _ := fx.s.zipFiles.Path(fx.acc.ID, job.Target)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("a dead link's zip file is present: %v", err)
			}
		})
	}
}

// TestZipVisitorDeleteLinkRemovesZip: deleting the link removes its ready zip, row and file, and the signed link dies.
func TestZipVisitorDeleteLinkRemovesZip(t *testing.T) {
	fx := anyoneZipFixture(t)
	link, secret := fx.makeAnyone(t, fx.b.ID, nil)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	job := visitorZip(t, visitor, fx.b.ID)
	runZip(t, fx.sharingFixture, job.ID)
	url, _ := visitorZipLink(t, visitor, job.Target)
	path, _ := fx.s.zipFiles.Path(fx.acc.ID, job.Target)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("zip file missing before delete: %v", err)
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/share-links/"+link.ID, fx.key, nil), 204)
	if _, err := fx.s.store.Zip(context.Background(), fx.acc.ID, job.Target); err == nil {
		t.Error("the zip row survived the link's delete")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the zip file survived the link's delete: %v", err)
	}
	wantStatus(t, getLink(t, fx.s, url, nil), 404)
}

// TestZipVisitorPerLinkAndAccountCaps: a link has one zip, and the account's visitors share a cap of their own that
// does not spend the signed-in callers' budget.
func TestZipVisitorPerLinkAndAccountCaps(t *testing.T) {
	fx := anyoneZipFixture(t, func(c *config.Config) { c.Zip.VisitorMaxPerAccount = 3 })
	// The cap is what is tested here, not the address rate (TestZipVisitorAddressRateLimit covers that).
	fx.s.limits["zip.create.visitor"] = newLimiter(100)
	ready := func(node string) jobJSON {
		_, secret := fx.makeAnyone(t, node, nil)
		v := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
		job := visitorZip(t, v, node)
		runZip(t, fx.sharingFixture, job.ID)
		return job
	}
	// Three links, each with a READY unexpired zip, fill the account's visitor cap.
	first := ready(fx.b.ID)
	ready(fx.c.ID)
	ready(fx.a.ID)
	// A fourth link (on its own folder, since a node holds one anyone link) is refused: the ready zips still hold their places.
	d := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/d")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/d/y.txt", "d")
	_, secretD := fx.makeAnyone(t, d.ID, nil)
	vD := byCred(t, fx.s, fx.visitAnyone(t, secretD, ""))
	rec := vD("POST", "/api/v1/zips", map[string]any{"nodes": []string{d.ID}})
	wantStatus(t, rec, 409)
	if !strings.Contains(rec.Body.String(), "too_many_zips_account") {
		t.Errorf("refusal %s does not name too_many_zips_account", rec.Body.String())
	}
	// An expired ready zip leaves the count, so the fourth link's zip is accepted.
	fx.execDB(t, "UPDATE zips SET expires_at = ? WHERE id = ?", time.Now().Add(-time.Hour).UnixMilli(), first.Target)
	visitorZip(t, vD, d.ID)
	// A signed-in caller is not held back by the visitors' cap, and its zips never count toward it.
	createZip(t, fx.sharingFixture, nil, fx.a.ID)
	createZip(t, fx.sharingFixture, nil, fx.b.ID)
}

// TestZipVisitorByteCap: a visitor's zip is held to zip.visitor_max_bytes, not the signed-in zip.max_bytes.
func TestZipVisitorByteCap(t *testing.T) {
	fx := anyoneZipFixture(t, func(c *config.Config) {
		c.Zip.VisitorMaxBytes = 10
		c.Zip.MaxBytes = 1 << 20
	})
	putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/big.txt", incompressible(64))
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	job := visitorZip(t, visitor, fx.b.ID)
	j := runZip(t, fx.sharingFixture, job.ID)
	if j.Status != "failed" {
		t.Fatalf("job %s %q, want failed on the visitor byte cap", j.Status, j.Error)
	}
	z, _ := fx.s.store.Zip(context.Background(), fx.acc.ID, job.Target)
	if z.ErrorCode != string(apierr.TooLarge) {
		t.Errorf("zip error code %q, want too_large", z.ErrorCode)
	}
	// The owner's zip of the same tree is held to the larger signed-in cap and succeeds.
	owner := createZip(t, fx.sharingFixture, nil, fx.b.ID)
	if j := runZip(t, fx.sharingFixture, owner.ID); j.Status != "succeeded" {
		t.Fatalf("owner zip %s: %s", j.Status, j.Error)
	}
}

// TestZipVisitorHalfDisk: a visitor's zip may use only half the disk budgets; a signed-in caller keeps the whole.
func TestZipVisitorHalfDisk(t *testing.T) {
	fx := anyoneZipFixture(t, func(c *config.Config) {
		c.Zip.MaxBytes = 1 << 20
		c.Zip.MaxDisk = 1 << 20
		c.Zip.MaxDiskPerAccount = 1 << 20
		c.Zip.VisitorMaxBytes = 1 << 20
	})
	putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/big.bin", incompressible(600<<10))
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	job := visitorZip(t, visitor, fx.b.ID)
	j := runZip(t, fx.sharingFixture, job.ID)
	if j.Status != "failed" || !strings.Contains(j.Error, "disk") {
		t.Fatalf("visitor zip %s %q, want failed on the half disk budget", j.Status, j.Error)
	}
	owner := createZip(t, fx.sharingFixture, nil, fx.b.ID)
	if j := runZip(t, fx.sharingFixture, owner.ID); j.Status != "succeeded" {
		t.Fatalf("owner zip %s: %s, want the whole budget", j.Status, j.Error)
	}
}

// TestZipVisitorAddressRateLimit: a visitor's zip requests are limited per client address, a few a minute.
func TestZipVisitorAddressRateLimit(t *testing.T) {
	fx := anyoneZipFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	var last *httptest.ResponseRecorder
	for i := 0; i < 4; i++ {
		last = visitor("POST", "/api/v1/zips", map[string]any{"nodes": []string{fx.b.ID}})
	}
	wantStatus(t, last, 429)
}

// TestZipLinkRouteStates: the zip link route answers by the zip's state, and only for the link that made it.
func TestZipLinkRouteStates(t *testing.T) {
	fx := anyoneZipFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	job := visitorZip(t, visitor, fx.b.ID)
	// Pending: not ready yet.
	rec := visitor("POST", "/api/v1/zips/"+job.Target+"/links", nil)
	wantStatus(t, rec, 409)
	if !strings.Contains(rec.Body.String(), "zip_not_ready") || rec.Header().Get("Retry-After") == "" {
		t.Errorf("pending refusal %s (Retry-After %q)", rec.Body.String(), rec.Header().Get("Retry-After"))
	}
	runZip(t, fx.sharingFixture, job.ID)
	visitorZipLink(t, visitor, job.Target)
	// A signed-in maker's zip is not linkable by this route: it is visitor-only.
	ownerJob := createZip(t, fx.sharingFixture, nil, fx.a.ID)
	runZip(t, fx.sharingFixture, ownerJob.ID)
	e := wantError(t, call(t, fx.s, "POST", "/api/v1/zips/"+ownerJob.Target+"/links", fx.key, nil), 403, apierr.Forbidden)
	if e.Details["reason"] != "visitor_only" {
		t.Errorf("reason %v, want visitor_only", e.Details["reason"])
	}
}

// TestZipDispositionNotAskable: a caller cannot ask POST /nodes/{nod}/links for the zip disposition; it is refused as
// any unknown disposition is, because only the server signs a zip link (decision 192).
func TestZipDispositionNotAskable(t *testing.T) {
	fx := anyoneZipFixture(t)
	for _, disp := range []string{"zip", "bogus"} {
		rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/links", fx.key, map[string]any{"disposition": disp})
		wantStatus(t, rec, 400)
		if !strings.Contains(rec.Body.String(), "invalid_request") {
			t.Errorf("disposition %q: %s, want invalid_request", disp, rec.Body.String())
		}
	}
}

// TestZipSignedLinkRefusals: a signed zip link is refused for a bad MAC, a wrong id/disposition pairing, an expired
// token and an expired zip, each with the answer a bad link gets.
func TestZipSignedLinkRefusals(t *testing.T) {
	fx := anyoneZipFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	job := visitorZip(t, visitor, fx.b.ID)
	runZip(t, fx.sharingFixture, job.ID)
	z, err := fx.s.store.Zip(context.Background(), fx.acc.ID, job.Target)
	if err != nil {
		t.Fatal(err)
	}
	url, _ := visitorZipLink(t, visitor, job.Target)
	// A MAC signed over the wrong tag does not verify.
	badMAC := fx.s.links.sign(linkClaims{nod: z.ID, acc: z.AccountID, disp: dispZip, exp: time.Now().Add(time.Hour).Unix()}, "not-the-sha")
	wantStatus(t, getLink(t, fx.s, linkPrefix+badMAC, nil), 404)
	// A zip id with a file disposition, and a node id with the zip disposition, are both malformed.
	zipAsFile := fx.s.links.sign(linkClaims{nod: z.ID, acc: z.AccountID, disp: dispAttachment, exp: time.Now().Add(time.Hour).Unix()}, z.SHA256)
	wantStatus(t, getLink(t, fx.s, linkPrefix+zipAsFile, nil), 404)
	nodeAsZip := fx.s.links.sign(linkClaims{nod: fx.b.ID, acc: z.AccountID, disp: dispZip, exp: time.Now().Add(time.Hour).Unix()}, z.SHA256)
	wantStatus(t, getLink(t, fx.s, linkPrefix+nodeAsZip, nil), 404)
	// A signed-in maker's READY zip is not served by a zip token, even one signed over its own SHA-256: only a link's zip is.
	memberJob := createZip(t, fx.sharingFixture, nil, fx.a.ID)
	runZip(t, fx.sharingFixture, memberJob.ID)
	mz, err := fx.s.store.Zip(context.Background(), fx.acc.ID, memberJob.Target)
	if err != nil {
		t.Fatal(err)
	}
	memberTok := fx.s.links.sign(linkClaims{nod: mz.ID, acc: mz.AccountID, disp: dispZip, exp: time.Now().Add(time.Hour).Unix()}, mz.SHA256)
	wantStatus(t, getLink(t, fx.s, linkPrefix+memberTok, nil), 404)
	// A zip token naming another account is not_found, even with the right zip id and SHA-256.
	other, _ := newAccount(t, fx.s, fx.admin, "Other")
	otherAcc := fx.s.links.sign(linkClaims{nod: z.ID, acc: other.ID, disp: dispZip, exp: time.Now().Add(time.Hour).Unix()}, z.SHA256)
	wantStatus(t, getLink(t, fx.s, linkPrefix+otherAcc, nil), 404)
	// An expired token is the node link's expired answer.
	expired := fx.s.links.sign(linkClaims{nod: z.ID, acc: z.AccountID, disp: dispZip, exp: time.Now().Add(-time.Hour).Unix()}, z.SHA256)
	wantError(t, getLink(t, fx.s, linkPrefix+expired, nil), 410, apierr.LinkExpired)
	// An expired zip is not_found.
	fx.execDB(t, "UPDATE zips SET expires_at = ? WHERE id = ?", time.Now().Add(-time.Hour).UnixMilli(), z.ID)
	wantStatus(t, getLink(t, fx.s, url, nil), 404)
}

// TestZipSignedLinkDiesWithTheLink: the signed zip link stops working the moment its link is deleted, even with a
// member's session cookie sent along.
func TestZipSignedLinkDiesWithTheLink(t *testing.T) {
	fx := anyoneZipFixture(t)
	link, secret := fx.makeAnyone(t, fx.b.ID, nil)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	job := visitorZip(t, visitor, fx.b.ID)
	runZip(t, fx.sharingFixture, job.ID)
	url, _ := visitorZipLink(t, visitor, job.Target)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/share-links/"+link.ID, fx.key, nil), 204)
	wantStatus(t, getLink(t, fx.s, url, nil), 404)
	wantStatus(t, rawSession(t, fx.s, fx.bobSess, "GET", linkPath(t, url), "", nil), 404)
}

// TestZipVisitorMustChangePaused: while the link's maker must change its password, the link is paused (decision 178):
// its visitor starts no zip, and a zip it already made is unreadable through its signed link.
func TestZipVisitorMustChangePaused(t *testing.T) {
	fx := anyoneZipFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "manager")
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", map[string]any{"audience": "anyone"})
	wantStatus(t, rec, 201)
	secret := secretOf(t, decode[shareLinkCreated](t, rec).URL)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	job := visitorZip(t, visitor, fx.b.ID)
	runZip(t, fx.sharingFixture, job.ID)
	url, _ := visitorZipLink(t, visitor, job.Target)
	setMustChange(t, fx.s, fx.acc.ID, fx.ann.ID, true)
	// The paused link's credential is dead: a new zip is the dead-link answer.
	wantStatus(t, visitor("POST", "/api/v1/zips", map[string]any{"nodes": []string{fx.b.ID}}), 404)
	// The zip it made is unreadable through its signed link.
	wantStatus(t, getLink(t, fx.s, url, nil), 404)
}

// zipRotation is one of the three writes that give a link new credentials. It performs the rotation and returns the
// secret and password a new visitor of the link needs.
type zipRotation struct {
	name   string
	rotate func(t *testing.T, fx *anyoneFixture, linkID, secret string) (string, string)
}

// zipRotations are the three rotations: a reset gives a new secret, a new password keeps the secret, and a change of
// audience keeps it too (here anyone to organisation and back, so a new visitor can open the link again).
func zipRotations() []zipRotation {
	return []zipRotation{
		{"reset", func(t *testing.T, fx *anyoneFixture, linkID, secret string) (string, string) {
			rec := call(t, fx.s, "POST", "/api/v1/share-links/"+linkID+"/reset", fx.key, nil)
			wantStatus(t, rec, 201)
			return secretOf(t, decode[shareLinkCreated](t, rec).URL), ""
		}},
		{"new password", func(t *testing.T, fx *anyoneFixture, linkID, secret string) (string, string) {
			const password = "a new long password"
			wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+linkID, fx.key,
				map[string]any{"password": password}), 200)
			return secret, password
		}},
		{"audience", func(t *testing.T, fx *anyoneFixture, linkID, secret string) (string, string) {
			wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+linkID, fx.key,
				map[string]any{"audience": "organisation"}), 200)
			wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+linkID, fx.key,
				map[string]any{"audience": "anyone"}), 200)
			return secret, ""
		}},
	}
}

// TestZipVisitorRotationEndsTheZips: each rotation of a link's credentials ends the zips its old ones made. The
// signed URL answers not_found, the row and the file are gone, the old credential cannot link the zip again, and a
// visitor with the new credential gets a NEW zip of the same folder. A pending job publishes nothing. The zips of a
// second link of the account and a member's own zip survive.
func TestZipVisitorRotationEndsTheZips(t *testing.T) {
	for _, rot := range zipRotations() {
		t.Run(rot.name, func(t *testing.T) {
			t.Run("ready zip", func(t *testing.T) {
				fx := anyoneZipFixture(t)
				link, secret := fx.makeAnyone(t, fx.b.ID, nil)
				// A second link of the account with its own ready zip, and a member's own zip.
				_, secret2 := fx.makeAnyone(t, fx.c.ID, nil)
				v2 := byCred(t, fx.s, fx.visitAnyone(t, secret2, ""))
				job2 := visitorZip(t, v2, fx.c.ID)
				runZip(t, fx.sharingFixture, job2.ID)
				url2, _ := visitorZipLink(t, v2, job2.Target)
				member := createZip(t, fx.sharingFixture, nil, fx.a.ID)
				runZip(t, fx.sharingFixture, member.ID)
				// The link's visitor makes a ready zip and holds its signed URL.
				visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
				job := visitorZip(t, visitor, fx.b.ID)
				if j := runZip(t, fx.sharingFixture, job.ID); j.Status != "succeeded" {
					t.Fatalf("job %s: %s", j.Status, j.Error)
				}
				url, _ := visitorZipLink(t, visitor, job.Target)
				wantStatus(t, getLink(t, fx.s, url, nil), 200)
				path, _ := fx.s.zipFiles.Path(fx.acc.ID, job.Target)
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("zip file missing before the rotation: %v", err)
				}
				newSecret, newPassword := rot.rotate(t, fx, link.ID, secret)
				// The signed URL fetched before the rotation is dead after it.
				wantStatus(t, getLink(t, fx.s, url, nil), 404)
				// The zip's row and file are gone.
				if _, err := fx.s.store.Zip(context.Background(), fx.acc.ID, job.Target); !errors.Is(err, store.ErrNotFound) {
					t.Errorf("the zip row survived the rotation: %v", err)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("the zip file survived the rotation: %v", err)
				}
				// The old credential cannot link the zip again.
				wantStatus(t, visitor("POST", "/api/v1/zips/"+job.Target+"/links", nil), 404)
				// A visitor with the new credential gets a NEW zip of the same folder, and it is made.
				fresh := byCred(t, fx.s, fx.visitAnyone(t, newSecret, newPassword))
				again := visitorZip(t, fresh, fx.b.ID)
				if again.Target == job.Target {
					t.Errorf("the new credential reused the old zip %s", job.Target)
				}
				if j := runZip(t, fx.sharingFixture, again.ID); j.Status != "succeeded" {
					t.Fatalf("the new zip's job %s: %s", j.Status, j.Error)
				}
				// The second link's zip and the member's zip survive the rotation.
				if _, err := fx.s.store.Zip(context.Background(), fx.acc.ID, job2.Target); err != nil {
					t.Errorf("the second link's zip row is gone: %v", err)
				}
				wantStatus(t, getLink(t, fx.s, url2, nil), 200)
				if _, err := fx.s.store.Zip(context.Background(), fx.acc.ID, member.Target); err != nil {
					t.Errorf("the member's zip row is gone: %v", err)
				}
				wantStatus(t, getZip(t, fx.sharingFixture, nil, member.Target, nil), 200)
			})
			t.Run("pending job", func(t *testing.T) {
				fx := anyoneZipFixture(t)
				link, secret := fx.makeAnyone(t, fx.b.ID, nil)
				visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
				job := visitorZip(t, visitor, fx.b.ID)
				rot.rotate(t, fx, link.ID, secret)
				runZip(t, fx.sharingFixture, job.ID)
				// Nothing was published: the row and the file are gone.
				if _, err := fx.s.store.Zip(context.Background(), fx.acc.ID, job.Target); !errors.Is(err, store.ErrNotFound) {
					t.Errorf("the pending zip's row survived the rotation: %v", err)
				}
				path, _ := fx.s.zipFiles.Path(fx.acc.ID, job.Target)
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("the pending zip published a file: %v", err)
				}
			})
		})
	}
}

// TestZipMakerRefusesAZipOlderThanTheCredentials: a row a race left behind, made before the link's current
// credentials, can neither run, be linked nor be served, and a create does not reuse it.
func TestZipMakerRefusesAZipOlderThanTheCredentials(t *testing.T) {
	fx := anyoneZipFixture(t)
	link, secret := fx.makeAnyone(t, fx.b.ID, nil)
	st, err := fx.s.store.ShareLinkStateByID(context.Background(), fx.acc.ID, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Insert the row directly, an hour before the link's current credentials, with a job of its own.
	z := &store.Zip{ID: id.New("zip"), AccountID: fx.acc.ID, CreatedBy: link.ID, CreatedKind: string(auth.KindShareLink),
		CreatedAt: st.Link.CredentialsAt.Add(-time.Hour), Roots: []string{fx.b.ID}}
	body, err := json.Marshal(zipPayload{Link: link.ID})
	if err != nil {
		t.Fatal(err)
	}
	job := &store.Job{ID: id.New("job"), AccountID: fx.acc.ID, Kind: jobZip, Target: z.ID, Status: store.JobQueued,
		Payload: string(body), CreatedAt: store.Now(), UpdatedAt: store.Now()}
	if err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		if err := tx.InsertZip(context.Background(), z); err != nil {
			return err
		}
		return tx.InsertJob(context.Background(), job)
	}); err != nil {
		t.Fatal(err)
	}
	// zipMaker refuses it with the gone answer.
	if _, err := fx.s.zipMaker(context.Background(), z, zipPayload{}); apierr.From(err).Code != apierr.Forbidden ||
		!strings.Contains(err.Error(), zipRefusedGone) {
		t.Errorf("zipMaker answered %v, want the gone refusal", err)
	}
	// The job fails and publishes nothing.
	if j := runZip(t, fx.sharingFixture, job.ID); j.Status != "failed" {
		t.Fatalf("job %s %q, want failed for a zip older than the credentials", j.Status, j.Error)
	}
	// Ready and unexpired, it is still refused by the link route and by a signed URL over its own SHA-256.
	sha := strings.Repeat("ab", 32)
	fx.execDB(t, "UPDATE zips SET state = 'ready', sha256 = ?, expires_at = ? WHERE id = ?",
		sha, time.Now().Add(time.Hour).UnixMilli(), z.ID)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	wantStatus(t, visitor("POST", "/api/v1/zips/"+z.ID+"/links", nil), 404)
	tok := fx.s.links.sign(linkClaims{nod: z.ID, acc: fx.acc.ID, disp: dispZip, exp: time.Now().Add(time.Hour).Unix()}, sha)
	wantStatus(t, getLink(t, fx.s, linkPrefix+tok, nil), 404)
	// A create of the same roots does not reuse it: the link gets a new zip, and it is made.
	again := visitorZip(t, visitor, fx.b.ID)
	if again.Target == z.ID {
		t.Error("the create reused the zip made before the credentials")
	}
	if j := runZip(t, fx.sharingFixture, again.ID); j.Status != "succeeded" {
		t.Fatalf("the new zip's job %s: %s", j.Status, j.Error)
	}
}

// TestZipVisitorCreateRefusedWhenARotationLandsBeforeTheWrite: the rotation lands after the visitor's credential is
// checked and before the create's write reads the link. The create must not insert a zip under the old credentials.
func TestZipVisitorCreateRefusedWhenARotationLandsBeforeTheWrite(t *testing.T) {
	fx := anyoneZipFixture(t)
	link, secret := fx.makeAnyone(t, fx.b.ID, nil)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	// The reset is later than the credential, so the credential's own check still passes.
	time.Sleep(2 * time.Millisecond)
	fired := false
	fx.s.beforeWrite = func() {
		fx.s.beforeWrite = nil
		fired = true
		wantStatus(t, call(t, fx.s, "POST", "/api/v1/share-links/"+link.ID+"/reset", fx.key, nil), 201)
	}
	deadVisitor(t, visitor("POST", "/api/v1/zips", map[string]any{"nodes": []string{fx.b.ID}}))
	if !fired {
		t.Fatal("the reset was not injected into the create")
	}
	ids, err := fx.s.store.ZipIDsOfLink(context.Background(), fx.acc.ID, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("the create left zips %v under the old credentials", ids)
	}
}
