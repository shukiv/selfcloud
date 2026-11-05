// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/office"
	"github.com/shukiv/selfcloud/internal/store"
)

// The content worker (slice q3, decision 46). It is one goroutine that does one bounded step at a time: a batch of the
// backfill, or the release of the due retries, or one pending row. A row's text is read or converted outside any
// transaction, and its result is written in one short write (store.FinishText). It does not use the jobs runner: a
// conversion waits on the network for up to the converter's time limit, and one job turn must not wait that long
// (decision 37 keeps conversions off the runner for the same reason).

// Converter makes a document into plain text, as office.Client does, on the content search's own slot (ConvertBulk), and
// says which build it is (Capabilities, for the key of a refusal).
type Converter interface {
	ConvertBulk(ctx context.Context, src io.Reader, size int64, ext, format string, dst io.Writer) error
	Capabilities(ctx context.Context) (office.Caps, error)
}

// Blobs opens the stored bytes of a blob. blob.Dir has this method.
type Blobs interface {
	Open(accountID, blobID string) (*os.File, error)
}

// WorkerOptions configure a Worker.
type WorkerOptions struct {
	Store *store.Store
	Blobs Blobs
	// Conv is the converter, or nil while office.enabled is off. A document then gets a skipped row, and the row is
	// released when the converter is turned on (store.ReleaseTextSkipped).
	Conv Converter
	// OfficeText is search.office_text: whether documents are sent to the converter at all (review S4). It is off by
	// default: an operator who runs a converter for previews has not agreed to send every tenant's documents to it.
	OfficeText bool
	// TextMax is search.text_max_bytes: the text kept per file. FullTextMax is search.fulltext_max_bytes: the largest
	// source read, plain or converted.
	TextMax, FullTextMax int64
	// CapAccount and CapTotal are search.max_text_per_account and search.max_text_total: the stored text one account and
	// the installation may hold, 0 for none (decision 46).
	CapAccount, CapTotal int64
	// Idle is how long the worker waits when it has nothing to do.
	Idle time.Duration
	Log  *slog.Logger
	Now  func() time.Time
}

// Worker extracts the text of pending content rows.
type Worker struct {
	o    WorkerOptions
	last string // the account served last: the next claim is the next account after it

	// pauseUntil is when the worker may next try the converter, after it could not reach it. pauseWait doubles on each
	// failure in a row, from outageWait to outageMax, and resets on a conversion that gets through. The next conversion
	// after the pause is the health check: one request, and no row is counted against it.
	pauseUntil time.Time
	pauseWait  time.Duration
}

// backfillBatch is how many blobs one backfill step queues: one short write.
const backfillBatch = 1000

// Waits and limits. An unreachable converter pauses the worker for a growing wait, and no row is counted. A conversion
// that fails while it is in flight is counted: it waits a minute, then ten, and after timeoutAttempts tries it is sticky
// until the converter's key changes. A time-out is counted the same way and, after its last try, sticky for a day (the
// classes of decision 37). A refusal is sticky until the key changes.
const (
	outageWait      = time.Minute
	outageMax       = 5 * time.Minute
	inflightWaitOne = time.Minute
	inflightWaitTwo = 10 * time.Minute
	timeoutWaitOne  = time.Minute
	timeoutWaitTwo  = 10 * time.Minute
	failedAttempts  = 3
	// parkWait is how long a row whose file is in the trash waits before it is looked at again.
	parkWait = time.Hour
)

// Fixed messages of a row. None carries a converter's text, a file name or a path.
const (
	msgTooLarge    = "the file is over the size limit"
	msgNotText     = "the file is not text"
	msgOLEUnknown  = "the type of the old office file could not be told"
	msgUnreadable  = "the file could not be read"
	msgRefused     = "the converter refused the document"
	msgBadOutput   = "the converter answered with another type of file"
	msgUnavailable = "the converter failed while it had the document"
	msgTimeout     = "the conversion took longer than the time limit"
)

// NewWorker returns a worker with its options checked. The limits must be positive.
func NewWorker(o WorkerOptions) *Worker {
	if o.Idle <= 0 {
		o.Idle = 2 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Worker{o: o}
}

// Run does steps until ctx is done. A step that fails is logged, and the worker waits before it tries again.
func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		did, err := w.Step(ctx)
		if err != nil && ctx.Err() == nil {
			w.o.Log.Error("content search step failed", "error", err)
		}
		if err != nil || !did {
			select {
			case <-ctx.Done():
				return
			case <-time.After(w.o.Idle):
			}
		}
	}
}

