// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
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
	"node.purge":              "design: a purge sends nothing more; the trash already reported file.deleted",
	"node.transfer":           "a move into My files reports file.moved from ownership.go; a change of owner in a team space reports nothing",
	"node.versions_pruned":    "history only; the file's current content does not change",
	"node.version.update":     "history only; the file's current content does not change",
	"node.version.delete":     "history only; the file's current content does not change",
	"node.sharing":            "sharing flags are not in the payload and are not on the event list",
	"node.appearance":         "colour and description are not in the payload and are not on the event list",
	"node.link":               "share links are not on the event list",
	"node.office":             "opening an editor is not a change to the file; the edits the editor saves are node.write, which reports file.updated",
	"node.copy":               "emitted per created node by copyTree (api/transfer.go), not from this audit entry",
	"node.zip":                "a stored zip is a download made by its caller, not a change to a file; decision 35 emits no webhook event for it",
	"upload.create":           "an upload begun is not a change to a file; upload.completed reports the file",
	"upload.delete":           "an upload dropped is not on the event list",
	"upload.expire":           "an upload expired is not on the event list",
	"user.files_moved":        "emitted per moved file by users.go, from the move itself; this entry is the summary",
	"user.update":             "user fields are not on the event list",
	"user.delete":             "deleting a user is not on the event list; its files are purged, which reports nothing",
	"user.logout":             "sign-in activity is not on the event list",
	"user.password_change":    "sign-in activity is not on the event list",
	"user.sign_in_link":       "sign-in activity is not on the event list",
	"user.tokens_revoke":      "tokens are not on the event list",
	"admin.create":            "an installation admin has no account, so there is nothing to report to",
	"admin.delete":            "an installation admin has no account, so there is nothing to report to",
	"auth.sign_in_link":       "sign-in activity is not on the event list",
	"link.throttled":          "share-link access attempts are not on the event list",
	"user.login":              "sign-in activity is not on the event list",
	"user.login_failed":       "sign-in activity is not on the event list",
	"user.password_reset":     "password changes are not on the event list",
	"verification.send":       "sending a verification is not a verification",
	"folder.create":           "directory events are not in v1 (decision 27)",
	"folder.update":           "directory events are not in v1 (decision 27)",
	"folder.delete":           "directory events are not in v1 (decision 27)",
	"folder.settings":         "directory events are not in v1 (decision 27)",
	"grant.set":               "grants are not on the event list",
	"grant.remove":            "grants are not on the event list",
	"group.create":            "groups are not on the event list",
	"group.update":            "groups are not on the event list",
	"group.delete":            "groups are not on the event list",
	"group.member.add":        "groups are not on the event list",
	"group.member.remove":     "groups are not on the event list",
	"permission.set":          "permissions are not on the event list",
	"permission.remove":       "permissions are not on the event list",
	"permission.expired":      "permissions are not on the event list",
	"access.request":          "access requests are not on the event list",
	"access.approve":          "access requests are not on the event list",
	"access.deny":             "access requests are not on the event list",
	"link.create":             "share links are not on the event list",
	"link.update":             "share links are not on the event list",
	"link.reset":              "share links are not on the event list",
	"link.delete":             "share links are not on the event list",
	"link.delete_public":      "share links are not on the event list",
	"link.expired":            "share links are not on the event list",
	"link.open":               "share links are not on the event list",
	"account.create":          "an account is not an event in v1; its users are reported",
	"account.update":          "an account is not an event in v1",
	"account.delete":          "an account is deleted with its events (cascade)",
	"account.deleted":         "an account is deleted with its events (cascade)",
	"account.storage_policy":  "quota settings are not on the event list",
	"account.sharing_policy":  "sharing settings are not on the event list",
	"key.create":              "keys are not on the event list",
	"key.revoke":              "keys are not on the event list",
	"token.mint":              "tokens are not on the event list",
	"tokens.revoke":           "tokens are not on the event list",
	"webhook.create":          "webhook administration is not an event",
	"webhook.update":          "webhook administration is not an event",
	"webhook.delete":          "webhook administration is not an event",
	"webhook.secret":          "webhook administration is not an event",
	"webhook.disabled":        "the worker's disable (or the overflow cap in a change): an administrative record, not an event",
	"webhook.retry":           "a failed delivery queued again by an owner's account key: not an event",
	"webhook.test":            "a test event queued by the account key: the event is the test itself, not a change",
	"label_field.create":      "label fields are not on the event list (decision 29)",
	"label_field.update":      "label fields are not on the event list (decision 29)",
	"label_field.delete":      "label fields are not on the event list (decision 29)",
	"node.label_set":          "label values are not on the event list (decision 29)",
	"node.label_clear":        "label values are not on the event list (decision 29)",
	"webhook.rewrap":          "a webhook secret sealed again under the current key by the admin command: the secret is the same, nothing is reported",
	"account.quota_warning":   "the quota-warning level is a setting, not a change of usage: the quota.warning event is the report",
	"node.lock":               "an editor takes a lock: a lock is not a change to the file, and the edits the editor saves are node.write",
	"node.unlock":             "an editor or the sweep ends a lock: a lock is not a change to the file",
	"comment.create":          "comments are not on the event list in r1 and r2; r6 decides whether comment.created is an opt-in event (decision 39)",
	"comment.edit":            "comments are not on the event list in r1 and r2; r6 decides (decision 39)",
	"comment.delete":          "comments are not on the event list in r1 and r2; r6 decides (decision 39)",
	"comment.resolve":         "comments are not on the event list in r1 and r2; r6 decides (decision 39)",
	"account.comments_policy": "the comment switch is an account setting, not a change to a file (decision 39)",
}

