// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"os"
	"testing"

	"github.com/shukiv/selfcloud/internal/auth"
)

// TestMain lowers the password hash cost for the whole package: its fixtures make many accounts and users, and
// each password hash otherwise costs 64 MiB of argon2id.
func TestMain(m *testing.M) {
	auth.UseTestHashCost()
	os.Exit(m.Run())
}
