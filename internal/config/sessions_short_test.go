// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"testing"
	"time"
)

// TestShortIdleTimeoutDefaultAndSetting: sessions.short_idle_timeout is 12 hours unless set, and a value set in the
// environment is read.
func TestShortIdleTimeoutDefaultAndSetting(t *testing.T) {
	cfg, err := Load("", env(map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sessions.ShortIdleTimeout.D() != 12*time.Hour {
		t.Errorf("default short idle = %v, want 12h", cfg.Sessions.ShortIdleTimeout.D())
	}
	cfg, err = Load("", env(map[string]string{"SELFCLOUD_SESSIONS_SHORT_IDLE_TIMEOUT": "2h"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sessions.ShortIdleTimeout.D() != 2*time.Hour {
		t.Errorf("short idle = %v, want 2h", cfg.Sessions.ShortIdleTimeout.D())
	}
}

// TestShortIdleTimeoutRange: the short limit must be more than 0 and not more than sessions.idle_timeout, and a
// start-up with another value is refused.
func TestShortIdleTimeoutRange(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"zero":                  {"SELFCLOUD_SESSIONS_SHORT_IDLE_TIMEOUT": "0s"},
		"negative":              {"SELFCLOUD_SESSIONS_SHORT_IDLE_TIMEOUT": "-1h"},
		"over the default idle": {"SELFCLOUD_SESSIONS_SHORT_IDLE_TIMEOUT": "169h"},
		"over a set idle":       {"SELFCLOUD_SESSIONS_IDLE_TIMEOUT": "6h", "SELFCLOUD_SESSIONS_SHORT_IDLE_TIMEOUT": "12h"},
	} {
		if _, err := Load("", env(vars)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Equal to the idle limit is allowed.
	if _, err := Load("", env(map[string]string{"SELFCLOUD_SESSIONS_IDLE_TIMEOUT": "6h",
		"SELFCLOUD_SESSIONS_SHORT_IDLE_TIMEOUT": "6h"})); err != nil {
		t.Errorf("equal to idle_timeout refused: %v", err)
	}
}
