// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"strings"
	"testing"
	"time"
)

// cli.device_token_ttl defaults to 720 hours (decision 125, owner's answer 7), and a value under a minute is refused at
// load, as tokens.max_ttl is.
func TestCLIDeviceTokenTTL(t *testing.T) {
	t.Parallel()
	if got := Defaults().CLI.DeviceTokenTTL.D(); got != 720*time.Hour {
		t.Errorf("default cli.device_token_ttl = %v, want 720h", got)
	}
	for _, tc := range []struct {
		ttl     Duration
		refused bool
	}{
		{0, true},
		{Duration(30 * time.Second), true},
		{Duration(time.Minute), false},
	} {
		c := Defaults()
		c.CLI.DeviceTokenTTL = tc.ttl
		err := c.Validate()
		got := err != nil && strings.Contains(err.Error(), "cli.device_token_ttl")
		if got != tc.refused {
			t.Errorf("device_token_ttl %v: refused by this check = %v (error %v), want %v", tc.ttl.D(), got, err, tc.refused)
		}
	}
}
