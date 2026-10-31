// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/thumb"
)

// derivedErrs runs validateDerived and returns the messages it reports.
func derivedErrs(d DerivedConfig) []string {
	var out []string
	validateDerived(d, func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) })
	return out
}

func TestDerivedDefaultsAreValid(t *testing.T) {
	t.Parallel()
	if errs := derivedErrs(Defaults().Derived); len(errs) != 0 {
		t.Fatalf("the defaults are refused: %v", errs)
	}
}

func TestDerivedRefusals(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		mutate func(*DerivedConfig)
		want   string
	}{
		"no budget":                {func(d *DerivedConfig) { d.MaxBytes = 0 }, "max_bytes"},
		"size too small":           {func(d *DerivedConfig) { d.ThumbSizes = []int{16} }, "outside 32 to 4096"},
		"size listed twice":        {func(d *DerivedConfig) { d.ThumbSizes = []int{256, 256} }, "listed twice"},
		"no sizes":                 {func(d *DerivedConfig) { d.ThumbSizes = nil }, "one to four"},
		"no pixel cap":             {func(d *DerivedConfig) { d.MaxPixels = 0 }, "max_pixels"},
		"memory under floor":       {func(d *DerivedConfig) { d.MemoryBytes = 64 << 20 }, "floor"},
		"no timeout":               {func(d *DerivedConfig) { d.Timeout = 0 }, "timeout"},
		"no workers":               {func(d *DerivedConfig) { d.Workers = 0 }, "workers"},
		"no cpu":                   {func(d *DerivedConfig) { d.CPUSeconds = 0 }, "cpu_seconds"},
		"account share over queue": {func(d *DerivedConfig) { d.ConvertAccountQueue = 33 }, "convert_account_queue"},
		"account share none":       {func(d *DerivedConfig) { d.ConvertAccountQueue = 0 }, "convert_account_queue"},
		"budget under one output":  {func(d *DerivedConfig) { d.MaxBytes = 100 * MiB }, "max_convert_bytes"},
	}
	for name, c := range cases {
		d := Defaults().Derived
		c.mutate(&d)
		errs := derivedErrs(d)
		if len(errs) == 0 || !strings.Contains(strings.Join(errs, "; "), c.want) {
			t.Errorf("%s: %v, want a message about %q", name, errs, c.want)
		}
	}
}

// TestDerivedMemoryFloorFollowsThePixelCap: the floor is thumb.MemoryFloor, so a larger cap needs more memory.
func TestDerivedMemoryFloorFollowsThePixelCap(t *testing.T) {
	t.Parallel()
	d := Defaults().Derived
	// 200 Mpx needs about 3.2 GB of decoder memory at 16 bytes a pixel; the default is 1 GiB.
	d.MaxPixels = 200_000_000
	if errs := derivedErrs(d); len(errs) == 0 {
		t.Fatal("a 200 Mpx cap with the default memory was accepted")
	}
	d.MemoryBytes = ByteSize(thumb.MemoryFloor(d.MaxPixels, int64(d.MaxSourceBytes), 1024))
	if errs := derivedErrs(d); len(errs) != 0 {
		t.Fatalf("the exact floor is refused: %v", errs)
	}
}

// The validator refuses a thumb_sizes entry the memory cannot serve, and names it: a 4096 thumbnail needs more
// than the 256 one, so a memory that is enough for 256 alone is not enough for the list.
func TestDerivedRefusesASizeTheMemoryCannotServe(t *testing.T) {
	t.Parallel()
	d := Defaults().Derived
	d.ThumbSizes = []int{256, 4096}
	d.MemoryBytes = ByteSize(thumb.MemoryFloor(d.MaxPixels, int64(d.MaxSourceBytes), 256))
	errs := derivedErrs(d)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "; "), "thumb_sizes entry 4096") {
		t.Fatalf("errors = %v; want one naming the 4096 entry", errs)
	}
	d.MemoryBytes = ByteSize(thumb.MemoryFloor(d.MaxPixels, int64(d.MaxSourceBytes), 4096))
	if errs := derivedErrs(d); len(errs) != 0 {
		t.Fatalf("the floor for 4096 is refused: %v", errs)
	}
}
