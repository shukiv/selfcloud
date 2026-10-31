// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"os"
	"testing"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/thumb"
)

// TestMain lowers the password hash cost for the whole package: its fixtures make many accounts and users, and
// each password hash otherwise costs 64 MiB of argon2id.
func TestMain(m *testing.M) {
	// The thumbnail decoder runs as this binary's `thumb` subcommand, as the server's does.
	if len(os.Args) > 1 && os.Args[1] == "thumb" {
		os.Exit(thumb.Child(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	auth.UseTestHashCost()
	os.Exit(m.Run())
}
