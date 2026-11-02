// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

// The audio tags read (design section 14, slice x3, decision 56). It runs on the thumbnail pool's workers and its queue, as
// one more kind of job: the child runs in tags mode, and its JSON is checked here before the values are stored. The row of
// kind audio_tags holds the read's state; the values go to the audio_tags table, in the same transaction that marks the row
// ready. No file is made, so the budget counts nothing for a tags row.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/audio"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/thumb"
)

// KindTags is the kind of a derived row that reads an audio file's tags.
const KindTags = store.AudioTagsKind

// TagsVersion names the code that reads tags. A change to the reader's rules changes it, so rows read by the old code are not
// served as current (the converter key adds the limits to it, as for thumbnails).
const TagsVersion = "tags-go-1"

// Failure messages of a tags read. Like the thumbnail messages, they are the server's own words, and none carries the
// file's text.
const (
	msgTagsCorrupt = "the audio file's tags could not be read"
	msgNotAudio    = "not an audio format the server reads"
	msgTagsOutput  = "the tags are over the output limit"
	msgTagsValue   = "the tags read did not pass the server's checks"
)

// tagsArgs are the child's arguments in tags mode. The child reads the file by seeking, so the input cap is for the one
// input that cannot seek (never sent: the parent passes the file). It is small, so such an input is refused, not read whole.
func tagsArgs(o Options) []string {
	return []string{"thumb", "-mode=" + thumb.ModeTags,
		"-max-pixels=" + strconv.FormatInt(o.MaxPixels, 10),
		"-max-input=" + strconv.FormatInt(tagsInputCap, 10),
		"-memory=" + strconv.FormatInt(o.MemoryBytes, 10),
		"-cpu=" + strconv.FormatInt(o.CPUSeconds, 10),
	}
}

// tagsInputCap is the most bytes a tags child reads from a stdin that cannot seek. The parent always sends the file, which
// can seek, so this is a bound on the fallback only.
const tagsInputCap = 1 << 20

// tagsOutputMax caps the child's JSON. Four values of at most MaxText runes fit well inside it; a larger output is refused.
const tagsOutputMax = 8 << 10

// maxDuration is the longest duration a tags value may hold: a week. A longer one is a corrupt header, not a song.
const maxDuration = 7 * 24 * 60 * 60 * 1000

// tagsConverterFor is the converter key of the tags rows this pool makes: the reader's version and the source limit.
func tagsConverterFor(o Options) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s audio=%d", TagsVersion, o.AudioMaxSourceBytes))
	return TagsVersion + "+" + hex.EncodeToString(sum[:6])
}

// TagsConverter returns the converter key of the tags rows. Readers of the rows must use it.
func (p *Pool) TagsConverter() string { return p.tagConverter }

// RequestTags answers for the tags of blobID. It works as Request does: a ready read is returned, a read not yet made is
// queued once, a failed one waits for its retry time, and ErrBusy means the queue is full.
func (p *Pool) RequestTags(ctx context.Context, accountID, blobID string, admit func() error) (Answer, error) {
	return p.request(ctx, accountID, blobID, KindTags, p.tagConverter, 0, true, admit)
}

// PeekTags answers for a tags read that exists, and queues nothing. A HEAD request uses it.
func (p *Pool) PeekTags(ctx context.Context, accountID, blobID string) (Answer, error) {
	d, err := p.o.Cache.Lookup(ctx, accountID, blobID, KindTags, p.tagConverter)
	if err != nil || d == nil {
		return Answer{}, err
	}
	return answerFor(d), nil
}

// processTags reads one audio file's tags and records the result.
func (p *Pool) processTags(ctx context.Context, j job) {
	src, err := p.o.Blobs.Open(j.accountID, j.blobID)
	if err != nil {
		p.drop(ctx, j)
		return
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		p.drop(ctx, j)
		return
	}
	if fi.Size() > p.o.AudioMaxSourceBytes {
		p.fail(ctx, j, msgSourceSize)
		return
	}
	var out bytes.Buffer
	args := tagsArgs(p.o)
	stderr := &capBuf{max: stderrKeep}
	code, err := p.runArgs(ctx, src, &limitWriter{w: &out, max: tagsOutputMax}, stderr, args)
	if ctx.Err() != nil {
		// Shutdown: nothing is recorded, and the next start clears the row.
		p.drop(ctx, j)
		return
	}
	if err != nil {
		// The child could not be started: a server fault, not the file's.
		p.log.Error("cannot run the thumbnail decoder for tags", "error", err)
		p.drop(ctx, j)
		return
	}
	if code != thumb.ExitOK {
		p.tagsFailure(ctx, j, code, out.Len() > tagsOutputMax)
		return
	}
	t, ok := checkedTags(out.Bytes())
	if !ok {
		p.fail(ctx, j, msgTagsValue)
		return
	}
	p.finishTags(ctx, j, t)
}

