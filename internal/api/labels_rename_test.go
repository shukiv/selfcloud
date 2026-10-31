// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// TestRenameKeepsAChoiceArchived: a choice named by ID is renamed with its archived flag left as it was when the
// request does not carry one, so a rename never restores an archived choice.
func TestRenameKeepsAChoiceArchived(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	path := "/api/v1/accounts/" + lf.acc.ID + "/label-fields/" + lf.status
	wantStatus(t, lf.keyDo("PATCH", path, map[string]any{"choices": []map[string]any{
		{"id": lf.openID, "name": "Open", "archived": true}}}), 200)
	wantStatus(t, lf.keyDo("PATCH", path, map[string]any{"choices": []map[string]any{
		{"id": lf.openID, "name": "Opened"}}}), 200)
	found := false
	for _, f := range listFields(t, lf.keyDo) {
		if f.ID != lf.status {
			continue
		}
		for _, c := range f.Choices {
			if c.ID == lf.openID {
				found = true
				if c.Name != "Opened" || !c.Archived {
					t.Errorf("after a rename: %+v, want the new name and still archived", c)
				}
			}
		}
	}
	if !found {
		t.Fatal("the renamed choice is not listed")
	}
}

// TestRenameFreesItsNameForANewChoice: a choice renamed away from a name frees that name for a new choice in the same
// request, so renaming Open to Opened and adding a new Open works in one PATCH, whatever the order of the list.
func TestRenameFreesItsNameForANewChoice(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	path := "/api/v1/accounts/" + lf.acc.ID + "/label-fields/" + lf.status
	wantStatus(t, lf.keyDo("PATCH", path, map[string]any{"choices": []map[string]any{
		{"name": "Open"}, {"id": lf.openID, "name": "Opened"}}}), 200)
	var openIDs []string
	renamed := ""
	for _, f := range listFields(t, lf.keyDo) {
		if f.ID != lf.status {
			continue
		}
		for _, c := range f.Choices {
			if c.Name == "Open" {
				openIDs = append(openIDs, c.ID)
			}
			if c.ID == lf.openID {
				renamed = c.Name
			}
		}
	}
	if renamed != "Opened" {
		t.Errorf("the renamed choice is %q, want Opened", renamed)
	}
	if len(openIDs) != 1 || openIDs[0] == lf.openID {
		t.Errorf("choices named Open = %v, want the one new choice", openIDs)
	}
}

// TestRenameStillRefusesAClashWithAnUnnamedChoice: a rename onto the name of a choice the request does not name is
// still refused as name_taken.
func TestRenameStillRefusesAClashWithAnUnnamedChoice(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	path := "/api/v1/accounts/" + lf.acc.ID + "/label-fields/" + lf.status
	e := wantError(t, lf.keyDo("PATCH", path, map[string]any{"choices": []map[string]any{
		{"id": lf.openID, "name": "Done"}}}), 409, apierr.Conflict)
	if e.Details["reason"] != "name_taken" {
		t.Errorf("reason %v, want name_taken", e.Details["reason"])
	}
}
