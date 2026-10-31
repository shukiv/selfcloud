// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Label field types. A field has one of them, and its values take that type.
const (
	LabelTypeText   = "text"
	LabelTypeNumber = "number"
	LabelTypeDate   = "date"
	LabelTypeUser   = "user"
	LabelTypeChoice = "choice"
)

// MaxLabelFields is how many label fields an account may define; MaxLabelChoices is how many choices one field may
// hold, archived ones included.
const (
	MaxLabelFields  = 50
	MaxLabelChoices = 100
)

// LabelField is a field an account owner defines. Choices are set on a choice field, in position order. Values is the
// number of nodes that hold a value for it, counted only when a reader asks for counts.
type LabelField struct {
	ID         string
	AccountID  string
	Name       string
	Key        string
	Type       string
	Restricted bool
	Position   int
	CreatedBy  string
	CreatedAt  time.Time
	UpdatedBy  string
	UpdatedAt  time.Time
	Choices    []*LabelChoice
	Values     int
}

// LabelChoice is one choice of a choice field. An archived choice keeps the values that use it and takes no new ones.
type LabelChoice struct {
	ID       string
	FieldID  string
	Name     string
	Key      string
	Position int
	Archived bool
}

// LabelValue is one node's value for one field. Exactly one of the value fields is set, the one the field's type names.
type LabelValue struct {
	FieldID  string
	Text     *string
	Num      *float64
	Date     *string
	UserID   *string
	ChoiceID *string
	SetBy    string
	SetAt    time.Time
}

// LabelFields lists an account's fields, in position order, with their choices. Values are counted only when counts is
// true: a count reads every value of the account, so a reader that is not an owner does not ask for it.
func (s *Store) LabelFields(ctx context.Context, accountID string, counts bool) ([]*LabelField, error) {
	return labelFieldsIn(ctx, s.r, accountID, counts)
}

// LabelField returns one field of the account, with its choices. A field of another account is ErrNotFound.
func (s *Store) LabelField(ctx context.Context, accountID, id string) (*LabelField, error) {
	return labelFieldIn(ctx, s.r, accountID, "id = ?", id)
}

// LabelField returns one field of the account inside a transaction, with its choices. ErrNotFound if it is not there.
func (tx *Tx) LabelField(ctx context.Context, accountID, id string) (*LabelField, error) {
	return labelFieldIn(ctx, tx.q, accountID, "id = ?", id)
}

// LabelFieldByKey returns the field whose name key is key, or ErrNotFound.
func (tx *Tx) LabelFieldByKey(ctx context.Context, accountID, key string) (*LabelField, error) {
	return labelFieldIn(ctx, tx.q, accountID, "name_key = ?", key)
}

// CountLabelFields returns how many fields the account defines.
func (tx *Tx) CountLabelFields(ctx context.Context, accountID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM label_fields WHERE account_id = ?", accountID).Scan(&n)
	return n, err
}

// CountFieldValues returns how many nodes hold a value for the field.
func (tx *Tx) CountFieldValues(ctx context.Context, fieldID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM node_labels WHERE field_id = ?", fieldID).Scan(&n)
	return n, err
}

// CountUserLabelValues counts the values that name a user as their value. Deleting the user deletes them.
func (tx *Tx) CountUserLabelValues(ctx context.Context, accountID, userID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM node_labels WHERE account_id = ? AND value_user = ?",
		accountID, userID).Scan(&n)
	return n, err
}

// CountFieldChoices returns how many choices the field holds, archived ones included.
func (tx *Tx) CountFieldChoices(ctx context.Context, fieldID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM label_choices WHERE field_id = ?", fieldID).Scan(&n)
	return n, err
}