// nodeWriteSite is one pinned count of node-writing calls or statements in one file: how many there are, the lock rule
// they follow (decision 32), and what each one reports, or why it reports nothing.
type nodeWriteSite struct {
	n    int
	lock string
	why  string
}

// The lock rules a node write follows (decision 32). A guarded write runs a store guard inside its transaction (guardNode
// or guardFolder, wopi_locks.go): it is refused while a live lock holds the node, or, for a folder, a file below it. Only
// the changes that name, place, content or owner make are guarded, for UpdateNodeHeld. A holder write is the save of the
// lock's own holder, and replaceContent lets through no other holder. An adding write creates a node, a folder, an account
// or a history row, which no lock holds, and it changes no current content of a locked file. A free write changes
// counters, settings or history that no lock refuses, and no file's content, name or place. A clearing write ends the
// account's locks in the same transaction (DeleteAccount). Every pinned site states one.
const (
	lockGuarded = "guarded"
	lockHolder  = "holder"
	lockAdds    = "adds"
	lockFree    = "free"
	lockClears  = "clears"
)

// nodeWriteSites pins, per file, how many node-writing calls and raw statements there are, what each one reports, and
// the lock rule each follows. A new one changes a count, and the test fails until its line here says both.
var nodeWriteSites = map[string]nodeWriteSite{
	"../api/accounts.go DeleteAccount":                                 {1, lockClears, "the account job's delete: the account's locks and sessions are cleared in the same write (store DeleteAccount)"},
	"../api/folders.go DeleteFolder":                                   {1, lockGuarded, "a folder delete: refused while a file in the folder is open in an editor (guardFolder)"},
	"../api/users.go DeleteFolder":                                     {1, lockGuarded, "a user's personal folder: the user's locks are released first (ReleaseUserLocks), so nothing is refused unless a lock was taken since"},
	"../api/nodes.go InsertNode":                                       {1, lockAdds, "newNode's insert: the caller's node.create reports a file; a folder reports nothing"},
	"../api/nodes.go UpdateNode":                                       {2, lockGuarded, "a rename, refused while the file is open; and handleCreateFile's new file, which no lock can hold"},
	"../api/nodes.go UpdateNodeHeld":                                   {1, lockHolder, "replaceContent's write: a PUT holds no lock, so an open file refuses it; PutFile passes its lock ID"},
	"../api/nodes.go newNode":                                          {3, lockAdds, "a folder on the path or a mkdir (nothing), and PUT create of a file (node.create, file.created)"},
	"../api/nodes.go replaceContent":                                   {1, lockHolder, "PUT's replacement: its node.write reports file.updated, as the WOPI PutFile's does"},
	"../api/office_locks.go replaceContent":                            {1, lockHolder, "WOPI PutFile: its node.write reports file.updated through the same helper as PUT; the holder is the lock ID"},
	"../api/ownership.go ClearSubtreeEditorsCanShare":                  {1, lockGuarded, "the editors_can_share flag, which is not in the payload"},
	"../api/ownership.go MoveSubtree":                                  {1, lockGuarded, "a move into My files (personal branch): file.moved for each file, explicitly in ownership.go"},
	"../api/ownership.go UpdateNode":                                   {1, lockGuarded, "the move into My files (personal branch): reported with the MoveSubtree above"},
	"../api/ownership.go SetSubtreeOwner":                              {1, lockGuarded, "a change of owner inside a team space: nothing is reported, the item does not move"},
	"../api/shortcuts.go InsertNode":                                   {1, lockAdds, "node.shortcut: file.created with kind shortcut and no target"},
	"../api/transfer.go ClearSubtreeEditorsCanShare":                   {1, lockGuarded, "the editors_can_share flag, which is not in the payload"},
	"../api/transfer.go InsertNode":                                    {1, lockAdds, "copyTree: each file made is reported by an explicit file.created (the audit names only the root)"},
	"../api/transfer.go MoveSubtree":                                   {1, lockGuarded, "node.move: each file below the moved node reports file.moved"},
	"../api/transfer.go TrashNode":                                     {1, lockGuarded, "applyReplace trashes what a copy or move displaces: the caller's node.trash reports it as file.deleted"},
	"../api/transfer.go UpdateNode":                                    {1, lockGuarded, "node.move: the node's folder and parent change, reported as file.moved"},
	"../api/trash.go PurgeNode":                                        {1, lockGuarded, "node.purge: nothing is reported (decision 27)"},
	"../api/trash.go TrashNode":                                        {1, lockGuarded, "trashIn: node.trash reports file.deleted for each file below"},
	"../api/trash.go Untrash":                                          {1, lockGuarded, "node.restore: reported as file.restored by the audit entry (decision 27)"},
	"../api/trash.go UpdateNode":                                       {1, lockGuarded, "node.restore: the restored node's files report file.restored (decision 27)"},
	"../api/uploads.go InsertNode":                                     {1, lockAdds, "completeUpload of a new file: node.create reports file.created; upload.completed is explicit"},
	"../api/uploads.go UpdateNode":                                     {1, lockGuarded, "completeUpload of a replacement: node.write reports file.updated; refused while the file is open"},
	"../api/users.go ClearSubtreeEditorsCanShare":                      {1, lockGuarded, "the editors_can_share flag, which is not in the payload"},
	"../api/users.go InsertNode":                                       {1, lockAdds, "the folder that takes a deleted user's files: a folder, which reports nothing"},
	"../api/users.go MoveFolderContents":                               {1, lockGuarded, "user.files_moved: each file moved reports file.moved, explicitly; refused while a file in the folder is open"},
	"../api/versions.go UpdateNode":                                    {1, lockGuarded, "node.version.restore: file.updated; refused while the file is open"},
	"../api/versions.go UpdateVersion":                                 {1, lockGuarded, "a version's label or keep flag: history only, refused while the file is open"},
	"../api/versions.go DeleteVersion":                                 {1, lockGuarded, "node.version.delete: history only, refused while the file is open"},
	"../store/nodes.go UpdateNodeHeld":                                 {1, lockGuarded, "UpdateNode, which passes no holder: the guard is in UpdateNodeHeld"},
	"../sweep/trash.go PurgeNode":                                      {1, lockGuarded, "the sweep's purge of a trash root: nothing is reported (decision 27)"},
	"../store/accounts.go DeleteAccount DELETE FROM accounts":          {1, lockClears, "the account's delete: its locks and sessions are cleared in the same write"},
	"../store/accounts.go InsertAccount INSERT INTO accounts":          {1, lockAdds, "a new account: no lock can hold it"},
	"../store/accounts.go UpdateAccount UPDATE accounts":               {1, lockFree, "the account's settings and quota: no file changes"},
	"../store/copy_jobs.go InsertNode":                                 {1, lockAdds, "a chunked copy's batch (decision 34): the copies are staged and report nothing; the publish reports them (api/copy_job.go copyPublish)"},
	"../store/copy_jobs.go CreateStagingFolder INSERT INTO folders":    {1, lockAdds, "a new staging folder row: no lock can hold it; nothing is reported"},
	"../store/copy_jobs.go CreateStagingFolder INSERT INTO nodes":      {1, lockAdds, "the staging folder's root: staged, reports nothing; no lock can hold a new node"},
	"../store/copy_jobs.go PublishStaged UPDATE nodes":                 {2, lockAdds, "PublishStaged's two statements move and rename the staged tree, which no editor can hold (it is not in a folder anyone sees); the publish reports the copy with copyPublish"},
	"../store/copy_jobs.go PublishStaged DELETE FROM nodes":            {1, lockFree, "PublishStaged removes the staging root: a staged node, which reports nothing"},
	"../store/copy_jobs.go PublishStaged DELETE FROM folders":          {1, lockFree, "PublishStaged removes the staging folder: it holds no node a caller can see, and reports nothing"},
	"../store/copy_jobs.go CleanupCopyStep DELETE FROM nodes":          {2, lockFree, "cleanup of a staged copy: staged nodes only, which no lock can hold and which report nothing (two statements: the staged rows, and the snapshot's)"},
	"../store/copy_jobs.go CleanupCopyStep DELETE FROM folders":        {1, lockFree, "cleanup removes the staging folder and the job's row: a folder with no visible node, which reports nothing"},
	"../store/folders.go DeleteFolder DELETE FROM folders":             {1, lockGuarded, "DeleteFolder: refused while a file in the folder is open (guardFolder)"},
	"../store/folders.go InsertFolder INSERT INTO folders":             {1, lockAdds, "a new folder row: no lock can hold it; nothing is reported"},
	"../store/folders.go InsertFolder INSERT INTO nodes":               {1, lockAdds, "the root node of a new folder: a folder, which reports nothing"},
	"../store/folders.go UpdateFolder UPDATE folders":                  {1, lockFree, "a folder's name and settings: a rename changes no path an editor holds"},
	"../store/nodes.go InsertNode INSERT INTO nodes":                   {1, lockAdds, "the InsertNode method itself; its callers are classified above"},
	"../store/nodes.go MoveSubtree UPDATE nodes":                       {1, lockGuarded, "MoveSubtree: every node below the moved node; refused while a file below is open (guardNode)"},
	"../store/nodes.go UpdateNodeHeld UPDATE nodes":                    {1, lockGuarded, "UpdateNodeHeld: refused for a change of a file's name, place, content or owner and a folder's place or owner; a description, colour or flag passes"},
	"../store/share_links.go SetSharingPolicy UPDATE accounts":         {1, lockFree, "the account's sharing policy: no file changes"},
	"../store/comments.go SetCommentsOn UPDATE accounts":               {1, lockFree, "the account's comment switch (decision 39): no file changes"},
	"../store/sharing.go ClearSubtreeEditorsCanShare UPDATE nodes":     {1, lockGuarded, "the editors_can_share flag below a node: written only inside a move or a copy, and the move is refused first"},
	"../store/sharing.go MoveFolderContents UPDATE nodes":              {2, lockGuarded, "MoveFolderContents: two statements, reported by users.go; refused while a file in the folder is open (guardFolder)"},
	"../store/sharing.go SetSubtreeOwner UPDATE nodes":                 {1, lockGuarded, "SetSubtreeOwner: owner only, nothing reported; refused while a file below is open (guardNode)"},
	"../store/sharing.go UpdateFolderSettings UPDATE folders":          {1, lockFree, "a folder's settings: no file changes"},
	"../store/storage.go SetUserQuotaDefault UPDATE accounts":          {1, lockFree, "the default quota: no file changes"},
	"../store/trash.go PurgeNode DELETE FROM nodes":                    {1, lockGuarded, "PurgeNode: nothing is reported (decision 27); refused while a file below is open"},
	"../store/trash.go TrashNode UPDATE nodes":                         {1, lockGuarded, "TrashNode: refused while a file below is open (guardNode)"},
	"../store/trash.go Untrash UPDATE nodes":                           {1, lockGuarded, "Untrash: refused while the file is open (guardNode)"},
	"../store/usage.go RecountAccount UPDATE accounts":                 {1, lockFree, "a usage counter recount: no file changes"},
	"../store/usage.go RecountAccount UPDATE folders":                  {1, lockFree, "a usage counter recount of a folder: no file changes"},
	"../store/usage.go adjustAccount UPDATE accounts":                  {1, lockFree, "a usage counter: no file changes"},
	"../store/usage.go adjustFolder UPDATE folders":                    {1, lockFree, "a usage counter of a folder: no file changes"},
	"../store/versions.go DeleteVersion DELETE FROM versions":          {1, lockGuarded, "a version's delete: refused while the file is open"},
	"../store/versions.go InsertVersion INSERT INTO versions":          {1, lockAdds, "a version of the content a write replaces: history, which changes no current content"},
	"../store/versions.go PruneVersions DELETE FROM versions":          {1, lockFree, "retention pruning: removes history only; the current content does not change (decision 32)"},
	"../store/versions.go UpdateVersion UPDATE versions":               {1, lockGuarded, "a version's label or keep flag: history, refused while the file is open"},
	"../store/webhook_quota.go SetQuotaWarningPercent UPDATE accounts": {1, lockFree, "the quota warning percent: no file changes"},
	"../store/webhook_quota.go syncQuotaWatch UPDATE accounts":         {1, lockFree, "the quota warning mark: no file changes"},
}

