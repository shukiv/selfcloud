// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"encoding/json"
	"strings"
	"testing"
)

// patched is the PATCH answer the fake gives: the whole document, as the server does.
const patched = `{"settings":[
 {"key":"flag.enabled","type":"bool","value":true,"default":false,"source":"settings","locked":false,"restart":false},
 {"key":"count.key","type":"int","value":10,"default":5,"source":"settings","locked":false,"restart":false},
 {"key":"dur.key","type":"duration","value":"48h0m0s","default":"168h0m0s","source":"settings","locked":false,"restart":false},
 {"key":"other.key","type":"string","value":"x","default":"x","source":"default","locked":false,"restart":false}],
 "facts":[],"pictures":[]}`

func TestSetSendsTheValuesByType(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	f.on("PATCH", settingsPath, reply{200, patched})
	r := f.run(Set, "flag.enabled=true", "count.key=10", "dur.key=48h", "size.key=64MiB", "name.key=Acme Drive")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	f.wantCalls("GET "+settingsPath, "PATCH "+settingsPath)
	if ct := f.contentType(1); ct != "application/json" {
		t.Errorf("content type %q", ct)
	}
	sameJSON(t, string(f.body(1)),
		`{"set":{"flag.enabled":true,"count.key":10,"dur.key":"48h","size.key":"64MiB","name.key":"Acme Drive"}}`)
}

func TestSetRefusesAValueOfTheWrongShapeBeforeThePatch(t *testing.T) {
	for _, arg := range []string{"flag.enabled=yes", "count.key=1.5", "count.key=", "count.key=99999999999999999999"} {
		t.Run(arg, func(t *testing.T) {
			isolate(t, adminKey)
			f := newFake(t)
			f.on("GET", settingsPath, reply{200, listing})
			r := f.run(Set, arg)
			if r.code != 2 {
				t.Errorf("exit %d, stderr %q", r.code, r.stderr)
			}
			f.wantCalls("GET " + settingsPath)
		})
	}
}

func TestSetRefusesAnUnknownKeyAndAPictureKey(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	r := f.run(Set, "nope.key=1")
	if r.code != 2 || !strings.Contains(r.stderr, `no setting named "nope.key"`) {
		t.Errorf("unknown: exit %d, stderr %q", r.code, r.stderr)
	}
	r = f.run(Set, "ui.brand.logo=x")
	if r.code != 2 || !strings.Contains(r.stderr, "ui.brand.logo is a picture: use settings logo set") {
		t.Errorf("picture: exit %d, stderr %q", r.code, r.stderr)
	}
	r = f.run(Set, "ui.brand.icon=x")
	if r.code != 2 || !strings.Contains(r.stderr, "settings icon set") {
		t.Errorf("icon: exit %d, stderr %q", r.code, r.stderr)
	}
	f.wantCalls("GET "+settingsPath, "GET "+settingsPath, "GET "+settingsPath)
}

func TestSetRefusesBadArgumentsWithNoRequest(t *testing.T) {
	for _, args := range [][]string{{"noequals"}, {"=v"}, {"a.key=1", "a.key=2"}, {}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			isolate(t, adminKey)
			f := newFake(t)
			r := f.run(Set, args...)
			if r.code != 2 {
				t.Errorf("exit %d, stderr %q", r.code, r.stderr)
			}
			f.wantCalls()
		})
	}
}

func TestSetSplitsAtTheFirstEquals(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	f.on("PATCH", settingsPath, reply{200, patched})
	if r := f.run(Set, "name.key=a=b"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	sameJSON(t, string(f.body(1)), `{"set":{"name.key":"a=b"}}`)
}

func TestSetShowsTheInvalidValuesTheServerNamed(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	f.on("PATCH", settingsPath, reply{400, `{"error":{"code":"validation_failed","message":"a setting is not valid",
	 "details":{"reason":"invalid_setting","keys":["dur.key","size.key"],"errors":["dur.key must be positive","size.key is too small"]}}}`})
	r := f.run(Set, "dur.key=-1h", "size.key=1")
	if r.code != 2 {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
	for _, want := range []string{"dur.key must be positive", "size.key is too small"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, r.stderr)
		}
	}
}