// InsertLabelField adds a field and its choices. The name key must be unused in the account: the caller checks it.
func (tx *Tx) InsertLabelField(ctx context.Context, f *LabelField) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO label_fields
		(id, account_id, name, name_key, type, restricted, position, created_by, created_at, updated_by, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.AccountID, f.Name, f.Key, f.Type, boolInt(f.Restricted), f.Position, f.CreatedBy, millis(f.CreatedAt),
		f.UpdatedBy, millis(f.UpdatedAt))
	if err != nil {
		return err
	}
	for _, c := range f.Choices {
		if err := tx.InsertLabelChoice(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// UpdateLabelField saves a field's name, type, restriction and updater. Its choices are saved with InsertLabelChoice
// and UpdateLabelChoice.
func (tx *Tx) UpdateLabelField(ctx context.Context, f *LabelField) error {
	_, err := tx.q.ExecContext(ctx, `UPDATE label_fields SET name = ?, name_key = ?, type = ?, restricted = ?,
		updated_by = ?, updated_at = ? WHERE account_id = ? AND id = ?`,
		f.Name, f.Key, f.Type, boolInt(f.Restricted), f.UpdatedBy, millis(f.UpdatedAt), f.AccountID, f.ID)
	return err
}

// DeleteLabelField removes a field with its choices and values, through the cascades.
func (tx *Tx) DeleteLabelField(ctx context.Context, accountID, id string) error {
	_, err := tx.q.ExecContext(ctx, "DELETE FROM label_fields WHERE account_id = ? AND id = ?", accountID, id)
	return err
}

// DeleteFieldChoices removes every choice of a field. It is used only when the field's type stops being choice, and
// then the field holds no values.
func (tx *Tx) DeleteFieldChoices(ctx context.Context, fieldID string) error {
	_, err := tx.q.ExecContext(ctx, "DELETE FROM label_choices WHERE field_id = ?", fieldID)
	return err
}

// InsertLabelChoice adds a choice to a field.
func (tx *Tx) InsertLabelChoice(ctx context.Context, c *LabelChoice) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO label_choices (id, field_id, name, name_key, position, archived)
		VALUES (?, ?, ?, ?, ?, ?)`, c.ID, c.FieldID, c.Name, c.Key, c.Position, boolInt(c.Archived))
	return err
}

// UpdateLabelChoice saves a choice's name and archived flag.
func (tx *Tx) UpdateLabelChoice(ctx context.Context, c *LabelChoice) error {
	_, err := tx.q.ExecContext(ctx, `UPDATE label_choices SET name = ?, name_key = ?, archived = ?
		WHERE field_id = ? AND id = ?`, c.Name, c.Key, boolInt(c.Archived), c.FieldID, c.ID)
	return err
}

// LabelValues returns a node's values, keyed by field ID. Only fields of accountID are read.
func (s *Store) LabelValues(ctx context.Context, accountID, nodeID string) (map[string]*LabelValue, error) {
	return labelValuesIn(ctx, s.r, accountID, nodeID)
}

// LabelValue returns the node's value for one field, or ErrNotFound when it has none.
func (tx *Tx) LabelValue(ctx context.Context, accountID, nodeID, fieldID string) (*LabelValue, error) {
	vals, err := labelValuesIn(ctx, tx.q, accountID, nodeID, fieldID)
	if err != nil {
		return nil, err
	}
	v := vals[fieldID]
	if v == nil {
		return nil, ErrNotFound
	}
	return v, nil
}

// SetLabelValue writes a node's value for a field, replacing any value it had. The caller has checked the value against
// the field's type.
func (tx *Tx) SetLabelValue(ctx context.Context, accountID, nodeID string, v *LabelValue) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO node_labels
		(node_id, field_id, account_id, value_text, value_num, value_date, value_user, value_choice, set_by, set_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (node_id, field_id) DO UPDATE SET value_text = excluded.value_text, value_num = excluded.value_num,
			value_date = excluded.value_date, value_user = excluded.value_user, value_choice = excluded.value_choice,
			set_by = excluded.set_by, set_at = excluded.set_at`,
		nodeID, v.FieldID, accountID, v.Text, v.Num, v.Date, v.UserID, v.ChoiceID, v.SetBy, millis(v.SetAt))
	return err
}

