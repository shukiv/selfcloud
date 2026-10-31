// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The guards below read the Go source of internal/ and fail when a path is not classified. A new audit
// action, or a new call that writes a node, must be given an event (webhookAuditEvents) or a reason for
// none (webhookAuditNoEvent, nodeWriteSites), so that no change is silently left out of the outbox.

// webhookAuditNoEvent is every audit action that writes no event, with the reason. The actions that do
// write one are in webhookAuditEvents.
var webhookAuditNoEvent = map[string]string{
	"node.purge":             "design: a purge sends nothing more; the trash already reported file.deleted",
	"node.transfer":          "a move into My files reports file.moved from ownership.go; a change of owner in a team space reports nothing",
	"node.versions_pruned":   "history only; the file's current content does not change",
	"node.version.update":    "history only; the file's current content does not change",
	"node.version.delete":    "history only; the file's current content does not change",
	"node.sharing":           "sharing flags are not in the payload and are not on the event list",
	"node.appearance":        "colour and description are not in the payload and are not on the event list",
	"node.link":              "share links are not on the event list",
	"node.copy":              "emitted per created node by copyTree (api/transfer.go), not from this audit entry",
	"upload.create":          "an upload begun is not a change to a file; upload.completed reports the file",
	"upload.delete":          "an upload dropped is not on the event list",
	"upload.expire":          "an upload expired is not on the event list",
	"user.files_moved":       "emitted per moved file by users.go, from the move itself; this entry is the summary",
	"user.update":            "user fields are not on the event list",
	"user.delete":            "deleting a user is not on the event list; its files are purged, which reports nothing",
	"user.logout":            "sign-in activity is not on the event list",
	"user.password_change":   "sign-in activity is not on the event list",
	"user.sign_in_link":      "sign-in activity is not on the event list",
	"user.tokens_revoke":     "tokens are not on the event list",
	"admin.create":           "an installation admin has no account, so there is nothing to report to",
	"admin.delete":           "an installation admin has no account, so there is nothing to report to",
	"auth.sign_in_link":      "sign-in activity is not on the event list",
	"link.throttled":         "share-link access attempts are not on the event list",
	"user.login":             "sign-in activity is not on the event list",
	"user.login_failed":      "sign-in activity is not on the event list",
	"user.password_reset":    "password changes are not on the event list",
	"verification.send":      "sending a verification is not a verification",
	"folder.create":          "directory events are not in v1 (decision 27)",
	"folder.update":          "directory events are not in v1 (decision 27)",
	"folder.delete":          "directory events are not in v1 (decision 27)",
	"folder.settings":        "directory events are not in v1 (decision 27)",
	"grant.set":              "grants are not on the event list",
	"grant.remove":           "grants are not on the event list",
	"group.create":           "groups are not on the event list",
	"group.update":           "groups are not on the event list",
	"group.delete":           "groups are not on the event list",
	"group.member.add":       "groups are not on the event list",
	"group.member.remove":    "groups are not on the event list",
	"permission.set":         "permissions are not on the event list",
	"permission.remove":      "permissions are not on the event list",
	"permission.expired":     "permissions are not on the event list",
	"access.request":         "access requests are not on the event list",
	"access.approve":         "access requests are not on the event list",
	"access.deny":            "access requests are not on the event list",
	"link.create":            "share links are not on the event list",
	"link.update":            "share links are not on the event list",
	"link.reset":             "share links are not on the event list",
	"link.delete":            "share links are not on the event list",
	"link.delete_public":     "share links are not on the event list",
	"link.expired":           "share links are not on the event list",
	"link.open":              "share links are not on the event list",
	"account.create":         "an account is not an event in v1; its users are reported",
	"account.update":         "an account is not an event in v1",
	"account.delete":         "an account is deleted with its events (cascade)",
	"account.deleted":        "an account is deleted with its events (cascade)",
	"account.storage_policy": "quota settings are not on the event list",
	"account.sharing_policy": "sharing settings are not on the event list",
	"key.create":             "keys are not on the event list",
	"key.revoke":             "keys are not on the event list",
	"token.mint":             "tokens are not on the event list",
	"tokens.revoke":          "tokens are not on the event list",
	"webhook.create":         "webhook administration is not an event",
	"webhook.update":         "webhook administration is not an event",
	"webhook.delete":         "webhook administration is not an event",
	"webhook.secret":         "webhook administration is not an event",
}