func TestSetShowsTheLockedKeys(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	f.on("PATCH", settingsPath, reply{409, `{"error":{"code":"conflict","message":"a setting is locked",
	 "details":{"reason":"setting_locked","keys":["dur.key","name.key"]}}}`})
	r := f.run(Set, "dur.key=1h")
	if r.code != 3 || !strings.Contains(r.stderr, "keys: dur.key, name.key") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestSetPrintsOnlyTheKeysSent(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	f.on("PATCH", settingsPath, reply{200, patched})
	r := f.run(Set, "flag.enabled=true", "dur.key=48h")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	want := table(
		[]string{"KEY", "VALUE", "SOURCE", "DEFAULT", "NOTE"},
		[]string{"flag.enabled", "true", "settings", "false", ""},
		[]string{"dur.key", "48h0m0s", "settings", "168h0m0s", ""},
	)
	if trimLines(r.stdout) != trimLines(want) {
		t.Errorf("stdout:\n%s\nwant:\n%s", r.stdout, want)
	}
	j := f.run(Set, "flag.enabled=true", "--json")
	if j.code != 0 {
		t.Fatalf("--json: exit %d, stderr %q", j.code, j.stderr)
	}
	sameJSON(t, j.stdout, patched)
}

func TestResetSendsTheKeysAndReadsNothing(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("PATCH", settingsPath, reply{200, patched})
	r := f.run(Reset, "flag.enabled", "count.key")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	f.wantCalls("PATCH " + settingsPath)
	sameJSON(t, string(f.body(0)), `{"reset":["flag.enabled","count.key"]}`)
	if !strings.Contains(r.stdout, "flag.enabled") || strings.Contains(r.stdout, "other.key") {
		t.Errorf("stdout %q", r.stdout)
	}
}

func TestResetRefusesNoKeyAndADuplicateWithNoRequest(t *testing.T) {
	for _, args := range [][]string{{}, {"a.key", "a.key"}} {
		isolate(t, adminKey)
		f := newFake(t)
		r := f.run(Reset, args...)
		if r.code != 2 {
			t.Errorf("%v: exit %d, stderr %q", args, r.code, r.stderr)
		}
		f.wantCalls()
	}
}

func TestResetOfAnUnknownKeyShowsTheKeys(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("PATCH", settingsPath, reply{400, `{"error":{"code":"validation_failed","message":"no such setting",
	 "details":{"reason":"unknown_setting","keys":["nope.key"]}}}`})
	r := f.run(Reset, "nope.key")
	if r.code != 2 || !strings.Contains(r.stderr, "keys: nope.key") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestSetWithAnAccountKeyAndA403HintsAtTheAdminKey(t *testing.T) {
	isolate(t, accountKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{403, `{"error":{"code":"forbidden","message":"an installation admin is required"}}`})
	r := f.run(Set, "count.key=1")
	if r.code != 3 || !strings.Contains(r.stderr, hint) {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
}

// Under --json stderr is the one error line a script parses: the detail lines and the admin-key hint are not added to it.
func TestARefusalUnderJSONIsOneEnvelopeLine(t *testing.T) {
	isolate(t, accountKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	f.on("PATCH", settingsPath, reply{409, `{"error":{"code":"conflict","message":"a setting is locked",
	 "details":{"reason":"setting_locked","keys":["dur.key"]}}}`})
	r := f.run(Set, "dur.key=1h", "--json")
	if r.code != 3 {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
	lines := strings.Split(strings.TrimRight(r.stderr, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stderr has %d lines: %q", len(lines), r.stderr)
	}
	var env struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil || env.Error.Code != "conflict" {
		t.Errorf("stderr is not the error envelope: %q (%v)", lines[0], err)
	}
	if strings.Contains(r.stderr, "keys:") || strings.Contains(r.stderr, hint) {
		t.Errorf("detail lines under --json: %q", r.stderr)
	}
}

// With an account key a 403 under --json carries no hint line either.
func TestA403UnderJSONHasNoHintLine(t *testing.T) {
	isolate(t, accountKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{403, `{"error":{"code":"forbidden","message":"an installation admin is required"}}`})
	r := f.run(Get, "--json")
	if r.code != 3 || strings.Contains(r.stderr, hint) || strings.Count(r.stderr, "\n") != 1 {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
}

// The problems and the keys are the server's text, so a terminal escape in them never reaches standard error.
func TestTheDetailLinesAreCleanedOfTerminalEscapes(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	f.on("PATCH", settingsPath,
		reply{400, `{"error":{"code":"validation_failed","message":"a setting is not valid",
		 "details":{"reason":"invalid_setting","keys":["dur.key"],"errors":["dur.key \u001b[31mred"]}}}`},
		reply{409, `{"error":{"code":"conflict","message":"a setting is locked",
		 "details":{"reason":"setting_locked","keys":["a\u001b[2Jb"]}}}`})
	invalid := f.run(Set, "dur.key=1h")
	locked := f.run(Set, "dur.key=1h")
	for name, r := range map[string]result{"errors": invalid, "keys": locked} {
		if strings.ContainsRune(r.stderr, '\x1b') {
			t.Errorf("%s: an escape reached stderr: %q", name, r.stderr)
		}
	}
	if !strings.Contains(invalid.stderr, "dur.key  [31mred") || !strings.Contains(locked.stderr, "keys: a [2Jb") {
		t.Errorf("the text is not kept with the escape as a space:\n%q\n%q", invalid.stderr, locked.stderr)
	}
}