// webhookAuditCalls maps the names of helpers that take an audit action to the position of the action
// argument.
var webhookAuditCalls = map[string]int{"auditEntry": 2, "auditAs": 2, "addUser": 5}

// nodeWriterMethods are the store calls that change a node row, a version, a folder or an account, and so are
// covered by a lock rule.
var nodeWriterMethods = map[string]bool{"InsertNode": true, "UpdateNode": true, "UpdateNodeHeld": true,
	"TrashNode": true, "Untrash": true, "PurgeNode": true, "MoveSubtree": true, "MoveFolderContents": true,
	"ClearSubtreeEditorsCanShare": true, "newNode": true, "SetSubtreeOwner": true, "replaceContent": true,
	"UpdateVersion": true, "DeleteVersion": true, "DeleteFolder": true, "DeleteAccount": true}

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

// writeStatement is a raw statement that writes a table a lock rule covers: any letter case, any spacing or line break,
// and the insert-or-replace forms, which SQLite also reads as writes. The verb is the first group, the table the second.
var writeStatement = regexp.MustCompile(`(?i)\b(insert\s+or\s+replace\s+into|replace\s+into|insert\s+into|update|delete\s+from)\s+(nodes|versions|folders|accounts)\b`)

// nodeWriteCounts counts, per file, the node-writing calls, keyed by the method's name, and the raw statements that write
// nodes, versions, folders and accounts, keyed by the file, the function that holds the statement, the verb in capitals and
// the table. A statement is pinned per function, since one file can hold writes that follow different rules.
func nodeWriteCounts(t *testing.T, files []guardFile) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, gf := range files {
		for _, decl := range gf.file.Decls {
			where := ""
			if fn, ok := decl.(*ast.FuncDecl); ok {
				where = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
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
					for _, m := range writeStatement.FindAllStringSubmatch(unquoteLit(t, n), -1) {
						verb := strings.ToUpper(strings.Join(strings.Fields(m[1]), " "))
						counts[gf.path+" "+where+" "+verb+" "+strings.ToLower(m[2])]++
					}
				}
				return true
			})
		}
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
	// Every pinned site states the lock rule it follows, and it is one of the three the store's guards and holder rule
	// give (decision 32).
	for k, s := range nodeWriteSites {
		if s.lock != lockGuarded && s.lock != lockHolder && s.lock != lockAdds && s.lock != lockFree && s.lock != lockClears {
			diff = append(diff, k+": no lock rule stated")
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		t.Errorf("node writes not classified:\n  %s", strings.Join(diff, "\n  "))
	}
}

