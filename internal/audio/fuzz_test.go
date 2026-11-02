// SPDX-License-Identifier: AGPL-3.0-or-later

package audio

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The readers take a file that anyone may upload, so each format's entry point is fuzzed with the test files as seeds. The
// inner read is fuzzed, not Read: Read recovers a panic, and a fuzz run must see a panic as a failure. A fuzz run finds only
// what its time allows; the seeds are the regression set and run on every go test.
func fuzzSeeds(f *testing.F) {
	names, err := filepath.Glob("testdata/*")
	if err != nil {
		f.Fatal(err)
	}
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, true)
		f.Add(b, false)
	}
}

// FuzzRead is the whole reader: the format is sniffed from the bytes, so every format is reached from one target.
func FuzzRead(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, b []byte, picture bool) {
		if len(b) > 1<<20 {
			t.Skip("larger than the seeds need")
		}
		res, err := read(bytes.NewReader(b), picture)
		if err == nil && res.Tags.DurationMS > maxDuration {
			t.Fatalf("duration %d is over a week", res.Tags.DurationMS)
		}
	})
}

// FuzzFormats gives each reader its own entry, so a corpus for one format is not lost to the sniffing of the others.
func FuzzFormats(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, b []byte, picture bool) {
		if len(b) > 1<<20 {
			t.Skip("larger than the seeds need")
		}
		// Each reader gets its own reader over the bytes: a failure here is that reader's own panic, not another format's.
		for _, rd := range []func(*reader, bool) (Result, error){readFLAC, readOgg, readMP4, readMP3} {
			d, err := newReader(bytes.NewReader(b))
			if err != nil {
				continue
			}
			_, _ = rd(d, picture)
		}
	})
}
