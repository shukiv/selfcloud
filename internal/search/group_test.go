// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"strings"
	"testing"
)

// noRanges is a request with no bounds set, so only the field under test adds a clause.
func noRanges(r Request) Request {
	r.ModifiedFrom, r.ModifiedTo, r.SizeMin, r.SizeMax = -1, -1, -1, -1
	return r
}

// TestCandidateSQLFiltersByGroup: a file group adds one clause on the node's type group and binds the group as an
// argument, never as text; the group keeps the rows to files.
func TestCandidateSQLFiltersByGroup(t *testing.T) {
	r := noRanges(Request{AccountID: "acc_a", Group: "pdf"})
	sqlText, args, err := r.candidateSQL(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sqlText, "AND n.kind = 'file' AND n.type_group = ?") {
		t.Errorf("no group clause in %q", sqlText)
	}
	if strings.Contains(sqlText, "pdf") {
		t.Error("the group value is in the SQL text")
	}
	found := false
	for _, a := range args {
		if a == "pdf" {
			found = true
		}
	}
	if !found {
		t.Errorf("group not bound: %v", args)
	}
}

// TestCandidateSQLWithoutGroupHasNoGroupClause: a request with no group, the search before ff1, reads no group column.
func TestCandidateSQLWithoutGroupHasNoGroupClause(t *testing.T) {
	r := noRanges(Request{AccountID: "acc_a"})
	sqlText, _, err := r.candidateSQL(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sqlText, "type_group") {
		t.Errorf("a group clause with no group: %q", sqlText)
	}
}
