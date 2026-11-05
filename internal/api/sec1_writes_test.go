// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// countBlobFiles counts the files under the blob directory of the data directory, the places a one-request write lands.
func countBlobFiles(t *testing.T, dataDir string) int {
	t.Helper()
	n := 0
	root := filepath.Join(dataDir, "blobs")
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// sec1 item 2 (decision 84): a one-request write checks the disk first, as an upload does. With the free space below
// storage.min_free, a small write is refused before any byte reaches the blob directory.
func TestOneRequestWriteIsRefusedWhenTheDiskIsBelowMinFree(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Storage.MinFree = config.ByteSize(1 << 60) })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := folderOf(t, s, key, acc.ID)
	before := countBlobFiles(t, s.cfg.DataDir)

	rec := rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/files?"+q("/one.txt"), key, "hello", nil)
	wantStatus(t, rec, http.StatusInsufficientStorage)
	if after := countBlobFiles(t, s.cfg.DataDir); after != before {
		t.Errorf("blob files went from %d to %d: the refused write reached the disk", before, after)
	}
}

// sec1 item 2 (decision 84): a one-request write whose declared length is more than the account's free quota is
// refused before any byte is written, not after.
func TestOneRequestWriteIsRefusedByQuotaBeforeItIsWritten(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID, admin, map[string]any{"quota_bytes": 10}), 200)
	fld := folderOf(t, s, key, acc.ID)
	before := countBlobFiles(t, s.cfg.DataDir)

	// The body counts its reads: a refusal on Content-Length must come before any of it is read.
	body := &countingReader{r: strings.NewReader(strings.Repeat("x", 100))}
	req := httptest.NewRequest("POST", "/api/v1/folders/"+fld+"/files?"+q("/big.bin"), body)
	req.ContentLength = 100
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, http.StatusInsufficientStorage)
	if body.reads != 0 {
		t.Errorf("the body was read %d times before the quota refused the write", body.reads)
	}
	if after := countBlobFiles(t, s.cfg.DataDir); after != before {
		t.Errorf("blob files went from %d to %d: the over-quota write reached the disk", before, after)
	}
}

// chunkedWrite posts a body with no Content-Length (chunked), counting its reads.
func chunkedWrite(t *testing.T, s *Server, key, fld, name string, size int) (*httptest.ResponseRecorder, *countingReader) {
	t.Helper()
	body := &countingReader{r: strings.NewReader(strings.Repeat("x", size))}
	req := httptest.NewRequest("POST", "/api/v1/folders/"+fld+"/files?"+q(name), body)
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec, body
}

// sec1 item 2, gap found in review (decision 84): a body with no Content-Length is counted as the cap, since it may
// be as large as the cap. With the disk holding less than the cap, it is refused before any of it is read, though one
// byte would fit.
func TestChunkedWriteIsCheckedAgainstTheCap(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := folderOf(t, s, key, acc.ID)
	free, err := s.freeBytes(s.cfg.DataDir)
	if err != nil {
		t.Skipf("free space cannot be read here: %v", err)
	}
	// Leave 1 KiB promised by the disk: far less than the cap, far more than the 100-byte body.
	s.cfg.Storage.MinFree = config.ByteSize(int64(free) - 1024)
	before := countBlobFiles(t, s.cfg.DataDir)

	rec, body := chunkedWrite(t, s, key, fld, "/chunked.txt", 100)
	wantStatus(t, rec, http.StatusInsufficientStorage)
	if body.reads != 0 {
		t.Errorf("the chunked body was read %d times before the disk refused it", body.reads)
	}
	if after := countBlobFiles(t, s.cfg.DataDir); after != before {
		t.Errorf("blob files went from %d to %d", before, after)
	}
}

// sec1 item 2, gap found in review (decision 84): a chunked body over the account's remaining quota is refused before
// any byte is read. The unsized body is counted as the cap, which is over the 10-byte quota here.
func TestChunkedWriteIsCheckedAgainstTheQuota(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID, admin, map[string]any{"quota_bytes": 10}), 200)
	fld := folderOf(t, s, key, acc.ID)
	before := countBlobFiles(t, s.cfg.DataDir)

	rec, body := chunkedWrite(t, s, key, fld, "/chunked.bin", 100)
	wantStatus(t, rec, http.StatusInsufficientStorage)
	if body.reads != 0 {
		t.Errorf("the chunked body was read %d times before the quota refused it", body.reads)
	}
	if after := countBlobFiles(t, s.cfg.DataDir); after != before {
		t.Errorf("blob files went from %d to %d", before, after)
	}
}

// countingReader counts the reads made of its body.
type countingReader struct {
	r     io.Reader
	reads int
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.reads++
	return c.r.Read(p)
}

