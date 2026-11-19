// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"strings"
	"testing"
)

// TestCreatedIndexServesOnlyTheNewestOrder: the created index's partial condition carries a term only the newest order
// writes (created_at > -1), so no other query can be planned onto it (decision 234). SQLite uses a partial index only
// when the query's WHERE contains its terms: a plain account read, a children listing and an unpinned recursive subtree
// step must all keep off it. Mutation: dropping the term from the index's condition draws the account read and the
// recursive step onto it, as the first round found on the six pinned walks.
func TestCreatedIndexServesOnlyTheNewestOrder(t *testing.T) {
	s, _ := searchFixture(t)
	if err := exec(s, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 5000)
		INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, mime_type, created_at, modified_at)
		SELECT printf('nod_p%05d', i), 'acc_a', 'fld_a', 'nod_root', printf('file %05d.txt', i), printf('k%05d', i), 'file',
			i % 4096, 'text/plain', i, i FROM n`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		query string
		args  []any
	}{
		{"account read", `SELECT id FROM nodes WHERE account_id = ? AND trashed_at IS NULL`, []any{"acc_a"}},
		{"children listing", `SELECT n.* FROM nodes n WHERE n.account_id = ? AND n.parent_id = ? AND n.trashed_at IS NULL
			ORDER BY n.name_key, n.id LIMIT ?`, []any{"acc_a", "nod_root", 26}},
		{"recursive subtree step", `WITH RECURSIVE sub(nid) AS (
				SELECT id FROM nodes WHERE account_id = ? AND id = ?
				UNION ALL
				SELECT c.id FROM nodes c JOIN sub ON c.parent_id = sub.nid WHERE c.account_id = ? AND c.trashed_at IS NULL
			)
			SELECT COUNT(*) FROM sub`, []any{"acc_a", "nod_root", "acc_a"}},
	} {
		p := explainPlan(t, s, c.query, c.args...)
		t.Logf("%s: %s", c.name, p)
		if strings.Contains(p, "nodes_created") {
			t.Errorf("%s: plan %q uses nodes_created; only the newest order may", c.name, p)
		}
	}
}
