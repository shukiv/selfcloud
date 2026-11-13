// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

var (
	testListField  = id.Derive("lbf", "cli11f-field")
	testListField2 = id.Derive("lbf", "cli11f-field-two")
	testListChoice = id.Derive("lbc", "cli11f-choice")
	testListOld    = id.Derive("lbc", "cli11f-old")
)

// labelFieldListItem is one item of GET /api/v1/label-fields as the API answers it. values is set only for an account
// key or the owner's own session.
func labelFieldListItem(fieldID, name, typ string, restricted bool, values *int, choices ...map[string]any) map[string]any {
	m := map[string]any{
		"id": fieldID, "name": name, "type": typ, "restricted": restricted, "choices": choices,
	}
	if values != nil {
		m["values"] = *values
	}
	return m
}

func TestLabelFieldListPrintsTheFields(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", "/api/v1/label-fields", page("",
		labelFieldListItem(testListField, "Priority", "text", false, intPtr(12)),
		labelFieldListItem(testListField2, "Stage", "choice", true, nil,
			labelChoice(testListChoice, "High", false),
			labelChoice(testListOld, "Old", true),
			labelChoice(id.Derive("lbc", "cli11f-later"), "Later", false)),
	))
	res := f.run(LabelFieldList)
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/label-fields")
	want := table(
		[]string{"ID", "NAME", "TYPE", "RESTRICTED", "CHOICES", "VALUES"},
		[]string{testListField, "Priority", "text", "", "0", "12"},
		[]string{testListField2, "Stage", "choice", "yes", "2", ""},
	)
	if res.stdout != want {
		t.Fatalf("stdout:\n%q\nwant:\n%q", res.stdout, want)
	}
}

func TestLabelFieldListJSONOmitsAbsentValues(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	item := labelFieldListItem(testListField2, "Stage", "choice", true, nil,
		labelChoice(testListChoice, "High", false))
	item["owner_id"] = "usr_NOT_A_FIELD"
	f.on("GET", "/api/v1/label-fields", page("", item))
	res := f.run(LabelFieldList, "--json")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "usr_NOT_A_FIELD") {
		t.Fatalf("stdout %q has a field the contract does not name", res.stdout)
	}
	var views []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.stdout), &views); err != nil {
		t.Fatalf("stdout %q is not a JSON list: %v", res.stdout, err)
	}
	if len(views) != 1 {
		t.Fatalf("got %d views, want 1", len(views))
	}
	v := views[0]
	if _, ok := v["values"]; ok {
		t.Fatalf("view %v has values, which the server left out", v)
	}
	if len(v) != 5 {
		t.Fatalf("view %v has %d keys, want 5", v, len(v))
	}
	for _, key := range []string{"id", "name", "type", "restricted", "choices"} {
		if _, ok := v[key]; !ok {
			t.Fatalf("view %v lacks %q", v, key)
		}
	}
	if got := string(v["choices"]); !strings.Contains(got, testListChoice) || strings.Contains(got, testListOld) {
		t.Fatalf("choices %s", got)
	}
}

func TestLabelFieldListAdminHint(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		name := "plain"
		if asJSON {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			isolate(t, testAdminKey)
			f := newFake(t)
			f.on("GET", "/api/v1/label-fields",
				apiErr(http.StatusForbidden, "forbidden", "this needs an account"))
			args := []string{}
			if asJSON {
				args = append(args, "--json")
			}
			res := f.run(LabelFieldList, args...)
			wantCode(t, res, 3)
			if !strings.Contains(res.stderr, "this needs an account") {
				t.Fatalf("stderr %q lacks the server's message", res.stderr)
			}
			hasHint := strings.Contains(res.stderr, labelFieldListAdminHint)
			if asJSON == hasHint {
				t.Fatalf("hint = %v under --json = %v; stderr %q", hasHint, asJSON, res.stderr)
			}
		})
	}
}

func TestLabelFieldListDeviceRefusalHasNoHint(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", "/api/v1/label-fields",
		apiErr(http.StatusForbidden, "forbidden", "this needs an account"))
	res := f.run(LabelFieldList)
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "this needs an account") {
		t.Fatalf("stderr %q lacks the server's message", res.stderr)
	}
	if strings.Contains(res.stderr, "selfcloud: hint:") {
		t.Fatalf("stderr %q has a hint a device token must not get", res.stderr)
	}
}

func TestLabelFieldListRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, args := range [][]string{{"extra"}, {"--account", testAcc}} {
		wantCode(t, f.run(LabelFieldList, args...), 2)
	}
	f.wantNoCalls()
}

func intPtr(n int) *int { return &n }
