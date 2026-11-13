// SPDX-License-Identifier: AGPL-3.0-or-later

// The node label commands (slice cli11f): label show, label set and label clear. A label field is described by the
// account's label field commands; these commands read and write one field's value on one node. A node is named by its
// nod_ id and a field by its lbf_ id. The node's label routes need a credential that manages the node, so an account key
// or a signed-in person's token works. A value the command cannot send is refused before it is changed.

package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/id"
)

// labelTextMaxChars is the API's limit for a text value (internal/api's maxLabelTextChars).
const labelTextMaxChars = 500

// labelRestrictedHint is told to a caller whose value a restricted field refused.
const labelRestrictedHint = "only a manager of the item may set this field"

// labelUserValue is a user label's value: the person, by id and name. A person without a name is its id.
type labelUserValue struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// labelChoiceValue is a choice label's value: the choice, by id and name, and whether it is archived.
type labelChoiceValue struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}

// labelNodeItem is one item of GET /api/v1/nodes/{nod}/labels as the API writes it. The value is decoded by its type.
type labelNodeItem struct {
	FieldID    string          `json:"field_id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Restricted bool            `json:"restricted"`
	Value      json.RawMessage `json:"value"`
}

// labelNodeView is one label as the commands print it, and the allow list --json prints: a field the API adds is not
// shown until it is added here, and a value is decoded into the type's own view, so a field inside it is not shown.
type labelNodeView struct {
	FieldID    string `json:"field_id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Restricted bool   `json:"restricted"`
	Value      any    `json:"value"`
}

// labelDecodeValue reads one value by the field's type.
func labelDecodeValue(typ string, raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	switch typ {
	case "number":
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, errors.New("the server's label value is not a number")
		}
		return f, nil
	case "user":
		var v labelUserValue
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, errors.New("the server's label value is not a person")
		}
		return v, nil
	case "choice":
		var v labelChoiceValue
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, errors.New("the server's label value is not a choice")
		}
		return v, nil
	default:
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, errors.New("the server's label value is not text")
		}
		return v, nil
	}
}

// labelNodeViews reads the list the node label route answered with, each item through the type's own view.
func labelNodeViews(items []json.RawMessage) ([]labelNodeView, error) {
	views := make([]labelNodeView, 0, len(items))
	for _, raw := range items {
		var item labelNodeItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, errors.New("the server's answer is not a label")
		}
		value, err := labelDecodeValue(item.Type, item.Value)
		if err != nil {
			return nil, err
		}
		views = append(views, labelNodeView{
			FieldID: item.FieldID, Name: item.Name, Type: item.Type, Restricted: item.Restricted, Value: value,
		})
	}
	return views, nil
}

// labelFieldArg checks a label field id.
func labelFieldArg(arg string) (string, error) {
	if !id.Valid("lbf", arg) {
		return "", exitcode.Usagef("a label field is named by its lbf_ id")
	}
	return arg, nil
}

// labelNameID writes a value that names a person or a choice: its name, then its id. A value without a name is its id.
func labelNameID(name, valueID string) string {
	if name == "" {
		return valueID
	}
	if valueID == "" {
		return name
	}
	return name + " (" + valueID + ")"
}

// labelValueText writes one decoded value as the plain output shows it.
func labelValueText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case labelUserValue:
		return labelNameID(t.Name, t.ID)
	case labelChoiceValue:
		text := labelNameID(t.Name, t.ID)
		if t.Archived {
			text += " [archived]"
		}
		return text
	}
	return ""
}

