// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/thumb"
)

// TestMain lets the test binary be the child: `thumb <flags>` runs the decoder, as the server's binary does.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "thumb" {
		os.Exit(thumb.Child(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// fixture is a store, a blob directory, a derived directory and a pool, with one account.
type fixture struct {
	t     *testing.T
	ctx   context.Context
	st    *store.Store
	blobs *blob.Dir
	cache *Cache
	pool  *Pool
	acc   string
	// clock is what the pool reads as the time; a test moves it forward to reach a retry time.
	clock time.Time
}

func newFixture(t *testing.T, queue int) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "test.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	blobs, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, err := NewFiles(filepath.Join(dir, "derived"))
	if err != nil {
		t.Fatal(err)
	}
	cache := NewCache(st, files, 1<<40, nil)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, ctx: ctx, st: st, blobs: blobs, cache: cache, acc: id.New("acc"),
		clock: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	f.pool = f.newPool(func(o *Options) {
		o.Workers = 1
		o.Queue = queue
		o.Exe = exe
	})
	if err := st.Write(ctx, func(tx *store.Tx) error {
		return tx.InsertAccount(ctx, &store.Account{ID: f.acc, Name: "Acme", Status: store.AccountActive,
			CreatedAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

// putBlob commits content as a blob of accountID and returns its ID.
func (f *fixture) putBlob(accountID string, content []byte) string {
	f.t.Helper()
	w, err := f.blobs.Create(f.ctx, accountID, 1<<30)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		f.t.Fatal(err)
	}
	c, err := w.Commit()
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		return tx.InsertBlob(f.ctx, &store.Blob{ID: c.ID, AccountID: accountID, Size: c.Size, SHA256: c.SHA256,
			State: store.BlobLive, CreatedAt: time.Now()})
	}); err != nil {
		f.t.Fatal(err)
	}
	return c.ID
}

// runQueued runs the next queued job on this goroutine, so a test sees each step's result.
// baseOptions are the limits of the fixture's pool.
func (f *fixture) baseOptions() Options {
	exe, err := os.Executable()
	if err != nil {
		f.t.Fatal(err)
	}
	return Options{Store: f.st, Blobs: f.blobs, Cache: f.cache, Exe: exe, Workers: 1, Queue: 8,
		Timeout: 30 * time.Second, CPUSeconds: 30, MemoryBytes: 512 << 20, MaxPixels: 50_000_000,
		MaxSourceBytes: 50 << 20, MaxOutputBytes: 16 << 20, MinFree: 0,
		Now: func() time.Time { return f.clock }}
}

// newPool returns a pool on the fixture's store and cache, with the base options changed by mod.
// queuedJobs is how many jobs wait in the pool's queue.
func (p *Pool) queuedJobs() int { return len(p.q.ready) }

// runOn runs the next queued job of pool p on this goroutine.
func (f *fixture) runOn(p *Pool) {
	f.t.Helper()
	select {
	case <-p.q.ready:
		j, ok := p.q.pop()
		if !ok {
			f.t.Fatal("a token with no job")
		}
		p.safeProcess(f.ctx, j)
	default:
		f.t.Fatal("no job is queued")
	}
}

func (f *fixture) newPool(mod func(*Options)) *Pool {
	o := f.baseOptions()
	mod(&o)
	return NewPool(o)
}

func (f *fixture) runQueued() {
	f.t.Helper()
	select {
	case <-f.pool.q.ready:
		j, ok := f.pool.q.pop()
		if !ok {
			f.t.Fatal("a token with no job")
		}
		f.pool.safeProcess(f.ctx, j)
	default:
		f.t.Fatal("no job is queued")
	}
}

func (f *fixture) queued() int { return len(f.pool.q.ready) }

func (f *fixture) state(accountID, blobID string, size int) *store.Derived {
	f.t.Helper()
	d, err := f.cache.Lookup(f.ctx, accountID, blobID, Kind(size), f.pool.Converter())
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

// stateOn returns the row a pool makes for a blob, under that pool's converter key.
func (f *fixture) stateOn(p *Pool, accountID, blobID string, size int) *store.Derived {
	f.t.Helper()
	d, err := f.cache.Lookup(f.ctx, accountID, blobID, Kind(size), p.Converter())
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

func (f *fixture) request(accountID, blobID string, size int) Answer {
	f.t.Helper()
	a, err := f.pool.Request(f.ctx, accountID, blobID, size, nil)
	if err != nil {
		f.t.Fatalf("Request: %v", err)
	}
	return a
}

func samplePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// headerOnlyPNG claims w by h and has no pixel data. The header is valid, so only the pixel cap refuses it.
func headerOnlyPNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6
	writeChunk := func(typ string, data []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(data)))
		b.Write(n[:])
		b.WriteString(typ)
		b.Write(data)
		var c [4]byte
		binary.BigEndian.PutUint32(c[:], crc32.ChecksumIEEE(append([]byte(typ), data...)))
		b.Write(c[:])
	}
	writeChunk("IHDR", ihdr)
	writeChunk("IEND", nil)
	return b.Bytes()
}

func TestRequestQueuesOnceThenServesReady(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	bl := f.putBlob(f.acc, samplePNG(t, 400, 300))

	if a := f.request(f.acc, bl, 64); a.State != store.DerivedPending {
		t.Fatalf("first request: %s", a.State)
	}
	if a := f.request(f.acc, bl, 64); a.State != store.DerivedPending {
		t.Fatalf("second request: %s", a.State)
	}
	if n := f.queued(); n != 1 {
		t.Fatalf("%d jobs queued for one thumbnail, want 1", n)
	}
	f.runQueued()

	a := f.request(f.acc, bl, 64)
	if a.State != store.DerivedReady || a.Row == nil {
		t.Fatalf("after the job: %s", a.State)
	}
	file, err := f.pool.Open(f.acc, a.Row.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	out, _ := io.ReadAll(file)
	if _, _, err := image.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("the derived file is not an image: %v", err)
	}
	if int64(len(out)) != a.Row.Size {
		t.Errorf("file is %d bytes, row says %d", len(out), a.Row.Size)
	}
	if n := f.queued(); n != 0 {
		t.Errorf("a ready thumbnail queued %d jobs", n)
	}
}

func TestBadSourceFailsOnceAndStaysFailed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	garbage := f.putBlob(f.acc, []byte("\x89PNG\r\n\x1a\nnot really a png"))
	f.request(f.acc, garbage, 64)
	f.runQueued()
	if d := f.state(f.acc, garbage, 64); d == nil || d.State != store.DerivedFailed || d.Error != msgCorrupt {
		t.Fatalf("after a corrupt source: %+v", d)
	}
	// Failure is sticky: asking again neither queues nor retries.
	if a := f.request(f.acc, garbage, 64); a.State != store.DerivedFailed || f.queued() != 0 {
		t.Errorf("a failed source was queued again: state %s, queued %d", a.State, f.queued())
	}
}

func TestBombAndSvgAreRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	bomb := f.putBlob(f.acc, headerOnlyPNG(30000, 30000))
	svg := f.putBlob(f.acc, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	f.request(f.acc, bomb, 64)
	f.request(f.acc, svg, 64)
	f.runQueued()
	f.runQueued()
	if d := f.state(f.acc, bomb, 64); d == nil || d.State != store.DerivedFailed || d.Error != msgTooLarge {
		t.Errorf("bomb: %+v", d)
	}
	if d := f.state(f.acc, svg, 64); d == nil || d.State != store.DerivedFailed || d.Error != msgUnsupported {
		t.Errorf("svg: %+v", d)
	}
}

func TestOtherAccountSeesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	f.request(f.acc, bl, 64)
	f.runQueued()
	other := id.New("acc")
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		return tx.InsertAccount(f.ctx, &store.Account{ID: other, Name: "Other", Status: store.AccountActive, CreatedAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	if d, err := f.cache.Lookup(f.ctx, other, bl, Kind(64), f.pool.Converter()); err != nil || d != nil {
		t.Fatalf("another account's lookup = %+v, %v; want nothing", d, err)
	}
	// Asking as the other account is refused: the blob is not its, and the insert checks that in SQL.
	if _, err := f.pool.Request(f.ctx, other, bl, 64, nil); !errors.Is(err, ErrNoBlob) {
		t.Fatalf("another account's request = %v, want ErrNoBlob", err)
	}
	if f.queued() != 0 {
		t.Errorf("a refused request queued work")
	}
	// The owner's row is untouched.
	if d := f.state(f.acc, bl, 64); d == nil || d.State != store.DerivedReady {
		t.Errorf("the owner's row changed: %+v", d)
	}
}

func TestFullQueueIsBusyAndLeavesNoRow(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 1)
	a := f.putBlob(f.acc, samplePNG(t, 32, 32))
	b := f.putBlob(f.acc, samplePNG(t, 33, 33))
	f.request(f.acc, a, 64)
	if _, err := f.pool.Request(f.ctx, f.acc, b, 64, nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("second request with a full queue = %v, want ErrBusy", err)
	}
	if d := f.state(f.acc, b, 64); d != nil {
		t.Errorf("a refused request left a row: %+v", d)
	}
}

func TestConverterChangeMakesNewRows(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	// A row from an older converter is not served and does not stop the thumbnail being made.
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		_, err := tx.InsertDerivedPending(f.ctx, &store.Derived{ID: id.New("drv"), AccountID: f.acc, BlobID: bl,
			Kind: Kind(64), Converter: "thumb-go-0", CreatedAt: time.Now()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if a := f.request(f.acc, bl, 64); a.State != store.DerivedPending || f.queued() != 1 {
		t.Fatalf("an old converter's row: state %s, queued %d", a.State, f.queued())
	}
}

func TestMissingFileIsQueuedAgain(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	f.request(f.acc, bl, 64)
	f.runQueued()
	ready := f.state(f.acc, bl, 64)
	if err := f.cache.Files().Remove(f.acc, ready.ID); err != nil {
		t.Fatal(err)
	}
	if a := f.request(f.acc, bl, 64); a.State != store.DerivedPending || f.queued() != 1 {
		t.Fatalf("a ready row with no file: state %s, queued %d", a.State, f.queued())
	}
}

func TestBudgetEvictsLeastRecentlyUsed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	var blobsIn []string
	for i := 0; i < 3; i++ {
		blobsIn = append(blobsIn, f.putBlob(f.acc, samplePNG(t, 64+i, 64)))
	}
	// last_used_at has millisecond resolution, so the steps are spaced: equal times would order by ID, and the test
	// would depend on random IDs.
	f.request(f.acc, blobsIn[0], 64)
	f.runQueued()
	one := f.state(f.acc, blobsIn[0], 64).Size
	time.Sleep(10 * time.Millisecond)
	f.request(f.acc, blobsIn[1], 64)
	f.runQueued()
	// The budget holds two thumbnails. Blob 0 is used last, so blob 1 is the least recently used.
	f.cache.maxBytes = 2*one + one/2
	time.Sleep(10 * time.Millisecond)
	f.cache.Use(f.acc, f.state(f.acc, blobsIn[0], 64).ID)
	if err := f.cache.Flush(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.request(f.acc, blobsIn[2], 64)
	f.runQueued()
	if d := f.state(f.acc, blobsIn[2], 64); d == nil || d.State != store.DerivedReady {
		t.Fatalf("the new thumbnail is not ready: %+v", d)
	}
	if d := f.state(f.acc, blobsIn[1], 64); d != nil {
		t.Errorf("the least recently used row was kept: %+v", d)
	}
	if d := f.state(f.acc, blobsIn[0], 64); d == nil {
		t.Errorf("the recently used row was evicted")
	}
}

func TestBudgetFullDropsTheFileAndQueuesAgain(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	f.cache.maxBytes = 1 // no thumbnail fits
	f.request(f.acc, bl, 64)
	f.runQueued()
	if d := f.state(f.acc, bl, 64); d != nil {
		t.Errorf("a refused thumbnail left a row: %+v", d)
	}
	// No file is left behind either.
	dir := filepath.Join(f.cache.Files().Root(), f.acc)
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("files left in %s: %d", dir, len(ents))
	}
	if a := f.request(f.acc, bl, 64); a.State != store.DerivedPending {
		t.Errorf("after the refusal: %s", a.State)
	}
}

func TestRemoveBlobRemovesRowsAndFiles(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	f.request(f.acc, bl, 64)
	f.runQueued()
	d := f.state(f.acc, bl, 64)
	path, _ := f.cache.Files().Path(f.acc, d.ID)
	if err := f.cache.RemoveBlob(f.ctx, f.acc, bl); err != nil {
		t.Fatal(err)
	}
	if f.state(f.acc, bl, 64) != nil {
		t.Error("the row survived its blob")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file survived its blob: %v", err)
	}
}

func TestRecoverClearsPendingWorkAndTemporaryFiles(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	f.request(f.acc, bl, 64) // pending, queued
	<-f.pool.q.ready         // the queue is lost with the process
	f.pool.q.pop()
	tmp, _ := f.cache.Files().TmpPath(id.New("drv"))
	if err := os.WriteFile(tmp, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := f.pool.Recover(f.ctx)
	if err != nil || n != 1 {
		t.Fatalf("Recover = %d, %v; want 1 pending row", n, err)
	}
	if d := f.state(f.acc, bl, 64); d != nil {
		t.Errorf("a pending row survived start-up: %+v", d)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a temporary file survived start-up")
	}
}

func TestTimeoutIsRecordedAsTimeout(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	f.pool.o.Timeout = time.Nanosecond
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	f.request(f.acc, bl, 64)
	f.runQueued()
	if d := f.state(f.acc, bl, 64); d == nil || d.State != store.DerivedFailed || d.Error != msgTimeout {
		t.Errorf("with a 1ns time limit: %+v", d)
	}
}

func TestOversizeSourceIsRefusedBeforeAChild(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	f.pool.o.MaxSourceBytes = 10
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	f.request(f.acc, bl, 64)
	f.runQueued()
	if d := f.state(f.acc, bl, 64); d == nil || d.Error != msgSourceSize {
		t.Errorf("an oversize source: %+v", d)
	}
}

func TestRefusedAdmissionQueuesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	refused := errors.New("rate limited")
	if _, err := f.pool.Request(f.ctx, f.acc, bl, 64, func() error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("Request = %v, want the admission error", err)
	}
	if d := f.state(f.acc, bl, 64); d != nil || f.queued() != 0 {
		t.Errorf("a refused admission left %+v and %d queued jobs", d, f.queued())
	}
	// A read that finds a row never asks for admission.
	f.request(f.acc, bl, 64)
	f.runQueued()
	asked := false
	if _, err := f.pool.Request(f.ctx, f.acc, bl, 64, func() error { asked = true; return refused }); err != nil || asked {
		t.Errorf("a ready thumbnail asked for admission (asked %v, err %v)", asked, err)
	}
}

func TestOutputOverLimitFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	f.pool.o.MaxOutputBytes = 16
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	f.request(f.acc, bl, 64)
	f.runQueued()
	if d := f.state(f.acc, bl, 64); d == nil || d.State != store.DerivedFailed || d.Error != msgOutput {
		t.Errorf("an output over its limit: %+v", d)
	}
	// No temporary file is left.
	tmps, _ := os.ReadDir(filepath.Join(f.cache.Files().Root(), "tmp"))
	if len(tmps) != 0 {
		t.Errorf("%d temporary files left", len(tmps))
	}
}

// TestChildStderrIsBounded: a child's stderr keeps its first bytes, and a write past the cap is still accepted in full,
// so the child is never blocked by its own diagnostics.
func TestChildStderrIsBounded(t *testing.T) {
	t.Parallel()
	c := &capBuf{max: 8}
	for i := 0; i < 1000; i++ {
		if n, err := c.Write([]byte("0123456789")); n != 10 || err != nil {
			t.Fatalf("Write = %d, %v; want 10, nil", n, err)
		}
	}
	if len(c.b) != 8 {
		t.Errorf("kept %d bytes, want 8", len(c.b))
	}
}
