// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
)

// Exit codes of the child. The parent reads them: a decode refusal is recorded as a failure of the
// source, and a child that could not set its limits is a server fault, recorded as nothing, so the
// request can be made again once the fault is gone.
const (
	ExitOK          = 0
	ExitUsage       = 64 // a flag is missing or malformed
	ExitCorrupt     = 65 // the image cannot be decoded, or does not match its header
	ExitTooLarge    = 66 // the input or the image is over a cap
	ExitUnsupported = 67 // the input is not JPEG, PNG or GIF
	ExitLimits      = 70 // the limits could not be set: nothing was read
	ExitInternal    = 71 // any other failure
)

// Limits are the caps a child sets on itself before it reads its input.
type Limits struct {
	Size      int   // the longer edge of the thumbnail, in pixels
	MaxPixels int64 // the pixel cap Header applies
	MaxInput  int64 // the most input bytes the child reads
	// MemoryBytes is how much address space the decoder may map beyond what the runtime holds when
	// the child starts (see applyLimits). It bounds the decoder's own allocations.
	MemoryBytes int64
	// CPUSeconds is the CPU time the child may use. At the soft limit the kernel sends SIGXCPU; the
	// hard limit, one second later, is SIGKILL, which the Go runtime cannot catch.
	CPUSeconds int64
}

// Child is the child process: `selfcloud thumb [flags]`. It reads one image from stdin and writes a
// JPEG thumbnail to stdout. It sets its own limits before it reads anything, and returns an exit code.
// It reads no configuration and no file other than stdin, and writes nothing to stderr but errors.
func Child(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("thumb", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var lim Limits
	fs.IntVar(&lim.Size, "size", 0, "longer edge in pixels")
	fs.Int64Var(&lim.MaxPixels, "max-pixels", 0, "pixel cap")
	fs.Int64Var(&lim.MaxInput, "max-input", 0, "input cap in bytes")
	fs.Int64Var(&lim.MemoryBytes, "memory", 0, "address-space headroom in bytes")
	fs.Int64Var(&lim.CPUSeconds, "cpu", 0, "CPU seconds")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if err := lim.validate(); err != nil {
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

	src, err := io.ReadAll(io.LimitReader(stdin, lim.MaxInput+1))
	if err != nil {
		fmt.Fprintln(stderr, "thumb: read:", err)
		return ExitInternal
	}
	if int64(len(src)) > lim.MaxInput {
		return ExitTooLarge
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

// validate refuses limits that would make the child do nothing useful, or that are unset.
func (l Limits) validate() error {
	switch {
	case l.Size <= 0 || l.Size > 4096:
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
