// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/thumb"
)

// ErrBusy is returned when the queue is full, or the account's share of it is. The caller answers with a retry-later.
var ErrBusy = errors.New("derived: the queue is full")

// ErrNoBlob is returned when the blob is not one of the account's, or does not exist.
var ErrNoBlob = errors.New("derived: no such blob in the account")

// Failure messages. They are the server's own words, stored on the row and shown to no one but the owner.
const (
	msgCorrupt     = "the image is corrupt or does not match its header"
	msgTooLarge    = "the image or its thumbnail is over a size limit"
	msgUnsupported = "not a JPEG, PNG or GIF"
	msgSourceSize  = "the source is over the size limit"
	msgStopped     = "the decoder was stopped by a signal"
	msgTimeout     = "the decode took longer than the time limit"
	msgOutput      = "the thumbnail is over the output limit"
	msgOutOfMemory = "the decoder ran out of memory"
	msgDisk        = "the derived file could not be written"
	msgFailed      = "the decoder failed"
)

// perAccountQueue is how many jobs one account may have waiting. The total is Options.Queue.
const perAccountQueue = 32

// maxAttempts is how many times a failure of the server's or the host's own making is tried before it becomes
// sticky. retryBackoff gives the wait after each failure but the last: attempt n waits retryBackoff[n-1].
const maxAttempts = 3

var retryBackoff = []time.Duration{time.Minute, 10 * time.Minute}

// Options configure a Pool.
type Options struct {
	Store *store.Store
	Blobs blob.Store
	Cache *Cache
	// Exe is the binary that runs the child: the server's own, which runs the thumb subcommand.
	Exe     string
	Workers int
	// Queue is how many jobs may wait, over all accounts. A request past it gets ErrBusy.
	Queue   int
	Timeout time.Duration
	// CPUSeconds, MemoryBytes, MaxPixels and MaxInput are the child's limits (thumb.Limits).
	CPUSeconds  int64
	MemoryBytes int64
	MaxPixels   int64
	// MaxSourceBytes refuses a source over the size before a child starts; MaxOutputBytes caps what the child
	// may write.
	MaxSourceBytes int64
	MaxOutputBytes int64
	// MinFree is the free space the derived directory must keep after a write (storage.min_free).
	MinFree uint64
	Log     *slog.Logger
	// Now is the clock. Nil means time.Now; tests set it.
	Now func() time.Time
}

// Pool makes derived files on a fixed number of workers, one child process per file.
type Pool struct {
	o         Options
	q         *queue
	log       *slog.Logger
	converter string
	now       func() time.Time
}

// job is one file to make. The row is the lock: a request inserts it before the job is queued.
type job struct {
	accountID string
	blobID    string
	derivedID string
	size      int
	// format, ext and guard are set by a conversion (convert.go) and are empty for a thumbnail.
	format string
	ext    string
	guard  func(context.Context) error
}

// NewPool returns a Pool. It starts nothing until Run.
func NewPool(o Options) *Pool {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if o.Workers <= 0 {
		o.Workers = 1
	}
	if o.Queue <= 0 {
		o.Queue = 1
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Pool{o: o, q: newQueue(o.Queue, perAccountQueue), log: log, converter: converterFor(o), now: now}
}

// converterFor is the converter key of the rows this pool makes. It names the encoder version and the limits that
// decide a refusal, so raising a limit makes new rows, and a refusal under the old limit is not kept for the new one.
// Timeout is not in it: a time limit's refusal is a server-side failure that is retried, not sticky.
func converterFor(o Options) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "px=%d src=%d mem=%d cpu=%d out=%d",
		o.MaxPixels, o.MaxSourceBytes, o.MemoryBytes, o.CPUSeconds, o.MaxOutputBytes))
	return EncoderVersion + "+" + hex.EncodeToString(sum[:6])
}

// Converter returns the converter key of the rows this pool makes. Readers of the rows must use it.
func (p *Pool) Converter() string { return p.converter }

// Answer is what a request learns about a derived file.
type Answer struct {
	State string         // store.DerivedReady, store.DerivedPending or store.DerivedFailed; "" when there is no row
	Row   *store.Derived // set when ready
	ID    string         // the row's ID, set for a conversion's answer (convert.go)
}

func answerFor(d *store.Derived) Answer {
	switch d.State {
	case store.DerivedReady:
		return Answer{State: store.DerivedReady, Row: d, ID: d.ID}
	case store.DerivedFailed:
		return Answer{State: store.DerivedFailed, Row: d, ID: d.ID}
	}
	return Answer{State: store.DerivedPending, ID: d.ID}
}

