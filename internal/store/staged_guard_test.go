// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// folderReadSite matches a read of the folders table in SQL: a FROM or a JOIN that names it. Writes (INSERT,
// UPDATE, DELETE) are not reads and do not match. A staging folder (migration 0027) has staged = 1, and no folder
// read may return one. So each read states staged in its own SQL literal: the literal is the quoted text the match
// sits in, from the quote before it to the quote after it. A read that splits its SQL across several literals does
// not count as filtered, even if a later literal says staged, because the filter is then not in the statement the
// read belongs to.
var folderReadSite = regexp.MustCompile(`\b(?:FROM|JOIN)\s+folders\b`)

// nodeReadSite matches a read of the nodes table in SQL: a FROM or JOIN that names it, or a use of the nodeSelect
// statement (nodes.go), which reads nodes through the statement's own FROM. A use that is the definition of nodeSelect
// is not a read.
var nodeReadSite = regexp.MustCompile(`\b(?:FROM|JOIN)\s+nodes\b|\bnodeSelect\b`)

// nodeIDCondition is a condition that scopes a node read to IDs a caller holds: an ID, a folder or parent ID, or a
// node ID compared with = or IN, or a recursion's node. A column that ends in _id, such as account_id, scopes nothing.
var nodeIDCondition = regexp.MustCompile(`(?i)\b(?:id|folder_id|parent_id|node_id)\s*(?:=|IN\b)|\bnid\b`)

// stagedFilter is the text that makes a folder read exclude staging folders.
const stagedFilter = "staged"

// sqlLiteral returns the quoted text that a match in src sits in: from the quote before it to the quote after it.
// Both kinds of quote close a SQL literal in this code: backquotes for raw strings, double quotes for the others.
func sqlLiteral(src string, start, end int) string {
	from := strings.LastIndexAny(src[:start], "`\"")
	if from < 0 {
		from = 0
	}
	to := len(src)
	if i := strings.IndexAny(src[end:], "`\""); i >= 0 {
		to = end + i
	}
	return src[from:to]
}

// concatLiteral matches a string literal that a Go expression adds to the statement: "+ `...`" or "+ \"...\"".
var concatLiteral = regexp.MustCompile("^\\s*\\+\\s*(?:`([^`]*)`|\"((?:[^\"\\\\]|\\\\.)*)\")")

// nodeReadCondition is the text a node read is scoped by, across the literals of its statement. For FROM nodes or JOIN
// nodes it is the rest of the literal after the match; for a use of nodeSelect it is nothing of its own. Then it takes
// each literal the Go expression adds after (+ " WHERE ..."), so a condition split over several literals is seen whole.
// The expression's own parts (a function call, a variable) are not in it, and a condition they add is not seen.
func nodeReadCondition(src string, match string, end int) string {
	rest := src[end:]
	var b strings.Builder
	if !strings.HasPrefix(match, "nodeSelect") {
		i := strings.IndexAny(rest, "`\"")
		if i < 0 {
			return rest
		}
		b.WriteString(rest[:i])
		rest = rest[i+1:]
	}
	for {
		m := concatLiteral.FindStringSubmatch(rest)
		if m == nil {
			break
		}
		b.WriteString(m[1] + m[2])
		rest = rest[len(m[0]):]
	}
	return b.String()
}

// unfilteredFolderReads is how many folder reads in each file do not filter staged, each one reviewed for the
// chunked copy (decision 34). A staging folder's ID is random and never sent to a caller, and no grant, link,
// shortcut or permission can name one, so a read that starts from a node or a folder ID a caller gave is safe:
// it finds no staging folder. The reasons are the categories; the reviewer re-decides a count that moves.
var unfilteredFolderReads = map[string]struct {
	n      int
	reason string
}{
	"audit.go":         {1, "the owner of a personal folder named by a node's folder ID: the node is reached through an ID the caller holds"},
	"folders.go":       {2, "RootReachedFolders joins the folders of the nodes a permission names: a staging root has no permission, so the join finds none; and the root rank of a folder ID the caller gave"},
	"nodes.go":         {2, "the personal folder owner of a folder ID the node insert or move names: that folder is the destination, which a request names by ID"},
	"permissions.go":   {6, "permissions and their nodes: a permission names a node a caller holds, and a staging root or node has no permission"},
	"reach.go":         {2, "reach through a node and its folder: the node was reached by a caller's ID, which a staged node never gives out"},
	"share_links.go":   {3, "share links name a node by ID the caller holds: a staged node has none published"},
	"sharing.go":       {5, "grants and hidden spaces name a folder by ID the owner gave; a staging folder has no grant and its ID is never returned"},
	"storage.go":       {2, "the personal folder of a user (LEFT JOIN, kind personal), and FolderUsage of a folder ID the caller gave"},
	"personal.go":      {1, "a user's personal folder: staging folders are team folders, so the kind filter excludes them"},
	"usage.go":         {6, "the account's reserved sum (a copy's reservation belongs in it), the folder counters of a named folder, and the consistency check over folders, which must count staging reservations"},
	"webhook_quota.go": {2, "personal folders of users in quota events: staging folders are team folders"},
	"copy_jobs.go":     {1, "the owner of the personal folder that a published copy names as its destination: the destination is a folder ID the request gave, and a staging folder is never a destination"},
}