// Step does one unit of work and reports whether it did any. The order is: the backfill first, until it is done, then
// one pending row. The retries are released when no row is due, so the failed rows wait without a write per step. While
// the converter is paused (unreachable), a step does nothing: no row is claimed and none is written.
func (w *Worker) Step(ctx context.Context) (bool, error) {
	if w.o.Now().Before(w.pauseUntil) {
		return false, nil
	}
	done, err := w.o.Store.TextBackfillDone(ctx)
	if err != nil {
		return false, err
	}
	if !done {
		_, err := w.o.Store.TextBackfillStep(ctx, backfillBatch)
		return true, err
	}
	job, ok, err := w.o.Store.ClaimText(ctx, w.last, w.o.Now())
	if err != nil {
		return false, err
	}
	if !ok {
		released, err := w.release(ctx)
		if err != nil || !released {
			return false, err
		}
		if job, ok, err = w.o.Store.ClaimText(ctx, w.last, w.o.Now()); err != nil || !ok {
			return false, err
		}
	}
	w.last = job.AccountID
	return true, w.process(ctx, job)
}

// release makes due rows pending again: the timed retries, and, when the converter's key has changed, its refusals. It
// reports whether any row became pending.
func (w *Worker) release(ctx context.Context) (bool, error) {
	n, err := w.o.Store.ReleaseTextRetries(ctx, w.o.Now())
	if err != nil {
		return false, err
	}
	if n == 0 && w.o.Conv != nil {
		if key, err := w.key(ctx); err == nil {
			if n, err = w.o.Store.ReleaseTextKey(ctx, key); err != nil {
				return false, err
			}
		}
	}
	// Capped rows wait for room; the release is in bounded batches, and it is the sweep's job (ReleaseTextCapped).
	return n > 0, nil
}

// key is the converter key of this build (office.ConverterKey), which a refusal is sticky under.
func (w *Worker) key(ctx context.Context) (string, error) {
	caps, err := w.o.Conv.Capabilities(ctx)
	if err != nil {
		return "", err
	}
	return office.ConverterKey(caps, limitsKey(w.o.TextMax, w.o.FullTextMax)), nil
}

// limitsKey names the limits that decide a refusal, for the converter key.
func limitsKey(textMax, fullMax int64) string {
	return strconv.FormatInt(textMax, 10) + "/" + strconv.FormatInt(fullMax, 10)
}

// process extracts one row and records what it found.
func (w *Worker) process(ctx context.Context, j store.TextJob) error {
	res := store.TextResult{ID: j.ID, AccountID: j.AccountID, BlobID: j.BlobID, Attempts: j.Attempts,
		CapAccount: w.o.CapAccount, CapTotal: w.o.CapTotal}
	paused := w.extract(ctx, j, &res)
	if ctx.Err() != nil {
		return nil // stopping: the row stays pending, and the next start takes it again
	}
	if paused {
		return nil // the converter could not be reached: the row is not written, and no attempt is counted
	}
	return w.o.Store.FinishText(ctx, res)
}

// extract decides the row's result. It sets res. It returns true when the converter could not be reached, which pauses the
// worker and leaves the row as it was.
func (w *Worker) extract(ctx context.Context, j store.TextJob, res *store.TextResult) (paused bool) {
	live, err := w.o.Store.TextLive(ctx, j.AccountID, j.BlobID)
	if err != nil {
		w.transient(res, msgUnreadable, outageWait, "")
		return false
	}
	if !live {
		// The file is in the trash (or under one): nothing is read, and nothing is sent. It is looked at again later.
		w.park(res)
		return false
	}
	if j.Size > w.o.FullTextMax {
		skip(res, msgTooLarge)
		return false
	}
	f, err := w.o.Blobs.Open(j.AccountID, j.BlobID)
	if err != nil {
		w.transient(res, msgUnreadable, outageWait, "")
		return false
	}
	defer f.Close()
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		w.transient(res, msgUnreadable, outageWait, "")
		return false
	}
	head = head[:n]
	switch fam := SniffFamily(head); fam {
	case FamilyBinary:
		skip(res, msgNotText)
	case FamilyText:
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			w.transient(res, msgUnreadable, outageWait, "")
			return false
		}
		if ok, reason, err := w.room(ctx, j.AccountID); err != nil {
			w.transient(res, msgUnreadable, outageWait, "")
		} else if !ok {
			skip(res, reason)
		} else {
			w.readPlain(f, j, res)
		}
	default:
		if !w.o.OfficeText {
			// Documents are not sent to a converter unless the operator has said so (review S4). No request is made.
			skip(res, store.TextErrOfficeTextOff)
			return false
		}
		if w.o.Conv == nil {
			skip(res, store.TextErrConverterOff)
			return false
		}
		ext := ""
		if fam == FamilyOffice {
			ext = officeExt(f, j.Size)
		} else if fam == FamilyPDF {
			ext = "pdf"
		} else {
			// An OLE2 compound file: the old Word, Excel or PowerPoint format, told apart by its stream names (ole.go). A file
			// whose type cannot be told is skipped and recorded, not sent as a guess (review, decision 46).
			var kerr error
			ext, kerr = oleKind(f)
			if kerr != nil || ext == "" {
				skip(res, msgOLEUnknown)
				return false
			}
		}
		if ext == "" {
			skip(res, msgNotText)
			return false
		}
		if ok, reason, err := w.room(ctx, j.AccountID); err != nil {
			w.transient(res, msgUnreadable, outageWait, "")
			return false
		} else if !ok {
			skip(res, reason)
			return false
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			w.transient(res, msgUnreadable, outageWait, "")
			return false
		}
		return w.convert(ctx, j, f, ext, res)
	}
	return false
}

