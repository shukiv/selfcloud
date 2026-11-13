// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

var (
	testLabelField    = id.Derive("lbf", "cli11f-field")
	testLabelField2   = id.Derive("lbf", "cli11f-field-two")
	testLabelField3   = id.Derive("lbf", "cli11f-field-three")
	testLabelField4   = id.Derive("lbf", "cli11f-field-four")
	testLabelField5   = id.Derive("lbf", "cli11f-field-five")
	testLabelField6   = id.Derive("lbf", "cli11f-field-six")
	testLabelField7   = id.Derive("lbf", "cli11f-field-seven")
	testLabelUser     = id.Derive("usr", "cli11f-user")
	testLabelChoice   = id.Derive("lbc", "cli11f-choice")
	testLabelOldOne   = id.Derive("lbc", "cli11f-old-choice")
	testLabelUnknown  = id.Derive("lbf", "cli11f-unknown")
	testLabelRestrict = id.Derive("lbf", "cli11f-restricted")
)

// labelItem returns one item of GET /api/v1/nodes/{nod}/labels as the API answers it.
func labelItem(fieldID, name, typ string, restricted bool, value any) map[string]any {
	return map[string]any{
		"field_id": fieldID, "name": name, "type": typ, "restricted": restricted, "value": value,
	}
}

// labelAPIErrWith is apiErr with the error's details.reason, as a restricted value's refusal has it.
func labelAPIErrWith(status int, code, message, reason string) reply {
	return reply{status: status, body: jsonOf(map[string]any{"error": map[string]any{
		"code": code, "message": message, "details": map[string]string{"reason": reason}}})}
}

func TestLabelShowPrintsEachValue(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", nodePath(testNode, "/labels"), page("",
		labelItem(testLabelField, "Priority", "text", false, "high"),
		labelItem(testLabelField2, "Score", "number", false, 2.5),
		labelItem(testLabelField3, "Due", "date", false, "2026-11-03"),
		labelItem(testLabelField4, "Owner", "user", false, map[string]any{"id": testLabelUser, "name": "Ada"}),
		labelItem(testLabelField5, "Stage", "choice", false, map[string]any{"id": testLabelChoice, "name": "High", "archived": false}),
		labelItem(testLabelField6, "Old stage", "choice", false, map[string]any{"id": testLabelOldOne, "name": "Old", "archived": true}),
		labelItem(testLabelField7, "Note", "text", false, nil),
	))
	res := f.run(LabelShow, testNode)
	wantCode(t, res, 0)
	f.wantCalls("GET " + nodePath(testNode, "/labels"))
	want := table(
		[]string{"FIELD", "NAME", "TYPE", "VALUE"},
		[]string{testLabelField, "Priority", "text", "high"},
		[]string{testLabelField2, "Score", "number", "2.5"},
		[]string{testLabelField3, "Due", "date", "2026-11-03"},
		[]string{testLabelField4, "Owner", "user", "Ada (" + testLabelUser + ")"},
		[]string{testLabelField5, "Stage", "choice", "High (" + testLabelChoice + ")"},
		[]string{testLabelField6, "Old stage", "choice", "Old (" + testLabelOldOne + ") [archived]"},
		[]string{testLabelField7, "Note", "text", ""},
	)
	if res.stdout != want {
		t.Fatalf("stdout:\n%q\nwant:\n%q", res.stdout, want)
	}
}

