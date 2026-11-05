// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"strings"
	"testing"
)

// TestUserReadsSearchTheEmailChangeLinks reads the query plan of the user reads that carry pending_until (userSelectCols):
// the user by ID and the account's user list. The correlated subquery on verifications must reach its rows through the
// verifications_user index, not by scanning the table, since every user read runs it. The plans are read on an empty
// database, as TestCommentsSQLDoesNotScan does, because the choice of index comes from the schema.
func TestUserReadsSearchTheEmailChangeLinks(t *testing.T) {
	s := openTest(t)
	cases := []struct {
		name string
		sql  string
		args []any
	}{
		{"user by ID", "SELECT " + userSelectCols + " FROM users WHERE id = ?", []any{"usr_a"}},
		{"account's user list, first page", "SELECT " + userSelectCols + " FROM users WHERE account_id = ? ORDER BY created_at, id LIMIT ?",
			[]any{"acc_a", 51}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			rows, err := s.r.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+c.sql, c.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, detail)
				if detail == "SCAN v" || strings.HasPrefix(detail, "SCAN v ") ||
					detail == "SCAN verifications" || strings.HasPrefix(detail, "SCAN verifications ") {
					t.Errorf("%s scans verifications: %q\nplan: %v", c.name, detail, plan)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(plan, " | ")
			if !strings.Contains(joined, "verifications_user") {
				t.Errorf("%s does not use the verifications_user index\nplan: %v", c.name, plan)
			}
			t.Logf("plan: %v", plan)
		})
	}
}
