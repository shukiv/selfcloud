// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// noSentenceYet are the audit actions that have no sentence of their own in the viewers, and so read as "made a change"
// (or as "Someone" for an actor the page cannot name). They were written before decision 41, and the review of the
// live audit log did not change them. Each one is a gap the owner has seen and not yet asked to close; adding a sentence
// removes it from this list, and the test fails until it is removed, so the list only shrinks.
var noSentenceYet = map[string]bool{
	"access.approve":          true,
	"access.deny":             true,
	"account.comments_policy": true,
	"account.quota_warning":   true,
	"account.sharing_policy":  true,
	"account.storage_policy":  true,
	"auth.sign_in_link":       true,
	"comment.create":          true,
	"comment.delete":          true,
	"comment.edit":            true,
	"comment.resolve":         true,
	"label_field.create":      true,
	"label_field.delete":      true,
	"label_field.update":      true,
	"link.create":             true,
	"link.delete":             true,
	"link.delete_public":      true,
	"link.expired":            true,
	"link.open":               true,
	"link.reset":              true,
	"link.throttled":          true,
	"link.update":             true,
	"node.appearance":         true,
	"node.copy":               true,
	"node.create":             true,
	"node.label_clear":        true,
	"node.label_set":          true,
	"node.link":               true,
	"node.lock":               true,
	"node.move":               true,
	"node.office":             true,
	"node.rename":             true,
	"node.shortcut":           true,
	"node.unlock":             true,
	"node.version.delete":     true,
	"node.version.restore":    true,
	"node.version.update":     true,
	"node.versions_pruned":    true,
	"node.write":              true,
	"node.zip":                true,
	"upload.create":           true,
	"upload.delete":           true,
	"upload.expire":           true,
	"user.files_moved":        true,
	"user.login":              true,
	"user.login_failed":       true,
	"user.logout":             true,
	"user.sign_in_link":       true,
	"user.tokens_revoke":      true,
	"webhook.create":          true,
	"webhook.delete":          true,
	"webhook.disabled":        true,
	"webhook.retry":           true,
	"webhook.rewrap":          true,
	"webhook.secret":          true,
	"webhook.test":            true,
	"webhook.update":          true,
}

// TestEveryAuditActionHasASentence fails for an audit action the server can write that has no sentence of its own in the
// audit viewers, unless the action is named in noSentenceYet. The viewers read web/src/lib/audit/sentence.ts, where each
// action has a case label. The actions come from the same source walk as TestEveryAuditActionIsClassified, so a new
// action is caught when it is written. The actions of decision 41 (impersonation, admin password refusals, password set)
// are not on the list: each has its sentence, and an action of this slice cannot be added to the list.
func TestEveryAuditActionHasASentence(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../web/src/lib/audit/sentence.ts")
	if err != nil {
		t.Fatal(err)
	}
	found := auditActions(t, parseInternal(t))
	var missing, stale []string
	for action, where := range found {
		has := strings.Contains(string(src), "case '"+action+"':")
		switch {
		case !has && !noSentenceYet[action]:
			missing = append(missing, action+" at "+where)
		case has && noSentenceYet[action]:
			stale = append(stale, action)
		}
	}
	for action := range noSentenceYet {
		if _, ok := found[action]; !ok {
			stale = append(stale, action+" (no longer written)")
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("audit actions with no sentence of their own in web/src/lib/audit/sentence.ts:\n  %s",
			strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("noSentenceYet names actions that now have a sentence, or are no longer written; remove them:\n  %s",
			strings.Join(stale, "\n  "))
	}
}
