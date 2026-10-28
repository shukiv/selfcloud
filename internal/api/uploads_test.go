// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	apispec "github.com/shukiv/selfcloud/api"
	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/sweep"
)

// md builds an Upload-Metadata header from key, value pairs. Values are base64, as tus requires.
func md(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, pairs[i]+" "+base64.StdEncoding.EncodeToString([]byte(pairs[i+1])))
	}
	return strings.Join(parts, ",")
}

// digest is the hex SHA-256 of s, and b64sum is the base64 SHA-256, as Upload-Checksum takes it.
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func b64sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.StdEncoding.EncodeToString(h[:])
}

// upCall sends a tus request with a bearer credential. Tus-Resumable is sent unless hdr sets it.
func upCall(t *testing.T, s *Server, method, p, cred string, hdr map[string]string, body io.Reader, length int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, p, body)
	req.ContentLength = length
	if _, ok := hdr["Tus-Resumable"]; !ok {
		req.Header.Set("Tus-Resumable", tusVersion)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// upSession is upCall as a signed-in browser sends it.
func upSession(t *testing.T, s *Server, sess testSession, method, p string, hdr map[string]string, body io.Reader, length int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, p, body)
	req.ContentLength = length
	req.Header.Set("Tus-Resumable", tusVersion)
	req.Header.Set("Origin", s.origin)
	req.Header.Set("X-CSRF-Token", sess.csrf)
	req.AddCookie(sess.cookie)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// createUpload starts an upload of length bytes with the given metadata.
func createUpload(t *testing.T, s *Server, cred string, length int64, metadata string) *httptest.ResponseRecorder {
	t.Helper()
	return upCall(t, s, "POST", "/api/v1/uploads", cred, map[string]string{
		"Upload-Length": strconv.FormatInt(length, 10), "Upload-Metadata": metadata}, nil, 0)
}

// uploadID reads the upload's ID from a creation reply's Location.
func uploadID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	wantStatus(t, rec, 201)
	return path.Base(rec.Header().Get("Location"))
}

// piece sends data at offset, with a checksum when sum is not empty.
func piece(t *testing.T, s *Server, cred, upl string, offset int64, data string, extra map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	hdr := map[string]string{"Content-Type": uploadBodyType, "Upload-Offset": strconv.FormatInt(offset, 10)}
	for k, v := range extra {
		hdr[k] = v
	}
	return upCall(t, s, "PATCH", "/api/v1/uploads/"+upl, cred, hdr, strings.NewReader(data), int64(len(data)))
}

func headUpload(t *testing.T, s *Server, cred, upl string) *httptest.ResponseRecorder {
	t.Helper()
	return upCall(t, s, "HEAD", "/api/v1/uploads/"+upl, cred, nil, nil, 0)
}

// uploadFile is the path of an upload's file on disk.
func uploadFile(s *Server, accID, upl string) string {
	return filepath.Join(s.cfg.DataDir, "uploads", accID, upl)
}

// reopen closes the server's database and opens a new server on the same data directory and
// database: what a restart after a crash looks like.
func reopen(t *testing.T, s *Server) *Server {
	t.Helper()
	cfg := s.cfg
	if err := s.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), store.Options{Path: cfg.Store.Path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s2, err := New(Options{Config: cfg, Store: st, Mailer: &mail.Recorder{}})
	if err != nil {
		t.Fatal(err)
	}
	return s2
}

// fileBody reads a node's contents back.
func fileBody(t *testing.T, s *Server, key, nodeID string) string {
	t.Helper()
	rec := rawCall(t, s, "GET", "/api/v1/nodes/"+nodeID+"/content", key, "", nil)
	wantStatus(t, rec, 200)
	return rec.Body.String()
}

// uploadSetup is an account with a folder and its owner key.
func uploadSetup(t *testing.T, mutate ...func(*config.Config)) (*Server, string, string, folderJSON) {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{}, mutate...)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	return s, key, acc.ID, f
}