// sec1 item 2, review F6 and F3 (decision 84): every one-request write is rate limited per caller at 300 a minute, large
// or small, so the 301st write in a minute from one caller is refused, and an earlier one is not. Small bodies are
// used, so the test stays quick; the limit does not depend on the size.
func TestOneRequestWritesAreRateLimitedPerCaller(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := folderOf(t, s, key, acc.ID)
	for i := 1; i <= 300; i++ {
		if rec := rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/files?"+q(fmt.Sprintf("/s%d.txt", i)), key, "x", nil); rec.Code >= 300 {
			t.Fatalf("write %d: status %d, want success before the limit", i, rec.Code)
		}
	}
	wantError(t, rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/files?"+q("/s301.txt"), key, "x", nil), 429, apierr.RateLimited)
}

// gatedReader blocks its first read until the gate is open, and says so when that read starts. A one-request write is
// checked before its body is read, so a write that is waiting here has been checked and accepted.
type gatedReader struct {
	r         io.Reader
	started   chan<- struct{}
	gate      <-chan struct{}
	signalled bool
}

func (g *gatedReader) Read(p []byte) (int, error) {
	if !g.signalled {
		g.signalled = true
		g.started <- struct{}{}
		<-g.gate
	}
	return g.r.Read(p)
}

// sec1 item 2, review F3 (decision 84): one-request writes reserve their bytes while they are in flight, so writes that
// run together cannot each pass against the same free space. Four writes of 16 MiB see 40 MiB above storage.min_free.
// Each waits in its body until the test lets it go, so every write has been checked before any byte arrives. At least
// one must be refused before its body is read, and at most two may be accepted (2 x 16 MiB fits, 3 do not).
func TestConcurrentOneRequestWritesCannotOverrunTheDisk(t *testing.T) {
	t.Parallel()
	const writeSize = 16 << 20
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Storage.MinFree = config.ByteSize(64 << 20) })
	s.freeBytes = func(string) (uint64, error) { return 64<<20 + 40<<20, nil }
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := folderOf(t, s, key, acc.ID)

	gate := make(chan struct{})
	var open sync.Once
	letGo := func() { open.Do(func() { close(gate) }) }
	t.Cleanup(letGo)

	started := make(chan struct{}, 4)
	done := make(chan int, 4)
	for i := 1; i <= 4; i++ {
		body := &gatedReader{r: strings.NewReader(strings.Repeat("x", writeSize)), started: started, gate: gate}
		req := httptest.NewRequest("POST", "/api/v1/folders/"+fld+"/files?"+q(fmt.Sprintf("/w%d.bin", i)), body)
		req.ContentLength = writeSize
		req.Header.Set("Authorization", "Bearer "+key)
		go func() {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			done <- rec.Code
		}()
	}

	// Each write either is refused at once, or is waiting in its body. Wait until all four are one or the other.
	var codes []int
	waiting := 0
	timeout := time.After(20 * time.Second)
	for waiting+len(codes) < 4 {
		select {
		case <-started:
			waiting++
		case code := <-done:
			codes = append(codes, code)
		case <-timeout:
			t.Fatalf("the writes did not settle: %d waiting, %d answered", waiting, len(codes))
		}
	}
	refusedBeforeBytes := 0
	for _, c := range codes {
		if c == http.StatusInsufficientStorage {
			refusedBeforeBytes++
		}
	}
	if refusedBeforeBytes < 1 {
		t.Errorf("all four writes were checked against the same free space and accepted: %d waiting in their bodies", waiting)
	}

	letGo()
	for len(codes) < 4 {
		select {
		case code := <-done:
			codes = append(codes, code)
		case <-timeout:
			t.Fatalf("the accepted writes did not finish: %d answered", len(codes))
		}
	}
	accepted := 0
	for _, c := range codes {
		if c == http.StatusCreated {
			accepted++
		}
	}
	if accepted > 2 {
		t.Errorf("%d writes of %d bytes were accepted into %d bytes above storage.min_free", accepted, writeSize, 40<<20)
	}
}

// sec1 item 2, review (decision 84): a large one-request write takes two limiters: write.one_request (300 a minute, every
// write) and write.one_request_large (60 a minute, as upload.create). The 61st large write in a minute from one caller is
// refused, and a small write from the same caller still passes.
func TestLargeOneRequestWritesAreLimitedAt60(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := folderOf(t, s, key, acc.ID)
	big := strings.Repeat("x", (1<<20)+1)
	for i := 1; i <= 60; i++ {
		if rec := rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/files?"+q(fmt.Sprintf("/f%d.bin", i)), key, big, nil); rec.Code >= 300 {
			t.Fatalf("large write %d: status %d, want success before the limit", i, rec.Code)
		}
	}
	wantError(t, rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/files?"+q("/f61.bin"), key, big, nil), 429, apierr.RateLimited)
	if rec := rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/files?"+q("/small.txt"), key, "x", nil); rec.Code >= 300 {
		t.Fatalf("small write after the large limit: status %d, want success", rec.Code)
	}
}