// Transient reports whether a failed row is one the caller should ask for again later: an outage, or a time-out that is
// counted. It returns the time the row may be queued again. A refusal is not transient, though it also has a retry time
// (a day on), so the row's error is what tells them apart.
func Transient(d *store.Derived) (time.Time, bool) {
	if d == nil || d.State != store.DerivedFailed || d.RetryAt.IsZero() {
		return time.Time{}, false
	}
	if d.Error == msgUnavailable || d.Error == msgConvertTimeout {
		return d.RetryAt, true
	}
	return time.Time{}, false
}

// Request answers for a thumbnail of blobID at size. A ready file is returned as it is. A file not yet made is queued,
// once: the row is inserted, and a second request finds it pending. A failed file stays failed until its retry time,
// when the next request queues it again. ErrBusy means the queue, or this account's share of it, is full, and nothing
// was written. admit, when set, is asked before anything is queued, so a caller can limit how much work one principal
// starts; reads that find a row never ask it.
func (p *Pool) Request(ctx context.Context, accountID, blobID string, size int, admit func() error) (Answer, error) {
	kind := Kind(size)
	c := p.o.Cache
	d, err := c.Lookup(ctx, accountID, blobID, kind, p.converter)
	if err != nil {
		return Answer{}, err
	}
	var missing, retry bool
	if d != nil {
		switch d.State {
		case store.DerivedReady:
			if p.fileExists(accountID, d.ID) {
				return Answer{State: store.DerivedReady, Row: d}, nil
			}
			// The file is gone, though the row says ready: a crash between the two removals, or a lost file.
			// The row is replaced, and the thumbnail is queued again below.
			p.log.Warn("derived file missing; queueing it again", "derived", d.ID, "account", accountID)
			missing = true
		case store.DerivedFailed:
			if d.RetryAt.IsZero() || p.now().Before(d.RetryAt) {
				return Answer{State: store.DerivedFailed, Row: d, ID: d.ID}, nil
			}
			retry = true
		default:
			return Answer{State: store.DerivedPending}, nil
		}
	}
	if admit != nil {
		if err := admit(); err != nil {
			return Answer{}, err
		}
	}
	// The place is reserved before the writer lock is taken, so a full queue costs no write, and nothing is
	// written that would have to be undone.
	if !p.q.reserve(accountID) {
		return Answer{}, ErrBusy
	}
	row := &store.Derived{ID: id.New("drv"), AccountID: accountID, BlobID: blobID, Kind: kind,
		Converter: p.converter, CreatedAt: time.Now()}
	if retry {
		row.ID = d.ID
	}
	var inserted bool
	err = p.o.Store.Write(ctx, func(tx *store.Tx) error {
		switch {
		case retry:
			var err error
			inserted, err = tx.RetryDerived(ctx, accountID, d.ID, p.now())
			return err
		case missing:
			if _, err := tx.DeleteDerivedRow(ctx, accountID, d.ID); err != nil {
				return err
			}
		}
		var err error
		inserted, err = tx.InsertDerivedPending(ctx, row)
		return err
	})
	if err != nil {
		p.q.release(accountID)
		return Answer{}, err
	}
	if !inserted {
		p.q.release(accountID)
		// Either another request queued it a moment ago, or the blob is not the account's. The lookup tells them apart.
		again, err := c.Lookup(ctx, accountID, blobID, kind, p.converter)
		if err != nil {
			return Answer{}, err
		}
		if again == nil {
			return Answer{}, ErrNoBlob
		}
		return answerFor(again), nil
	}
	p.q.push(accountID, job{accountID: accountID, blobID: blobID, derivedID: row.ID, size: size})
	return Answer{State: store.DerivedPending}, nil
}

// Peek answers as Request does for a row that exists, and queues nothing. A ready file whose file is gone is reported
// as no row. It is what a HEAD request uses: a HEAD never starts work.
func (p *Pool) Peek(ctx context.Context, accountID, blobID string, size int) (Answer, error) {
	d, err := p.o.Cache.Lookup(ctx, accountID, blobID, Kind(size), p.converter)
	if err != nil || d == nil {
		return Answer{}, err
	}
	if d.State == store.DerivedReady && !p.fileExists(accountID, d.ID) {
		return Answer{}, nil
	}
	return answerFor(d), nil
}

// Cache returns the pool's cache of rows and files.
func (p *Pool) Cache() *Cache { return p.o.Cache }

// Open opens a ready file for reading.
func (p *Pool) Open(accountID, derivedID string) (*os.File, error) {
	path, err := p.o.Cache.Files().Path(accountID, derivedID)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (p *Pool) fileExists(accountID, derivedID string) bool {
	path, err := p.o.Cache.Files().Path(accountID, derivedID)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Run makes files until ctx is cancelled. It also writes the recorded uses, and a final flush on exit.
func (p *Pool) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for range p.o.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case <-p.q.ready:
					// A token means a job was pushed, and only a pop takes one, so the pop finds it.
					if j, ok := p.q.pop(); ok {
						p.safeProcess(ctx, j)
					}
				}
			}
		}()
	}
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-t.C:
			if err := p.o.Cache.Flush(context.WithoutCancel(ctx)); err != nil {
				p.log.Error("cannot record derived file uses", "error", err)
			}
		}
	}
	wg.Wait()
	if err := p.o.Cache.Flush(context.WithoutCancel(ctx)); err != nil {
		p.log.Error("cannot record derived file uses", "error", err)
	}
}

