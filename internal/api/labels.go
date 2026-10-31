// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
)

// maxLabelTextChars is the longest text a text label may hold, in characters.
const maxLabelTextChars = 500

// labelChoiceJSON is one choice of a choice field. An archived choice keeps the values that use it.
type labelChoiceJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}

// labelFieldJSON is a field as the account sees it. Values is the number of nodes holding a value for the field; the
// account owner and the account key see it, and nobody else.
type labelFieldJSON struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Restricted bool              `json:"restricted"`
	Choices    []labelChoiceJSON `json:"choices"`
	Values     *int              `json:"values,omitempty"`
}

// nodeLabelJSON is one field's value on a node. Value is null when the node has none; a user value carries its
// display name only for a caller that may learn names (peopleNamesSeen), and a choice carries its name and archived flag.
type nodeLabelJSON struct {
	FieldID    string `json:"field_id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Restricted bool   `json:"restricted"`
	Value      any    `json:"value"`
}

// labelUserJSON is the user a user-type value names.
type labelUserJSON struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// labelChoiceValueJSON is the choice a choice-type value names.
type labelChoiceValueJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}

// labelChoiceBody is a choice in a create or update call. An update without an ID adds a choice; one with an ID
// renames or archives that choice.
type labelChoiceBody struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	// Archived is optional: absent, a choice named by ID keeps its flag, and a new choice is not archived.
	Archived *bool `json:"archived,omitempty"`
}

// labelFieldBody is the body of a create call.
type labelFieldBody struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Restricted bool              `json:"restricted"`
	Choices    []labelChoiceBody `json:"choices"`
}

// labelFieldPatch is the body of an update call. A field that is absent is left as it is. Choices are changed one by
// one: a choice that is not named is kept, so a choice is archived, never deleted.
type labelFieldPatch struct {
	Name       *string           `json:"name"`
	Type       *string           `json:"type"`
	Restricted *bool             `json:"restricted"`
	Choices    []labelChoiceBody `json:"choices"`
}

// labelDefiner reports whether p defines the labels of accountID: the account's owner signed in, or its account key.
// A delegated token never does, and an installation admin does not reach an account's labels.
func labelDefiner(p *auth.Principal, accountID string) bool {
	return p != nil && !p.IsAdmin() && p.ManagesAccount(accountID)
}

// labelAccount loads the account a label definition call names, for a caller that defines its labels. Other callers
// are told the account does not exist, as managedAccount does; an installation admin is refused outright.
func (s *Server) labelAccount(r *http.Request) (*store.Account, error) {
	p := principalOf(r)
	if p != nil && p.IsAdmin() {
		return nil, apierr.New(apierr.Forbidden, "installation admins do not reach an account's labels")
	}
	acc, err := s.writableAccount(r)
	if err != nil {
		return nil, err
	}
	if !labelDefiner(p, acc.ID) {
		return nil, apierr.New(apierr.NotFound, "no such account")
	}
	return acc, nil
}

// labelFieldsJSON converts fields for a caller. Values is set only when the caller may see counts.
func labelFieldsJSON(fields []*store.LabelField, counts bool) []labelFieldJSON {
	out := make([]labelFieldJSON, 0, len(fields))
	for _, f := range fields {
		j := labelFieldJSON{ID: f.ID, Name: f.Name, Type: f.Type, Restricted: f.Restricted, Choices: []labelChoiceJSON{}}
		for _, c := range f.Choices {
			j.Choices = append(j.Choices, labelChoiceJSON{ID: c.ID, Name: c.Name, Archived: c.Archived})
		}
		if counts {
			n := f.Values
			j.Values = &n
		}
		out = append(out, j)
	}
	return out
}

// handleListLabelFields lists the account's label fields. Every user of the account, the account key and a delegated
// token see them, restricted ones included: the names of restricted fields are visible to every member.
func (s *Server) handleListLabelFields(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	ctx := r.Context()
	counts := labelDefiner(p, p.AccountID)
	fields, err := s.store.LabelFields(ctx, p.AccountID, counts)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, listResponse[labelFieldJSON]{Items: labelFieldsJSON(fields, counts)})
	return nil
}

// labelChoicesFor checks a list of choices against the choices a field holds, and returns the names and keys to store.
// A new choice is given an ID; a named one must be one of the field's. Two choices of one field may not share a name.
func labelChoicesFor(existing []*store.LabelChoice, in []labelChoiceBody) ([]*store.LabelChoice, error) {
	total := len(existing)
	byID := map[string]*store.LabelChoice{}
	for _, c := range existing {
		byID[c.ID] = c
	}
	named := map[string]bool{}
	for _, b := range in {
		if b.ID == "" {
			continue
		}
		if byID[b.ID] == nil {
			return nil, apierr.New(apierr.InvalidRequest, "a choice named by its id is not one of this field's choices")
		}
		named[b.ID] = true
	}
	// The keys in use: a choice the request names keeps no key yet, so a rename frees its old name for the rest.
	keys := map[string]string{}
	for _, c := range existing {
		if !named[c.ID] {
			keys[c.Key] = c.ID
		}
	}
	out := make([]*store.LabelChoice, len(in))
	// The renames come first, so a new choice may take the name a renamed choice gives up in the same request.
	for i, b := range in {
		if b.ID == "" {
			continue
		}
		found := byID[b.ID]
		name, key, err := names.Clean(b.Name)
		if err != nil {
			return nil, err
		}
		if other, taken := keys[key]; taken && other != found.ID {
			return nil, apierr.New(apierr.Conflict, "another choice of this field has that name").With("reason", "name_taken")
		}
		keys[key] = found.ID
		archived := found.Archived
		if b.Archived != nil {
			archived = *b.Archived
		}
		out[i] = &store.LabelChoice{ID: found.ID, FieldID: found.FieldID, Name: name, Key: key,
			Position: found.Position, Archived: archived}
	}
	for i, b := range in {
		if b.ID != "" {
			continue
		}
		name, key, err := names.Clean(b.Name)
		if err != nil {
			return nil, err
		}
		if _, taken := keys[key]; taken {
			return nil, apierr.New(apierr.Conflict, "another choice of this field has that name").With("reason", "name_taken")
		}
		total++
		if total > store.MaxLabelChoices {
			return nil, apierr.Newf(apierr.Conflict, "a field may hold at most %d choices", store.MaxLabelChoices).
				With("reason", "limit").With("limit", store.MaxLabelChoices)
		}
		keys[key] = ""
		out[i] = &store.LabelChoice{ID: id.New("lbc"), Name: name, Key: key,
			Archived: b.Archived != nil && *b.Archived, Position: total}
	}
	return out, nil
}

// labelTypeOK reports whether t is a field type.
func labelTypeOK(t string) bool {
	switch t {
	case store.LabelTypeText, store.LabelTypeNumber, store.LabelTypeDate, store.LabelTypeUser, store.LabelTypeChoice:
		return true
	}
	return false
}

// handleCreateLabelField defines a label field. The account owner and the account key may; the field's name is checked
// and must be new in the account, and the account holds at most store.MaxLabelFields fields.
func (s *Server) handleCreateLabelField(w http.ResponseWriter, r *http.Request) error {
	var in labelFieldBody
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	name, key, err := names.Clean(in.Name)
	if err != nil {
		return err
	}
	if !labelTypeOK(in.Type) {
		return apierr.New(apierr.InvalidRequest, "the type must be text, number, date, user or choice")
	}
	if in.Type != store.LabelTypeChoice && len(in.Choices) > 0 {
		return apierr.New(apierr.InvalidRequest, "only a choice field has choices")
	}
	acc, err := s.labelAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	now := store.Now()
	f := &store.LabelField{ID: id.New("lbf"), AccountID: acc.ID, Name: name, Key: key, Type: in.Type,
		Restricted: in.Restricted, CreatedBy: p.ID, CreatedAt: now, UpdatedBy: p.ID, UpdatedAt: now}
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		count, err := tx.CountLabelFields(ctx, acc.ID)
		if err != nil {
			return err
		}
		if count >= store.MaxLabelFields {
			return apierr.Newf(apierr.Conflict, "an account may define at most %d label fields", store.MaxLabelFields).
				With("reason", "limit").With("limit", store.MaxLabelFields)
		}
		if _, err := tx.LabelFieldByKey(ctx, acc.ID, key); err == nil {
			return apierr.New(apierr.Conflict, "a label field has that name").With("reason", "name_taken")
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		f.Position = count
		choices, err := labelChoicesFor(nil, in.Choices)
		if err != nil {
			return err
		}
		for _, c := range choices {
			c.FieldID = f.ID
		}
		f.Choices = choices
		if err := tx.InsertLabelField(ctx, f); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "label_field.create", f.ID,
			map[string]any{"type": f.Type, "restricted": f.Restricted, "choices": len(choices)}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, labelFieldsJSON([]*store.LabelField{f}, true)[0])
	return nil
}

// handleUpdateLabelField changes a label field: its name, restriction, type and choices. The type stays while the field
// holds values, so changing it is conflict with reason type_locked. Leaving the choice type drops the field's choices,
// which hold no values by then.
func (s *Server) handleUpdateLabelField(w http.ResponseWriter, r *http.Request) error {
	var in labelFieldPatch
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Type != nil && !labelTypeOK(*in.Type) {
		return apierr.New(apierr.InvalidRequest, "the type must be text, number, date, user or choice")
	}
	acc, err := s.labelAccount(r)
	if err != nil {
		return err
	}
	fieldID := r.PathValue("lbf")
	if !id.Valid("lbf", fieldID) {
		return labelFieldNotFound()
	}
	ctx := r.Context()
	p := principalOf(r)
	var changed []string
	var out *store.LabelField
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.LabelField(ctx, acc.ID, fieldID)
		if errors.Is(err, store.ErrNotFound) {
			return labelFieldNotFound()
		}
		if err != nil {
			return err
		}
		f := *cur
		f.Choices = nil
		f.UpdatedBy, f.UpdatedAt = p.ID, store.Now()
		if in.Name != nil {
			name, key, err := names.Clean(*in.Name)
			if err != nil {
				return err
			}
			if other, err := tx.LabelFieldByKey(ctx, acc.ID, key); err == nil && other.ID != cur.ID {
				return apierr.New(apierr.Conflict, "a label field has that name").With("reason", "name_taken")
			} else if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if f.Name != name {
				changed = append(changed, "name")
			}
			f.Name, f.Key = name, key
		}
		var opened bool
		values := 0
		if in.Restricted != nil && *in.Restricted != f.Restricted {
			changed = append(changed, "restricted")
			f.Restricted = *in.Restricted
			if !f.Restricted {
				// Every reader sees the values at once, so the audit says how many there are.
				opened = true
				if values, err = tx.CountFieldValues(ctx, cur.ID); err != nil {
					return err
				}
			}
		}
		if in.Type != nil && *in.Type != cur.Type {
			values, err := tx.CountFieldValues(ctx, cur.ID)
			if err != nil {
				return err
			}
			if values > 0 {
				return apierr.New(apierr.Conflict, "the field holds values, so its type cannot change").With("reason", "type_locked")
			}
			changed = append(changed, "type")
			f.Type = *in.Type
			if f.Type != store.LabelTypeChoice {
				if err := tx.DeleteFieldChoices(ctx, cur.ID); err != nil {
					return err
				}
				cur.Choices = nil
			}
		}
		if f.Type != store.LabelTypeChoice && len(in.Choices) > 0 {
			return apierr.New(apierr.InvalidRequest, "only a choice field has choices")
		}
		choices, err := labelChoicesFor(cur.Choices, in.Choices)
		if err != nil {
			return err
		}
		if len(choices) > 0 {
			changed = append(changed, "choices")
		}
		if err := tx.UpdateLabelField(ctx, &f); err != nil {
			return err
		}
		// The renames are written before the new choices: a rename frees its old name in the table, and a new choice
		// may take that name.
		for _, c := range choices {
			if c.ID == "" || !hasChoice(cur.Choices, c.ID) {
				continue
			}
			c.FieldID = cur.ID
			if err := tx.UpdateLabelChoice(ctx, c); err != nil {
				return err
			}
		}
		for _, c := range choices {
			if c.ID == "" || !hasChoice(cur.Choices, c.ID) {
				c.FieldID = cur.ID
				if err := tx.InsertLabelChoice(ctx, c); err != nil {
					return err
				}
			}
		}
		if len(changed) > 0 {
			details := map[string]any{"changed": changed}
			if opened {
				details["values"] = values
			}
			if err := tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "label_field.update", cur.ID, details)); err != nil {
				return err
			}
		}
		out, err = tx.LabelField(ctx, acc.ID, cur.ID)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, labelFieldsJSON([]*store.LabelField{out}, true)[0])
	return nil
}

// hasChoice reports whether a choice with the ID is among choices.
func hasChoice(choices []*store.LabelChoice, id string) bool {
	for _, c := range choices {
		if c.ID == id {
			return true
		}
	}
	return false
}

// labelFieldNotFound is the answer for a label field the caller may not see, or that is not in the account.
func labelFieldNotFound() error { return notFound("label field") }

// handleDeleteLabelField removes a label field with its values. The web interface states the number of values first.
func (s *Server) handleDeleteLabelField(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.labelAccount(r)
	if err != nil {
		return err
	}
	fieldID := r.PathValue("lbf")
	if !id.Valid("lbf", fieldID) {
		return labelFieldNotFound()
	}
	ctx := r.Context()
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := tx.LabelField(ctx, acc.ID, fieldID); errors.Is(err, store.ErrNotFound) {
			return labelFieldNotFound()
		} else if err != nil {
			return err
		}
		values, err := tx.CountFieldValues(ctx, fieldID)
		if err != nil {
			return err
		}
		if err := tx.DeleteLabelField(ctx, acc.ID, fieldID); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "label_field.delete", fieldID,
			map[string]any{"values": values}))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleListNodeLabels lists the values of a node, one entry for each field the caller may see. A restricted field is
// left out for everyone but a manager of the node, and so is never sent to anyone else. Every reader of the node sees
// the others.
func (s *Server) handleListNodeLabels(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	ctx := r.Context()
	n, role, err := nodeAccessWith(ctx, s.store, p, nodeID)
	if err != nil {
		return err
	}
	fields, err := s.store.LabelFields(ctx, n.AccountID, false)
	if err != nil {
		return err
	}
	vals, err := s.store.LabelValues(ctx, n.AccountID, nodeID)
	if err != nil {
		return err
	}
	var userIDs []string
	for _, v := range vals {
		if v.UserID != nil {
			userIDs = append(userIDs, *v.UserID)
		}
	}
	var userNames map[string]string
	if peopleNamesSeen(p, n.AccountID) {
		if userNames, err = s.store.UserNames(ctx, n.AccountID, userIDs); err != nil {
			return err
		}
	}
	out := listResponse[nodeLabelJSON]{Items: []nodeLabelJSON{}}
	for _, f := range fields {
		if f.Restricted && role != store.RoleManager {
			continue
		}
		j := nodeLabelJSON{FieldID: f.ID, Name: f.Name, Type: f.Type, Restricted: f.Restricted}
		if v := vals[f.ID]; v != nil {
			j.Value = labelValueJSON(f, v, userNames)
		}
		out.Items = append(out.Items, j)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// labelValueJSON is a stored value as the API sends it: a string, a number, a user or a choice.
func labelValueJSON(f *store.LabelField, v *store.LabelValue, userNames map[string]string) any {
	switch {
	case v.Text != nil:
		return *v.Text
	case v.Num != nil:
		return *v.Num
	case v.Date != nil:
		return *v.Date
	case v.UserID != nil:
		return labelUserJSON{ID: *v.UserID, Name: userNames[*v.UserID]}
	case v.ChoiceID != nil:
		for _, c := range f.Choices {
			if c.ID == *v.ChoiceID {
				return labelChoiceValueJSON{ID: c.ID, Name: c.Name, Archived: c.Archived}
			}
		}
	}
	return nil
}

// labelSetter is the caller of a call that changes a node's label values. The caller needs editor on the node, and
// manager when the field is restricted. A token has editor at most, so it never sets a restricted value.
func labelSetter(ctx context.Context, rd accessReader, p *auth.Principal, nodeID string, f *store.LabelField) (*store.Node, error) {
	n, role, err := nodeAccessWith(ctx, rd, p, nodeID)
	if err != nil {
		return nil, err
	}
	if store.RoleRank(role) < store.RoleRank(store.RoleEditor) {
		return nil, roleTooLow(store.RoleEditor)
	}
	if f.Restricted && role != store.RoleManager {
		return nil, apierr.New(apierr.Forbidden, "only a manager of the item may set this field").With("reason", "restricted_label")
	}
	return n, nil
}

// handleSetNodeLabel sets a node's value for one field. The body is {"value": ...}, in the field's type: a string for
// text and date, a number, a user ID or a choice ID. The value is checked against the field inside the write, and the
// node's own row is not written.
func (s *Server) handleSetNodeLabel(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Value json.RawMessage `json:"value"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if len(in.Value) == 0 || string(in.Value) == "null" {
		return apierr.New(apierr.InvalidRequest, "value is required; DELETE clears a value")
	}
	p := principalOf(r)
	nodeID := r.PathValue("nod")
	fieldID := r.PathValue("lbf")
	if !id.Valid("nod", nodeID) || !id.Valid("lbf", fieldID) {
		return nodeNotFound()
	}
	ctx := r.Context()
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		f, err := tx.LabelField(ctx, p.AccountID, fieldID)
		if errors.Is(err, store.ErrNotFound) {
			return labelFieldNotFound()
		}
		if err != nil {
			return err
		}
		n, err := labelSetter(ctx, tx, p, nodeID, f)
		if err != nil {
			return err
		}
		v, err := labelValueFrom(ctx, tx, n.AccountID, f, in.Value)
		if err != nil {
			return err
		}
		v.SetBy, v.SetAt = p.ID, store.Now()
		if err := tx.SetLabelValue(ctx, n.AccountID, nodeID, v); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "node.label_set", nodeID,
			map[string]any{"field": f.ID, "type": f.Type}, n.FolderID))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleClearNodeLabel removes a node's value for one field. Clearing a field with no value changes nothing and is not