// nodeWriteSite is one pinned count of node-writing calls or statements in one file, with what each one
// reports, or why it reports nothing.
type nodeWriteSite struct {
	n   int
	why string
}

// nodeWriteSites pins, per file, how many node-writing calls and raw statements there are, and what each
// reports. A new one changes a count, and the test fails until its line here says what it reports.
var nodeWriteSites = map[string]nodeWriteSite{
	"../api/nodes.go InsertNode":                      {1, "newNode's insert: the caller's node.create reports a file; a folder reports nothing"},
	"../api/nodes.go UpdateNode":                      {3, "PUT create (node.create), PUT write (node.write), and rename, sharing or appearance (only the rename reports, file.updated)"},
	"../api/nodes.go newNode":                         {3, "a folder on the path or a mkdir (nothing), and PUT create of a file (node.create, file.created)"},
	"../api/ownership.go ClearSubtreeEditorsCanShare": {1, "the editors_can_share flag, which is not in the payload"},
	"../api/ownership.go MoveSubtree":                 {1, "a move into My files (personal branch): file.moved for each file, explicitly in ownership.go"},
	"../api/ownership.go UpdateNode":                  {1, "the move into My files (personal branch): reported with the MoveSubtree above"},
	"../api/ownership.go SetSubtreeOwner":             {1, "a change of owner inside a team space: nothing is reported, the item does not move"},
	"../api/shortcuts.go InsertNode":                  {1, "node.shortcut: file.created with kind shortcut and no target"},
	"../api/transfer.go ClearSubtreeEditorsCanShare":  {1, "the editors_can_share flag, which is not in the payload"},
	"../api/transfer.go InsertNode":                   {1, "copyTree: each file made is reported by an explicit file.created (the audit names only the root)"},
	"../api/transfer.go MoveSubtree":                  {1, "node.move: each file below the moved node reports file.moved"},
	"../api/transfer.go TrashNode":                    {1, "applyReplace trashes what a copy or move displaces: the caller's node.trash reports it as file.deleted"},
	"../api/transfer.go UpdateNode":                   {1, "node.move: the node's folder and parent change, reported as file.moved"},
	"../api/trash.go PurgeNode":                       {1, "node.purge: nothing is reported (decision 27)"},
	"../api/trash.go TrashNode":                       {1, "trashIn: node.trash reports file.deleted for each file below"},
	"../api/trash.go Untrash":                         {1, "node.restore: reported as file.restored by the audit entry (decision 27)"},
	"../api/trash.go UpdateNode":                      {1, "node.restore: the restored node's files report file.restored (decision 27)"},
	"../api/uploads.go InsertNode":                    {1, "completeUpload of a new file: node.create reports file.created; upload.completed is explicit"},
	"../api/uploads.go UpdateNode":                    {1, "completeUpload of a replacement: node.write reports file.updated; upload.completed is explicit"},
	"../api/users.go ClearSubtreeEditorsCanShare":     {1, "the editors_can_share flag, which is not in the payload"},
	"../api/users.go InsertNode":                      {1, "the folder that takes a deleted user's files: a folder, which reports nothing"},
	"../api/users.go MoveFolderContents":              {1, "user.files_moved: each file moved reports file.moved, explicitly"},
	"../api/versions.go UpdateNode":                   {1, "node.version.restore: file.updated"},
	"../store/folders.go INSERT INTO nodes":           {1, "the root node of a new folder: a folder, which reports nothing"},
	"../store/nodes.go INSERT INTO nodes":             {1, "the InsertNode method itself; its callers are classified above"},
	"../store/nodes.go UPDATE nodes":                  {2, "the UpdateNode method and the MoveSubtree method themselves; their callers are classified above"},
	"../store/sharing.go UPDATE nodes":                {4, "SetSubtreeOwner (owner only: nothing reported), MoveFolderContents (two statements, reported by users.go), and ClearSubtreeEditorsCanShare (the flag)"},
	"../store/trash.go DELETE FROM nodes":             {1, "the PurgeNode method itself: nothing is reported"},
	"../store/trash.go UPDATE nodes":                  {2, "the TrashNode and Untrash methods themselves; their callers are classified above"},
	"../sweep/trash.go PurgeNode":                     {1, "the sweep's purge of a trash root: nothing is reported (decision 27)"},
}

