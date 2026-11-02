// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"slices"
	"testing"
)

// TestPlaylistIndexes: an account's deletion finds its playlists by account_id, so that column is indexed; an entry's node
// is never looked up in the entries table (the reach is read from nodes), so no index names it.
func TestPlaylistIndexes(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	rows, err := s.r.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name IN ('playlists', 'playlist_entries')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names, "playlists_account") {
		t.Errorf("no index on playlists.account_id; indexes are %v", names)
	}
	if slices.Contains(names, "playlist_entries_node") {
		t.Errorf("playlist_entries_node is unread and should be dropped; indexes are %v", names)
	}
}