// flushEvery is how often the recorded uses are written.
const flushEvery = 10 * time.Second

// safeProcess runs one job. A panic in the parent is a bug in this code, and it must not stop the pool: the job is
// dropped, so a later request queues it again.
func (p *Pool) safeProcess(ctx context.Context, j job) {
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("derived job panicked", "panic", fmt.Sprint(r), "derived", j.derivedID)
			p.drop(context.WithoutCancel(ctx), j)
		}
	}()
	p.process(ctx, j)
}

// process makes one derived file and records the result.
func (p *Pool) process(ctx context.Context, j job) {
	src, err := p.o.Blobs.Open(j.accountID, j.blobID)
	if err != nil {
		// The blob is gone, so nothing can be made: the row goes with it.
		p.drop(ctx, j)
		return
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		p.drop(ctx, j)
		return
	}
	if fi.Size() > p.o.MaxSourceBytes {
		p.fail(ctx, j, msgSourceSize)
		return
	}
	tmp, err := p.o.Cache.Files().TmpPath(j.derivedID)
	if err != nil {
		p.drop(ctx, j)
		return
	}
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		p.log.Error("cannot create a derived temporary file", "error", err)
		p.drop(ctx, j)
		return
	}
	cw := &capWriter{w: out, max: p.o.MaxOutputBytes, hash: sha256.New()}
	code, err := p.runChild(ctx, src, cw, &capBuf{max: stderrKeep}, j.size)
	syncErr := out.Sync()
	closeErr := out.Close()
	if err == nil && (syncErr != nil || closeErr != nil) {
		err = errors.Join(syncErr, closeErr)
	}
	// From here, the temporary file is either finished or removed.
	switch {
	case ctx.Err() != nil:
		// Shutdown: the job is dropped and nothing is recorded, so the next start tries it again.
		p.discard(tmp)
		p.drop(ctx, j)
		return
	case cw.overflow:
		p.discard(tmp)
		p.fail(ctx, j, msgOutput)
		return
	case cw.werr != nil || syncErr != nil || closeErr != nil:
		// The disk refused a write or the flush: a host condition, retried later.
		p.discard(tmp)
		p.log.Error("cannot write a derived file", "error", firstErr(cw.werr, syncErr, closeErr))
		p.transient(ctx, j, msgDisk)
		return
	case err != nil:
		// The child could not be started: a server fault, not the image's. Nothing is recorded.
		p.discard(tmp)
		p.log.Error("cannot run the thumbnail decoder", "error", err)
		p.drop(ctx, j)
		return
	}
	if code != thumb.ExitOK {
		p.discard(tmp)
		p.exitFailure(ctx, j, code)
		return
	}
	p.finish(ctx, j, tmp, cw.n, hex.EncodeToString(cw.hash.Sum(nil)))
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// exitOutOfMemory is the exit status of the Go runtime's fatal error: an allocation over the child's address-space
// limit, or any other crash of the runtime. The runtime exits with 2.
const exitOutOfMemory = 2

// exitFailure records the outcome of a child that did not finish. A refusal that is the image's own (corrupt, over a
// cap, unsupported) is sticky for its key. A limit that could not be set is a server fault: the row is dropped and
// a later request tries again. Every other outcome is a condition of the host or the run (a signal, a time limit, out
// of memory, a crash): it is retried after a wait, up to maxAttempts.
func (p *Pool) exitFailure(ctx context.Context, j job, code int) {
	switch code {
	case thumb.ExitLimits, thumb.ExitUsage:
		p.log.Error("the thumbnail decoder could not start with its limits", "exit", code)
		p.drop(ctx, j)
	case thumb.ExitCorrupt:
		p.logRefusal(code, j)
		p.fail(ctx, j, msgCorrupt)
	case thumb.ExitTooLarge:
		p.logRefusal(code, j)
		p.fail(ctx, j, msgTooLarge)
	case thumb.ExitUnsupported:
		p.logRefusal(code, j)
		p.fail(ctx, j, msgUnsupported)
	case exitTimeout:
		p.transient(ctx, j, msgTimeout)
	case -1:
		p.transient(ctx, j, msgStopped)
	case exitOutOfMemory:
		p.transient(ctx, j, msgOutOfMemory)
	default:
		p.transient(ctx, j, msgFailed)
	}
}

