// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import "context"

// secureDeleteFTS turns on FTS5's secure-delete option for every full-text table of search (names and content). With it
// set, a deleted row's words are overwritten in the table's segments instead of being left in them, so they are not in the
// database file after a checkpoint, nor in a VACUUM INTO copy (decision 46, review S2). It is a table setting, stored in
// the table, so running it at every open is harmless. The cost is measured in decision 46.
func (s *Store) secureDeleteFTS(ctx context.Context) error {
	for _, table := range []string{"search_words", "search_trigram", "search_text_fts"} {
		if _, err := s.w.ExecContext(ctx, "INSERT INTO "+table+"("+table+", rank) VALUES ('secure-delete', 1)"); err != nil {
			return err
		}
	}
	return nil
}