func TestLabelShowJSONPrintsOnlyTheContractFields(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", nodePath(testNode, "/labels"), page("",
		map[string]any{
			"field_id": testLabelField, "name": "Priority", "type": "text", "restricted": false, "value": "high",
			"secret": "sck_NOT_A_FIELD",
		},
		labelItem(testLabelField2, "Owner", "user", true, map[string]any{
			"id": testLabelUser, "name": "Ada", "token": "sck_secret_value",
		}),
	))
	res := f.run(LabelShow, testNode, "--json")
	wantCode(t, res, 0)
	for _, secret := range []string{"sck_NOT_A_FIELD", "sck_secret_value", "secret", "token"} {
		if strings.Contains(res.stdout, secret) {
			t.Fatalf("stdout %q has %q, which the contract does not name", res.stdout, secret)
		}
	}
	var views []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.stdout), &views); err != nil {
		t.Fatalf("stdout %q is not a JSON list: %v", res.stdout, err)
	}
	if len(views) != 2 {
		t.Fatalf("got %d views, want 2", len(views))
	}
	for _, v := range views {
		if len(v) != 5 {
			t.Fatalf("view %v has %d keys, want 5", v, len(v))
		}
		for _, key := range []string{"field_id", "name", "type", "restricted", "value"} {
			if _, ok := v[key]; !ok {
				t.Fatalf("view %v lacks %q", v, key)
			}
		}
	}
	var text string
	if err := json.Unmarshal(views[0]["value"], &text); err != nil || text != "high" {
		t.Fatalf("value %s: %v", views[0]["value"], err)
	}
	var person struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(views[1]["value"], &person); err != nil {
		t.Fatalf("value %s: %v", views[1]["value"], err)
	}
	if person.ID != testLabelUser || person.Name != "Ada" {
		t.Fatalf("value %+v", person)
	}
}

func TestLabelShowRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, args := range [][]string{
		{},
		{testNode, testOther},
		{"not-a-node"},
	} {
		wantCode(t, f.run(LabelShow, args...), 2)
	}
	f.wantNoCalls()
}

func TestLabelSetSendsTheTypedValue(t *testing.T) {
	cases := []struct {
		name  string
		typ   string
		given string
		body  any
		shown string
	}{
		{"text", "text", "high", "high", "high"},
		{"number", "number", "2.50", 2.5, "2.50"},
		{"date", "date", "2026-11-03", "2026-11-03", "2026-11-03"},
		{"user", "user", testLabelUser, testLabelUser, testLabelUser},
		{"choice", "choice", testLabelChoice, testLabelChoice, testLabelChoice},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			f.on("GET", nodePath(testNode, "/labels"), page("",
				labelItem(testLabelField, "Priority", tc.typ, false, nil)))
			f.on("PUT", nodePath(testNode, "/labels/"+testLabelField), done(http.StatusNoContent))
			res := f.run(LabelSet, testNode, testLabelField, tc.given)
			wantCode(t, res, 0)
			f.wantCalls("GET "+nodePath(testNode, "/labels"), "PUT "+nodePath(testNode, "/labels/"+testLabelField))
			if got := f.wantBody(1)["value"]; got != tc.body {
				t.Fatalf("body value %v, want %v", got, tc.body)
			}
			for _, want := range []string{"field=" + testLabelField, "value=" + tc.shown} {
				if !strings.Contains(res.stdout, want) {
					t.Errorf("stdout %q lacks %q", res.stdout, want)
				}
			}
		})
	}
}