// ClearLabelValue removes a node's value for a field. It reports whether there was one.
func (tx *Tx) ClearLabelValue(ctx context.Context, accountID, nodeID, fieldID string) (bool, error) {
	res, err := tx.q.ExecContext(ctx, "DELETE FROM node_labels WHERE account_id = ? AND node_id = ? AND field_id = ?",
		accountID, nodeID, fieldID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// HasLabelFields reports whether the account defines any field. An account with none has no values either, so a copy
// can skip the label copy.
func (tx *Tx) HasLabelFields(ctx context.Context, accountID string) (bool, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM label_fields WHERE account_id = ?)", accountID).Scan(&n)
	return n > 0, err
}

// LabelCopy is one copied node and the node it was copied from.
type LabelCopy struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// CopyLabelValues gives each copied node the values of its source, in one statement. A copy keeps its labels; a
// source with none adds no row. A restricted value is copied only when keepRestricted is set, which the caller
// sets only for a copy made by a manager of the source: the copy would otherwise hand the value to whoever can
// read the copy.
func (tx *Tx) CopyLabelValues(ctx context.Context, accountID string, pairs []LabelCopy, keepRestricted bool) error {
	if len(pairs) == 0 {
		return nil
	}
	b, err := json.Marshal(pairs)
	if err != nil {
		return err
	}
	_, err = tx.q.ExecContext(ctx, `INSERT INTO node_labels
		(node_id, field_id, account_id, value_text, value_num, value_date, value_user, value_choice, set_by, set_at)
		SELECT j.value ->> '$.to', nl.field_id, nl.account_id, nl.value_text, nl.value_num, nl.value_date,
			nl.value_user, nl.value_choice, nl.set_by, nl.set_at
		FROM json_each(?) AS j JOIN node_labels nl ON nl.node_id = j.value ->> '$.from'
		JOIN label_fields f ON f.id = nl.field_id
		WHERE nl.account_id = ? AND (f.restricted = 0 OR ?)`, string(b), accountID, keepRestricted)
	return err
}

// SubtreeHasRestricted reports whether any node under id of accountID, id included and trashed nodes too, holds a
// value of a restricted field. The walk is the one a move makes over the subtree, and each step is a primary key
// lookup on node_labels.
func (tx *Tx) SubtreeHasRestricted(ctx context.Context, accountID, id string) (bool, error) {
	var found bool
	err := tx.q.QueryRowContext(ctx, `WITH RECURSIVE sub(nid) AS (
		SELECT id FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT c.id FROM nodes c JOIN sub ON c.parent_id = sub.nid WHERE c.account_id = ?
	)
	SELECT EXISTS (SELECT 1 FROM sub JOIN node_labels nl ON nl.node_id = sub.nid AND nl.account_id = ?
		JOIN label_fields f ON f.id = nl.field_id WHERE f.restricted = 1)`,
		accountID, id, accountID, accountID).Scan(&found)
	return found, err
}

// labelFieldsIn reads the fields of an account that match where, with their choices and, when counts is set, their
// value counts. Each part is one query, whatever the number of fields.
func labelFieldsIn(ctx context.Context, q querier, accountID string, counts bool) ([]*LabelField, error) {
	rows, err := q.QueryContext(ctx, labelFieldSelect+" WHERE account_id = ? ORDER BY position, name_key", accountID)
	if err != nil {
		return nil, err
	}
	fields, err := scanLabelFields(rows)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return fields, nil
	}
	byField := make(map[string]*LabelField, len(fields))
	for _, f := range fields {
		byField[f.ID] = f
	}
	crows, err := q.QueryContext(ctx, `SELECT c.id, c.field_id, c.name, c.name_key, c.position, c.archived
		FROM label_choices c JOIN label_fields f ON f.id = c.field_id
		WHERE f.account_id = ? ORDER BY c.position, c.name_key`, accountID)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var c LabelChoice
		var archived int
		if err := crows.Scan(&c.ID, &c.FieldID, &c.Name, &c.Key, &c.Position, &archived); err != nil {
			return nil, err
		}
		c.Archived = archived == 1
		if f := byField[c.FieldID]; f != nil {
			f.Choices = append(f.Choices, &c)
		}
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}
	if counts {
		// The count shown in the list leaves out the values of a staged copy: a copy is not visible until it publishes.
		// (Where a count decides a deletion, CountFieldValues and CountUserLabelValues, staged values count, because
		// the deletion removes them.)
		vrows, err := q.QueryContext(ctx, `SELECT v.field_id, COUNT(*) FROM node_labels v
			JOIN nodes n ON n.id = v.node_id JOIN folders f ON f.id = n.folder_id AND f.staged = 0
			WHERE v.account_id = ? GROUP BY v.field_id`, accountID)
		if err != nil {
			return nil, err
		}
		defer vrows.Close()
		for vrows.Next() {
			var id string
			var n int
			if err := vrows.Scan(&id, &n); err != nil {
				return nil, err
			}
			if f := byField[id]; f != nil {
				f.Values = n
			}
		}
		if err := vrows.Err(); err != nil {
			return nil, err
		}
	}
	return fields, nil
}