// logRefusal records a refusal of the image. It is information, not a warning: a refusal is what the server is for,
// and it is recorded once for the key.
func (p *Pool) logRefusal(code int, j job) {
	p.log.Info("thumbnail decoder refused a source", "exit", code, "derived", j.derivedID)
}

// finish moves a finished file into place and records it ready, if the budget and the disk allow.
func (p *Pool) finish(ctx context.Context, j job, tmp string, size int64, sha string) {
	switch p.o.Cache.Install(ctx, j.accountID, j.derivedID, tmp, size, sha, p.o.MinFree) {
	case installNoRoom:
		p.transient(ctx, j, msgDisk)
	case installDropped:
		p.drop(ctx, j)
	}
}

// fail records a job as failed for good: the image's own refusal, which is sticky for its key.
func (p *Pool) fail(ctx context.Context, j job, reason string) {
	p.record(ctx, j, reason, false)
}

// transient records a failure of the server's or the host's own making. The row is failed with a retry time, so a
// later request queues it again, until maxAttempts failures; then it is sticky under the same key.
func (p *Pool) transient(ctx context.Context, j job, reason string) {
	p.record(ctx, j, reason, true)
}

// record writes the outcome of a job. Once the context is cancelled, nothing is recorded: a shutdown is not an
// outcome of the image, and the row is left for the next start to clear.
func (p *Pool) record(ctx context.Context, j job, reason string, transient bool) {
	failRow(ctx, p.o.Store, p.now, p.log, j, reason, transient)
}

// drop removes a pending row, so a later request queues the work again.
func (p *Pool) drop(ctx context.Context, j job) {
	dropRow(ctx, p.o.Store, p.log, j)
}

func (p *Pool) discard(tmp string) {
	discardTemp(p.log, tmp)
}

func (p *Pool) discardFinal(final string) {
	discardFinal(p.log, final)
}

// runChild runs `exe thumb` with the source on its stdin and returns its exit code. An exit by signal is -1. A
// non-nil error means the child did not run to an exit at all: it could not start, or its output failed.
func (p *Pool) runChild(ctx context.Context, src *os.File, out, stderr io.Writer, size int) (int, error) {
	tctx, cancel := context.WithTimeout(ctx, p.o.Timeout)
	defer cancel()
	args := []string{"thumb",
		"-size=" + strconv.Itoa(size),
		"-max-pixels=" + strconv.FormatInt(p.o.MaxPixels, 10),
		"-max-input=" + strconv.FormatInt(p.o.MaxSourceBytes, 10),
		"-memory=" + strconv.FormatInt(p.o.MemoryBytes, 10),
		"-cpu=" + strconv.FormatInt(p.o.CPUSeconds, 10),
	}
	cmd := exec.CommandContext(tctx, p.o.Exe, args...)
	cmd.Env = []string{} // the child inherits nothing: no secret, no path, no locale
	cmd.Dir = "/"        // and no working directory: a core or a stray file has nowhere to go
	cmd.Stdin = src      // the file descriptor itself, not a copy
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.WaitDelay = 5 * time.Second
	cmd.SysProcAttr = childAttr()
	err := cmd.Run()
	if err == nil {
		return thumb.ExitOK, nil
	}
	// A failure with the time limit passed is the source taking too long, even when the limit passed before the
	// child started: it is not a fault of the server.
	if tctx.Err() != nil && ctx.Err() == nil {
		return exitTimeout, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, err
}

// exitTimeout stands for a child stopped by the time limit. It is not an exit code: children exit with
// 0..255, and -1 is a signal.
const exitTimeout = -2

// capWriter counts and hashes what the child writes, and refuses past max. A write the file refuses is kept in werr,
// so it can be told from the child's own exit.
type capWriter struct {
	w        io.Writer
	max      int64
	n        int64
	hash     hash.Hash
	overflow bool
	werr     error
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.n+int64(len(p)) > c.max {
		c.overflow = true
		return 0, errors.New("derived: output over the limit")
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	c.hash.Write(p[:n])
	if err != nil && c.werr == nil {
		c.werr = err
	}
	return n, err
}

// stderrKeep is how much of a child's stderr is kept. A decoder that crashes can print a long trace, and nothing
// reads the text: the exit code decides the outcome. The rest is discarded, so the child is never blocked.
const stderrKeep = 4 << 10

// capBuf keeps the first max bytes written to it and discards the rest.
type capBuf struct {
	b   []byte
	max int
}

func (c *capBuf) Write(p []byte) (int, error) {
	if room := c.max - len(c.b); room > 0 {
		c.b = append(c.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// Recover is the start-up step of the pool: it clears the work the last process left.
func (p *Pool) Recover(ctx context.Context) (int64, error) {
	return p.o.Cache.Recover(ctx)
}
