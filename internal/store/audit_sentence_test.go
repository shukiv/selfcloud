// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// noSentenceYet are the audit actions that have no sentence of their own in the viewers. It is empty: every action the
// server writes has its sentence in web/src/lib/audit/sentence.ts (slice audit-s1 gave the older ones theirs). The map
// stays, so the two-way check below still runs: an action written without a sentence fails the test, and an action
// listed here that has a sentence fails it too. A new gap is an action to give a sentence, not an entry to add here.
var noSentenceYet = map[string]bool{}

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