// webhookAuditCalls maps the names of helpers that take an audit action to the position of the action
// argument.
var webhookAuditCalls = map[string]int{"auditEntry": 2, "auditAs": 2, "addUser": 5}

// nodeWriterMethods are the store calls that change a node row.
var nodeWriterMethods = map[string]bool{"InsertNode": true, "UpdateNode": true, "TrashNode": true, "Untrash": true,
	"PurgeNode": true, "MoveSubtree": true, "MoveFolderContents": true, "ClearSubtreeEditorsCanShare": true,
	"newNode": true, "SetSubtreeOwner": true}

// guardFile is one parsed non-test Go file under internal/.
type guardFile struct {
	path string
	fset *token.FileSet
	file *ast.File
}

// parseInternal parses every non-test Go file under internal/ (the parent of this package).
func parseInternal(t *testing.T) []guardFile {
	t.Helper()
	var out []guardFile
	err := filepath.WalkDir("..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		out = append(out, guardFile{path: filepath.ToSlash(p), fset: fset, file: f})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// auditActions returns every audit action the source names, with the place that names it. An action given
// by an identifier is resolved to the string literals assigned to it in the same function. An identifier
// that is a parameter of its function is passed on to the callers, which are checked where they name the
// action.
func auditActions(t *testing.T, files []guardFile) map[string]string {
	t.Helper()
	found := map[string]string{}
	for _, gf := range files {
		for _, decl := range gf.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			params := map[string]bool{}
			for _, fl := range fn.Type.Params.List {
				for _, nm := range fl.Names {
					params[nm.Name] = true
				}
			}
			assigned := map[string][]string{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.AssignStmt:
					for i, lhs := range n.Lhs {
						if i >= len(n.Rhs) {
							break
						}
						lit, ok := n.Rhs[i].(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						name := ""
						switch l := lhs.(type) {
						case *ast.Ident:
							name = l.Name
						case *ast.SelectorExpr:
							if l.Sel.Name == "Action" {
								name = "Action"
							}
						}
						if name != "" {
							assigned[name] = append(assigned[name], unquoteLit(t, lit))
						}
					}
				case *ast.ValueSpec:
					for i, nm := range n.Names {
						if i < len(n.Values) {
							if lit, ok := n.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								assigned[nm.Name] = append(assigned[nm.Name], unquoteLit(t, lit))
							}
						}
					}
				}
				return true
			})
			add := func(expr ast.Expr, where token.Position) {
				switch e := expr.(type) {
				case *ast.BasicLit:
					if e.Kind != token.STRING {
						t.Errorf("%s: audit action is not a string: %s", where, e.Value)
						return
					}
					found[unquoteLit(t, e)] = where.String()
				case *ast.Ident:
					if vals, ok := assigned[e.Name]; ok {
						for _, v := range vals {
							found[v] = where.String()
						}
						return
					}
					if params[e.Name] {
						return
					}
					t.Errorf("%s: audit action %q is not a literal, and is not assigned one in its function", where, e.Name)
				default:
					t.Errorf("%s: audit action is not a literal or a name", where)
				}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					name := ""
					switch f := n.Fun.(type) {
					case *ast.Ident:
						name = f.Name
					case *ast.SelectorExpr:
						name = f.Sel.Name
					}
					pos, ok := webhookAuditCalls[name]
					if ok && len(n.Args) > pos {
						add(n.Args[pos], gf.fset.Position(n.Pos()))
					}
				case *ast.CompositeLit:
					if isAuditEntryType(n.Type) {
						for _, el := range n.Elts {
							kv, ok := el.(*ast.KeyValueExpr)
							if !ok {
								continue
							}
							if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Action" {
								add(kv.Value, gf.fset.Position(n.Pos()))
							}
						}
					}
				}
				return true
			})
			// The assignments to Action on an entry (e.Action = "...") name actions too.
			for name, vals := range assigned {
				if name == "Action" {
					for _, v := range vals {
						found[v] = gf.path + " (assignment)"
					}
				}
			}
		}
	}
	return found
}

