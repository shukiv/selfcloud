// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"testing"
	"time"
)

// TestShortIdleUnsetFollowsIdleTimeout: sessions.short_idle_timeout unset gives the shorter of 12 hours and
// sessions.idle_timeout, so an installation whose idle timeout is under 12 hours still starts (decision 75).
func TestShortIdleUnsetFollowsIdleTimeout(t *testing.T) {
	cfg, err := Load("", env(map[string]string{"SELFCLOUD_SESSIONS_IDLE_TIMEOUT": "6h"}))
	if err != nil {
		t.Fatalf("idle 6h with the short key unset refused: %v", err)
	}
	if got := cfg.Sessions.ShortIdle().D(); got != 6*time.Hour {
		t.Errorf("idle 6h, short unset: effective short idle = %v, want 6h", got)
	}
	cfg, err = Load("", env(map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Sessions.ShortIdle().D(); got != 12*time.Hour {
		t.Errorf("defaults: effective short idle = %v, want 12h", got)
	}
}

// TestShortIdleExplicitSetting: a value set in the environment is the effective one.
func TestShortIdleExplicitSetting(t *testing.T) {
	cfg, err := Load("", env(map[string]string{"SELFCLOUD_SESSIONS_SHORT_IDLE_TIMEOUT": "1h"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Sessions.ShortIdle().D(); got != time.Hour {
		t.Errorf("explicit 1h: effective short idle = %v", got)
	}
}

// TestShortIdleRange: an explicit value must be more than 0 and not more than sessions.idle_timeout, and a negative
// value is refused. Equal to the idle limit is allowed.
func TestShortIdleRange(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"negative":              {"SELFCLOUD_SESSIONS_SHORT_IDLE_TIMEOUT": "-1h"},
		"over the default idle": {"SELFCLOUD_SESSIONS_SHORT_IDLE_TIMEOUT": "169h"},
		"over a set idle":       {"SELFCLOUD_SESSIONS_IDLE_TIMEOUT": "6h", "SELFCLOUD_SESSIONS_SHORT_IDLE_TIMEOUT": "12h"},
	} {
		if _, err := Load("", env(vars)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	cfg, err := Load("", env(map[string]string{"SELFCLOUD_SESSIONS_IDLE_TIMEOUT": "6h",
		"SELFCLOUD_SESSIONS_SHORT_IDLE_TIMEOUT": "6h"}))
	if err != nil {
		t.Fatalf("equal to idle_timeout refused: %v", err)
	}
	if got := cfg.Sessions.ShortIdle().D(); got != 6*time.Hour {
		t.Errorf("equal to idle: effective = %v", got)
	}
}
