// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strings"
)

// AudioTags are the values read from an audio file (migration 0041). DurationMS is 0 when it is not known.
type AudioTags struct {
	Title      string
	Artist     string
	Album      string
	DurationMS int64
}

// UpsertAudioTags stores the values of a blob, replacing any earlier read of it. The blob must be the account's: the
// statement selects it by account, so a blob of another account stores nothing and reports no error, and the caller
// reads the derived row, which says the same.
func (tx *Tx) UpsertAudioTags(ctx context.Context, accountID, blobID string, t AudioTags) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO audio_tags (blob_id, account_id, title, artist, album, duration_ms)
		SELECT b.id, b.account_id, ?, ?, ?, ? FROM blobs b WHERE b.id = ? AND b.account_id = ?
		ON CONFLICT (blob_id) DO UPDATE SET title = excluded.title, artist = excluded.artist,
		album = excluded.album, duration_ms = excluded.duration_ms`,
		t.Title, t.Artist, t.Album, t.DurationMS, blobID, accountID)
	return err
}

// AudioTagsKind is the kind of the derived row that reads an audio file's tags.
const AudioTagsKind = "audio_tags"

// AudioTagsFor returns the tags of the nodes among nodeIDs whose blob has a read under converter that is ready, keyed by
// node ID. The nodes are of accountID only. A node with no ready read under that converter, or not an audio file, is
// absent: a values row left by an older converter, or by a read that has since failed, is not shown. It is one query per
// listing page or batch: the IDs are bound, not joined into the text.
func (s *Store) AudioTagsFor(ctx context.Context, accountID string, nodeIDs []string, converter string) (map[string]AudioTags, error) {
	out := map[string]AudioTags{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	args := []any{AudioTagsKind, converter, DerivedReady, accountID}
	marks := make([]string, len(nodeIDs))
	for i, id := range nodeIDs {
		marks[i] = "?"
		args = append(args, id)
	}
	rows, err := s.r.QueryContext(ctx, `SELECT n.id, t.title, t.artist, t.album, t.duration_ms FROM nodes n
		JOIN audio_tags t ON t.blob_id = n.blob_id AND t.account_id = n.account_id
		JOIN derived d ON d.blob_id = n.blob_id AND d.account_id = n.account_id
		WHERE d.kind = ? AND d.converter = ? AND d.state = ?
		AND n.account_id = ? AND n.id IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var t AudioTags
		if err := rows.Scan(&id, &t.Title, &t.Artist, &t.Album, &t.DurationMS); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}