func TestLabelSetUnknownField(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", nodePath(testNode, "/labels"), page("",
		labelItem(testLabelField2, "Other", "text", false, nil)))
	res := f.run(LabelSet, testNode, testLabelUnknown, "high")
	wantCode(t, res, 4)
	f.wantCalls("GET " + nodePath(testNode, "/labels"))
	if !strings.Contains(res.stderr, "no such label field on this item") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestLabelSetRefusals(t *testing.T) {
	long := strings.Repeat("x", 501)
	for _, tc := range []struct {
		name  string
		typ   string
		args  []string
		calls []string
	}{
		{"no arguments", "", nil, nil},
		{"two arguments", "", []string{testNode, testLabelField}, nil},
		{"bad node", "", []string{"not-a-node", testLabelField, "high"}, nil},
		{"bad field", "", []string{testNode, "not-a-field", "high"}, nil},
		{"empty value", "text", []string{testNode, testLabelField, ""}, []string{"GET " + nodePath(testNode, "/labels")}},
		{"value too long", "text", []string{testNode, testLabelField, long}, []string{"GET " + nodePath(testNode, "/labels")}},
		{"bad number", "number", []string{testNode, testLabelField, "not a number"}, []string{"GET " + nodePath(testNode, "/labels")}},
		{"bad date", "date", []string{testNode, testLabelField, "2026-02-30"}, []string{"GET " + nodePath(testNode, "/labels")}},
		{"bad user", "user", []string{testNode, testLabelField, "not-a-user"}, []string{"GET " + nodePath(testNode, "/labels")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			f.on("GET", nodePath(testNode, "/labels"), page("",
				labelItem(testLabelField, "Priority", tc.typ, false, nil)))
			res := f.run(LabelSet, tc.args...)
			wantCode(t, res, 2)
			if got := f.requests(); strings.Join(got, "\n") != strings.Join(tc.calls, "\n") {
				t.Fatalf("calls %v, want %v", got, tc.calls)
			}
		})
	}
}

func TestLabelSetRestrictedHint(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		name := "plain"
		if asJSON {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			f.on("GET", nodePath(testNode, "/labels"), page("",
				labelItem(testLabelRestrict, "Secret", "text", true, nil)))
			f.on("PUT", nodePath(testNode, "/labels/"+testLabelRestrict),
				labelAPIErrWith(http.StatusForbidden, "forbidden", "this field is restricted", "restricted_label"))
			args := []string{testNode, testLabelRestrict, "high"}
			if asJSON {
				args = append(args, "--json")
			}
			res := f.run(LabelSet, args...)
			wantCode(t, res, 3)
			if strings.Contains(res.stderr, "this field is restricted") == false {
				t.Fatalf("stderr %q lacks the server's message", res.stderr)
			}
			hasHint := strings.Contains(res.stderr, "only a manager of the item may set this field")
			if asJSON == hasHint {
				t.Fatalf("hint = %v under --json = %v; stderr %q", hasHint, asJSON, res.stderr)
			}
		})
	}
}

func TestLabelSetNumberRefusesNonFiniteValues(t *testing.T) {
	for _, given := range []string{"NaN", "Inf", "-Inf"} {
		isolate(t, testAccountKey)
		f := newFake(t)
		f.on("GET", nodePath(testNode, "/labels"), page("",
			labelItem(testLabelField, "Score", "number", false, nil)))
		res := f.run(LabelSet, testNode, testLabelField, "--", given)
		wantCode(t, res, 2)
		f.wantCalls("GET " + nodePath(testNode, "/labels"))
	}
}

func TestLabelClearAsksByField(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	res := f.run(LabelClear, testNode, testLabelField)
	wantCode(t, res, 2)
	f.wantNoCalls()

	typeAtTerminal(testLabelField + "\n")
	f.on("DELETE", nodePath(testNode, "/labels/"+testLabelField), done(http.StatusNoContent))
	res = f.run(LabelClear, testNode, testLabelField)
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + nodePath(testNode, "/labels/"+testLabelField))
	if !strings.Contains(res.stdout, "cleared "+testLabelField+"\n") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestLabelClearSkipsTheQuestionWithYes(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", nodePath(testNode, "/labels/"+testLabelField), done(http.StatusNoContent))
	res := f.run(LabelClear, testNode, testLabelField, "--yes")
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + nodePath(testNode, "/labels/"+testLabelField))
	if strings.Contains(res.stderr, "Type ") {
		t.Fatalf("stderr %q asks a question", res.stderr)
	}
}

func TestLabelClearRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, args := range [][]string{
		{},
		{testNode},
		{"not-a-node", testLabelField},
		{testNode, "not-a-field"},
		{testNode, testLabelField, "extra"},
	} {
		wantCode(t, f.run(LabelClear, args...), 2)
	}
	f.wantNoCalls()
}
