// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"strconv"
)

// ErrBadFilter is a type or modified range the server does not accept (decision 110): an unknown type, or a range whose
// start is not before its end. The API answers it as invalid_request.
var ErrBadFilter = errors.New("store: bad filter")

// ListTypeFolder and ListTypeShortcut are the two type values that name a kind rather than a file group.
const (
	ListTypeFolder   = "folder"
	ListTypeShortcut = "shortcut"
)

// ListFilter is a file list's filter (decision 110): a type, one value only, and a modified range. A type is either a
// kind (a folder or a shortcut) or a file group. ModAfter is inclusive and ModBefore exclusive, in milliseconds; a nil
// bound is no bound. The zero value filters nothing.
type ListFilter struct {
	Kind      string // KindDir or KindShortcut; "" for none
	Group     string // a file group ID (fileGroups); "" for none
	ModAfter  *int64
	ModBefore *int64
}

// ListTypeFilter reads the type value of a list: "" is no type, "folder" and "shortcut" name a kind, and any group ID names
// a file group. Any other value is ErrBadFilter. The value is checked against the table, so only a fixed text reaches SQL.
func ListTypeFilter(t string) (ListFilter, error) {
	switch t {
	case "":
		return ListFilter{}, nil
	case ListTypeFolder:
		return ListFilter{Kind: KindDir}, nil
	case ListTypeShortcut:
		return ListFilter{Kind: KindShortcut}, nil
	}
	for _, g := range fileGroups {
		if g.id == t {
			return ListFilter{Group: g.id}, nil
		}
	}
	return ListFilter{}, fmt.Errorf("%w: type %q is not a type", ErrBadFilter, t)
}

// Validate refuses a modified range whose start is not before its end.
func (f ListFilter) Validate() error {
	if f.ModAfter != nil && f.ModBefore != nil && *f.ModAfter >= *f.ModBefore {
		return fmt.Errorf("%w: modified_after must be before modified_before", ErrBadFilter)
	}
	return nil
}

// Active says whether the filter narrows anything.
func (f ListFilter) Active() bool {
	return f.Kind != "" || f.Group != "" || f.ModAfter != nil || f.ModBefore != nil
}

// Tag is the canonical text of the filter, for the cursor's consistency tag. It is "" for the zero filter, so an
// unfiltered cursor is the same as before.
func (f ListFilter) Tag() string {
	if !f.Active() {
		return ""
	}
	return "k=" + f.Kind + ";g=" + f.Group + ";a=" + bound(f.ModAfter) + ";b=" + bound(f.ModBefore)
}

// encodeFilteredCursor is encodeCursor with the filter's tag as one more part, when the filter narrows anything. With no
// filter the cursor is the plain one, so cursors from before the filters still open.
func encodeFilteredCursor(f ListFilter, parts ...string) string {
	if t := f.Tag(); t != "" {
		parts = append(parts, t)
	}
	return encodeCursor(parts...)
}

// decodeFilteredCursor reads a cursor of n parts for filter f. With a filter, the cursor has one part more, the filter's
// tag, which must be the same; a plain cursor and a tagged one do not open under the other's filter.
func decodeFilteredCursor(c string, n int, f ListFilter) ([]string, error) {
	t := f.Tag()
	if t == "" {
		return decodeCursor(c, n)
	}
	parts, err := decodeCursor(c, n+1)
	if err != nil {
		return nil, err
	}
	if parts[n] != t {
		return nil, ErrBadCursor
	}
	return parts[:n], nil
}

func bound(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}

// admitsKind says whether a child of kind can match the filter, so a listing skips the kinds it excludes: a kind type
// admits only its own kind, a file group admits only files, and anything else admits every kind.
func (f ListFilter) admitsKind(kind string) bool {
	if f.Kind != "" {
		return kind == f.Kind
	}
	if f.Group != "" {
		return kind == KindFile
	}
	return true
}

// Where is the SQL that narrows the rows of alias (a nodes alias the caller joins) by the filter, with one bound
// argument per value. The alias is a fixed name from the caller, never user text. The group and the kind are compared
// as parameters, and the group is first checked against the table by ListTypeFilter.
func (f ListFilter) Where(alias string) (string, []any) {
	var sqlText string
	var args []any
	if f.Kind != "" {
		sqlText += " AND " + alias + ".kind = ?"
		args = append(args, f.Kind)
	}
	if f.Group != "" {
		sqlText += " AND " + alias + ".kind = 'file' AND " + alias + ".type_group = ?"
		args = append(args, f.Group)
	}
	if f.ModAfter != nil {
		sqlText += " AND " + alias + ".modified_at >= ?"
		args = append(args, *f.ModAfter)
	}
	if f.ModBefore != nil {
		sqlText += " AND " + alias + ".modified_at < ?"
		args = append(args, *f.ModBefore)
	}
	return sqlText, args
}
