// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// TestReservationTotalsCountsACopy: the sweep compares the reserved bytes of the accounts with the uploads' bytes. A
// copy's reservation is in the first, so the second must count it too, or every sweep during a copy warns.
func TestReservationTotalsCountsACopy(t *testing.T) {
	fx := newCopyFixture(t)
	var counted, declared int64
	if err := fx.s.Write(context.Background(), func(tx *Tx) error {
		var err error
		counted, declared, err = tx.ReservationTotals(context.Background())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if counted != declared {
		t.Errorf("reserved %d, declared by uploads and copies %d: the sweep would warn during a copy", counted, declared)
	}
}

// TestLabelListCountLeavesOutAStagedCopy: the count of a label field in the list is the values a caller can see. A
// staged copy's value is not visible until the publish, so the count stays at the source's one value.
func TestLabelListCountLeavesOutAStagedCopy(t *testing.T) {
	fx := newCopyFixture(t)
	ctx := context.Background()
	if _, err := fx.s.w.ExecContext(ctx, `INSERT INTO label_fields (id, account_id, name, name_key, type, created_at, updated_at)
		VALUES ('lbf_t', 'acc_a', 'Note', 'note', 'text', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.s.w.ExecContext(ctx, `INSERT INTO node_labels (node_id, field_id, account_id, value_text, set_at)
		VALUES ('nod_s1', 'lbf_t', 'acc_a', 'a remark', 0)`); err != nil {
		t.Fatal(err)
	}
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.CopyJobOf(ctx, "job_1")
		if err != nil {
			return err
		}
		_, _, err = tx.StagedCopyBatch(ctx, cur, 10, true, "key_1", Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := fx.labelValuesOf(t, "staged"); n != 1 {
		t.Fatalf("staged label values = %d, want 1: the batch did not copy the label", n)
	}
	fields, err := fx.s.LabelFields(ctx, "acc_a", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fields {
		if f.ID == "lbf_t" && f.Values != 1 {
			t.Errorf("count in the list = %d, want 1 (the source's value; the staged one is not visible)", f.Values)
		}
	}
}

// labelValuesOf counts the label values of the staging folder (staged) or of the source (anything else).
func (fx *copyFixture) labelValuesOf(t *testing.T, which string) int {
	t.Helper()
	folder := "fld_src"
	if which == "staged" {
		folder = "fld_stage"
	}
	var n int
	if err := fx.s.r.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM node_labels v
		JOIN nodes n ON n.id = v.node_id WHERE n.folder_id = ?`, folder).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
