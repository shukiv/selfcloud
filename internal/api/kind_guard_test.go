// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// nodeKindSite matches a comparison of a node's kind with one of the kinds, in Go or in SQL. The kinds are dir, file
// and shortcut. Folder kinds (team, personal), job kinds and principal kinds never match.
// Each match is one comparison, so a line with two of them counts twice.
var nodeKindSite = regexp.MustCompile(`(?:\.Kind|\bkind|\bKind)\s*(?:==|!=|<>|=)\s*(?:store\.|')?(?:Kind(?:Dir|File|Shortcut)|dir|file|shortcut)'?` +
	`|(?:==|!=|<>|=)\s*store\.Kind(?:Dir|File|Shortcut)\b`)

// reviewedNodeKindSites is how many node-kind comparisons each file holds, as reviewed for slice p3. Each one was read
// and decided: a check that accepts only a file or a directory refuses a shortcut (fails closed), and an SQL count of
// bytes counts files only, so a shortcut takes no quota. A new comparison, or a moved one, changes a count here, and
// the reviewer decides it again before the count is updated. docs/design.md section 15 (decision 24, p3) records them.
// Slice j2 added three, each reviewed for event emission and neither an access decision: copyTree reports a file.created for
// each node that is not a directory and counts them for the big-folder rule (transfer.go), the outbox skips a directory
// when it reports a subtree (store/webhook_events.go), and the threshold count excludes directories in SQL
// (store/webhook_events.go countSubtree). Decision 27 records them. Slice l1 and l2 added four in office.go, each
// refusing whatever is not a file (an editor opens a file only, and a shortcut or folder is never sent to it).
// Decision 31 records them. Slice l3 moved the PutFile and the lock operations' comparisons to office_locks.go, which
// refuse whatever is not a file for the same reason (decision 32). Slice l3b added one in store/nodes.go
// (changesLockedPlace: a directory's write passes a lock unless it moves; anything else takes the file rule, so a shortcut
// is guarded as a file, which fails closed) and one in api/lock_break.go (the break refuses whatever is not a file).
var reviewedNodeKindSites = map[string]int{
	"internal/api/access.go":           2,
	"internal/api/access_requests.go":  1,
	"internal/api/download.go":         1,
	"internal/api/links.go":            2,
	"internal/api/lock_break.go":       1,
	"internal/api/nodes.go":            12,
	"internal/api/office.go":           2,
	"internal/api/office_locks.go":     3,
	"internal/api/share_links.go":      2,
	"internal/api/shortcuts.go":        5,
	"internal/api/tokens.go":           1,
	"internal/api/transfer.go":         11,
	"internal/api/uploads.go":          2,
	"internal/api/versions.go":         3,
	"internal/api/views.go":            1,
	"internal/store/folders.go":        1,
	"internal/store/nodes.go":          4,
	"internal/store/webhook_events.go": 3,
	"internal/store/sharing.go":        2,
	"internal/store/storage.go":        1,
	"internal/store/usage.go":          2,
	// Chunked copy (decision 34). copy_jobs.go: a batch's copy of a file (counted in the copies' file total, which the
	// publish reports), the file selectors of the staged totals and the per-file events, and the non-folder selectors of
	// the staged item count and the per-file events (shortcuts included, as the one-write copy reports them). None of
	// these decides access.
	// copy_job.go: the publish's shortcut check, which decides only whether the edit is recorded in Recent, as copyTree's.
	"internal/store/copy_jobs.go": 6,
	"internal/api/copy_job.go":    1,
}

// TestNodeKindComparisonsAreReviewed fails when a node-kind comparison appears, moves or goes in internal/api or
// internal/store without the count above being updated by a reviewer. It is the mechanical half of the slice p3 review:
// the decisions are in the design record, and this test keeps them from drifting.
func TestNodeKindComparisonsAreReviewed(t *testing.T) {
	root := filepath.Join("..", "..")
	got := map[string]int{}
	for _, dir := range []string{"internal/api", "internal/store"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(root, dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if n := len(nodeKindSite.FindAllString(string(b), -1)); n > 0 {
				got[dir+"/"+name] = n
			}
		}
	}
	for file, n := range got {
		if reviewedNodeKindSites[file] != n {
			t.Errorf("%s has %d node-kind comparisons, reviewed %d: review each one, then update reviewedNodeKindSites",
				file, n, reviewedNodeKindSites[file])
		}
	}
	for file, n := range reviewedNodeKindSites {
		if got[file] != n {
			t.Errorf("%s has %d node-kind comparisons now, reviewed %d", file, got[file], n)
		}
	}
}
