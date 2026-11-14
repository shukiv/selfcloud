// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"strings"
	"testing"
)

// zipErrs runs validateZip and returns the messages it reports.
func zipErrs(z ZipConfig) []string {
	var out []string
	validateZip(z, func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) })
	return out
}

func TestZipVisitorDefaultsAreValid(t *testing.T) {
	t.Parallel()
	if errs := zipErrs(Defaults().Zip); len(errs) != 0 {
		t.Fatalf("the defaults are refused: %v", errs)
	}
	d := Defaults().Zip
	if d.VisitorMaxPerAccount != 3 {
		t.Errorf("default zip.visitor_max_per_account = %d, want 3", d.VisitorMaxPerAccount)
	}
	if d.VisitorMaxBytes != 10*GiB {
		t.Errorf("default zip.visitor_max_bytes = %d, want 10 GiB", d.VisitorMaxBytes)
	}
	// The byte cap is not tied to the disk budget: a visitor zip is held to half the disk at run time, so the cap may
	// be larger than the whole disk (as max_per_principal is not tied to a count of zips).
	z := Defaults().Zip
	z.MaxDisk = 1 << 20
	z.MaxBytes = 1 << 20
	z.MaxDiskPerAccount = 1 << 20
	z.VisitorMaxBytes = 10 * GiB
	if errs := zipErrs(z); len(errs) != 0 {
		t.Errorf("a visitor byte cap over the disk is refused: %v", errs)
	}
}

// TestZipVisitorRefusals: each bad visitor limit is refused with a message that names its key.
func TestZipVisitorRefusals(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		mutate func(*ZipConfig)
		want   string
	}{
		"no visitor per-account cap":   {func(z *ZipConfig) { z.VisitorMaxPerAccount = 0 }, "zip.visitor_max_per_account"},
		"negative visitor per-account": {func(z *ZipConfig) { z.VisitorMaxPerAccount = -1 }, "zip.visitor_max_per_account"},
		"no visitor byte cap":          {func(z *ZipConfig) { z.VisitorMaxBytes = 0 }, "zip.visitor_max_bytes"},
		"negative visitor byte cap":    {func(z *ZipConfig) { z.VisitorMaxBytes = -1 }, "zip.visitor_max_bytes"},
	}
	for name, c := range cases {
		z := Defaults().Zip
		c.mutate(&z)
		errs := zipErrs(z)
		if len(errs) == 0 || !strings.Contains(strings.Join(errs, "; "), c.want) {
			t.Errorf("%s: %v, want a message about %q", name, errs, c.want)
		}
	}
}

// TestZipVisitorSettingsFromEnvironment: the visitor keys are read from the environment as the other zip keys are,
// and a bad value stops the load.
func TestZipVisitorSettingsFromEnvironment(t *testing.T) {
	cfg, err := Load("", env(map[string]string{
		"SELFCLOUD_ZIP_VISITOR_MAX_PER_ACCOUNT": "5",
		"SELFCLOUD_ZIP_VISITOR_MAX_BYTES":       "2GiB",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Zip.VisitorMaxPerAccount != 5 || cfg.Zip.VisitorMaxBytes != 2*GiB {
		t.Errorf("visitor limits = %d and %d, want 5 and 2 GiB", cfg.Zip.VisitorMaxPerAccount, cfg.Zip.VisitorMaxBytes)
	}
	if _, err := Load("", env(map[string]string{"SELFCLOUD_ZIP_VISITOR_MAX_PER_ACCOUNT": "0"})); err == nil {
		t.Error("a zero visitor per-account cap was accepted")
	}
}
