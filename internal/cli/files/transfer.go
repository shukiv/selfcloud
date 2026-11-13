// SPDX-License-Identifier: AGPL-3.0-or-later

package files

// The large-file commands (decision 142 and 3.9, slice cli6): put sends a file by tus in checked pieces and
// resumes from the offset the server holds; get reads a file in ranges into a part file, and resumes from the part
// file's size with If-Range, so a file that changed is never mixed; upload ls and upload cancel manage the uploads this
// client began. A transfer is bounded by a stall limit, not by the whole-request timeout, because a large piece on a
// slow link may take longer than any fixed limit.

import (
	"context"
	"errors"
	"flag"
	"os"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/config"
)

const (
	// defaultPieceSize fits under a proxy that caps a request at 100 MB.
	defaultPieceSize = 64 << 20
	minPieceSize     = 1 << 20
	envPieceSize     = "SELFCLOUD_PIECE_SIZE"
	defaultStall     = time.Minute
	// maxTries is how many failures in a row, with nothing gained between them, a transfer takes before it gives up.
	maxTries = 5
)

// transferWait is the wait before the try-th repeat after a failure: one second, doubling, at most thirty. Tests make it
// zero.
var transferWait = func(try int) time.Duration {
	if try < 1 || try > 5 {
		return 30 * time.Second
	}
	return time.Second << (try - 1)
}

// transferFlags are the flags put and get share.
type transferFlags struct {
	piece string
	stall time.Duration
}

func (f *transferFlags) register(set *flag.FlagSet) {
	set.StringVar(&f.piece, "piece-size", "", "the `size` of each piece, at least 1MiB, such as 32MiB "+
		"(default 64MiB, or "+envPieceSize+")")
	set.DurationVar(&f.stall, "stall-timeout", defaultStall, "end a piece when no byte has moved for this long")
}

// pieceSize is the piece size: --piece-size, else SELFCLOUD_PIECE_SIZE, else the default. explicit is false for the
// default, which a server with a lower limit may lower. The refusal names the source, never the value.
func (f *transferFlags) pieceSize() (size int64, explicit bool, err error) {
	if f.stall <= 0 {
		return 0, false, exitcode.Usagef("--stall-timeout must be longer than zero")
	}
	v, from := f.piece, "--piece-size"
	if v == "" {
		v, from = os.Getenv(envPieceSize), envPieceSize
	}
	if v == "" {
		return defaultPieceSize, false, nil
	}
	var b config.ByteSize
	if err := b.UnmarshalText([]byte(v)); err != nil {
		return 0, false, exitcode.Usagef("%s is not a size, such as 64MiB", from)
	}
	if int64(b) < minPieceSize {
		return 0, false, exitcode.Usagef("%s must be at least 1MiB (%d bytes)", from, minPieceSize)
	}
	return int64(b), true, nil
}

// pause waits before a repeat: the server's Retry-After when it gave one, up to thirty seconds, else transferWait.
func pause(ctx context.Context, try int, cause error) error {
	d := transferWait(try)
	var ae *client.APIError
	if errors.As(cause, &ae) && ae.RetryAfter > 0 {
		d = min(ae.RetryAfter, 30*time.Second)
	}
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// repeatable reports whether a failed request of a transfer may be made again: a connection that failed or stalled, a
// server error, a server that asks for a wait, or another request holding the upload. A refusal is never repeated.
func repeatable(err error) bool {
	var te *client.TransportError
	if errors.As(err, &te) {
		return true
	}
	var ae *client.APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch {
	case ae.Status >= 500, ae.Status == 429, ae.Code == "locked":
		return true
	}
	return false
}

// apiCode is the API's error code of err, or "".
func apiCode(err error) string {
	var ae *client.APIError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

// withHint keeps err's exit code and code, and adds a sentence to its message.
func withHint(err error, hint string) error {
	code, name, msg := exitcode.Of(err)
	return &exitcode.Error{Exit: code, Name: name, Message: msg + "; " + hint}
}
