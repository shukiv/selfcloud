// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package derived

import "syscall"

// childAttr is how the child is started. Windows has no process groups in this sense, and its thumbnails are refused
// anyway (thumb.Supported is false there), so the attributes are empty.
func childAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