func TestUploadHappyPathInChunks(t *testing.T) {
	s, key, accID, f := uploadSetup(t, func(c *config.Config) { c.Upload.MaxChunkSize = 8 })
	data := "abcdefghijklmnopqrstuvwxyz0123"
	rec := createUpload(t, s, key, int64(len(data)), md("folder", f.ID, "name", "alpha.txt", "sha256", digest(data)))
	upl := uploadID(t, rec)
	if rec.Header().Get("Upload-Offset") != "0" || rec.Header().Get("Upload-Expires") == "" {
		t.Fatalf("creation headers = %v", rec.Header())
	}
	for off := 0; off < len(data); off += 8 {
		end := min(off+8, len(data))
		h := headUpload(t, s, key, upl)
		wantStatus(t, h, 200)
		if h.Header().Get("Upload-Offset") != strconv.Itoa(off) || h.Header().Get("Upload-Length") != "30" {
			t.Fatalf("HEAD before piece at %d = %v", off, h.Header())
		}
		rec := piece(t, s, key, upl, int64(off), data[off:end], nil)
		if end < len(data) {
			wantStatus(t, rec, 204)
			if rec.Header().Get("Upload-Offset") != strconv.Itoa(end) {
				t.Fatalf("offset after piece = %s, want %d", rec.Header().Get("Upload-Offset"), end)
			}
		} else {
			wantStatus(t, rec, 204)
			nodeID := rec.Header().Get(nodeHeader)
			if !strings.HasPrefix(nodeID, "nod_") {
				t.Fatalf("completing piece gave node %q", nodeID)
			}
			if got := fileBody(t, s, key, nodeID); got != data {
				t.Fatalf("contents = %q", got)
			}
			n := decode[nodeJSON](t, call(t, s, "GET", "/api/v1/nodes/"+nodeID, key, nil))
			if n.Name != "alpha.txt" || n.SHA256 != digest(data) || n.Size != 30 || n.MimeType != "text/plain" {
				t.Fatalf("node = %+v", n)
			}
		}
	}
	wantError(t, headUpload(t, s, key, upl), 404, apierr.NotFound)
	if u := usageOf(t, s, key, accID); u.UsedBytes != 30 || u.UsedFiles != 1 {
		t.Errorf("usage = %+v", u)
	}
	if _, err := os.Stat(uploadFile(s, accID, upl)); !errors.Is(err, os.ErrNotExist) {
		t.Error("upload file still under uploads/ after completion")
	}
}

func TestUploadResumeAfterTruncatedPiece(t *testing.T) {
	s, key, _, f := uploadSetup(t)
	data := "0123456789"
	upl := uploadID(t, createUpload(t, s, key, 10, md("folder", f.ID, "name", "r.bin")))
	// The connection dies after four bytes of a ten-byte piece.
	body := io.MultiReader(strings.NewReader(data[:4]), &brokenReader{})
	rec := upCall(t, s, "PATCH", "/api/v1/uploads/"+upl, key, map[string]string{
		"Content-Type": uploadBodyType, "Upload-Offset": "0"}, body, 10)
	wantError(t, rec, 400, apierr.InvalidRequest)
	h := headUpload(t, s, key, upl)
	if h.Header().Get("Upload-Offset") != "4" {
		t.Fatalf("offset after the dropped connection = %s, want 4", h.Header().Get("Upload-Offset"))
	}
	rec = piece(t, s, key, upl, 4, data[4:], nil)
	wantStatus(t, rec, 204)
	if got := fileBody(t, s, key, rec.Header().Get(nodeHeader)); got != data {
		t.Errorf("resumed contents = %q", got)
	}
}

