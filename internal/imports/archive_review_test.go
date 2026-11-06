// SPDX-License-Identifier: AGPL-3.0-or-later

package imports

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"math"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// rawZip writes one stored entry whose header declares the given sizes, with no data behind them. It is a zip whose
// central directory says more than the archive holds, which a reader must refuse without reading the entry.
func rawZip(t *testing.T, name string, uncompressed, compressed uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fh := &zip.FileHeader{Name: name, Method: zip.Store, UncompressedSize64: uncompressed, CompressedSize64: compressed}
	if _, err := w.CreateRaw(fh); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A declared size above the largest signed int is refused: read as a negative number it would pass every cap.
func TestZipSizeOverMaxInt64IsRefused(t *testing.T) {
	data := rawZip(t, "big.bin", 1<<63, 1<<63)
	if _, err := scanBytes(t, data, caps()); err == nil {
		t.Fatal("a zip entry declaring a size over MaxInt64 was accepted")
	}
}

// A compressed size larger than the archive itself is not a real entry.
func TestZipCompressedLargerThanArchiveIsRefused(t *testing.T) {
	data := rawZip(t, "small.bin", 5, 1<<40)
	if _, err := scanBytes(t, data, caps()); err == nil {
		t.Fatal("a zip entry whose compressed size is larger than the archive was accepted")
	}
}

// A tar.gz whose extended (PAX) headers, which the reader consumes and which are not entries, expand far past the ratio
// is refused: the bytes of a header that is not an entry count the same as those of an entry.
func TestTarGzSkippedHeadersCountTowardTheRatio(t *testing.T) {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	big := strings.Repeat("0", 500_000)
	for i := 0; i < 3; i++ {
		h := &tar.Header{Typeflag: tar.TypeReg, Name: []string{"a.txt", "b.txt", "c.txt"}[i], Mode: 0o644,
			PAXRecords: map[string]string{"comment": big}}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := scanBytes(t, gz.Bytes(), caps())
	ae := refusedWith(t, err, apierr.TooLarge)
	if ae.Details["reason"] != "ratio" {
		t.Fatalf("reason = %v, want ratio", ae.Details["reason"])
	}
}

// Headers that are not entries count toward the entry cap, so a tar of them cannot hold more than the caps allow.
func TestTarGlobalHeadersCountTowardTheEntryCap(t *testing.T) {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for i := 0; i < 1001; i++ {
		h := &tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax", PAXRecords: map[string]string{"comment": "x"}}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := scanBytes(t, raw.Bytes(), caps())
	ae := refusedWith(t, err, apierr.TooLarge)
	if ae.Details["reason"] != "entries" {
		t.Fatalf("reason = %v, want entries", ae.Details["reason"])
	}
}

// The ratio test divides, so a packed size near MaxInt64 does not overflow the product into a small number.
func TestOverRatioDoesNotOverflow(t *testing.T) {
	if !overRatio(101*1024, 1024, 100) {
		t.Fatal("101 KiB from 1 KiB packed at ratio 100 is over the ratio")
	}
	if overRatio(100*1024, 1024, 100) {
		t.Fatal("exactly 100 times the packed size is not over the ratio")
	}
	if overRatio(math.MaxInt64, math.MaxInt64, 100) {
		t.Fatal("a size equal to the packed size is not over a ratio of 100")
	}
}
