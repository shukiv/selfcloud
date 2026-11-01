// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"modernc.org/sqlite"

	"github.com/shukiv/selfcloud/internal/names"
)

// NameCollation is the name of the collation that orders names in a listing sorted by name (names.Compare). The
// listing's index on name is built with it (migration 0035), so every connection must register it before it opens the
// database. The registration runs when the package loads, which is before any connection is opened.
const NameCollation = "selfcloud_name"

func init() {
	sqlite.MustRegisterCollationUtf8(NameCollation, names.Compare)
}
