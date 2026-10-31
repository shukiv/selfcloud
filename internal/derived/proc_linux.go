// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package derived

import (
	"syscall"
)

// childAttr is how the child is started. Pdeathsig kills the child when the server's thread that started it
// dies, so a child cannot outlive a server that crashed. Setpgid puts it in its own process group, so a Ctrl-C or
// a group signal sent to the server's terminal does not reach it first; the server stops it, not the terminal.
func childAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
}
