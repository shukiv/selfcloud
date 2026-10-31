// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/office"
	"github.com/shukiv/selfcloud/internal/store"
)

// Converter makes a document into another format. office.Client is the one implementation; tests use a fake. size is the
// source's length, which the request is sent with.
type Converter interface {
	Convert(ctx context.Context, src io.Reader, size int64, ext, format string, dst io.Writer) error
}

// Failure messages of a conversion. Like the thumbnail messages they are the server's own words: none carries the
// converter's text, a file name or a path.
const (
	msgNotConvertible = "the converter does not convert this type"
	msgRefused        = "the converter refused the document"
	msgBadOutput      = "the converter answered with another type of file"
	msgUnavailable    = "the converter is unavailable"
	msgConvertTimeout = "the conversion took longer than the time limit"
	msgConvertOutput  = "the converted file is over the output limit"
	msgConvertDisk    = "the converted file could not be written"
)

// stickyFor is how long a sticky failure of a conversion stands before the work is queued again. A document that could
// not be converted may convert once the converter has been fixed, so the failure does not wait for the 30-day sweep.
const stickyFor = 24 * time.Hour

// perAccountConvert is the default share of the conversion queue one account may hold (decision 37). The total is
// ConvertOptions.Queue.
const perAccountConvert = 8

// ConvertOptions configure a ConvertPool.
type ConvertOptions struct {
	Store *store.Store
	Blobs blob.Store
	Cache *Cache
	Conv  Converter
	// Workers is how many conversions run at once; Queue is how many may wait, over all accounts; PerAccount is the most
	// of those places one account may hold. The conversion's own limits, not the thumbnail workers': a conversion waits on
	// the network, not the CPU.
	Workers    int
	Queue      int
	PerAccount int
	// Timeout is the longest one conversion may take. MaxSourceBytes refuses a source before it is sent;
	// MaxOutputBytes caps what Collabora may return.
	Timeout        time.Duration
	MaxSourceBytes int64
	MaxOutputBytes int64
	// MinFree is the free space the derived directory must keep after a write (storage.min_free).
	MinFree uint64
	Log     *slog.Logger
	Now     func() time.Time
}

// ConvertPool makes converted files on a fixed number of workers. It shares the cache, the budget and the rows with
// the thumbnail pool; only the work and its limits differ (design decision 37).
type ConvertPool struct {
	o   ConvertOptions
	q   *queue
	log *slog.Logger
	now func() time.Time
}

// NewConvertPool returns a ConvertPool. It starts nothing until Run.
func NewConvertPool(o ConvertOptions) *ConvertPool {
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
	if o.PerAccount <= 0 {
		o.PerAccount = min(perAccountConvert, o.Queue)
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &ConvertPool{o: o, q: newQueue(o.Queue, o.PerAccount), log: log, now: now}
}

// Request answers for a conversion of blobID to format, stored under kind and converter. It works as Pool.Request does:
// a ready file is returned, a file not yet made is queued once, a failed file waits for its retry time, and ErrBusy means
// the queue, or this account's share of it, is full with nothing written. ext is the source's extension, which Collabora
// is told the input is; the caller puts it in converter too, so copies with other extensions keep their own rows. guard is
// asked before the conversion starts and before the file is kept; a refusal drops the row and keeps nothing. admit is asked
// before anything is queued.
func (p *ConvertPool) Request(ctx context.Context, accountID, blobID, kind, format, ext, converter string,
	guard func(context.Context) error, admit func() error) (Answer, error) {
	c := p.o.Cache
	d, err := c.Lookup(ctx, accountID, blobID, kind, converter)
	if err != nil {
		return Answer{}, err
	}
	var missing, retry bool
	if d != nil {
		switch d.State {
		case store.DerivedReady:
			if p.fileExists(accountID, d.ID) {
				return answerFor(d), nil
			}
			p.log.Warn("converted file missing; making it again", "derived", d.ID, "account", accountID)
			missing = true
		case store.DerivedFailed:
			if d.RetryAt.IsZero() || p.now().Before(d.RetryAt) {
				return answerFor(d), nil
			}
			retry = true
		default:
			return answerFor(d), nil
		}
	}
	if admit != nil {
		if err := admit(); err != nil {
			return Answer{}, err
		}
	}
	if !p.q.reserve(accountID) {
		return Answer{}, ErrBusy
	}
	row := &store.Derived{ID: id.New("drv"), AccountID: accountID, BlobID: blobID, Kind: kind,
		Converter: converter, CreatedAt: time.Now()}
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
		again, err := c.Lookup(ctx, accountID, blobID, kind, converter)
		if err != nil {
			return Answer{}, err
		}
		if again == nil {
			return Answer{}, ErrNoBlob
		}
		return answerFor(again), nil
	}
	p.q.push(accountID, job{accountID: accountID, blobID: blobID, derivedID: row.ID, format: format, ext: ext,
		guard: guard})
	return Answer{State: store.DerivedPending, ID: row.ID}, nil
}

