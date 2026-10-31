// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// rawExec changes the database under a running server, as another writer would. The copy holds no write between its
// steps, so the change lands between two of them.
func rawExec(t *testing.T, s *Server, q string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

// rawCount counts the rows of a query that takes the folder ID as its one argument.
func rawCount(t *testing.T, s *Server, q, folderID string) int {
	t.Helper()
	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(q, folderID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// structureCase is one change to the source tree, made between two steps of a copy: at the first step of stage
// (batch or publish), with the folder's ID as the first argument of change.
type structureCase struct {
	name   string
	stage  string
	change string // SQL run as raw statement, with the folder ID as argument
	// wantFail: the copy must fail with source_changed. Otherwise it must succeed.
	wantFail bool
	// check runs after a successful copy (nil for none).
	check func(t *testing.T, fx *chunkedFixture)
}

// TestSourceChangesDuringACopy: a copy never succeeds with a tree whose structure differs from its snapshot, and never
// hides the difference: a snapshot node trashed, deleted or moved fails the copy with source_changed and nothing visible,
// at the batch that would copy it and at the publish. A rename is not structural and copies what is read. A file moved
// in after the snapshot is simply not copied.
func TestSourceChangesDuringACopy(t *testing.T) {
	t.Parallel()
	cases := []structureCase{
		{name: "trashed after the snapshot", stage: "batch", wantFail: true,
			change: `UPDATE nodes SET trashed_at = 1 WHERE name = 'a.txt' AND folder_id = ?`},
		{name: "deleted after the snapshot", stage: "batch", wantFail: true,
			change: `DELETE FROM nodes WHERE name = 'a.txt' AND folder_id = ?`},
		{name: "moved deeper", stage: "batch", wantFail: true,
			change: `UPDATE nodes SET parent_id = (SELECT id FROM nodes WHERE name = 'sub' AND folder_id = ?)
				WHERE name = 'a.txt' AND folder_id = ?`},
		{name: "moved out of the tree", stage: "batch", wantFail: true,
			change: `UPDATE nodes SET parent_id = (SELECT id FROM nodes WHERE folder_id = ? AND parent_id IS NULL)
				WHERE name = 'a.txt' AND folder_id = ?`},
		{name: "directory moved after its batch", stage: "publish", wantFail: true,
			change: `UPDATE nodes SET parent_id = (SELECT id FROM nodes WHERE folder_id = ? AND parent_id IS NULL)
				WHERE name = 'sub' AND folder_id = ?`},
		{name: "trashed after its batch", stage: "publish", wantFail: true,
			change: `UPDATE nodes SET trashed_at = 1 WHERE name = 'b.txt' AND folder_id = ?`},
		{name: "deleted after its batch", stage: "publish", wantFail: true,
			change: `DELETE FROM nodes WHERE name = 'b.txt' AND folder_id = ?`},
		{name: "renamed is copied as read", stage: "batch",
			change: `UPDATE nodes SET name = 'renamed.txt', name_key = 'renamed.txt' WHERE name = 'a.txt' AND folder_id = ?`,
			check: func(t *testing.T, fx *chunkedFixture) {
				if n := rawCount(t, fx.s, `SELECT COUNT(*) FROM nodes WHERE name = 'renamed.txt' AND folder_id = ?`,
					fx.f.ID); n != 2 {
					t.Errorf("renamed.txt in the folder = %d, want 2 (the source and the copy)", n)
				}
			}},
		{name: "file moved in after the snapshot is not copied", stage: "batch",
			change: "",
			check: func(t *testing.T, fx *chunkedFixture) {
				if n := rawCount(t, fx.s, `SELECT COUNT(*) FROM nodes WHERE name = 'new.txt' AND folder_id = ?`,
					fx.f.ID); n != 1 {
					t.Errorf("new.txt in the folder = %d, want 1 (the source only)", n)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newChunkedFixture(t)
			fx.s.cfg.Copy.MaxNodes = 100
			before := usageOf(t, fx.s, fx.key, fx.acc.ID)
			fx.queue(t)
			done := false
			fx.s.copyFault = func(stage string) error {
				if stage != tc.stage || done {
					return nil
				}
				done = true
				if tc.change == "" {
					putNode(t, fx.s, fx.key, fx.f.ID, "/tree/new.txt", "n")
					return nil
				}
				args := []any{fx.f.ID}
				if strings.Count(tc.change, "?") == 2 {
					args = append(args, fx.f.ID)
				}
				rawExec(t, fx.s, tc.change, args...)
				return nil
			}
			done2 := fx.run(t)
			if tc.wantFail {
				if done2.Status != store.JobFailed {
					t.Fatalf("job = %+v, want failed", done2)
				}
				if !strings.Contains(done2.Error, "source_changed") {
					t.Errorf("job error = %q, want source_changed", done2.Error)
				}
				// The source's own change may move its top level, so the copy's state is checked, not the folder's.
				u := usageOf(t, fx.s, fx.key, fx.acc.ID)
				if u.ReservedBytes != 0 || u.UsedBytes != before.UsedBytes || u.UsedFiles != before.UsedFiles {
					t.Errorf("usage = %+v, was %+v; want no reservation and no change", u, before)
				}
				if _, err := fx.s.store.CopyJobOf(context.Background(), fx.job.ID); err == nil {
					t.Error("copy row still there after the failed copy")
				}
				wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.job.Target, fx.key, nil), 404, apierr.NotFound)
				return
			}
			if done2.Status != store.JobSucceeded {
				t.Fatalf("job = %+v, want succeeded", done2)
			}
			if tc.check != nil {
				tc.check(t, fx)
			}
		})
	}
}