// LabelShow is `selfcloud label show NODE`: the node's label values, as the caller may see them.
func LabelShow(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("label show")
	var g conn.Global
	g.Register(set)
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return fail(stderr, g, exitcode.Usagef("label show takes one node: its nod_ id"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	cn, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	resp, err := send(cn, request{method: http.MethodGet, path: nodeRoute(nodeID, "/labels")})
	if err != nil {
		return fail(stderr, g, err)
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(resp.Body, &list); err != nil {
		return fail(stderr, g, errors.New("the server's answer is not a list of labels"))
	}
	views, err := labelNodeViews(list.Items)
	if err != nil {
		return fail(stderr, g, err)
	}
	if g.JSON {
		if err := output.JSON(stdout, views); err != nil {
			return fail(stderr, g, err)
		}
		return exitcode.OK
	}
	rows := make([][]string, 0, len(views))
	for _, v := range views {
		rows = append(rows, []string{v.FieldID, v.Name, v.Type, labelValueText(v.Value)})
	}
	if err := output.Table(stdout, []string{"FIELD", "NAME", "TYPE", "VALUE"}, rows); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// labelValueFor checks a value against the field's type and returns the body's JSON value. A value it cannot send is a
// usage error, so nothing is changed. A choice is sent as it was given: the server decides whether it is one of the
// field's choices.
func labelValueFor(typ, value string) (any, error) {
	switch typ {
	case "number":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, exitcode.Usagef("a number field takes a number")
		}
		return f, nil
	case "date":
		t, err := time.Parse("2006-01-02", value)
		if err != nil || t.Format("2006-01-02") != value {
			return nil, exitcode.Usagef("a date field takes a date as YYYY-MM-DD")
		}
		return value, nil
	case "user":
		if !id.Valid("usr", value) {
			return nil, exitcode.Usagef("a user field takes a usr_ id")
		}
		return value, nil
	default:
		return value, nil
	}
}

// labelRestrictedRefusal reports whether err is the API's refusal of a restricted field's value.
func labelRestrictedRefusal(err error) bool {
	if !isForbidden(err) {
		return false
	}
	var ae *client.APIError
	return errors.As(err, &ae) && ae.Reason == "restricted_label"
}

// LabelSet is `selfcloud label set NODE FIELD VALUE`: it sets one label field's value on the node. The field's type is
// read first, so the value is sent in the shape the API expects; a field the node does not show the caller is refused
// before the change.
func LabelSet(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("label set")
	var g conn.Global
	g.Register(set)
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 3 {
		return fail(stderr, g, exitcode.Usagef("label set takes a node, a field and a value"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	fieldID, err := labelFieldArg(pos[1])
	if err != nil {
		return fail(stderr, g, err)
	}
	value := pos[2]
	cn, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	resp, err := send(cn, request{method: http.MethodGet, path: nodeRoute(nodeID, "/labels")})
	if err != nil {
		return fail(stderr, g, err)
	}
	var list struct {
		Items []labelNodeItem `json:"items"`
	}
	if err := json.Unmarshal(resp.Body, &list); err != nil {
		return fail(stderr, g, errors.New("the server's answer is not a list of labels"))
	}
	typ := ""
	for _, v := range list.Items {
		if v.FieldID == fieldID {
			typ = v.Type
			break
		}
	}
	if typ == "" {
		return fail(stderr, g, exitcode.NotFoundf("no such label field on this item"))
	}
	if typ == "text" {
		if value == "" {
			return fail(stderr, g, exitcode.Usagef("a text label cannot be empty; give a value, or clear the field"))
		}
		if utf8.RuneCountInString(value) > labelTextMaxChars {
			return fail(stderr, g, exitcode.Usagef("a text label is at most %d characters", labelTextMaxChars))
		}
	}
	bodyValue, err := labelValueFor(typ, value)
	if err != nil {
		return fail(stderr, g, err)
	}
	req, err := jsonRequest(http.MethodPut, nodeRoute(nodeID, "/labels/"+fieldID), map[string]any{"value": bodyValue})
	if err != nil {
		return fail(stderr, g, err)
	}
	if _, err := send(cn, req); err != nil {
		code := fail(stderr, g, err)
		if !g.JSON && labelRestrictedRefusal(err) {
			_, _ = io.WriteString(stderr, "selfcloud: hint: "+labelRestrictedHint+"\n")
		}
		return code
	}
	if err := output.KeyValues(stdout, []output.KV{
		{Key: "field", Value: fieldID},
		{Key: "value", Value: value},
	}); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// LabelClear is `selfcloud label clear NODE FIELD [--yes]`: it removes one field's value from the node. It asks first,
// by the field's id, and without a terminal and --yes it refuses before anything is sent.
func LabelClear(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("label clear")
	var g conn.Global
	g.Register(set)
	var yes bool
	set.BoolVar(&yes, "yes", false, "clear the value without asking")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 2 {
		return fail(stderr, g, exitcode.Usagef("label clear takes a node and a field"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	fieldID, err := labelFieldArg(pos[1])
	if err != nil {
		return fail(stderr, g, err)
	}
	if err := confirm(yes, fieldID, "clear this label field's value on the item", stderr); err != nil {
		return fail(stderr, g, err)
	}
	cn, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	if _, err := send(cn, request{method: http.MethodDelete, path: nodeRoute(nodeID, "/labels/"+fieldID)}); err != nil {
		return fail(stderr, g, err)
	}
	if _, err := fmt.Fprintf(stdout, "cleared %s\n", fieldID); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}
