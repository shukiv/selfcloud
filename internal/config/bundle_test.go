// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"strings"
	"testing"
)

// bundleErrs runs validateBundle and returns the messages it reports.
func bundleErrs(b BundleConfig) []string {
	var out []string
	validateBundle(b, func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) })
	return out
}

func TestBundleDefaultsAreValid(t *testing.T) {
	t.Parallel()
	if errs := bundleErrs(Defaults().Bundle); len(errs) != 0 {
		t.Fatalf("the defaults are refused: %v", errs)
	}
	if d := Defaults().Bundle; d.MaxDisk != 30*GiB || d.MaxDiskPerAccount != 10*GiB {
		t.Errorf("default bundle disk = %d and %d, want 30 GiB and 10 GiB", d.MaxDisk, d.MaxDiskPerAccount)
	}
}

// TestBundleRefusals: each bad value is refused with a message that names its key.
func TestBundleRefusals(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		mutate func(*BundleConfig)
		want   string
	}{
		"no disk budget":            {func(b *BundleConfig) { b.MaxDisk = 0 }, "bundle.max_disk"},
		"negative disk budget":      {func(b *BundleConfig) { b.MaxDisk = -1 }, "bundle.max_disk"},
		"no account share":          {func(b *BundleConfig) { b.MaxDiskPerAccount = 0 }, "bundle.max_disk_per_account"},
		"share over the budget":     {func(b *BundleConfig) { b.MaxDiskPerAccount = b.MaxDisk + 1 }, "bundle.max_disk_per_account"},
		"share over a small budget": {func(b *BundleConfig) { b.MaxDisk = 1 << 20; b.MaxDiskPerAccount = 2 << 20 }, "bundle.max_disk_per_account"},
	}
	for name, c := range cases {
		b := Defaults().Bundle
		c.mutate(&b)
		errs := bundleErrs(b)
		if len(errs) == 0 || !strings.Contains(strings.Join(errs, "; "), c.want) {
			t.Errorf("%s: %v, want a message about %q", name, errs, c.want)
		}
	}
}

// TestBundleSettingsFromEnvironment: the keys are read from the environment as the other blocks are, and a bad value
// stops the load.
func TestBundleSettingsFromEnvironment(t *testing.T) {
	cfg, err := Load("", env(map[string]string{
		"SELFCLOUD_BUNDLE_MAX_DISK":             "2GiB",
		"SELFCLOUD_BUNDLE_MAX_DISK_PER_ACCOUNT": "512MiB",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bundle.MaxDisk != 2*GiB || cfg.Bundle.MaxDiskPerAccount != 512*MiB {
		t.Errorf("bundle disk = %d and %d, want 2 GiB and 512 MiB", cfg.Bundle.MaxDisk, cfg.Bundle.MaxDiskPerAccount)
	}
	if _, err := Load("", env(map[string]string{"SELFCLOUD_BUNDLE_MAX_DISK_PER_ACCOUNT": "0"})); err == nil {
		t.Error("a zero per-account share was accepted")
	}
}
