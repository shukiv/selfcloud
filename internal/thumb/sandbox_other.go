// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package thumb

import "errors"

// LandlockABI is 0 off Linux: there is no Landlock.
func LandlockABI() int { return 0 }

// setNoNewPrivs and restrictFiles refuse off Linux. The decoder runs only where applyLimits enforces its limits, which
// is Linux only, so these are not reached.
func setNoNewPrivs() error { return errors.New("the decoder sandbox is Linux only") }

func restrictFiles() (int, error) { return 0, errors.New("the decoder sandbox is Linux only") }
