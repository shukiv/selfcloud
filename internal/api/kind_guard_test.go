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
// Slice s1 added two in thumbnails.go, each reviewed: the thumbnail route refuses a node that is not a file (a folder
// or a shortcut fails closed, with not_found), and has_thumbnail is set only on a file, so other kinds stay without it.
// Neither decides access; access is nodeAccess, which runs first. Decision 33 records them.
// Slice j2 added three, each reviewed for event emission and neither an access decision: copyTree reports a file.created for
// each node that is not a directory and counts them for the big-folder rule (transfer.go), the outbox skips a directory
// when it reports a subtree (store/webhook_events.go), and the threshold count excludes directories in SQL
// (store/webhook_events.go countSubtree). Decision 27 records them. Slice l1 and l2 added four in office.go, each
// refusing whatever is not a file (an editor opens a file only, and a shortcut or folder is never sent to it).
// Decision 31 records them. Slice l3 moved the PutFile and the lock operations' comparisons to office_locks.go, which
// refuse whatever is not a file for the same reason (decision 32). Slice l3b added one in store/nodes.go
// (changesLockedPlace: a directory's write passes a lock unless it moves; anything else takes the file rule, so a shortcut
// is guarded as a file, which fails closed) and one in api/lock_break.go (the break refuses whatever is not a file).
// Slice s3 added three in exports.go, one for each conversion route (preview, export create and export download). Each
// refuses whatever is not a file with not_found, which fails closed; access is nodeAccess, which runs first. Decision 37
// records them.
// Slice x4 added two in playlists.go, each reviewed: the read marks an entry unavailable unless the node is a file (a
// folder or a shortcut fails closed), and the add refuses a node that is not a file with the same not_found as a missing
// one. Neither decides access; reachRoles runs first in both. Decision 58 records them.
// Round 6 reviewed the import's node-kind comparisons (decision 92), one reason per site. Each one accepts only a directory
// and refuses or skips anything else, so a kind added later fails closed. imports.go handleCreateImport: the parent an
// import is aimed at must be a directory of the folder, else invalid_request; access is requireNodeRole, which runs first.
// import_job.go importMakeDir: an earlier import's directory is reused only when it is a directory of the target folder
// under the expected parent; any other kind is not reused, and a new directory is made. import_job.go checkReusedDirs: a
// reused directory must be a directory of the target folder, else target_moved. import_job.go importPublish: the target
// directory must be a directory of the target folder, else target_moved. Neither import check decides access.
var reviewedNodeKindSites = map[string]int{
	// The import's own item kinds (store/imports.go), and StagedCounts, are reviewed in the store's guard (round 6): the
	// item kinds are import_items.kind, not a node's, and StagedCounts counts files of the staging folder, which holds
	// only the files and directories an import writes.
	"internal/api/imports.go":    1,
	"internal/api/import_job.go": 3,
	"internal/store/imports.go":  5,

	"internal/api/access.go":          2,
	"internal/api/playlists.go":       2,
	"internal/api/access_requests.go": 1,
	"internal/api/download.go":        1,
	"internal/api/exports.go":         3,
	"internal/api/links.go":           3,
	"internal/api/lock_break.go":      1,
	"internal/api/nodes.go":           13, // 13th: mkdirUnder's parent must be a directory (fu2, decision 74); a shortcut or file parent is refused
	"internal/api/office.go":          2,
	"internal/api/office_locks.go":    3,
	// Slice l4 added one in office_rename.go: the WOPI RenameFile refuses a folder or a shortcut as not_found, after the
	// token's read-only check and before the rename. It grants nothing; the token already names a file. Decision 73.
	"internal/api/office_rename.go": 1,
	// Slice l5 added three in office_saveas.go, each reviewed: the copy's directory must be a directory (a file or a
	// shortcut parent is refused as not_found, which grants nothing); an overwrite replaces a file only (a folder or a
	// shortcut is taken as a name clash, answered 409 with a free name, never replaced); and the copy's source must be a
	// file (a folder or a shortcut is not_found). Decision 87.
	"internal/api/office_saveas.go":    3,
	"internal/api/share_links.go":      2,
	"internal/api/shortcuts.go":        5,
	"internal/api/thumbnails.go":       2,
	"internal/api/zips.go":             5,
	"internal/api/tokens.go":           1,
	"internal/api/transfer.go":         11,
	"internal/api/uploads.go":          2,
	"internal/api/versions.go":         3,
	"internal/api/views.go":            1,
	"internal/store/folders.go":        1,
	"internal/store/nodes.go":          6,
	"internal/store/webhook_events.go": 3,
	"internal/store/sharing.go":        2,
	// Slice st (decision 79) added two in PersonalBreakdown: the trash's file count and its bytes, each "n.kind = 'file'"
	// on the caller's own My files, read for the Storage page. A shortcut holds no bytes and is not counted as a file, so
	// it adds nothing to the figures; no access is decided here (the caller's own folder is its only input).
	"internal/store/storage.go": 3,
	"internal/store/usage.go":   2,
	// Slice r1 added two in comments.go, both the same refusal: commentNode and the create write refuse a shortcut as
	// not_found, since a shortcut has no comments of its own. That is a refusal on a kind the walk already reached, not
	// an access decision: nodeAccessWith runs first, and it already refuses a shortcut to a link visitor. Decision 39.
	"internal/api/comments.go": 2,
	// Slice r4 added one in activity.go: the activity route refuses a shortcut as not_found, as the comment routes do,
	// after nodeAccessWith has run. A refusal that grants nothing. Decision 50.
	"internal/api/activity.go": 1,
	// Slice r5 added one in viewers.go: the who-viewed route refuses a shortcut as not_found, as the activity route does,
	// after nodeAccessWith has run. A refusal that grants nothing. Decision 51.
	"internal/api/viewers.go": 1,
	// Slice x2 added three. media_resume.go: the place is written only for a file, and a folder or a shortcut gets not_found,
	// after nodeAccessWith has run inside the write. subtitles.go: the list gives nothing for a folder or a shortcut, after
	// nodeAccessWith; a sibling that is not a file is skipped, and each sibling listed also passes nodeAccessWith. All three
	// fail closed and grant nothing. Decision 52.
	"internal/api/media_resume.go": 1,
	"internal/api/subtitles.go":    2,
	// Chunked copy (decision 34). copy_jobs.go: a batch's copy of a file (counted in the copies' file total, which the
	// publish reports), the file selectors of the staged totals and the per-file events, and the non-folder selectors of
	// the staged item count and the per-file events (shortcuts included, as the one-write copy reports them). None of
	// these decides access.
	// copy_job.go: the publish's shortcut check, which decides only whether the edit is recorded in Recent, as copyTree's.
	"internal/store/copy_jobs.go": 6,
	"internal/api/copy_job.go":    1,
	// Search (decision 40). search.go: the scope check refuses a scope that is not a directory with invalid_request; the
	// reach check of the scope ran first, so this is a refusal of a bad argument, not an access decision. search_index.go:
	// the extension of a file's search row is read from a file's name only. Neither decides access.
	"internal/api/search.go":         2,
	"internal/store/search_index.go": 1,
	// Slice q3 (decision 46). search.go: a content search (in=content) given a kind other than file is refused with
	// invalid_request, before any candidate is read. An input refusal that grants nothing, so it fails closed.
	// store/nodes.go: two comparisons, "n.Kind == KindFile && n.BlobID != """ before the content row is queued, on an insert
	// and on a new version. A folder or a shortcut queues no content row, which is the fail-closed direction (no text is
	// kept for a non-file). Access is nodeAccess and is not changed.
	// store/search_text.go: two, both "n.kind = 'file'" in SQL: the backfill's read of file nodes that queues rows, and the
	// check that counts the rows a file should have. Neither decides access; a folder or shortcut is simply not queued.
	"internal/store/search_text.go": 2,
	// Slice x3 (decision 56). tags.go: tagsEligible compares a node's kind with a file, and the single route, the batch
	// route and the listing (withTags) all call it. It refuses whatever is not a file (a folder, a shortcut), so it fails
	// closed; access is nodeAccess for the single and batch routes, and the listing's own access for withTags. The thumbnail
	// route's size check reads the audio limit for an audio file and adds no kind comparison.
	"internal/api/tags.go": 1,
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
