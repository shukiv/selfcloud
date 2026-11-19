// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"testing"
	"time"
)

// TestChangesSettings: the changes feed keeps 720 hours by default and admits 1000 waiters; the environment and the file
// set both, and the retention may not fall below an hour.
func TestChangesSettings(t *testing.T) {
	c, err := Load("", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Changes.Retention.D() != 720*time.Hour {
		t.Errorf("default retention = %v, want 720h", c.Changes.Retention.D())
	}
	if c.Changes.MaxWaiters != 1000 {
		t.Errorf("default max_waiters = %d, want 1000", c.Changes.MaxWaiters)
	}

	c, err = Load("", env(map[string]string{"SELFCLOUD_CHANGES_RETENTION": "48h", "SELFCLOUD_CHANGES_MAX_WAITERS": "50"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Changes.Retention.D() != 48*time.Hour || c.Changes.MaxWaiters != 50 {
		t.Errorf("env settings = %v / %d, want 48h / 50", c.Changes.Retention.D(), c.Changes.MaxWaiters)
	}

	p := writeFile(t, "changes:\n  retention: 24h\n  max_waiters: 10\n")
	c, err = Load(p, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Changes.Retention.D() != 24*time.Hour || c.Changes.MaxWaiters != 10 {
		t.Errorf("file settings = %v / %d, want 24h / 10", c.Changes.Retention.D(), c.Changes.MaxWaiters)
	}

	// The bounds: an hour is the shortest retention, and zero waiters is allowed (every wait answers at once).
	for _, vars := range []map[string]string{
		{"SELFCLOUD_CHANGES_RETENTION": "1h"},
		{"SELFCLOUD_CHANGES_MAX_WAITERS": "0"},
	} {
		if _, err := Load("", env(vars)); err != nil {
			t.Errorf("%v: %v", vars, err)
		}
	}
	for name, vars := range map[string]map[string]string{
		"retention too short": {"SELFCLOUD_CHANGES_RETENTION": "30m"},
		"retention zero":      {"SELFCLOUD_CHANGES_RETENTION": "0"},
		"waiters negative":    {"SELFCLOUD_CHANGES_MAX_WAITERS": "-1"},
	} {
		if _, err := Load("", env(vars)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