// TestNodeWriteScanCountsEveryForm plants one statement of each form in a source file and checks that the scan counts it:
// any letter case, any spacing or line break, the replace forms, and each table a write can change (decision 32).
func TestNodeWriteScanCountsEveryForm(t *testing.T) {
	const src = `package store

func planted() {
	_ = "UPDATE nodes SET name = ?"
	_ = "update   nodes\n\tSET name = ?"
	_ = "INSERT\n  OR REPLACE INTO nodes (id) VALUES (?)"
	_ = "replace into nodes (id) VALUES (?)"
	_ = "Delete From NODES WHERE id = ?"
	_ = "UPDATE versions SET label = ?"
	_ = "delete from versions where id = ?"
	_ = "INSERT OR REPLACE INTO versions (id) VALUES (?)"
	_ = "UPDATE folders SET name = ?"
	_ = "DELETE FROM folders WHERE id = ?"
	_ = "DELETE FROM accounts WHERE id = ?"
	_ = "update accounts set status = ?"
	_ = "SELECT id FROM nodes"
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "planted.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := nodeWriteCounts(t, []guardFile{{path: "planted.go", fset: fset, file: f}})
	want := map[string]int{
		"planted.go planted UPDATE nodes":                    2,
		"planted.go planted INSERT OR REPLACE INTO nodes":    1,
		"planted.go planted REPLACE INTO nodes":              1,
		"planted.go planted DELETE FROM nodes":               1,
		"planted.go planted UPDATE versions":                 1,
		"planted.go planted DELETE FROM versions":            1,
		"planted.go planted INSERT OR REPLACE INTO versions": 1,
		"planted.go planted UPDATE folders":                  1,
		"planted.go planted DELETE FROM folders":             1,
		"planted.go planted DELETE FROM accounts":            1,
		"planted.go planted UPDATE accounts":                 1,
	}
	if len(got) != len(want) {
		t.Errorf("counts = %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%s = %d, want %d (all counts %v)", k, got[k], n, got)
		}
	}
}
