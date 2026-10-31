// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// TestSearchRebuildEmptiesAndQueues: the command runs on a fresh installation, which has no folders to queue, and reports
// how many folder ranges wait for the search job.
func TestSearchRebuildEmptiesAndQueues(t *testing.T) {
	t.Setenv("SELFCLOUD_DATA_DIR", t.TempDir())
	code, out, errOut := runCmd(t, "admin", "search-rebuild")
	if code != 0 {
		t.Fatalf("search-rebuild = %d %q %q", code, out, errOut)
	}
	if !strings.Contains(out, "search index emptied; 0 folder ranges queued") {
		t.Errorf("output = %q", out)
	}
}
