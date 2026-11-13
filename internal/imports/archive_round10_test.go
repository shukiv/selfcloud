// SPDX-License-Identifier: AGPL-3.0-or-later

package imports

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

// A cancelled context stops a walk while it skips the entries before its cursor, and the error is the context's, not a
// refusal of the archive: a shutdown must not fail an import as a bad archive.
func TestWalkStopsOnACancelledContextDuringASkip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	skipped := func(e Entry, _ io.Reader) error { return nil }
	zipData := zipOf(t, map[string]string{"a": "1", "b": "2", "c": "3"}, []string{"a", "b", "c"})
	var tarEntries []tarEntry
	for _, n := range []string{"a", "b", "c"} {
		tarEntries = append(tarEntries, tarEntry{hdr: tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"})
	}
	tarData := tarOf(t, tarEntries, true)
	cases := []struct {
		name   string
		data   []byte
		format Format
	}{
		{"zip", zipData, Zip},
		{"tar.gz", tarData, TarGz},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Walk(ctx, bytes.NewReader(c.data), int64(len(c.data)), c.format, 2, skipped)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("a walk with a cancelled context, skipping 2 entries, returned %v; want context.Canceled", err)
			}
		})
	}
}