// audited.
func (s *Server) handleClearNodeLabel(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	nodeID := r.PathValue("nod")
	fieldID := r.PathValue("lbf")
	if !id.Valid("nod", nodeID) || !id.Valid("lbf", fieldID) {
		return nodeNotFound()
	}
	ctx := r.Context()
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		f, err := tx.LabelField(ctx, p.AccountID, fieldID)
		if errors.Is(err, store.ErrNotFound) {
			return labelFieldNotFound()
		}
		if err != nil {
			return err
		}
		n, err := labelSetter(ctx, tx, p, nodeID, f)
		if err != nil {
			return err
		}
		had, err := tx.ClearLabelValue(ctx, n.AccountID, nodeID, fieldID)
		if err != nil || !had {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "node.label_clear", nodeID,
			map[string]any{"field": f.ID}, n.FolderID))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// labelValueFrom checks a raw JSON value against the field's type and returns it as a store value. A user must be a
// member of the account; a choice must be one of the field's and not archived.
func labelValueFrom(ctx context.Context, tx *store.Tx, accountID string, f *store.LabelField, raw json.RawMessage) (*store.LabelValue, error) {
	v := &store.LabelValue{FieldID: f.ID}
	switch f.Type {
	case store.LabelTypeText:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, apierr.New(apierr.InvalidRequest, "a text value must be a string")
		}
		clean, err := cleanLabelText(s)
		if err != nil {
			return nil, err
		}
		v.Text = &clean
	case store.LabelTypeNumber:
		var x float64
		if err := json.Unmarshal(raw, &x); err != nil {
			return nil, apierr.New(apierr.InvalidRequest, "a number value must be a finite number")
		}
		v.Num = &x
	case store.LabelTypeDate:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, apierr.New(apierr.InvalidRequest, "a date value must be a string, YYYY-MM-DD")
		}
		t, err := time.Parse("2006-01-02", s)
		if err != nil || t.Format("2006-01-02") != s {
			return nil, apierr.New(apierr.InvalidRequest, "a date value must be a date, YYYY-MM-DD")
		}
		v.Date = &s
	case store.LabelTypeUser:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil || !id.Valid("usr", s) {
			return nil, apierr.New(apierr.InvalidRequest, "a user value must be a member of the account")
		}
		if _, err := tx.User(ctx, accountID, s); errors.Is(err, store.ErrNotFound) {
			return nil, apierr.New(apierr.InvalidRequest, "a user value must be a member of the account")
		} else if err != nil {
			return nil, err
		}
		v.UserID = &s
	case store.LabelTypeChoice:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, apierr.New(apierr.InvalidRequest, "a choice value must be one of the field's choices")
		}
		for _, c := range f.Choices {
			if c.ID != s {
				continue
			}
			if c.Archived {
				return nil, apierr.New(apierr.InvalidRequest, "that choice is archived and takes no new values")
			}
			v.ChoiceID = &s
			return v, nil
		}
		return nil, apierr.New(apierr.InvalidRequest, "a choice value must be one of the field's choices")
	}
	return v, nil
}

