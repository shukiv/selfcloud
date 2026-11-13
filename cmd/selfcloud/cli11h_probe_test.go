// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"testing"

	"github.com/shukiv/selfcloud/internal/thumb"
)

// TestMain runs the thumbnail decoder when the derived pool starts this binary as `thumb`, and the tests otherwise.
// The pool starts the binary that runs the server, which in a test is this one (internal/api/main_test.go does the same
// for its package).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "thumb" {
		os.Exit(thumb.Child(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func init() {
	// thumbnail waits for a real image's preview, which the derived pool makes from the PNG the probe puts.
	fileProbes["thumbnail"] = func(p *fileProbe) []string {
		nodeID := valueOf(p.t, p.do("put", p.picture("thumbnail.png", 64, 64), "--folder", p.folder, "/"), "id=")
		return []string{nodeID, p.t.TempDir(), "--wait-timeout", "30s"}
	}
	// Office is off on the probe server: the POST answers not found, and the GET is never reached. The probe still records
	// the command's route, which is what TestRemoteCommandsCallTheirRoute is about.
	fileProbes["export"] = func(p *fileProbe) []string {
		return []string{p.touch("/export.pdf"), "--format", "pdf"}
	}
	probeExit["export"] = 4
}
