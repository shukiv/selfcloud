// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package thumb

import "errors"

// applyLimits refuses on every platform but Linux. Other systems accept an address-space limit
// without enforcing it, and the child cannot tell, so it decodes nothing rather than run unlimited.
func applyLimits(Limits) error {
	return errors.New("decoder limits are enforced on Linux only")
}

// Supported reports whether the limits this package sets are enforced on this platform.
const Supported = false