// cleanLabelText checks a text value: valid UTF-8, at most maxLabelTextChars characters, not blank, and no control or
// bidirectional embedding character. Unlike a description, a value is refused rather than trimmed, so the text the
// reader sees is the text the editor typed. It is normalised to NFC.
func cleanLabelText(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", apierr.New(apierr.InvalidRequest, "the text must be valid UTF-8")
	}
	s = norm.NFC.String(s)
	if strings.TrimSpace(s) == "" {
		return "", apierr.New(apierr.InvalidRequest, "the text is required; DELETE clears a value")
	}
	if utf8.RuneCountInString(s) > maxLabelTextChars {
		return "", apierr.Newf(apierr.InvalidRequest, "a text value may be at most %d characters", maxLabelTextChars)
	}
	if err := checkTextChars(s, false); err != nil {
		return "", err
	}
	return s, nil
}

// arabicLetterMark is U+061C. It is a bidi control that isBidiControl does not list, so the text rule adds it.
const arabicLetterMark = 0x061C

// checkTextChars refuses a text that holds a control, a bidi control, or U+061C. It is the rule for every text a person
// types and another person reads (decision 29, decision 39). A bidi control would change how the text reads, so it is
// refused rather than removed. A line feed is a control character, and a label value is one line, so it is refused there.
// A comment body is discussion, so multiline lets a line feed through; the caller has already made every break an LF.
func checkTextChars(s string, multiline bool) error {
	for _, r := range s {
		if multiline && r == '\n' {
			continue
		}
		if unicode.IsControl(r) || isBidiControl(r) || r == arabicLetterMark {
			return apierr.New(apierr.InvalidRequest, "the text may not hold a control or bidi control character")
		}
	}
	return nil
}
