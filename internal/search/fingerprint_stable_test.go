// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import "testing"

// TestFingerprintKeepsItsValueWithoutTheNewFields: a request that names none of the created bounds, the newest order,
// starred or shared keeps the fingerprint it had before those fields existed, so a cursor made before them still opens.
// The literals were computed on the tree before the fields were added.
func TestFingerprintKeepsItsValueWithoutTheNewFields(t *testing.T) {
	term := &Request{AccountID: "acc_a", Terms: []string{"report"}, ModifiedFrom: -1, ModifiedTo: -1,
		SizeMin: -1, SizeMax: -1, Limit: 25}
	if got := term.Fingerprint(); got != "b3ada6465e0d8c121d700463ae2b6b11" {
		t.Errorf("a term request's fingerprint = %s; want the value it had before the new fields", got)
	}
	filtered := &Request{AccountID: "acc_a", Kind: "file", ModifiedFrom: 1000, ModifiedTo: 2000,
		SizeMin: -1, SizeMax: -1, LabelFilters: [][2]string{{"lbf_a", "Hel"}}, ScopeID: "nod_a", Limit: 2}
	if got := filtered.Fingerprint(); got != "b56b263fb224a595efda47f749084b73" {
		t.Errorf("a filtered request's fingerprint = %s; want the value it had before the new fields", got)
	}
}