// room reports whether the account may store another file of text (store.TextRoom, with the caps of the worker). A
// capped file is skipped with the cap's reason, and nothing is read.
func (w *Worker) room(ctx context.Context, accountID string) (bool, string, error) {
	if w.o.CapAccount == 0 && w.o.CapTotal == 0 {
		return true, "", nil
	}
	return w.o.Store.TextRoom(ctx, accountID, w.o.TextMax, w.o.CapAccount, w.o.CapTotal)
}

// readPlain reads a plain-text file, in process.
func (w *Worker) readPlain(f *os.File, j store.TextJob, res *store.TextResult) {
	text, truncated, err := ReadText(io.LimitReader(f, j.Size), w.o.TextMax)
	switch {
	case errors.Is(err, ErrNotText):
		skip(res, msgNotText)
	case err != nil:
		w.transient(res, msgUnreadable, outageWait, "")
	default:
		ready(res, text, truncated, "plain")
	}
}

// officeExt is the true type of a ZIP-based document: the extension the converter is told (review S4). It is decided by
// the entries of the container, not by the name. OLE2 files are not decided here. An unknown container is "".
func officeExt(f *os.File, size int64) string {
	// The cap is read from the end record before zip.NewReader builds an entry for each record (sec1, decision 84, F5).
	if n, ok := zipEntryCount(f, size, maxOfficeEntries); !ok || n > maxOfficeEntries {
		return ""
	}
	zr, err := zip.NewReader(f, size)
	if err != nil {
		return ""
	}
	// The names are read in one pass that keeps no entry: mimetype stops it, since its content decides, and the three
	// document names are only flags (sec1, decision 84). A map of every entry cost memory with the entry count.
	var mimetype *zip.File
	var docx, xlsx, pptx bool
	for _, e := range zr.File {
		switch e.Name {
		case "mimetype":
			mimetype = e
		case "word/document.xml":
			docx = true
		case "xl/workbook.xml":
			xlsx = true
		case "ppt/presentation.xml":
			pptx = true
		}
		if mimetype != nil {
			break
		}
	}
	if m := mimetype; m != nil {
		rc, err := m.Open()
		if err != nil {
			return ""
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, 256))
		if err != nil {
			return ""
		}
		switch string(bytes.TrimSpace(b)) {
		case "application/vnd.oasis.opendocument.text":
			return "odt"
		case "application/vnd.oasis.opendocument.spreadsheet":
			return "ods"
		case "application/vnd.oasis.opendocument.presentation":
			return "odp"
		}
		return ""
	}
	switch {
	case docx:
		return "docx"
	case xlsx:
		return "xlsx"
	case pptx:
		return "pptx"
	}
	return ""
}

// maxOfficeEntries is the most entries of a container officeExt decides. A real document has far fewer; a container
// with more is left undecided, and its text is not extracted (sec1, decision 84).
const maxOfficeEntries = 10000

