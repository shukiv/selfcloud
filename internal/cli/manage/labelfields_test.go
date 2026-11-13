// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

var (
	testField  = id.Derive("lbf", "cli9b4-field")
	testChoice = id.Derive("lbc", "cli9b4-choice")
	testOther  = id.Derive("lbc", "cli9b4-other")
	testThird  = id.Derive("lbc", "cli9b4-third")
)

func accLabelFields() string        { return "/api/v1/accounts/" + testAcc + "/label-fields" }
func accLabelField(f string) string { return accLabelFields() + "/" + f }

// labelField returns a field document as the API answers it.
func labelField(fieldID, name, typ string, restricted bool, choices ...map[string]any) map[string]any {
	return map[string]any{
		"id": fieldID, "name": name, "type": typ, "restricted": restricted,
		"choices": choices, "values": 0,
	}
}

func labelChoice(choiceID, name string, archived bool) map[string]any {
	return map[string]any{"id": choiceID, "name": name, "archived": archived}
}

func TestLabelFieldAddSendsTheDocument(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accLabelFields(), doc(http.StatusCreated,
		labelField(testField, "Priority", "choice", false, labelChoice(testChoice, "High", false))))
	res := f.runAcc(LabelFieldAdd, "--type", "choice", "--choice", "High", "Priority")
	wantCode(t, res, 0)
	f.wantCalls("POST " + accLabelFields())
	f.wantBearer(testAccountKey)
	body := f.bodyOf(0)
	if body["name"] != "Priority" || body["type"] != "choice" || body["restricted"] != false {
		t.Fatalf("body %v", body)
	}
	choices, ok := body["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices %v", body["choices"])
	}
	if choice := choices[0].(map[string]any); choice["name"] != "High" || choice["id"] != nil {
		t.Fatalf("choice %v", choice)
	}
	for _, want := range []string{"id=" + testField, "name=Priority", "type=choice", "restricted=false", "choice=" + testChoice + " High"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
}

func TestLabelFieldAddWithoutChoicesSendsAnEmptyList(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accLabelFields(), doc(http.StatusCreated, labelField(testField, "Due", "date", true)))
	res := f.runAcc(LabelFieldAdd, "--type", "date", "--restricted", "Due")
	wantCode(t, res, 0)
	if got, ok := f.bodyOf(0)["choices"].([]any); !ok || len(got) != 0 {
		t.Fatalf("choices %v, want an empty list", f.bodyOf(0)["choices"])
	}
	if got := f.bodyOf(0)["restricted"]; got != true {
		t.Fatalf("restricted %v", got)
	}
}

func TestLabelFieldAddRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	// a bad name, a bad type, no type, no name, two names, no account, and a bad account all cost no request
	for _, args := range [][]string{
		{"--type", "text", "a/b"},
		{"--type", "money", "field"},
		{"field"},
		{"--type", "text"},
		{"--type", "text", "a", "b"},
		{"--type", "text", "--choice", "a/b", "field"},
	} {
		wantCode(t, f.runAcc(LabelFieldAdd, args...), 2)
	}
	wantCode(t, f.run(LabelFieldAdd, "--type", "text", "x"), 2)
	wantCode(t, f.run(LabelFieldAdd, "--type", "text", "--account", "acc_bad", "x"), 2)
	f.wantNoCalls()
}

func TestLabelFieldUpdateSendsOnlyWhatIsGiven(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accLabelField(testField), doc(http.StatusOK, labelField(testField, "Due date", "date", true)))
	res := f.runAcc(LabelFieldUpdate, testField, "--name", "Due date", "--restricted", "true")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + accLabelField(testField))
	body := f.bodyOf(0)
	if len(body) != 2 || body["name"] != "Due date" || body["restricted"] != true {
		t.Fatalf("body %v, want only the name and the restriction", body)
	}
	if !strings.Contains(res.stdout, "name=Due date") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestLabelFieldUpdateChoices(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accLabelField(testField), doc(http.StatusOK, labelField(testField, "Priority", "choice", false)))
	res := f.runAcc(LabelFieldUpdate, testField, "--add-choice", "Low", "--rename-choice", testChoice+"=Urgent")
	wantCode(t, res, 0)
	// A new or renamed choice is given its name, so no read is needed.
	f.wantCalls("PATCH " + accLabelField(testField))
	choices := f.bodyOf(0)["choices"].([]any)
	first := choices[0].(map[string]any)
	if first["id"] != nil || first["name"] != "Low" || first["archived"] != nil {
		t.Fatalf("add %v", first)
	}
	second := choices[1].(map[string]any)
	if second["id"] != testChoice || second["name"] != "Urgent" || second["archived"] != nil {
		t.Fatalf("rename %v", second)
	}
}