// isAuditEntryType reports whether a composite literal's type is AuditEntry, with or without its package.
func isAuditEntryType(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name == "AuditEntry"
	case *ast.SelectorExpr:
		return t.Sel.Name == "AuditEntry"
	}
	return false
}

// unquoteLit returns the value of a string literal.
func unquoteLit(t *testing.T, lit *ast.BasicLit) string {
	t.Helper()
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		t.Fatalf("literal %s: %v", lit.Value, err)
	}
	return v
}

// TestEveryAuditActionIsClassified fails for an audit action that is in neither webhookAuditEvents nor
// webhookAuditNoEvent, and for an entry in either map that no source names any more.
func TestEveryAuditActionIsClassified(t *testing.T) {
	found := auditActions(t, parseInternal(t))
	var unclassified, stale []string
	for action, where := range found {
		_, emits := webhookAuditEvents[action]
		_, quiet := webhookAuditNoEvent[action]
		switch {
		case emits && quiet:
			t.Errorf("%q is in both maps", action)
		case !emits && !quiet:
			unclassified = append(unclassified, action+" at "+where)
		}
	}
	for action := range webhookAuditEvents {
		if _, ok := found[action]; !ok {
			stale = append(stale, action)
		}
	}
	for action := range webhookAuditNoEvent {
		if _, ok := found[action]; !ok {
			stale = append(stale, action)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(stale)
	if len(unclassified) > 0 {
		t.Errorf("audit actions with no event and no reason:\n  %s", strings.Join(unclassified, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("classified actions that no source names:\n  %s", strings.Join(stale, "\n  "))
	}
}

// nodeWriteCounts counts, per file, the node-writing calls and the raw statements that write nodes.
func nodeWriteCounts(t *testing.T, files []guardFile) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, gf := range files {
		ast.Inspect(gf.file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				name := ""
				switch f := n.Fun.(type) {
				case *ast.Ident:
					name = f.Name
				case *ast.SelectorExpr:
					name = f.Sel.Name
				}
				if nodeWriterMethods[name] {
					counts[gf.path+" "+name]++
				}
			case *ast.BasicLit:
				if n.Kind != token.STRING {
					return true
				}
				s := unquoteLit(t, n)
				for _, stmt := range []string{"UPDATE nodes", "INSERT INTO nodes", "DELETE FROM nodes"} {
					if strings.Contains(s, stmt) {
						counts[gf.path+" "+stmt]++
					}
				}
			}
			return true
		})
	}
	return counts
}

// TestEveryNodeWriteIsClassified pins the count of each node-writing call and statement per file. A new
// one changes a count, and the test fails until nodeWriteSites says what it reports.
func TestEveryNodeWriteIsClassified(t *testing.T) {
	got := nodeWriteCounts(t, parseInternal(t))
	var diff []string
	keys := map[string]bool{}
	for k := range got {
		keys[k] = true
	}
	for k := range nodeWriteSites {
		keys[k] = true
	}
	for k := range keys {
		if got[k] != nodeWriteSites[k].n {
			diff = append(diff, k+": "+strconv.Itoa(got[k])+" found, "+strconv.Itoa(nodeWriteSites[k].n)+" classified")
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		t.Errorf("node writes not classified:\n  %s", strings.Join(diff, "\n  "))
	}
}
