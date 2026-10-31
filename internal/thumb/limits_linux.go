// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package thumb

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// applyLimits sets the child's own address-space and CPU limits, and reads them back.
//
// The address-space limit is relative. A Go binary maps about 1.2 GB of address space before main
// runs (its runtime reserves it), so an absolute RLIMIT_AS low enough to matter cannot be set: the
// runtime would be out of memory at once. The limit is therefore the address space the child holds
// when it starts, plus MemoryBytes. Any mapping beyond that fails, which the runtime reports as a
// fatal out-of-memory error and exits. Lowering one's own limits needs no capability, so this works
// as a non-root user in a container with the default seccomp profile.
//
// The CPU limit is enforced by the kernel: SIGXCPU at the soft limit, SIGKILL at the hard one.
func applyLimits(l Limits) error {
	base, err := vmSizeBytes()
	if err != nil {
		return err
	}
	as := uint64(base) + uint64(l.MemoryBytes)
	if err := setAndCheck(syscall.RLIMIT_AS, as, as); err != nil {
		return fmt.Errorf("address space: %w", err)
	}
	cpu := uint64(l.CPUSeconds)
	if err := setAndCheck(syscall.RLIMIT_CPU, cpu, cpu+1); err != nil {
		return fmt.Errorf("cpu time: %w", err)
	}
	// The child has its input, its output and its errors, and the runtime's own few descriptors. It writes no file
	// and dumps no core. Each limit is set at its hard value, so the child cannot raise it again.
	if err := setAndCheck(syscall.RLIMIT_NOFILE, childFiles, childFiles); err != nil {
		return fmt.Errorf("open files: %w", err)
	}
	if err := setAndCheck(syscall.RLIMIT_FSIZE, 0, 0); err != nil {
		return fmt.Errorf("file size: %w", err)
	}
	if err := setAndCheck(syscall.RLIMIT_CORE, 0, 0); err != nil {
		return fmt.Errorf("core dump: %w", err)
	}
	return nil
}

// childFiles is the most open descriptors the child may hold.
const childFiles = 16

// setAndCheck sets a limit and reads it back, so a limit that did not take is an error, not a
// silent absence.
func setAndCheck(resource int, cur, max uint64) error {
	want := syscall.Rlimit{Cur: cur, Max: max}
	if err := syscall.Setrlimit(resource, &want); err != nil {
		return err
	}
	var got syscall.Rlimit
	if err := syscall.Getrlimit(resource, &got); err != nil {
		return err
	}
	if got.Cur != want.Cur || got.Max != want.Max {
		return errors.New("the limit did not take")
	}
	return nil
}

// vmSizeBytes returns the address space the process holds now, from /proc/self/status.
func vmSizeBytes() (int64, error) {
	st, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, fmt.Errorf("read address space: %w", err)
	}
	for _, ln := range bytes.Split(st, []byte("\n")) {
		rest, ok := bytes.CutPrefix(ln, []byte("VmSize:"))
		if !ok {
			continue
		}
		f := bytes.Fields(rest)
		if len(f) != 2 || string(f[1]) != "kB" {
			break
		}
		kb, err := strconv.ParseInt(string(f[0]), 10, 64)
		if err != nil || kb <= 0 {
			break
		}
		return kb << 10, nil
	}
	return 0, errors.New("no VmSize in /proc/self/status")
}

// Supported reports whether the limits this package sets are enforced on this platform.
const Supported = true
