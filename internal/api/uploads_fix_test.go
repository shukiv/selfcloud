package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"selfcloud/internal/apierr"
	"selfcloud/internal/config"
	"selfcloud/internal/store"
	"selfcloud/internal/sweep"
)

// dirFiles counts the regular files under dir; a missing directory has none.
func dirFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			n++
		}
		return err
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return n
}

// Item 1: a completion that fails after the rename leaves nothing behind once the upload is deleted
// or expires.
func TestFailedCompletionLeavesNoFileOnDelete(t *testing.T) {
	s, key, accID, f := uploadSetup(t)
	upl := uploadID(t, createUpload(t, s, key, 4, md("folder", f.ID, "name", "f1.txt")))
	s.beforeCommitUpload = func() error { return errors.New("simulated failure after the rename") }
	if rec := piece(t, s, key, upl, 0, "abcd", nil); rec.Code == http.StatusNoContent {
		t.Fatal("completion succeeded despite the injected failure")
	}
	s.beforeCommitUpload = nil
	if n := dirFiles(t, filepath.Join(s.cfg.DataDir, "blobs")); n != 1 {
		t.Fatalf("the rename should have left one blob file, found %d", n)
	}
	wantStatus(t, upCall(t, s, "DELETE", "/api/v1/uploads/"+upl, key, nil, nil, 0), 204)
	if n := dirFiles(t, filepath.Join(s.cfg.DataDir, "blobs")); n != 0 {
		t.Errorf("blob files after DELETE = %d, want 0", n)
	}
	if n := dirFiles(t, filepath.Join(s.cfg.DataDir, "uploads", accID)); n != 0 {
		t.Errorf("upload files after DELETE = %d, want 0", n)
	}
}