// TestFolderReadsExcludeStagingByDefault fails when a folder read appears, moves or goes in internal/store without its
// count above being updated by a reviewer. A new read must say staged in the SQL literal it sits in, or be counted here
// with a reason. This is what keeps a staging folder out of every folder list, lookup and count.
func TestFolderReadsExcludeStagingByDefault(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		for _, loc := range folderReadSite.FindAllStringIndex(text, -1) {
			// A DELETE names the table without reading it.
			if strings.HasSuffix(strings.TrimSpace(text[max(0, loc[0]-8):loc[0]]), "DELETE") {
				continue
			}
			if strings.Contains(sqlLiteral(text, loc[0], loc[1]), stagedFilter) {
				continue
			}
			got[f]++
		}
	}
	for f, want := range unfilteredFolderReads {
		if got[f] != want.n {
			t.Errorf("%s: %d unfiltered folder reads, %d classified (%s)", f, got[f], want.n, want.reason)
		}
	}
	for f, n := range got {
		if _, ok := unfilteredFolderReads[f]; !ok {
			t.Errorf("%s: %d unfiltered folder reads not classified", f, n)
		}
	}
}

// unscopedNodeReads is how many node reads in each file are not scoped by an ID: their condition names no node, folder
// or parent ID, so they read every node of the account they name. A staged node is reachable only by an ID a caller was
// given, or by its folder, so such a read is safe only where its reason says why it cannot reach a staged node.
var unscopedNodeReads = map[string]struct {
	n      int
	reason string
}{
	"nodes.go": {2, "blob garbage collection: whether a blob is still named by a node (EXISTS over nodes.blob_id). A staged node names its blob, so the blob is kept, as a copy's nodes always have kept it; the read returns no node"},
	"trash.go": {2, "trash listing and expiry over trashed_at: a staged node is never trashed (a copy never copies a trashed node, and a trashed source fails the copy), so no staged node matches"},
}

// TestAccountWideNodeReadsAreClassified fails when a read of nodes that is not scoped by an ID appears, moves or goes in
// internal/store, unless its count is recorded above with the reason it cannot see a staging folder. The check looks at
// each read site (a FROM or JOIN of nodes, and each use of nodeSelect) and at the condition that follows it, so a query
// built on nodeSelect is checked too. A staged node is never in a list or a count a caller reads, so the reviewer must say
// why an account-wide read does not reach one.
func TestAccountWideNodeReadsAreClassified(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	var sites []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		for _, loc := range nodeReadSite.FindAllStringIndex(text, -1) {
			match := text[loc[0]:loc[1]]
			// A comment names the statement without reading it.
			lineStart := strings.LastIndex(text[:loc[0]], "\n") + 1
			if strings.HasPrefix(strings.TrimSpace(text[lineStart:loc[0]]), "//") {
				continue
			}
			// The definition of nodeSelect is not a read, and a DELETE names the table without reading it.
			if strings.HasPrefix(match, "nodeSelect") && strings.HasPrefix(strings.TrimSpace(text[loc[1]:]), "=") {
				continue
			}
			if strings.Contains(text[max(0, loc[0]-400):loc[0]], "const nodeSelect") {
				continue
			}
			if strings.HasSuffix(strings.TrimSpace(text[max(0, loc[0]-8):loc[0]]), "DELETE") {
				continue
			}
			cond := nodeReadCondition(text, match, loc[1])
			if nodeIDCondition.MatchString(cond) {
				continue
			}
			got[f]++
			sites = append(sites, f+": "+strings.TrimSpace(match)+" | "+strings.TrimSpace(cond))
		}
	}
	for f, want := range unscopedNodeReads {
		if got[f] != want.n {
			t.Errorf("%s: %d unscoped node reads, %d classified (%s)", f, got[f], want.n, want.reason)
		}
	}
	for f, n := range got {
		if _, ok := unscopedNodeReads[f]; !ok {
			t.Errorf("%s: %d unscoped node reads not classified", f, n)
		}
	}
	if testing.Verbose() {
		for _, s := range sites {
			t.Log(s)
		}
	}
}