// brokenReader fails as a connection does when it drops.
type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestUploadCrashAndRestart(t *testing.T) {
	s, key, accID, f := uploadSetup(t)
	data := "first-half|second-half"
	upl := uploadID(t, createUpload(t, s, key, int64(len(data)), md("folder", f.ID, "name", "c.txt")))
	wantStatus(t, piece(t, s, key, upl, 0, data[:11], nil), 204)
	// A crash after a write but before its offset was recorded leaves bytes past the offset.
	fh, err := os.OpenFile(uploadFile(s, accID, upl), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.Write([]byte("GARBAGE-NOT-RECORDED")); err != nil {
		t.Fatal(err)
	}
	fh.Close()

	s = reopen(t, s)
	h := headUpload(t, s, key, upl)
	if h.Header().Get("Upload-Offset") != "11" {
		t.Fatalf("offset after restart = %s, want 11", h.Header().Get("Upload-Offset"))
	}
	wantStatus(t, piece(t, s, key, upl, 11, data[11:], nil), 204)
}

// TestUploadCompletionIsIdempotentAcrossCrash makes the commit fail after the file has moved into
// blobs/, then restarts and completes again: one file, one node, correct usage.
func TestUploadCompletionIsIdempotentAcrossCrash(t *testing.T) {
	s, key, accID, f := uploadSetup(t)
	data := "crash-me"
	upl := uploadID(t, createUpload(t, s, key, int64(len(data)), md("folder", f.ID, "name", "x.txt")))
	s.beforeCommitUpload = func() error { return errors.New("simulated crash before commit") }
	rec := piece(t, s, key, upl, 0, data, nil)
	if rec.Code == 204 {
		t.Fatal("completion succeeded despite the simulated crash")
	}
	s.beforeCommitUpload = nil

	s = reopen(t, s)
	// The upload is still there, complete, and the file is already a blob.
	if h := headUpload(t, s, key, upl); h.Header().Get("Upload-Offset") != "8" {
		t.Fatalf("after crash HEAD = %d %v", h.Code, h.Header())
	}
	rec = piece(t, s, key, upl, 8, "", nil)
	wantStatus(t, rec, 204)
	if got := fileBody(t, s, key, rec.Header().Get(nodeHeader)); got != data {
		t.Errorf("contents = %q", got)
	}
	if u := usageOf(t, s, key, accID); u.UsedFiles != 1 || u.UsedBytes != int64(len(data)) {
		t.Errorf("usage after retry = %+v", u)
	}
	if n := blobFiles(t, s); n != 1 {
		t.Errorf("blob files = %d, want 1", n)
	}
}

func TestUploadPieceChecksum(t *testing.T) {
	s, key, accID, f := uploadSetup(t)
	upl := uploadID(t, createUpload(t, s, key, 10, md("folder", f.ID, "name", "k.bin")))
	wantStatus(t, piece(t, s, key, upl, 0, "01234", map[string]string{"Upload-Checksum": "sha256 " + b64sum("01234")}), 204)
	before, err := os.Stat(uploadFile(s, accID, upl))
	if err != nil {
		t.Fatal(err)
	}
	// A wrong piece is refused, and nothing it wrote stays.
	rec := piece(t, s, key, upl, 5, "XXXXX", map[string]string{"Upload-Checksum": "sha256 " + b64sum("56789")})
	if rec.Code != apierr.ChecksumMismatch.Status() {
		t.Fatalf("status = %d, want 460: %s", rec.Code, rec.Body)
	}
	if after, _ := os.Stat(uploadFile(s, accID, upl)); after.Size() != before.Size() {
		t.Errorf("file size after refused piece = %d, want %d", after.Size(), before.Size())
	}
	if h := headUpload(t, s, key, upl); h.Header().Get("Upload-Offset") != "5" {
		t.Errorf("offset after refused piece = %s", h.Header().Get("Upload-Offset"))
	}
	wantStatus(t, piece(t, s, key, upl, 5, "56789", map[string]string{"Upload-Checksum": "sha1 " + base64.StdEncoding.EncodeToString(sha1Of("56789"))}), 204)
	// A malformed checksum is refused before anything happens.
	wantError(t, piece(t, s, key, upl, 10, "", map[string]string{"Upload-Checksum": "md5 abc"}), 400, apierr.InvalidRequest)
}

func TestUploadWholeFileChecksumMismatchDropsUpload(t *testing.T) {
	s, key, accID, f := uploadSetup(t)
	upl := uploadID(t, createUpload(t, s, key, 3, md("folder", f.ID, "name", "w.txt", "sha256", digest("nope"))))
	rec := piece(t, s, key, upl, 0, "abc", nil)
	if rec.Code != apierr.ChecksumMismatch.Status() {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	wantError(t, headUpload(t, s, key, upl), 404, apierr.NotFound)
	if _, err := os.Stat(uploadFile(s, accID, upl)); !errors.Is(err, os.ErrNotExist) {
		t.Error("file of a dropped upload remains")
	}
	if u := usageOf(t, s, key, accID); u.UsedFiles != 0 {
		t.Errorf("usage = %+v", u)
	}
}

func TestUploadOffsetMismatch(t *testing.T) {
	s, key, _, f := uploadSetup(t)
	upl := uploadID(t, createUpload(t, s, key, 6, md("folder", f.ID, "name", "o.txt")))
	wantStatus(t, piece(t, s, key, upl, 0, "abc", nil), 204)
	rec := piece(t, s, key, upl, 1, "zz", nil)
	e := wantError(t, rec, 409, apierr.UploadOffsetMismatch)
	if e.Details["offset"] != float64(3) {
		t.Errorf("details = %v", e.Details)
	}
}

func TestUploadOwnership(t *testing.T) {
	m := newMemberFixture(t)
	// Another key of the same account, and the member, both get not_found for the owner's upload.
	other := call(t, m.s, "POST", "/api/v1/accounts/"+m.acc.ID+"/keys", adminKey(t, m.s), map[string]any{"name": "other"})
	wantStatus(t, other, 201)
	otherKey := decode[createdKey](t, other).Key
	upl := uploadID(t, createUpload(t, m.s, m.key, 4, md("folder", m.f.ID, "name", "own.txt")))

	wantError(t, headUpload(t, m.s, otherKey, upl), 404, apierr.NotFound)
	wantError(t, piece(t, m.s, otherKey, upl, 0, "abcd", nil), 404, apierr.NotFound)
	wantError(t, upCall(t, m.s, "DELETE", "/api/v1/uploads/"+upl, otherKey, nil, nil, 0), 404, apierr.NotFound)
	wantError(t, upSession(t, m.s, m.sess, "HEAD", "/api/v1/uploads/"+upl, nil, nil, 0), 404, apierr.NotFound)

	// The member's own upload is hers alone, not the owner key's.
	mine := uploadID(t, upSessionCreate(t, m, "mine.txt", 2))
	wantError(t, headUpload(t, m.s, m.key, mine), 404, apierr.NotFound)
	wantStatus(t, upSession(t, m.s, m.sess, "HEAD", "/api/v1/uploads/"+mine, nil, nil, 0), 200)
}

// upSessionCreate starts an upload as the member, with write on its folder.
func upSessionCreate(t *testing.T, m *memberFixture, name string, length int64) *httptest.ResponseRecorder {
	t.Helper()
	return upSession(t, m.s, m.sess, "POST", "/api/v1/uploads", map[string]string{
		"Upload-Length": strconv.FormatInt(length, 10), "Upload-Metadata": md("folder", m.f.ID, "name", name)}, nil, 0)
}

func TestUploadNeedsWriteAccess(t *testing.T) {
	m := newMemberFixture(t)
	// The member's grant on g is read only.
	wantError(t, upSession(t, m.s, m.sess, "POST", "/api/v1/uploads", map[string]string{
		"Upload-Length": "1", "Upload-Metadata": md("folder", m.g.ID, "name", "no.txt")}, nil, 0),
		403, apierr.Forbidden)
	// An installation admin never writes files.
	admin := adminKey(t, m.s)
	wantError(t, upCall(t, m.s, "POST", "/api/v1/uploads", admin, map[string]string{
		"Upload-Length": "1", "Upload-Metadata": md("folder", m.f.ID, "name", "no.txt")}, nil, 0), 403, apierr.Forbidden)
}

func TestUploadGrantRevokedStopsPiece(t *testing.T) {
	m := newMemberFixture(t)
	rec := upSessionCreate(t, m, "long.txt", 6)
	upl := uploadID(t, rec)
	wantStatus(t, upSession(t, m.s, m.sess, "PATCH", "/api/v1/uploads/"+upl, map[string]string{
		"Content-Type": uploadBodyType, "Upload-Offset": "0"}, strings.NewReader("abc"), 3), 204)
	m.revoke(t, m.f.ID)
	wantError(t, upSession(t, m.s, m.sess, "PATCH", "/api/v1/uploads/"+upl, map[string]string{
		"Content-Type": uploadBodyType, "Upload-Offset": "3"}, strings.NewReader("def"), 3), 404, apierr.NotFound)
	// The grant is gone, so the upload is out of reach for the member too.
	wantError(t, upSession(t, m.s, m.sess, "HEAD", "/api/v1/uploads/"+upl, nil, nil, 0), 404, apierr.NotFound)
}

func TestUploadOnConflict(t *testing.T) {
	s, key, _, f := uploadSetup(t)
	putNode(t, s, key, f.ID, "/same.txt", "old")

	// fail: the name is taken, so creation is refused.
	wantError(t, createUpload(t, s, key, 3, md("folder", f.ID, "name", "same.txt")), 409, apierr.Conflict)

	// rename: the file is saved as "same (1).txt".
	upl := uploadID(t, createUpload(t, s, key, 3, md("folder", f.ID, "name", "same.txt", "on_conflict", "rename")))
	wantStatus(t, piece(t, s, key, upl, 0, "new", nil), 204)
	kids := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/folders/"+f.ID+"/children", key, nil))
	if len(kids.Items) != 2 || kids.Items[0].Name != "same (1).txt" && kids.Items[1].Name != "same (1).txt" {
		t.Fatalf("children after rename = %+v", kids.Items)
	}

	// replace: the existing node keeps its ID and takes the new contents.
	old := nodeIn(t, s, key, f.ID, "same.txt")
	upl = uploadID(t, createUpload(t, s, key, 8, md("folder", f.ID, "name", "same.txt", "on_conflict", "replace")))
	rec := piece(t, s, key, upl, 0, "replaced", nil)
	wantStatus(t, rec, 204)
	if rec.Header().Get(nodeHeader) != old {
		t.Errorf("replace gave node %s, want the existing %s", rec.Header().Get(nodeHeader), old)
	}
	if got := fileBody(t, s, key, old); got != "replaced" {
		t.Errorf("contents = %q", got)
	}
}

func TestUploadParentsAndPathAreCreatedOnCompletion(t *testing.T) {
	s, key, _, f := uploadSetup(t)
	// The directory does not exist, so creation without parents is refused.
	wantError(t, createUpload(t, s, key, 1, md("folder", f.ID, "name", "a.txt", "path", "/x/y")), 404, apierr.NotFound)
	upl := uploadID(t, createUpload(t, s, key, 1, md("folder", f.ID, "name", "a.txt", "path", "/x/y", "parents", "true")))
	wantStatus(t, piece(t, s, key, upl, 0, "z", nil), 204)
	in := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/folders/"+f.ID+"/children?"+q("/x/y"), key, nil))
	if len(in.Items) != 1 || in.Items[0].Name != "a.txt" {
		t.Errorf("children of /x/y = %+v", in.Items)
	}
}

func TestUploadLimits(t *testing.T) {
	s, key, _, f := uploadSetup(t, func(c *config.Config) {
		c.Upload.MaxChunkSize = 4
		c.Upload.MaxFileSize = 16
		c.Upload.MaxSmallFileSize = 16
	})
	// Declared length above upload.max_file_size.
	e := wantError(t, createUpload(t, s, key, 17, md("folder", f.ID, "name", "big")), 413, apierr.TooLarge)
	if e.Details["limit_bytes"] != float64(16) {
		t.Errorf("details = %v", e.Details)
	}
	upl := uploadID(t, createUpload(t, s, key, 10, md("folder", f.ID, "name", "lim.bin")))
	// A piece over upload.max_chunk_size is refused whole.
	rec := piece(t, s, key, upl, 0, "12345", nil)
	wantError(t, rec, 413, apierr.ChunkTooLarge)
	if h := headUpload(t, s, key, upl); h.Header().Get("Upload-Offset") != "0" {
		t.Errorf("refused pieces moved the offset to %s", h.Header().Get("Upload-Offset"))
	}
	// A piece that would pass the declared length is refused.
	wantStatus(t, piece(t, s, key, upl, 0, "1234", nil), 204)
	wantStatus(t, piece(t, s, key, upl, 4, "5678", nil), 204)
	wantError(t, piece(t, s, key, upl, 8, "1234", nil), 413, apierr.TooLarge)
	// A creation whose body is over the cap is refused before anything is made.
	wantError(t, upCall(t, s, "POST", "/api/v1/uploads", key, map[string]string{
		"Upload-Length": "10", "Upload-Metadata": md("folder", f.ID, "name", "c.bin"),
		"Content-Type": uploadBodyType}, strings.NewReader("123456789"), 9), 413, apierr.ChunkTooLarge)
	// Declared max chunk is advertised.
	opt := upCall(t, s, "OPTIONS", "/api/v1/uploads", "", nil, nil, 0)
	wantStatus(t, opt, 204)
	if opt.Header().Get("Selfcloud-Max-Chunk-Size") != "4" || opt.Header().Get("Tus-Max-Size") != "16" ||
		!strings.Contains(opt.Header().Get("Tus-Extension"), "termination") {
		t.Errorf("OPTIONS = %v", opt.Header())
	}
}

func TestUploadExpiresAndSweepRemovesIt(t *testing.T) {
	s, key, accID, f := uploadSetup(t, func(c *config.Config) { c.Upload.Expiry = config.Duration(time.Hour) })
	upl := uploadID(t, createUpload(t, s, key, 5, md("folder", f.ID, "name", "e.txt")))
	wantStatus(t, piece(t, s, key, upl, 0, "ab", nil), 204)
	// An upload file with no row, old enough to be abandoned.
	orphan := uploadFile(s, accID, "upl_"+strings.Repeat("a", 26))
	if err := os.WriteFile(orphan, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(orphan, past, past); err != nil {
		t.Fatal(err)
	}
	sw := sweep.New(sweep.Options{Store: s.store, Blobs: s.blobs, DeleteGrace: time.Hour, Interval: time.Hour,
		UploadExpiry: time.Hour})
	// The abandoned file is older than the expiry, so the first pass removes it; the upload is not
	// expired yet.
	res, err := sw.Once(context.Background(), store.Now())
	if err != nil || res.Uploads != 0 || res.UploadFiles != 1 {
		t.Fatalf("pass before expiry = %+v, %v", res, err)
	}
	res, err = sw.Once(context.Background(), store.Now().Add(2*time.Hour))
	if err != nil || res.Uploads != 1 || res.UploadFiles != 0 {
		t.Fatalf("pass after expiry = %+v, %v", res, err)
	}
	if _, err := os.Stat(uploadFile(s, accID, upl)); !errors.Is(err, os.ErrNotExist) {
		t.Error("expired upload's file remains")
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Error("orphan upload file remains")
	}
	wantError(t, headUpload(t, s, key, upl), 404, apierr.NotFound)
}

func TestUploadExpiredIsGone(t *testing.T) {
	s, key, _, f := uploadSetup(t, func(c *config.Config) { c.Upload.Expiry = config.Duration(time.Millisecond) })
	upl := uploadID(t, createUpload(t, s, key, 5, md("folder", f.ID, "name", "gone.txt")))
	time.Sleep(5 * time.Millisecond)
	wantError(t, piece(t, s, key, upl, 0, "abcde", nil), 410, apierr.UploadExpired)
}

func TestUploadTermination(t *testing.T) {
	s, key, accID, f := uploadSetup(t)
	upl := uploadID(t, createUpload(t, s, key, 5, md("folder", f.ID, "name", "t.txt")))
	wantStatus(t, piece(t, s, key, upl, 0, "ab", nil), 204)
	wantStatus(t, upCall(t, s, "DELETE", "/api/v1/uploads/"+upl, key, nil, nil, 0), 204)
	wantError(t, headUpload(t, s, key, upl), 404, apierr.NotFound)
	if _, err := os.Stat(uploadFile(s, accID, upl)); !errors.Is(err, os.ErrNotExist) {
		t.Error("terminated upload's file remains")
	}
	wantError(t, upCall(t, s, "DELETE", "/api/v1/uploads/"+upl, key, nil, nil, 0), 404, apierr.NotFound)
}

func TestUploadDiskSpace(t *testing.T) {
	// One account alone: its share is set to the whole pool, so this tests the install-wide rule.
	s, key, _, f := uploadSetup(t, func(c *config.Config) { c.Storage.MinFree = 50; c.Upload.MaxDiskSharePercent = 100 })
	free := uint64(100)
	s.freeBytes = func(string) (uint64, error) { return free, nil }
	// 100 bytes free, 50 kept back: a 60-byte file does not fit, a 50-byte one does.
	wantError(t, createUpload(t, s, key, 60, md("folder", f.ID, "name", "big.bin")), 507, apierr.StorageFull)
	upl := uploadID(t, createUpload(t, s, key, 50, md("folder", f.ID, "name", "ok.bin")))
	free = 55
	wantError(t, piece(t, s, key, upl, 0, strings.Repeat("x", 10), nil), 507, apierr.StorageFull)
	if h := headUpload(t, s, key, upl); h.Header().Get("Upload-Offset") != "0" {
		t.Errorf("offset after refused piece = %s", h.Header().Get("Upload-Offset"))
	}
}

func TestUploadOnePieceAtATime(t *testing.T) {
	s, key, _, f := uploadSetup(t)
	s.uploadSlotWait = 100 * time.Millisecond
	upl := uploadID(t, createUpload(t, s, key, 4, md("folder", f.ID, "name", "busy.txt")))
	pr, pw := io.Pipe()
	done := make(chan *httptest.ResponseRecorder)
	go func() {
		done <- upCall(t, s, "PATCH", "/api/v1/uploads/"+upl, key, map[string]string{
			"Content-Type": uploadBodyType, "Upload-Offset": "0"}, pr, 4)
	}()
	_, _ = pw.Write([]byte("ab")) // the first piece is now in progress
	// Wait until the first piece holds the slot.
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.uploadSlotMu.Lock()
		_, busy := s.uploadSlots[upl]
		s.uploadSlotMu.Unlock()
		if busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first piece never took the slot")
		}
		time.Sleep(time.Millisecond)
	}
	wantError(t, piece(t, s, key, upl, 0, "zz", nil), 409, apierr.Locked)
	_, _ = pw.Write([]byte("cd"))
	_ = pw.Close()
	wantStatus(t, <-done, 204)
}

func TestUploadProtocolHeaders(t *testing.T) {
	s, key, _, f := uploadSetup(t)
	// Without Tus-Resumable 1.0.0 the request is refused, with the version the server speaks.
	rec := upCall(t, s, "POST", "/api/v1/uploads", key, map[string]string{"Tus-Resumable": "0.2.2",
		"Upload-Length": "1", "Upload-Metadata": md("folder", f.ID, "name", "v.txt")}, nil, 0)
	wantError(t, rec, 412, apierr.PreconditionFailed)
	if rec.Header().Get("Tus-Version") != tusVersion {
		t.Errorf("Tus-Version = %q", rec.Header().Get("Tus-Version"))
	}
	upl := uploadID(t, createUpload(t, s, key, 3, md("folder", f.ID, "name", "h.txt")))
	// A piece must be an offset octet stream.
	wantError(t, upCall(t, s, "PATCH", "/api/v1/uploads/"+upl, key, map[string]string{
		"Content-Type": "text/plain", "Upload-Offset": "0"}, strings.NewReader("abc"), 3), 415, apierr.UnsupportedMedia)
	// Bad metadata is refused with the name rules.
	wantError(t, createUpload(t, s, key, 1, md("folder", f.ID, "name", "bad/name")), 400, apierr.InvalidName)
	wantError(t, createUpload(t, s, key, 1, md("folder", f.ID, "name", "a.txt", "on_conflict", "nope")), 400, apierr.InvalidRequest)
	wantError(t, createUpload(t, s, key, 1, md("folder", f.ID, "name", "a.txt", "path", "/a/../b")), 400, apierr.InvalidPath)
	wantError(t, createUpload(t, s, key, 1, md("folder", f.ID, "name", "a.txt", "sha256", "xyz")), 400, apierr.InvalidRequest)
	// A declared length is required.
	wantError(t, upCall(t, s, "POST", "/api/v1/uploads", key, map[string]string{
		"Upload-Metadata": md("folder", f.ID, "name", "a.txt")}, nil, 0), 400, apierr.InvalidRequest)
	// tus-js-client and Uppy send filename; it is accepted in place of name.
	rec = createUpload(t, s, key, 1, md("folder", f.ID, "filename", "fn.txt"))
	wantStatus(t, rec, 201)
}

func TestUploadCreationWithBody(t *testing.T) {
	s, key, _, f := uploadSetup(t)
	rec := upCall(t, s, "POST", "/api/v1/uploads", key, map[string]string{
		"Upload-Length": "5", "Upload-Metadata": md("folder", f.ID, "name", "cb.txt"),
		"Content-Type": uploadBodyType}, strings.NewReader("hello"), 5)
	wantStatus(t, rec, 201)
	if got := fileBody(t, s, key, rec.Header().Get(nodeHeader)); got != "hello" {
		t.Errorf("contents = %q", got)
	}
}

func TestUploadSpecDocumentsTusHeaders(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(apispec.Spec, &doc); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, p := range []string{"/api/v1/uploads", "/api/v1/uploads/{upl}"} {
		b, err := yaml.Marshal(doc.Paths[p])
		if err != nil {
			t.Fatal(err)
		}
		text.Write(b)
	}
	// The components hold the header definitions, so check those too.
	all := text.String()
	for _, h := range tusHeaders {
		if !strings.Contains(all, h) {
			t.Errorf("api/openapi.yaml does not mention the header %s on the upload routes", h)
		}
	}
}

// sha1Of is the SHA-1 digest of s, for the sha1 checksum test.
func sha1Of(s string) []byte {
	sum := sha1.Sum([]byte(s))
	return sum[:]
}