// tagsFailure records a tags read that did not finish. A file the reader refuses is sticky for its key; a fault of the host
// or the run is retried, as for a thumbnail.
func (p *Pool) tagsFailure(ctx context.Context, j job, code int, overflow bool) {
	if overflow {
		p.fail(ctx, j, msgTagsOutput)
		return
	}
	switch code {
	case thumb.ExitLimits, thumb.ExitUsage:
		p.log.Error("the tags reader could not start with its limits", "exit", code)
		p.drop(ctx, j)
	case thumb.ExitCorrupt:
		p.logRefusal(code, j)
		p.fail(ctx, j, msgTagsCorrupt)
	case thumb.ExitTooLarge:
		p.logRefusal(code, j)
		p.fail(ctx, j, msgTooLarge)
	case thumb.ExitUnsupported:
		p.logRefusal(code, j)
		p.fail(ctx, j, msgNotAudio)
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

// checkedTags decodes the child's JSON and checks each value: plain text, not over MaxText runes, no control or format
// character, and a duration within a week. A value that fails is not stored, so the child is not trusted for its text.
func checkedTags(b []byte) (audio.Tags, bool) {
	var in struct {
		Title      string `json:"title"`
		Artist     string `json:"artist"`
		Album      string `json:"album"`
		DurationMS int64  `json:"duration_ms"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return audio.Tags{}, false
	}
	if dec.More() {
		return audio.Tags{}, false
	}
	for _, s := range []string{in.Title, in.Artist, in.Album} {
		if !plainText(s) {
			return audio.Tags{}, false
		}
	}
	// A duration out of range is a bad header: it is given up, and the title and artist are kept.
	if in.DurationMS < 0 || in.DurationMS > maxDuration {
		in.DurationMS = 0
	}
	return audio.Tags{Title: in.Title, Artist: in.Artist, Album: in.Album, DurationMS: in.DurationMS}, true
}

// plainText reports whether s is valid UTF-8, within the length, and free of control and format characters (a zero-width
// joiner aside, which Arabic and Persian need).
func plainText(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > audio.MaxText {
		return false
	}
	for _, r := range s {
		if r == 0x200C || r == 0x200D {
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return !strings.ContainsAny(s, "\r\n")
}

// finishTags stores the values and marks the row ready in one transaction. If the row was removed while the read ran, nothing
// is stored.
func (p *Pool) finishTags(ctx context.Context, j job, t audio.Tags) {
	at := p.now()
	err := p.o.Store.Write(ctx, func(tx *store.Tx) error {
		ok, err := tx.CompleteDerived(ctx, j.accountID, j.derivedID, 0, "", at)
		if err != nil || !ok {
			return err
		}
		return tx.UpsertAudioTags(ctx, j.accountID, j.blobID, store.AudioTags{Title: t.Title, Artist: t.Artist,
			Album: t.Album, DurationMS: t.DurationMS})
	})
	if err != nil {
		p.log.Error("cannot record audio tags", "error", err, "derived", j.derivedID)
		p.drop(ctx, j)
	}
}

// limitWriter passes up to max bytes to w and refuses the rest: the child's output is then cut and marked over the limit.
type limitWriter struct {
	w   *bytes.Buffer
	max int
}

func (l *limitWriter) Write(b []byte) (int, error) {
	if l.w.Len()+len(b) > l.max {
		// Keep one byte past the limit, so the caller can see the output was over it.
		room := l.max + 1 - l.w.Len()
		if room > 0 {
			l.w.Write(b[:min(room, len(b))])
		}
		return len(b), nil
	}
	return l.w.Write(b)
}