// Peek answers for a row that exists and queues nothing. A HEAD request uses it.
func (p *ConvertPool) Peek(ctx context.Context, accountID, blobID, kind, converter string) (Answer, error) {
	d, err := p.o.Cache.Lookup(ctx, accountID, blobID, kind, converter)
	if err != nil || d == nil {
		return Answer{}, err
	}
	if d.State == store.DerivedReady && !p.fileExists(accountID, d.ID) {
		return Answer{}, nil
	}
	return answerFor(d), nil
}

// Open opens a ready file for reading.
func (p *ConvertPool) Open(accountID, derivedID string) (*os.File, error) {
	path, err := p.o.Cache.Files().Path(accountID, derivedID)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (p *ConvertPool) fileExists(accountID, derivedID string) bool {
	path, err := p.o.Cache.Files().Path(accountID, derivedID)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Cache returns the cache of rows and files the pool shares with the thumbnails.
func (p *ConvertPool) Cache() *Cache { return p.o.Cache }

// Run makes files until ctx is cancelled. The thumbnail pool writes the recorded uses, so this one does not.
func (p *ConvertPool) Run(ctx context.Context) {
	done := make(chan struct{}, p.o.Workers)
	for range p.o.Workers {
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				select {
				case <-ctx.Done():
					return
				case <-p.q.ready:
					if j, ok := p.q.pop(); ok {
						p.safeProcess(ctx, j)
					}
				}
			}
		}()
	}
	for range p.o.Workers {
		<-done
	}
}

// safeProcess runs one conversion. A panic is a bug in this code: the row is dropped, so a later request queues it again.
func (p *ConvertPool) safeProcess(ctx context.Context, j job) {
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("conversion panicked", "derived", j.derivedID)
			dropRow(context.WithoutCancel(ctx), p.o.Store, p.log, j)
		}
	}()
	p.process(ctx, j)
}

// failure classes of a conversion (decision 37). A sticky failure is the document's own: it is retried after stickyFor.
// A counted failure is one the server's or the host's own time or disk made: it is retried with backoff, and gives up to a
// sticky failure after maxAttempts. An outage of the converter counts for nothing: it is retried after one backoff, and
// the attempts stay where they were.
type failClass int

const (
	classSticky failClass = iota
	classCounted
	classOutage
)

// failConvert records the outcome of a conversion job by its class. Once the context is cancelled nothing is recorded.
func (p *ConvertPool) failConvert(ctx context.Context, j job, reason string, class failClass) {
	if ctx.Err() != nil {
		return
	}
	at := p.now()
	row, err := p.o.Store.DerivedByID(ctx, j.accountID, j.derivedID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		p.log.Error("cannot read a derived row", "error", err, "derived", j.derivedID)
		return
	}
	attempts := row.Attempts
	var retryAt time.Time
	switch class {
	case classSticky:
		attempts++
		retryAt = at.Add(stickyFor)
	case classCounted:
		attempts++
		if attempts < maxAttempts {
			retryAt = at.Add(retryBackoff[attempts-1])
		} else {
			retryAt = at.Add(stickyFor)
			reason = fmt.Sprintf("%s (gave up after %d attempts; tried again after a day)", reason, attempts)
		}
	case classOutage:
		retryAt = at.Add(retryBackoff[0])
	}
	err = p.o.Store.Write(ctx, func(tx *store.Tx) error {
		_, err := tx.FailDerived(ctx, j.accountID, j.derivedID, reason, at, attempts, retryAt)
		return err
	})
	if err != nil {
		p.log.Error("cannot record a failed derived file", "error", err)
	}
}