func TestFailedCompletionLeavesNoFileOnExpiry(t *testing.T) {
	s, key, accID, f := uploadSetup(t, func(c *config.Config) { c.Upload.Expiry = config.Duration(time.Hour) })
	upl := uploadID(t, createUpload(t, s, key, 4, md("folder", f.ID, "name", "f2.txt")))
	s.beforeCommitUpload = func() error { return errors.New("simulated failure after the rename") }
	_ = piece(t, s, key, upl, 0, "abcd", nil)
	s.beforeCommitUpload = nil
	sw := sweep.New(sweep.Options{Store: s.store, Blobs: s.blobs, DeleteGrace: time.Hour, Interval: time.Hour,
		UploadExpiry: time.Hour})
	if _, err := sw.Once(context.Background(), store.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := dirFiles(t, filepath.Join(s.cfg.DataDir, "blobs")); n != 0 {
		t.Errorf("blob files after expiry = %d, want 0", n)
	}
	if n := dirFiles(t, filepath.Join(s.cfg.DataDir, "uploads", accID)); n != 0 {
		t.Errorf("upload files after expiry = %d, want 0", n)
	}
}

// Item 1: completion runs on a context detached from the request, so a client that disconnects at
// the last byte does not roll back the finished work.
func TestCompletionSurvivesClientDisconnect(t *testing.T) {
	s, key, _, f := uploadSetup(t)
	upl := uploadID(t, createUpload(t, s, key, 4, md("folder", f.ID, "name", "gone-client.txt")))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("PATCH", "/api/v1/uploads/"+upl, strings.NewReader("abcd")).WithContext(ctx)
	req.ContentLength = 4
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Tus-Resumable", tusVersion)
	req.Header.Set("Content-Type", uploadBodyType)
	req.Header.Set("Upload-Offset", "0")
	// The client goes away while the commit is running.
	s.beforeCommitUpload = func() error { cancel(); return nil }
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	s.beforeCommitUpload = nil
	wantStatus(t, rec, 204)
	if got := fileBody(t, s, key, rec.Header().Get(nodeHeader)); got != "abcd" {
		t.Errorf("contents = %q", got)
	}
}

// Item 2: open uploads are capped per caller and per account.
func TestUploadOpenCaps(t *testing.T) {
	s, key, accID, f := uploadSetup(t, func(c *config.Config) { c.Upload.MaxOpenPerPrincipal = 2 })
	for i := 0; i < 2; i++ {
		uploadID(t, createUpload(t, s, key, 1, md("folder", f.ID, "name", fmt.Sprintf("o%d.txt", i))))
	}
	e := wantError(t, createUpload(t, s, key, 1, md("folder", f.ID, "name", "o2.txt")), 429, apierr.TooManyUploads)
	if e.Details["scope"] != "caller" {
		t.Errorf("details = %v", e.Details)
	}
	// The account cap counts every caller: a second key of the account cannot exceed it either.
	s2, key2, acc2, f2 := uploadSetup(t, func(c *config.Config) {
		c.Upload.MaxOpenPerPrincipal = 10
		c.Upload.MaxOpenPerAccount = 3
	})
	_ = accID
	second := call(t, s2, "POST", "/api/v1/accounts/"+acc2+"/keys", adminKey(t, s2), map[string]any{"name": "two"})
	wantStatus(t, second, 201)
	key3 := decode[createdKey](t, second).Key
	for i := 0; i < 3; i++ {
		uploadID(t, createUpload(t, s2, key2, 1, md("folder", f2.ID, "name", fmt.Sprintf("a%d.txt", i))))
	}
	e = wantError(t, createUpload(t, s2, key3, 1, md("folder", f2.ID, "name", "x.txt")), 429, apierr.TooManyUploads)
	if e.Details["scope"] != "account" {
		t.Errorf("details = %v", e.Details)
	}
}

// Item 2: creation is rate limited per caller.
func TestUploadCreationRateLimited(t *testing.T) {
	s, key, _, f := uploadSetup(t)
	// Production allows 60 a minute, refilling at one a second; a test with a slow race build would
	// earn a token mid-loop, so the test uses a bucket of five that cannot refill meanwhile.
	s.limits["upload.create"] = newLimiterRate(5, time.Hour)
	for i := 0; i < 5; i++ {
		// Zero-length files complete at once, so they hold no open slot.
		wantStatus(t, createUpload(t, s, key, 0, md("folder", f.ID, "name", fmt.Sprintf("r%d.txt", i))), 201)
	}
	wantError(t, createUpload(t, s, key, 0, md("folder", f.ID, "name", "r5.txt")), 429, apierr.RateLimited)
}

// Item 2: the disk check counts every open upload's unwritten bytes, not just the new piece.
func TestDiskCheckCountsReservations(t *testing.T) {
	s, key, _, f := uploadSetup(t, func(c *config.Config) { c.Upload.MaxDiskSharePercent = 100 })
	s.freeBytes = func(string) (uint64, error) { return 100, nil }
	upl := uploadID(t, createUpload(t, s, key, 60, md("folder", f.ID, "name", "d60.bin")))
	wantError(t, createUpload(t, s, key, 50, md("folder", f.ID, "name", "d50.bin")), 507, apierr.StorageFull)
	wantStatus(t, piece(t, s, key, upl, 0, strings.Repeat("x", 60), nil), 204)
	// Written, so the reservation is gone and the second upload fits.
	wantStatus(t, createUpload(t, s, key, 90, md("folder", f.ID, "name", "d90.bin")), 201)
}

// Item 3: a checksummed piece that is cut short cannot be verified, so it is dropped whole.
func TestCutShortChecksummedPieceIsDropped(t *testing.T) {
	s, key, accID, f := uploadSetup(t)
	upl := uploadID(t, createUpload(t, s, key, 6, md("folder", f.ID, "name", "cut.bin")))
	body := io.MultiReader(strings.NewReader("abc"), brokenReader{})
	rec := upCall(t, s, "PATCH", "/api/v1/uploads/"+upl, key, map[string]string{
		"Content-Type": uploadBodyType, "Upload-Offset": "0", "Upload-Checksum": "sha256 " + b64sum("abcdef")}, body, 6)
	wantError(t, rec, 400, apierr.InvalidRequest)
	if h := headUpload(t, s, key, upl); h.Header().Get("Upload-Offset") != "0" {
		t.Errorf("offset after cut-short checksummed piece = %s, want 0", h.Header().Get("Upload-Offset"))
	}
	if fi, err := os.Stat(uploadFile(s, accID, upl)); err != nil || fi.Size() != 0 {
		t.Errorf("file after cut-short checksummed piece: %v, %v", fi, err)
	}
}

// Item 4: a creation-with-upload that fails deletes the upload, row and file.
func TestFailedCreationWithBodyDeletesUpload(t *testing.T) {
	s, key, accID, f := uploadSetup(t)
	body := io.MultiReader(strings.NewReader("abc"), brokenReader{})
	rec := upCall(t, s, "POST", "/api/v1/uploads", key, map[string]string{
		"Upload-Length": "10", "Upload-Metadata": md("folder", f.ID, "name", "cwb.bin"),
		"Content-Type": uploadBodyType}, body, 10)
	wantError(t, rec, 400, apierr.InvalidRequest)
	if n := dirFiles(t, filepath.Join(s.cfg.DataDir, "uploads", accID)); n != 0 {
		t.Errorf("upload files after failed creation = %d, want 0", n)
	}
	page, _, err := s.store.AuditLog(context.Background(), accID, store.Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range page {
		if e.Action == "upload.delete" && e.Details["reason"] == "creation failed" {
			found = true
		}
	}
	if !found {
		t.Error("no audited upload.delete for the failed creation")
	}
}

// Item 5: a missing file of an upload with nothing recorded is made again, not a 500.
func TestMissingFileWithNothingRecordedIsRecreated(t *testing.T) {
	s, key, accID, f := uploadSetup(t)
	upl := uploadID(t, createUpload(t, s, key, 5, md("folder", f.ID, "name", "re.txt")))
	if err := os.Remove(uploadFile(s, accID, upl)); err != nil {
		t.Fatal(err)
	}
	rec := piece(t, s, key, upl, 0, "hello", nil)
	wantStatus(t, rec, 204)
	if got := fileBody(t, s, key, rec.Header().Get(nodeHeader)); got != "hello" {
		t.Errorf("contents = %q", got)
	}
}

// stopWriter is a response writer whose read deadline can be pulled in, as a server connection's is.
// The first deadline set in the past (what a stop does) closes stopped.
type stopWriter struct {
	*httptest.ResponseRecorder
	stopped chan struct{}
	once    sync.Once
}

func (w *stopWriter) SetReadDeadline(t time.Time) error {
	if !t.After(time.Now().Add(time.Second)) {
		w.once.Do(func() { close(w.stopped) })
	}
	return nil
}

// stallBody sends some bytes and then stalls, as a half-open connection does, until stopped or
// released. Then it fails the way a dropped connection does.
type stallBody struct {
	head     io.Reader
	stopped  <-chan struct{}
	released <-chan struct{}
}

func (b *stallBody) Read(p []byte) (int, error) {
	if b.head != nil {
		n, err := b.head.Read(p)
		if err == io.EOF {
			b.head = nil
			if n > 0 {
				return n, nil
			}
		} else {
			return n, err
		}
	}
	select {
	case <-b.stopped:
	case <-b.released:
	}
	return 0, io.ErrUnexpectedEOF
}

// Item 6: a new piece from the owner stops the half-open one holding the slot, waits for it, and
// then goes on from the durable offset the holder left.
func TestSlotHolderIsStoppedAndResumed(t *testing.T) {
	s, key, _, f := uploadSetup(t)
	upl := uploadID(t, createUpload(t, s, key, 6, md("folder", f.ID, "name", "stall.bin")))
	stopped := make(chan struct{})
	released := make(chan struct{})
	defer close(released)
	holder := &stopWriter{ResponseRecorder: httptest.NewRecorder(), stopped: stopped}
	holderDone := make(chan struct{})
	go func() {
		defer close(holderDone)
		req := httptest.NewRequest("PATCH", "/api/v1/uploads/"+upl, &stallBody{
			head: strings.NewReader("abc"), stopped: stopped, released: released})
		req.ContentLength = 6
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Tus-Resumable", tusVersion)
		req.Header.Set("Content-Type", uploadBodyType)
		req.Header.Set("Upload-Offset", "0")
		s.Handler().ServeHTTP(holder, req)
	}()
	waitForSlot(t, s, upl)

	rec := piece(t, s, key, upl, 3, "def", nil)
	<-holderDone
	wantStatus(t, rec, 204)
	if got := fileBody(t, s, key, rec.Header().Get(nodeHeader)); got != "abcdef" {
		t.Errorf("contents = %q", got)
	}
	if holder.Code != 400 {
		t.Errorf("holder answered %d, want 400 for the cut-short piece", holder.Code)
	}
}

// Item 6: a holder that does not let go in time keeps the slot, and the new request gets 409 locked.
func TestSlotHolderThatStaysGivesLocked(t *testing.T) {
	s, key, _, f := uploadSetup(t)
	s.uploadSlotWait = 100 * time.Millisecond
	upl := uploadID(t, createUpload(t, s, key, 6, md("folder", f.ID, "name", "stuck.bin")))
	stopped := make(chan struct{})
	released := make(chan struct{})
	defer close(released)
	// The holder ignores the stop: its writer does not implement deadlines.
	holder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest("PATCH", "/api/v1/uploads/"+upl, &stallBody{stopped: stopped, released: released})
		req.ContentLength = 6
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Tus-Resumable", tusVersion)
		req.Header.Set("Content-Type", uploadBodyType)
		req.Header.Set("Upload-Offset", "0")
		s.Handler().ServeHTTP(holder, req)
	}()
	waitForSlot(t, s, upl)
	wantError(t, piece(t, s, key, upl, 0, "zz", nil), 409, apierr.Locked)
	_ = done
}

// waitForSlot waits until a request holds the upload's PATCH slot.
func waitForSlot(t *testing.T, s *Server, upl string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.uploadSlotMu.Lock()
		_, busy := s.uploadSlots[upl]
		s.uploadSlotMu.Unlock()
		if busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no request took the slot")
		}
		time.Sleep(time.Millisecond)
	}
}

