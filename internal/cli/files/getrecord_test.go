// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// TestDownloadRecordKeepsANodeOrAZipAndNothingElse: a part file's record names what it was read from, a file or a zip.
// A record that names anything else is not read, so the part file is not continued.
func TestDownloadRecordKeepsANodeOrAZipAndNothingElse(t *testing.T) {
	isolate(t, testAccountKey)
	part := filepath.Join(t.TempDir(), "x.zip.part")
	for _, tc := range []struct {
		name string
		key  string
		want bool
	}{
		{"a node", id.Derive("nod", "record-node"), true},
		{"a zip", id.Derive("zip", "record-zip"), true},
		{"a folder", id.Derive("fld", "record-folder"), false},
		{"free text", "../../etc", false},
	} {
		err := saveDownloadRecord(downloadRecord{Part: part, Server: "https://example.test", Node: tc.key,
			ETag: `"abc"`, Size: 10, StartedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		_, err = loadDownloadRecord(part)
		if got := err == nil; got != tc.want {
			t.Errorf("%s: record read = %v, want %v", tc.name, got, tc.want)
		}
	}
}
