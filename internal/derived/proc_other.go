// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !windows

package derived

import "syscall"

// childAttr is how the child is started. Only Linux has Pdeathsig; elsewhere the child is in its own process group.
func childAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