// convert sends a document to the converter for plain text, and classifies its answer. It returns true when the
// converter could not be reached. A conversion whose output is over the text limit is a success: the writer stops the
// converter once it has the limit, and the text is marked truncated. The conversion's time limit is the client's: it
// starts when the request does, not when the worker first asks (review S4).
func (w *Worker) convert(ctx context.Context, j store.TextJob, f *os.File, ext string, res *store.TextResult) (paused bool) {
	key, err := w.key(ctx)
	if err != nil {
		// The converter's capabilities cannot be had: it cannot be reached (or does not answer), so the worker pauses.
		if ctx.Err() != nil {
			return false
		}
		w.pause()
		return true
	}
	// A row at the cap has used its three tries, and its last try did not come back: it is refused under the current key, and
	// nothing is sent. Without this check a document that stops the converter, or the server, would be sent again on every
	// restart, with the count going up without a limit (review, decision 46). A new key releases it, with its count reset
	// (store.ReleaseTextKey), as for any refusal.
	if j.Attempts >= failedAttempts {
		w.refused(res, key, msgUnavailable)
		return false
	}
	tw := &textWriter{max: int(w.o.TextMax)}
	// The attempt is written before the document is sent, so a conversion cut off by a shutdown or a crash still counts,
	// and a document that kills the converter runs out of tries (review, decision 46). The worker is one goroutine, so no
	// request waits for the bulk slot here: the queue time is not in the count.
	if err := w.o.Store.StartText(ctx, j.ID); err != nil {
		w.transient(res, msgUnreadable, outageWait, "")
		return false
	}
	err = w.o.Conv.ConvertBulk(ctx, f, j.Size, ext, "txt", tw)
	if ctx.Err() != nil {
		return false
	}
	res.Source, res.Converter = "converter", key
	if tw.more {
		err = nil
	}
	switch {
	case err == nil:
		w.pauseWait = 0
		text, truncated, err := ReadText(bytes.NewReader(keepWhole(tw.buf)), w.o.TextMax)
		if err != nil {
			w.refused(res, key, msgBadOutput)
			return false
		}
		ready(res, text, truncated || tw.more, "converter")
	case errors.Is(err, office.ErrUnreachable):
		// No request reached the converter, so this is not an attempt: the count written before the send is taken back.
		if rerr := w.o.Store.RefundText(ctx, j.ID); rerr != nil {
			w.o.Log.Warn("content search: the attempt could not be taken back", "err", rerr)
		}
		w.pause()
		return true
	case errors.Is(err, context.DeadlineExceeded):
		res.Attempts = j.Attempts + 1
		switch {
		case res.Attempts == 1:
			w.transient(res, msgTimeout, timeoutWaitOne, "converter")
		case res.Attempts < failedAttempts:
			w.transient(res, msgTimeout, timeoutWaitTwo, "converter")
		default:
			// The third try timed out: the document waits for a new converter key, as a refusal does (review, decision 46).
			w.refused(res, key, msgTimeout)
		}
	case errors.Is(err, office.ErrBadOutput):
		w.refused(res, key, msgBadOutput)
	case errors.Is(err, office.ErrRefused), errors.Is(err, office.ErrUnsupported), errors.Is(err, office.ErrTooLarge):
		w.refused(res, key, msgRefused)
	default:
		// The converter failed while it had the document: an error status, a reset, or a body that stopped. The document
		// may be the cause, so the try is counted. After failedAttempts tries the row waits for a new converter key.
		res.Attempts = j.Attempts + 1
		if res.Attempts < failedAttempts {
			wait := inflightWaitOne
			if res.Attempts > 1 {
				wait = inflightWaitTwo
			}
			w.transient(res, msgUnavailable, wait, "converter")
		} else {
			w.refused(res, key, msgUnavailable)
		}
	}
	return false
}

// pause records an unreachable converter: the worker waits, doubling from outageWait to outageMax.
func (w *Worker) pause() {
	if w.pauseWait < outageWait {
		w.pauseWait = outageWait
	} else {
		w.pauseWait = min(w.pauseWait*2, outageMax)
	}
	w.pauseUntil = w.o.Now().Add(w.pauseWait)
	w.o.Log.Warn("content search: the converter cannot be reached; pausing", "wait", w.pauseWait)
}

// ready records a row's text. The text is kept in its search form, which is what the full-text table indexes.
func ready(res *store.TextResult, text []byte, truncated bool, source string) {
	res.State = store.TextReady
	res.Source = source
	res.Error = ""
	res.Text = names.SearchForm(string(text))
	res.Bytes = int64(len(text))
	res.Truncated = truncated
	res.NextTry = time.Time{}
}

// skip records a permanent answer: the row is not read again, unless its blob changes or the cap or the policy changes.
func skip(res *store.TextResult, msg string) {
	res.State = store.TextSkipped
	res.Error = msg
	res.NextTry = time.Time{}
}

// park keeps a pending row for a file in the trash. It is not a try, and no attempt is counted.
func (w *Worker) park(res *store.TextResult) {
	res.State = store.TextPending
	res.Error = store.TextErrParked
	res.NextTry = w.o.Now().Add(parkWait)
}

// refused records a refusal of the converter: sticky under the converter key, which is the row's converter column. It
// is tried again only when the key changes (store.ReleaseTextKey), never by the clock (review S4).
func (w *Worker) refused(res *store.TextResult, key, msg string) {
	res.State = store.TextFailed
	res.Error = msg
	res.Converter = key
	res.Sticky = true
}

// transient keeps the row pending, due after wait. source names the reader when it is the converter.
func (w *Worker) transient(res *store.TextResult, msg string, wait time.Duration, source string) {
	res.State = store.TextPending
	res.Error = msg
	res.NextTry = w.o.Now().Add(wait)
	if source != "" {
		res.Source = source
	}
}
