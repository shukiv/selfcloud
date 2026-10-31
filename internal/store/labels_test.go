// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// labelSchemaFixture is schemaFixture with one label field of each kind on acc_a, and a choice field's first choice.
func labelSchemaFixture(t *testing.T) (*Store, string) {
	t.Helper()
	s := schemaFixture(t)
	ctx := context.Background()
	if _, err := s.w.ExecContext(ctx, `INSERT INTO label_fields (id, account_id, name, name_key, type, created_at, updated_at)
		VALUES ('lbf_text', 'acc_a', 'Note', 'note', 'text', 0, 0), ('lbf_status', 'acc_a', 'Status', 'status', 'choice', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO label_choices (id, field_id, name, name_key) VALUES ('lbc_open', 'lbf_status', 'Open', 'open')`); err != nil {
		t.Fatal(err)
	}
	return s, "lbf_text"
}

// TestNodeLabelsHoldOneValue pins the value columns: a row carries at most one value, so a second column is refused.
func TestNodeLabelsHoldOneValue(t *testing.T) {
	s, _ := labelSchemaFixture(t)
	if err := insertNodeSQL(t, s, "nod_one", "file", 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.w.ExecContext(ctx, `INSERT INTO node_labels (node_id, field_id, account_id, value_text, value_num, set_at)
		VALUES ('nod_one', 'lbf_text', 'acc_a', 'a', 1, 0)`); err == nil {
		t.Error("a label row with a text and a number value was accepted")
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO node_labels (node_id, field_id, account_id, value_text, set_at)
		VALUES ('nod_one', 'lbf_text', 'acc_a', ?, 0)`, fmt.Sprintf("%501s", "x")); err == nil {
		t.Error("a text value of 501 characters was accepted")
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO node_labels (node_id, field_id, account_id, value_date, set_at)
		VALUES ('nod_one', 'lbf_text', 'acc_a', '2026-1-01', 0)`); err == nil {
		t.Error("a date that is not YYYY-MM-DD was accepted")
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO node_labels (node_id, field_id, account_id, value_text, set_at)
		VALUES ('nod_one', 'lbf_text', 'acc_a', 'ok', 0)`); err != nil {
		t.Fatalf("a valid text value: %v", err)
	}
}

// TestLabelFieldsCascade pins the removals: a field takes its choices and values; a node takes its values; and a
// field name is unique in its account.
func TestLabelFieldsCascade(t *testing.T) {
	s, _ := labelSchemaFixture(t)
	ctx := context.Background()
	if err := insertNodeSQL(t, s, "nod_one", "file", 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO node_labels (node_id, field_id, account_id, value_choice, set_at)
		VALUES ('nod_one', 'lbf_status', 'acc_a', 'lbc_open', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO label_fields (id, account_id, name, name_key, type, created_at, updated_at)
		VALUES ('lbf_dup', 'acc_a', 'NOTE', 'note', 'number', 0, 0)`); err == nil {
		t.Error("a second field with the same name key in the account was accepted")
	}
	if _, err := s.w.ExecContext(ctx, `DELETE FROM nodes WHERE id = 'nod_one'`); err != nil {
		t.Fatal(err)
	}
	if n := labelRows(t, s, "node_labels"); n != 0 {
		t.Errorf("values after their node was deleted = %d, want 0", n)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO node_labels (node_id, field_id, account_id, value_choice, set_at)
		SELECT 'nod_root', 'lbf_status', 'acc_a', 'lbc_open', 0 WHERE 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `DELETE FROM label_fields WHERE id = 'lbf_status'`); err != nil {
		t.Fatal(err)
	}
	if n := labelRows(t, s, "label_choices"); n != 0 {
		t.Errorf("choices after their field was deleted = %d, want 0", n)
	}
	if n := labelRows(t, s, "node_labels"); n != 0 {
		t.Errorf("values after their field was deleted = %d, want 0", n)
	}
}

func labelRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.w.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCopyLabelValuesGivesTheCopyItsSourcesValues: a copy takes the values of its source, field by field, and the
// source keeps its own. A node with no value adds no row.
func TestCopyLabelValuesGivesTheCopyItsSourcesValues(t *testing.T) {
	s, _ := labelSchemaFixture(t)
	ctx := context.Background()
	for _, id := range []string{"nod_src", "nod_plain", "nod_copy", "nod_copy_plain"} {
		if err := insertNodeSQL(t, s, id, "file", 1, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO node_labels (node_id, field_id, account_id, value_text, value_num, set_by, set_at)
		VALUES ('nod_src', 'lbf_text', 'acc_a', 'kept', NULL, 'usr_x', 5)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO node_labels (node_id, field_id, account_id, value_choice, set_at)
		VALUES ('nod_src', 'lbf_status', 'acc_a', 'lbc_open', 6)`); err != nil {
		t.Fatal(err)
	}
	err := s.Write(ctx, func(tx *Tx) error {
		return tx.CopyLabelValues(ctx, "acc_a", []LabelCopy{
			{From: "nod_src", To: "nod_copy"}, {From: "nod_plain", To: "nod_copy_plain"},
		}, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	var text, by string
	var choice *string
	var at int64
	if err := s.w.QueryRowContext(ctx, `SELECT value_text, set_by, set_at FROM node_labels
		WHERE node_id = 'nod_copy' AND field_id = 'lbf_text'`).Scan(&text, &by, &at); err != nil {
		t.Fatal(err)
	}
	if text != "kept" || by != "usr_x" || at != 5 {
		t.Errorf("copied text value = %q by %q at %d, want kept by usr_x at 5", text, by, at)
	}
	if err := s.w.QueryRowContext(ctx, `SELECT value_choice FROM node_labels
		WHERE node_id = 'nod_copy' AND field_id = 'lbf_status'`).Scan(&choice); err != nil || choice == nil || *choice != "lbc_open" {
		t.Errorf("copied choice = %v, %v; want lbc_open", choice, err)
	}
	if n := labelRows(t, s, "node_labels"); n != 4 {
		t.Errorf("rows = %d, want 4 (two on the source, two on the copy; the plain node adds none)", n)
	}
	// Another account's labels are not read by a copy that names this account.
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.CopyLabelValues(ctx, "acc_other", []LabelCopy{{From: "nod_src", To: "nod_copy_plain"}}, true)
	}); err != nil {
		t.Fatal(err)
	}
	if n := labelRows(t, s, "node_labels"); n != 4 {
		t.Errorf("rows after a copy under another account = %d, want 4", n)
	}
}

// TestLabelFieldRoundTrip reads back what the transaction wrote: the field, its choices with their archive flag, its
// value, and the value counts a reader may ask for.
func TestLabelFieldRoundTrip(t *testing.T) {
	s := schemaFixture(t)
	ctx := context.Background()
	now := time.UnixMilli(1000).UTC()
	f := &LabelField{ID: "lbf_r", AccountID: "acc_a", Name: "Status", Key: "status", Type: LabelTypeChoice,
		Restricted: true, Position: 0, CreatedBy: "usr_a", CreatedAt: now, UpdatedBy: "usr_a", UpdatedAt: now,
		Choices: []*LabelChoice{{ID: "lbc_1", FieldID: "lbf_r", Name: "Open", Key: "open", Position: 1},
			{ID: "lbc_2", FieldID: "lbf_r", Name: "Done", Key: "done", Position: 2}}}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertLabelField(ctx, f) }); err != nil {
		t.Fatal(err)
	}
	if err := insertNodeSQL(t, s, "nod_rt", "file", 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	choice := "lbc_2"
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.SetLabelValue(ctx, "acc_a", "nod_rt", &LabelValue{FieldID: "lbf_r", ChoiceID: &choice, SetBy: "usr_a", SetAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LabelField(ctx, "acc_a", "lbf_r")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Restricted || got.Type != LabelTypeChoice || len(got.Choices) != 2 || got.Choices[1].Name != "Done" {
		t.Errorf("field = %+v, want restricted choice field with two choices", got)
	}
	if _, err := s.LabelField(ctx, "acc_b", "lbf_r"); err != ErrNotFound {
		t.Errorf("a field of another account: err = %v, want ErrNotFound", err)
	}
	list, err := s.LabelFields(ctx, "acc_a", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Values != 1 {
		t.Errorf("listed fields = %+v, want one field with one value", list)
	}
	vals, err := s.LabelValues(ctx, "acc_a", "nod_rt")
	if err != nil {
		t.Fatal(err)
	}
	if v := vals["lbf_r"]; v == nil || v.ChoiceID == nil || *v.ChoiceID != "lbc_2" || v.Text != nil {
		t.Errorf("value = %+v, want the choice lbc_2 and no other column", v)
	}
}