// process converts one file and records the result.
func (p *ConvertPool) process(ctx context.Context, j job) {
	if j.guard != nil {
		if err := j.guard(ctx); err != nil {
			p.log.Info("conversion dropped: its caller no longer has access", "derived", j.derivedID)
			dropRow(ctx, p.o.Store, p.log, j)
			return
		}
	}
	if _, ok := office.ContentType(j.format); !ok || !validConvertExt(j.ext) {
		p.failConvert(ctx, j, msgNotConvertible, classSticky)
		return
	}
	src, err := p.o.Blobs.Open(j.accountID, j.blobID)
	if err != nil {
		dropRow(ctx, p.o.Store, p.log, j)
		return
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		dropRow(ctx, p.o.Store, p.log, j)
		return
	}
	if fi.Size() > p.o.MaxSourceBytes {
		p.failConvert(ctx, j, msgSourceSize, classSticky)
		return
	}
	tmp, err := p.o.Cache.Files().TmpPath(j.derivedID)
	if err != nil {
		dropRow(ctx, p.o.Store, p.log, j)
		return
	}
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		p.log.Error("cannot create a converted temporary file", "error", err)
		dropRow(ctx, p.o.Store, p.log, j)
		return
	}
	cw := &capWriter{w: out, max: p.o.MaxOutputBytes, hash: sha256.New()}
	cctx, cancel := context.WithTimeout(ctx, p.o.Timeout)
	err = p.o.Conv.Convert(cctx, src, fi.Size(), j.ext, j.format, cw)
	cancel()
	syncErr := out.Sync()
	closeErr := out.Close()
	discard := func() { discardTemp(p.log, tmp) }
	switch {
	case ctx.Err() != nil:
		// Shutdown: nothing is recorded, and the next start clears the row.
		discard()
		dropRow(ctx, p.o.Store, p.log, j)
		return
	case cw.overflow:
		discard()
		p.failConvert(ctx, j, msgConvertOutput, classSticky)
		return
	case cw.werr != nil || syncErr != nil || closeErr != nil:
		discard()
		p.log.Error("cannot write a converted file", "error", firstErr(cw.werr, syncErr, closeErr))
		p.failConvert(ctx, j, msgConvertDisk, classCounted)
		return
	case err == nil:
	case errors.Is(err, office.ErrUnsupported):
		discard()
		p.failConvert(ctx, j, msgNotConvertible, classSticky)
		return
	case errors.Is(err, office.ErrRefused):
		discard()
		p.failConvert(ctx, j, msgRefused, classSticky)
		return
	case errors.Is(err, office.ErrBadOutput):
		discard()
		p.failConvert(ctx, j, msgBadOutput, classSticky)
		return
	case errors.Is(err, office.ErrTooLarge):
		discard()
		p.failConvert(ctx, j, msgConvertOutput, classSticky)
		return
	case errors.Is(err, context.DeadlineExceeded):
		discard()
		p.failConvert(ctx, j, msgConvertTimeout, classCounted)
		return
	case errors.Is(err, office.ErrUnavailable):
		discard()
		p.failConvert(ctx, j, msgUnavailable, classOutage)
		return
	default:
		discard()
		p.log.Error("conversion failed", "error", err)
		p.failConvert(ctx, j, msgUnavailable, classOutage)
		return
	}
	// The output must fit the budget at all. One that does not is refused before anything is evicted: eviction would
	// otherwise empty the derived files of every account for a file that can never be kept (decision 37).
	if cw.n > p.o.Cache.maxBytes {
		discard()
		p.failConvert(ctx, j, msgConvertOutput, classSticky)
		return
	}
	// The caller is asked again before the file is kept, so a revoke during the conversion keeps nothing.
	if j.guard != nil {
		if err := j.guard(ctx); err != nil {
			discard()
			dropRow(ctx, p.o.Store, p.log, j)
			return
		}
	}
	p.finish(ctx, j, tmp, cw.n, hex.EncodeToString(cw.hash.Sum(nil)))
}

// finish moves a converted file into place and records it ready, under the same budget rule as a thumbnail.
func (p *ConvertPool) finish(ctx context.Context, j job, tmp string, size int64, sha string) {
	switch p.o.Cache.Install(ctx, j.accountID, j.derivedID, tmp, size, sha, p.o.MinFree) {
	case installNoRoom:
		p.failConvert(ctx, j, msgConvertDisk, classCounted)
	case installDropped:
		dropRow(ctx, p.o.Store, p.log, j)
	}
}

// validConvertExt is the extension check the client makes too, repeated here so a bad job never reaches the client.
func validConvertExt(ext string) bool {
	if ext == "" || len(ext) > 8 {
		return false
	}
	for i := 0; i < len(ext); i++ {
		c := ext[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// ConverterKey returns the converter key a conversion pool stores its rows under: the build of Collabora (its
// capabilities) and the limits that decide a refusal. The time limit is not in it, as for thumbnails: a time-out is
// retried, not kept.
func ConverterKey(caps office.Caps, maxSource, maxOutput int64) string {
	limits := "src=" + strconv.FormatInt(maxSource, 10) + " out=" + strconv.FormatInt(maxOutput, 10)
	return office.ConverterKey(caps, limits)
}
