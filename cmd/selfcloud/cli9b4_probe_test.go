// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// The label field and sharing policy commands (cli9b4) run against the real API with the probe account's key. Their
// fixtures are made through the same commands, outside the recording.

func init() {
	for name, fn := range labelSharingProbes {
		fileProbes[name] = fn
	}
}

// choiceLine returns the output line of the choice with id choiceID.
func choiceLine(t *testing.T, out, choiceID string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "choice="); ok {
			if id, _, _ := strings.Cut(rest, " "); id == choiceID {
				return line
			}
		}
	}
	t.Fatalf("no choice %s in %q", choiceID, out)
	return ""
}

// choiceID returns the first choice's id in the output of label field add.
func choiceID(t *testing.T, out string) string {
	t.Helper()
	rest := valueOf(t, out, "choice=")
	id, _, _ := strings.Cut(rest, " ")
	if id == "" {
		t.Fatalf("no choice id in %q", out)
	}
	return id
}

var labelSharingProbes = map[string]func(p *fileProbe) []string{
	"label field add": func(p *fileProbe) []string {
		return []string{"--account", p.acc, "--type", "text", "probe-field"}
	},
	"label field update": func(p *fileProbe) []string {
		out := p.do("label", "field", "add", "--account", p.acc, "--type", "choice", "--choice", "probe-a", "probe-field-update")
		return []string{"--account", p.acc, valueOf(p.t, out, "id="), "--archive-choice", choiceID(p.t, out)}
	},
	"label field rm": func(p *fileProbe) []string {
		field := valueOf(p.t, p.do("label", "field", "add", "--account", p.acc, "--type", "text", "probe-field-rm"), "id=")
		p.answer = field + "\n"
		return []string{"--account", p.acc, field}
	},
	"sharing policy show": func(p *fileProbe) []string {
		return []string{"--account", p.acc}
	},
	"sharing policy set": func(p *fileProbe) []string {
		old := valueOf(p.t, p.do("sharing", "policy", "show", "--account", p.acc), "comments=")
		want := "true"
		if old == "true" {
			want = "false"
		}
		// Leave the policy as the other probes expect it, after this test.
		p.t.Cleanup(func() { p.do("sharing", "policy", "set", "--account", p.acc, "--comments", old) })
		return []string{"--account", p.acc, "--comments", want}
	},
}

// TestLabelFieldsAndSharingPolicyRoundTripOnTheRealServer: the commands do what they say on the real API.
func TestLabelFieldsAndSharingPolicyRoundTripOnTheRealServer(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	out := p.do("label", "field", "add", "--account", p.acc, "--type", "choice", "--choice", "Low", "--choice", "High", "Probe field")
	field := valueOf(t, out, "id=")
	low, high := choiceLine(t, out, choiceID(t, out)), ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " High") {
			high = line
		}
	}
	if high == "" {
		t.Fatalf("no High choice in %q", out)
	}
	if got := valueOf(t, p.do("label", "field", "update", "--account", p.acc, field, "--name", "Probe renamed"), "name="); got != "Probe renamed" {
		t.Fatalf("name %q after the rename", got)
	}
	archived := p.do("label", "field", "update", "--account", p.acc, field, "--archive-choice", choiceID(t, out))
	if !strings.HasSuffix(choiceLine(t, archived, choiceID(t, out)), " (archived)") {
		t.Fatalf("the choice is not archived: %q", archived)
	}
	restored := p.do("label", "field", "update", "--account", p.acc, field, "--restore-choice", choiceID(t, out))
	if strings.HasSuffix(choiceLine(t, restored, choiceID(t, out)), " (archived)") {
		t.Fatalf("the choice is still archived: %q", restored)
	}
	added := p.do("label", "field", "update", "--account", p.acc, field, "--add-choice", "Later", "--rename-choice", choiceID(t, out)+"=Urgent")
	if !strings.Contains(added, " Later") || !strings.Contains(choiceLine(t, added, choiceID(t, out)), "Urgent") {
		t.Fatalf("the added and renamed choices are not listed: %q (low was %q, high %q)", added, low, high)
	}
	p.do("label", "field", "rm", "--account", p.acc, field, "--yes")

	old := valueOf(t, p.do("sharing", "policy", "show", "--account", p.acc), "comments=")
	want := "true"
	if old == "true" {
		want = "false"
	}
	changed := p.do("sharing", "policy", "set", "--account", p.acc, "--comments", want)
	if got := valueOf(t, changed, "comments="); got != want {
		t.Fatalf("comments %q after the change", got)
	}
	if got := valueOf(t, p.do("sharing", "policy", "show", "--account", p.acc), "comments="); got != want {
		t.Fatalf("comments %q after reading back", got)
	}
	p.do("sharing", "policy", "set", "--account", p.acc, "--comments", old)
}
