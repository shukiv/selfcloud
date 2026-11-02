// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"

	"github.com/shukiv/selfcloud/internal/audio"
)

// Exit codes of the child. The parent reads them: a decode refusal is recorded as a failure of the
// source, and a child that could not set its limits is a server fault, recorded as nothing, so the
// request can be made again once the fault is gone.
const (
	ExitOK          = 0
	ExitUsage       = 64 // a flag is missing or malformed
	ExitCorrupt     = 65 // the image or the audio file cannot be decoded, or does not match its header
	ExitTooLarge    = 66 // the input or the image is over a cap
	ExitUnsupported = 67 // the input is not JPEG, PNG or GIF (an image), or not an audio format (a tags read)
	ExitNoPicture   = 68 // an audio file with no cover the server can show
	ExitLimits      = 70 // the limits could not be set: nothing was read
	ExitInternal    = 71 // any other failure
)

// The child's modes. An image mode makes a thumbnail: of an image file, or of the cover picture of an audio file. The tags
// mode reads an audio file's title, artist, album and duration, and writes them as JSON (decision 56).
const (
	ModeImage = "image"
	ModeTags  = "tags"
)

// Limits are the caps a child sets on itself before it reads anything.
type Limits struct {
	Size      int   // the longer edge of the thumbnail, in pixels (image mode)
	MaxPixels int64 // the pixel cap Header applies
	MaxInput  int64 // the most input bytes an image is read to (image mode); an audio file is read in windows, not whole
	// MemoryBytes is how much address space the decoder may map beyond what the runtime holds when
	// the child starts (see applyLimits). It bounds the decoder's own allocations.
	MemoryBytes int64
	// CPUSeconds is the CPU time the child may use. At the soft limit the kernel sends SIGXCPU; the
	// hard limit, one second later, is SIGKILL, which the Go runtime cannot catch.
	CPUSeconds int64
}

// Child is the child process: `selfcloud thumb [flags]`. In image mode it reads one image, or the cover of one audio file,
// from stdin and writes a JPEG thumbnail to stdout. In tags mode it reads one audio file and writes its tags as JSON. It sets
// its own limits before it reads anything, and returns an exit code. It reads no configuration and no file other than stdin,
// and writes nothing to stderr but errors.
func Child(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("thumb", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var lim Limits
	var mode string
	fs.StringVar(&mode, "mode", ModeImage, "image (a thumbnail) or tags (an audio file's tags as JSON)")
	fs.IntVar(&lim.Size, "size", 0, "longer edge in pixels")
	fs.Int64Var(&lim.MaxPixels, "max-pixels", 0, "pixel cap")
	fs.Int64Var(&lim.MaxInput, "max-input", 0, "input cap in bytes")
	fs.Int64Var(&lim.MemoryBytes, "memory", 0, "address-space headroom in bytes")
	fs.Int64Var(&lim.CPUSeconds, "cpu", 0, "CPU seconds")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if err := lim.validate(mode); err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsage
	}
	// The first act: no limit, no decode. Then the child gives up its privileges (harden).
	if err := harden(lim); err != nil {
		fmt.Fprintln(stderr, "thumb: limits:", err)
		return ExitLimits
	}
	// The Go runtime's soft target is a GC hint, not a cap; it keeps a long decode from holding
	// garbage it could free.
	debug.SetMemoryLimit(lim.MemoryBytes * 3 / 4)

	if mode == ModeTags {
		return childTags(stdin, lim, stdout, stderr)
	}
	return childImage(stdin, lim, stdout, stderr)
}

// childImage makes the thumbnail. An input that starts like an audio file is read as one, and its cover is thumbnailed; any
// other input is read whole, up to MaxInput, and decoded as an image.
func childImage(stdin io.Reader, lim Limits, stdout, stderr io.Writer) int {
	if rs, ok := seekable(stdin); ok {
		head := make([]byte, 12)
		n, _ := io.ReadFull(rs, head)
		if _, err := rs.Seek(0, io.SeekStart); err != nil {
			return ExitInternal
		}
		if audio.Sniff(head[:n]) {
			return childAudioCover(rs, lim, stdout, stderr)
		}
	}
	src, code := readInput(stdin, lim.MaxInput, stderr)
	if code != ExitOK {
		return code
	}
	out, err := Make(src, lim.Size, lim.MaxPixels)
	if err != nil {
		return exitFor(err)
	}
	if _, err := io.Copy(stdout, bytes.NewReader(out)); err != nil {
		fmt.Fprintln(stderr, "thumb: write:", err)
		return ExitInternal
	}
	return ExitOK
}