// Item 7: creating, terminating and expiring uploads are audited, expiry as the system.
func TestUploadsAreAudited(t *testing.T) {
	s, key, accID, f := uploadSetup(t, func(c *config.Config) { c.Upload.Expiry = config.Duration(time.Hour) })
	gone := uploadID(t, createUpload(t, s, key, 5, md("folder", f.ID, "name", "a1.txt")))
	wantStatus(t, upCall(t, s, "DELETE", "/api/v1/uploads/"+gone, key, nil, nil, 0), 204)
	expired := uploadID(t, createUpload(t, s, key, 5, md("folder", f.ID, "name", "a2.txt")))
	sw := sweep.New(sweep.Options{Store: s.store, Blobs: s.blobs, DeleteGrace: time.Hour, Interval: time.Hour,
		UploadExpiry: time.Hour})
	if _, err := sw.Once(context.Background(), store.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	entries, _, err := s.store.AuditLog(context.Background(), accID, store.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]*store.AuditEntry{}
	for _, e := range entries {
		seen[e.Action+" "+e.Target] = e
	}
	if seen["upload.create "+gone] == nil || seen["upload.delete "+gone] == nil {
		t.Errorf("create/delete not audited: %v", seen)
	}
	if e := seen["upload.expire "+expired]; e == nil || e.ActorKind != "system" {
		t.Errorf("expiry not audited as system: %+v", e)
	}
}

// Item 8: a completion that meets a taken name waits for a commit, the file stays where it is, and
// the owner commits it under another name.
func TestAwaitingCommitAndCommit(t *testing.T) {
	s, key, accID, f := uploadSetup(t)
	upl := uploadID(t, createUpload(t, s, key, 2, md("folder", f.ID, "name", "y.txt")))
	putNode(t, s, key, f.ID, "/y.txt", "taken")
	blobsBefore := dirFiles(t, filepath.Join(s.cfg.DataDir, "blobs"))

	rec := piece(t, s, key, upl, 0, "ab", nil)
	e := wantError(t, rec, 409, apierr.Conflict)
	if e.Details == nil {
		t.Fatal("no details")
	}
	if rec.Header().Get(stateHeader) != awaitingState || rec.Header().Get("Location") == "" {
		t.Errorf("awaiting headers = %v", rec.Header())
	}
	if h := headUpload(t, s, key, upl); h.Header().Get(stateHeader) != awaitingState || h.Header().Get("Upload-Offset") != "2" {
		t.Errorf("HEAD while waiting = %v", h.Header())
	}
	if dirFiles(t, filepath.Join(s.cfg.DataDir, "blobs")) != blobsBefore {
		t.Error("a waiting upload moved its file into blobs/")
	}
	if dirFiles(t, filepath.Join(s.cfg.DataDir, "uploads", accID)) != 1 {
		t.Error("the waiting upload's file is not under uploads/")
	}

	// Only the owner may commit: another key of the account gets not_found.
	other := call(t, s, "POST", "/api/v1/accounts/"+accID+"/keys", adminKey(t, s), map[string]any{"name": "o"})
	wantStatus(t, other, 201)
	otherKey := decode[createdKey](t, other).Key
	wantError(t, upCall(t, s, "POST", "/api/v1/uploads/"+upl+"/commit", otherKey,
		map[string]string{"Content-Type": "application/json"}, strings.NewReader(`{"name":"y2.txt"}`), 17), 404, apierr.NotFound)

	// Commit under a free name: 201 with the node, and the upload is gone.
	rec2 := upCall(t, s, "POST", "/api/v1/uploads/"+upl+"/commit", key,
		map[string]string{"Content-Type": "application/json"}, strings.NewReader(`{"name":"y2.txt"}`), 17)
	wantStatus(t, rec2, 201)
	n := decode[nodeJSON](t, rec2)
	if n.Name != "y2.txt" || fileBody(t, s, key, n.ID) != "ab" {
		t.Errorf("committed node = %+v", n)
	}
	wantError(t, headUpload(t, s, key, upl), 404, apierr.NotFound)
	if dirFiles(t, filepath.Join(s.cfg.DataDir, "uploads", accID)) != 0 {
		t.Error("committed upload's file is still under uploads/")
	}
}

// Item 8: committing an upload that is not waiting for a name is a conflict.
func TestCommitOnlyWhileAwaiting(t *testing.T) {
	s, key, _, f := uploadSetup(t)
	upl := uploadID(t, createUpload(t, s, key, 5, md("folder", f.ID, "name", "nw.txt")))
	wantError(t, upCall(t, s, "POST", "/api/v1/uploads/"+upl+"/commit", key,
		map[string]string{"Content-Type": "application/json"}, strings.NewReader(`{}`), 2), 409, apierr.Conflict)
	// Unknown fields are refused, as in the rest of the API.
	wantError(t, upCall(t, s, "POST", "/api/v1/uploads/"+upl+"/commit", key,
		map[string]string{"Content-Type": "application/json"}, strings.NewReader(`{"x":1}`), 7), 400, apierr.InvalidRequest)
	_ = strconv.Itoa
}