const labelFieldSelect = `SELECT id, account_id, name, name_key, type, restricted, position, created_by, created_at,
	updated_by, updated_at FROM label_fields`

// labelFieldIn reads one field of the account that matches where, with its choices.
func labelFieldIn(ctx context.Context, q querier, accountID, where string, arg any) (*LabelField, error) {
	rows, err := q.QueryContext(ctx, labelFieldSelect+" WHERE account_id = ? AND "+where, accountID, arg)
	if err != nil {
		return nil, err
	}
	fields, err := scanLabelFields(rows)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, ErrNotFound
	}
	f := fields[0]
	crows, err := q.QueryContext(ctx, `SELECT id, field_id, name, name_key, position, archived FROM label_choices
		WHERE field_id = ? ORDER BY position, name_key`, f.ID)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var c LabelChoice
		var archived int
		if err := crows.Scan(&c.ID, &c.FieldID, &c.Name, &c.Key, &c.Position, &archived); err != nil {
			return nil, err
		}
		c.Archived = archived == 1
		f.Choices = append(f.Choices, &c)
	}
	return f, crows.Err()
}

func scanLabelFields(rows *sql.Rows) ([]*LabelField, error) {
	defer rows.Close()
	var out []*LabelField
	for rows.Next() {
		var f LabelField
		var restricted int
		var created, updated int64
		if err := rows.Scan(&f.ID, &f.AccountID, &f.Name, &f.Key, &f.Type, &restricted, &f.Position, &f.CreatedBy,
			&created, &f.UpdatedBy, &updated); err != nil {
			return nil, err
		}
		f.Restricted = restricted == 1
		f.CreatedAt, f.UpdatedAt = fromMillis(created), fromMillis(updated)
		out = append(out, &f)
	}
	return out, rows.Err()
}

// labelValuesIn reads a node's values of accountID, for all its fields or for the ones named.
func labelValuesIn(ctx context.Context, q querier, accountID, nodeID string, fields ...string) (map[string]*LabelValue, error) {
	query := `SELECT field_id, value_text, value_num, value_date, value_user, value_choice, set_by, set_at
		FROM node_labels WHERE account_id = ? AND node_id = ?`
	args := []any{accountID, nodeID}
	if len(fields) > 0 {
		query += " AND field_id = ?"
		args = append(args, fields[0])
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*LabelValue{}
	for rows.Next() {
		var v LabelValue
		var text, date, user, choice sql.NullString
		var num sql.NullFloat64
		var at int64
		if err := rows.Scan(&v.FieldID, &text, &num, &date, &user, &choice, &v.SetBy, &at); err != nil {
			return nil, err
		}
		if text.Valid {
			v.Text = &text.String
		}
		if num.Valid {
			v.Num = &num.Float64
		}
		if date.Valid {
			v.Date = &date.String
		}
		if user.Valid {
			v.UserID = &user.String
		}
		if choice.Valid {
			v.ChoiceID = &choice.String
		}
		v.SetAt = fromMillis(at)
		out[v.FieldID] = &v
	}
	return out, rows.Err()
}