// childAudioCover thumbnails the cover of an audio file. The picture is decoded by Make, which accepts only JPEG, PNG and
// GIF: any other picture, or none, is ExitNoPicture.
func childAudioCover(rs io.ReadSeeker, lim Limits, stdout, stderr io.Writer) int {
	res, err := audio.Read(rs, true)
	if err != nil {
		return audioExit(err)
	}
	if res.Picture == nil {
		return ExitNoPicture
	}
	out, err := Make(res.Picture, lim.Size, lim.MaxPixels)
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			return ExitTooLarge
		}
		return ExitNoPicture
	}
	if _, err := io.Copy(stdout, bytes.NewReader(out)); err != nil {
		fmt.Fprintln(stderr, "thumb: write:", err)
		return ExitInternal
	}
	return ExitOK
}

// tagsJSON is the output of a tags read. Empty values are left out; the parent stores what is there.
type tagsJSON struct {
	Title      string `json:"title,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// childTags reads an audio file's tags and writes them as one JSON object.
func childTags(stdin io.Reader, lim Limits, stdout, stderr io.Writer) int {
	rs, code := inputSeeker(stdin, lim.MaxInput, stderr)
	if code != ExitOK {
		return code
	}
	res, err := audio.Read(rs, false)
	if err != nil {
		return audioExit(err)
	}
	out, err := json.Marshal(tagsJSON{Title: res.Tags.Title, Artist: res.Tags.Artist, Album: res.Tags.Album,
		DurationMS: res.Tags.DurationMS})
	if err != nil {
		return ExitInternal
	}
	if _, err := stdout.Write(out); err != nil {
		fmt.Fprintln(stderr, "thumb: write:", err)
		return ExitInternal
	}
	return ExitOK
}

// audioExit maps an audio reader's error to an exit code.
func audioExit(err error) int {
	switch {
	case errors.Is(err, audio.ErrUnsupported):
		return ExitUnsupported
	case errors.Is(err, audio.ErrTooLarge):
		return ExitTooLarge
	case errors.Is(err, audio.ErrCorrupt):
		return ExitCorrupt
	}
	return ExitInternal
}

// seekable reports whether stdin can seek. The parent passes the file itself, which can.
func seekable(stdin io.Reader) (io.ReadSeeker, bool) {
	rs, ok := stdin.(io.ReadSeeker)
	if !ok {
		return nil, false
	}
	if _, err := rs.Seek(0, io.SeekCurrent); err != nil {
		return nil, false
	}
	return rs, true
}

// inputSeeker gives a seekable input: stdin itself when it can seek, and otherwise its bytes, read up to max.
func inputSeeker(stdin io.Reader, max int64, stderr io.Writer) (io.ReadSeeker, int) {
	if rs, ok := seekable(stdin); ok {
		return rs, ExitOK
	}
	src, code := readInput(stdin, max, stderr)
	if code != ExitOK {
		return nil, code
	}
	return bytes.NewReader(src), ExitOK
}

// readInput reads stdin whole, up to max bytes. More than max is ExitTooLarge, and a read that fails is ExitInternal.
func readInput(stdin io.Reader, max int64, stderr io.Writer) ([]byte, int) {
	src, err := io.ReadAll(io.LimitReader(stdin, max+1))
	if err != nil {
		fmt.Fprintln(stderr, "thumb: read:", err)
		return nil, ExitInternal
	}
	if int64(len(src)) > max {
		return nil, ExitTooLarge
	}
	return src, ExitOK
}

// harden applies the child's limits and then takes away its privileges. The limits come first, while the process is
// whole. The privileges are taken from the calling goroutine's thread, which is locked here: the decoder runs on this
// thread, and spawns no goroutine that could run elsewhere. No new privileges is a refusal when it fails. Landlock is
// best effort: where the kernel has none, the decoder runs with the limits and no_new_privs, as the server reported
// at start. Nothing is logged here, since the parent discards the child's output.
func harden(lim Limits) error {
	runtime.LockOSThread()
	if err := applyLimits(lim); err != nil {
		return err
	}
	if err := setNoNewPrivs(); err != nil {
		return err
	}
	_, _ = restrictFiles()
	return nil
}

// validate refuses limits that would make the child do nothing useful, or that are unset. The size is needed only to make
// a thumbnail; a tags read needs no size.
func (l Limits) validate(mode string) error {
	switch {
	case mode != ModeImage && mode != ModeTags:
		return errors.New("thumb: -mode must be image or tags")
	case mode == ModeImage && (l.Size <= 0 || l.Size > 4096):
		return errors.New("thumb: -size must be between 1 and 4096")
	case l.MaxPixels <= 0:
		return errors.New("thumb: -max-pixels must be positive")
	case l.MaxInput <= 0:
		return errors.New("thumb: -max-input must be positive")
	case l.MemoryBytes <= 0:
		return errors.New("thumb: -memory must be positive")
	case l.CPUSeconds <= 0:
		return errors.New("thumb: -cpu must be positive")
	}
	return nil
}

// exitFor maps a decoder error to its exit code.
func exitFor(err error) int {
	switch {
	case errors.Is(err, ErrUnsupported):
		return ExitUnsupported
	case errors.Is(err, ErrTooLarge):
		return ExitTooLarge
	case errors.Is(err, ErrCorrupt):
		return ExitCorrupt
	}
	return ExitInternal
}