func TestLabelFieldUpdateChoicesInTheDocumentedOrder(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", "/api/v1/label-fields", page("",
		labelField(testField, "Priority", "choice", false,
			labelChoice(testChoice, "High", false), labelChoice(testOther, "Low", false),
			labelChoice(testThird, "Later", true))))
	f.on("PATCH", accLabelField(testField), doc(http.StatusOK, labelField(testField, "Priority", "choice", false)))
	res := f.runAcc(LabelFieldUpdate, testField,
		"--restore-choice", testThird, "--archive-choice", testOther,
		"--rename-choice", testChoice+"=Urgent", "--add-choice", "New")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/label-fields", "PATCH "+accLabelField(testField))
	choices := f.bodyOf(1)["choices"].([]any)
	want := []map[string]any{
		{"id": nil, "name": "New", "archived": nil},
		{"id": testChoice, "name": "Urgent", "archived": nil},
		{"id": testOther, "name": "Low", "archived": true},
		{"id": testThird, "name": "Later", "archived": false},
	}
	if len(choices) != len(want) {
		t.Fatalf("choices %v, want %v", choices, want)
	}
	for i, w := range want {
		got := choices[i].(map[string]any)
		for k, v := range w {
			if got[k] != v {
				t.Fatalf("choice %d %v, want %v", i, got, w)
			}
		}
	}
}

func TestLabelFieldUpdateArchivesAndRestoresByName(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", "/api/v1/label-fields", page("",
		labelField(testField, "Priority", "choice", false,
			labelChoice(testChoice, "High", false), labelChoice(testOther, "Low", true))))
	f.on("PATCH", accLabelField(testField), doc(http.StatusOK, labelField(testField, "Priority", "choice", false)))
	res := f.runAcc(LabelFieldUpdate, testField, "--archive-choice", testChoice, "--restore-choice", testOther)
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/label-fields", "PATCH "+accLabelField(testField))
	choices := f.bodyOf(1)["choices"].([]any)
	want := []map[string]any{
		{"id": testChoice, "name": "High", "archived": true},
		{"id": testOther, "name": "Low", "archived": false},
	}
	for i, w := range want {
		got := choices[i].(map[string]any)
		for k, v := range w {
			if got[k] != v {
				t.Fatalf("choice %d %v, want %v", i, got, w)
			}
		}
	}
}

func TestLabelFieldUpdateMissingFieldOrChoice(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", "/api/v1/label-fields", page("", labelField(id.Derive("lbf", "other"), "Other", "choice", false)))
	res := f.runAcc(LabelFieldUpdate, testField, "--archive-choice", testChoice)
	wantCode(t, res, 4)
	f.wantCalls("GET /api/v1/label-fields")

	f.set("GET", "/api/v1/label-fields", page("", labelField(testField, "Priority", "choice", false,
		labelChoice(testChoice, "High", false))))
	res = f.runAcc(LabelFieldUpdate, testField, "--archive-choice", testOther)
	wantCode(t, res, 2)
	f.set("GET", "/api/v1/label-fields", page("", labelField(testField, "Priority", "choice", false,
		labelChoice(testChoice, "High", false))))
	f.wantCalls("GET /api/v1/label-fields", "GET /api/v1/label-fields")
}

func TestLabelFieldUpdateRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, args := range [][]string{
		{},
		{testField},
		{"lbf_bad", "--name", "x"},
		{testField, "--type", "money"},
		{testField, "--restricted", "yes"},
		{testField, "--restricted"},
		{testField, "--rename-choice", "no-name"},
		{testField, "--rename-choice", "lbc_bad=Name"},
		{testField, "--archive-choice", "nope"},
		{testField, "--restore-choice", "nope"},
		{testField, "--add-choice", "a/b"},
	} {
		wantCode(t, f.runAcc(LabelFieldUpdate, args...), 2)
	}
	wantCode(t, f.run(LabelFieldUpdate, testField, "--name", "x"), 2)
	f.wantNoCalls()
}

func TestLabelFieldRmAsks(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", accLabelField(testField), done(http.StatusNoContent))
	// no terminal and no --yes: refused before a request
	wantCode(t, f.runAcc(LabelFieldRm, testField), 2)
	f.wantNoCalls()
	// a terminal where the field's id is typed
	typeAtTerminal(testField + "\n")
	res := f.runAcc(LabelFieldRm, testField)
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + accLabelField(testField))
	for _, want := range []string{"target=" + testField, "status=done"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
}

func TestLabelFieldRmYesAndRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", accLabelField(testField), done(http.StatusNoContent))
	wantCode(t, f.runAcc(LabelFieldRm, testField, "--yes"), 0)
	f.wantCalls("DELETE " + accLabelField(testField))
	for _, args := range [][]string{{}, {"lbf_bad"}, {"a", "b"}} {
		wantCode(t, f.runAcc(LabelFieldRm, args...), 2)
	}
	wantCode(t, f.run(LabelFieldRm, testField, "--yes"), 2)
	f.wantCalls("DELETE " + accLabelField(testField))
}

func TestLabelFieldJSONPrintsTheDocument(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accLabelFields(), doc(http.StatusCreated,
		labelField(testField, "Priority", "choice", false, labelChoice(testChoice, "High", false))))
	res := f.runAcc(LabelFieldAdd, "--type", "choice", "--json", "Priority")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"choices": [`+"\n"+`    {`) || !strings.Contains(res.stdout, `"id": "`+testChoice+`"`) {
		t.Fatalf("stdout %q is not the API's document", res.stdout)
	}
}

func TestLabelFieldConflictIsRefused(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accLabelFields(), apiErrWith(http.StatusConflict, "conflict", "a label field has that name", "name_taken"))
	res := f.runAcc(LabelFieldAdd, "--type", "text", "Taken")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "a label field has that name") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestLabelFieldAddKeepsRepeatedChoicesInOrder(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accLabelFields(), doc(http.StatusCreated,
		labelField(testField, "Priority", "choice", false, labelChoice(testChoice, "High", false))))
	res := f.runAcc(LabelFieldAdd, "--type", "choice", "--choice", "High", "--choice", "Low", "Priority")
	wantCode(t, res, 0)
	choices := f.bodyOf(0)["choices"].([]any)
	want := []string{"High", "Low"}
	for i, name := range want {
		if got := choices[i].(map[string]any)["name"]; got != name {
			t.Fatalf("choice %d is %v, want %q", i, got, name)
		}
	}
}

func TestLabelFieldOutputOmitsUnknownFields(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	field := labelField(testField, "Priority", "choice", false, labelChoice(testChoice, "High", false))
	field["token"] = "sck_secret_value"
	field["hash"] = "a hidden hash"
	f.on("POST", accLabelFields(), doc(http.StatusCreated, field))
	res := f.runAcc(LabelFieldAdd, "--type", "choice", "Priority")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") || strings.Contains(res.stdout, "hash=") {
		t.Fatalf("unknown fields were printed: stdout %q, stderr %q", res.stdout, res.stderr)
	}
	res = f.runAcc(LabelFieldAdd, "--type", "choice", "--json", "Priority")
	wantCode(t, res, 0)
	for _, unwanted := range []string{"sck_secret_value", "a hidden hash", `"values"`} {
		if strings.Contains(res.stdout, unwanted) {
			t.Fatalf("stdout %q has %q", res.stdout, unwanted)
		}
	}
}

func TestLabelFieldNotFoundAndForbidden(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", accLabelField(testField), apiErr(http.StatusNotFound, "not_found", "no such label field"))
	wantCode(t, f.runAcc(LabelFieldRm, testField, "--yes"), 4)
	f.on("PATCH", accLabelField(testField), apiErr(http.StatusForbidden, "forbidden", "the account's owner or key must do this"))
	res := f.runAcc(LabelFieldUpdate, testField, "--name", "New")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "owner or key") || strings.Contains(res.stderr, "hint") {
		t.Fatalf("stderr %q", res.stderr)
	}
	f.wantCalls("DELETE "+accLabelField(testField), "PATCH "+accLabelField(testField))
}

func TestLabelFieldAdminKeyGetsNoHint(t *testing.T) {
	isolate(t, "sc_admin_TESTKEY_0123456789abcdefghijklmnopqrstuvwxyz")
	f := newFake(t)
	f.on("POST", accLabelFields(), apiErr(http.StatusForbidden, "forbidden", "an installation admin does not reach an account's labels"))
	res := f.runAcc(LabelFieldAdd, "--type", "text", "x")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "hint") {
		t.Fatalf("stderr %q has a hint", res.stderr)
	}
}

func TestLabelFieldRmWrongAnswerChangesNothing(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	typeAtTerminal("lbf_something_else\n")
	wantCode(t, f.runAcc(LabelFieldRm, testField), 2)
	f.wantNoCalls()
}
