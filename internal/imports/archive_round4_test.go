// SPDX-License-Identifier: AGPL-3.0-or-later

package imports

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// storedZip builds a zip of the named files, stored without compression, so the archive is as large as its names.
func storedZip(t *testing.T, names []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(nil)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A deep archive at the entry cap, every entry with its own deep path, scans in memory bounded by its paths: the scan keeps
// one key per entry, not one per prefix of each path (round 4, N2). The file that clashes with the first entry's top folder
// still refuses the archive.
func TestDeepPathsScanInBoundedMemory(t *testing.T) {
	// Each name is about 3.2 KB, under the 4 KiB a central-directory header may take (F18), so the scan reaches the paths.
	const entries, depth, seg = 2000, 32, 100
	var names []string
	pathBytes := 0
	for i := 0; i < entries-1; i++ {
		parts := make([]string, depth)
		for k := range parts {
			parts[k] = fmt.Sprintf("%0*d", seg, i*depth+k)
		}
		name := parts[0]
		for _, p := range parts[1:] {
			name += "/" + p
		}
		pathBytes += len(name)
		names = append(names, name)
	}
	// The clash: a file at the top-level name of the first entry, which is a folder of the archive.
	names = append(names, fmt.Sprintf("%0*d", seg, 0))
	data := storedZip(t, names)
	caps := Caps{MaxEntries: entries, MaxDepth: depth + 1, MaxRatio: 100, MaxFile: 1 << 30, MaxTotal: 1 << 30,
		MaxPathBytes: 64 << 20}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := Scan(bytes.NewReader(data), int64(len(data)), caps)
	runtime.ReadMemStats(&after)

	// The bound is on the bytes allocated, which include the short-lived name parsing of each entry (about 9 times the paths
	// here). The old scan allocated about 25 times the paths, because it kept a key for every folder above each entry, so the
	// bound is 12 times the paths plus 8 MiB: linear in the paths, not in their depth.
	bound := uint64(12*pathBytes + 8<<20)
	if got := after.TotalAlloc - before.TotalAlloc; got > bound {
		t.Fatalf("the scan allocated %d bytes for %d bytes of paths; the bound is %d", got, pathBytes, bound)
	}
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != apierr.InvalidPath {
		t.Fatalf("the clash was not refused as an invalid path: %v", err)
	}
}

// An archive whose entries' paths hold more bytes than max_path_bytes is refused at the scan, however few entries it has
// (round 4, N2).
func TestPathBytesCapRefusesALongArchive(t *testing.T) {
	var names []string
	for i := 0; i < 10; i++ {
		names = append(names, fmt.Sprintf("%0*d", 200, i))
	}
	data := storedZip(t, names)
	caps := Caps{MaxEntries: 100, MaxDepth: 16, MaxRatio: 100, MaxFile: 1 << 30, MaxTotal: 1 << 30, MaxPathBytes: 1000}
	_, err := Scan(bytes.NewReader(data), int64(len(data)), caps)
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != apierr.TooLarge {
		t.Fatalf("2000 bytes of paths under a cap of 1000 were not refused as too large: %v", err)
	}
}
