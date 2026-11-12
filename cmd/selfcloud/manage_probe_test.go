// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// The folder and group commands run against the real API with the probe account's key. The probes make their fixtures
// through the same commands, outside the recording.

// init registers the probes. A hide is a person's own choice, and the real server answers an account key with a refusal (403, exit
// 3). The probe checks that the command reaches its route and reports the refusal.
func init() {
	probeExit["folder hide"] = 3
	probeExit["folder show-again"] = 3
	for name, fn := range manageProbes {
		fileProbes[name] = fn
	}
}

// member makes an active member of the probe account in its store and returns the user's id.
func (p *fileProbe) member(email string) string {
	p.t.Helper()
	ctx := context.Background()
	now := store.Now()
	u := &store.User{ID: id.New("usr"), AccountID: p.acc, Email: email, EmailKey: strings.ToLower(email), DisplayName: "Probe",
		Locale: "en", Role: store.RoleMember, Status: store.UserActive, VerifiedAt: &now, CreatedAt: now, UpdatedAt: now}
	if err := p.st.Write(ctx, func(tx *store.Tx) error { return tx.InsertUser(ctx, u) }); err != nil {
		p.t.Fatal(err)
	}
	return u.ID
}

// newFolder makes a team folder through the command and returns its id.
func (p *fileProbe) newFolder(name string) string {
	return valueOf(p.t, p.do("folder", "create", "--account", p.acc, name), "id=")
}

// newGroup makes a group through the command and returns its id.
func (p *fileProbe) newGroup(name string) string {
	return valueOf(p.t, p.do("group", "create", "--account", p.acc, name), "id=")
}

var manageProbes = map[string]func(p *fileProbe) []string{
	"folder create": func(p *fileProbe) []string { return []string{"--account", p.acc, "probe-created"} },
	"folder ls":     func(p *fileProbe) []string { return []string{"--account", p.acc} },
	"folder show":   func(p *fileProbe) []string { return []string{"--account", p.acc, p.folder} },
	"folder rename": func(p *fileProbe) []string { return []string{"--account", p.acc, p.folder, "probe-renamed"} },
	"folder rm": func(p *fileProbe) []string {
		p.answer = "probe-removed\n"
		return []string{"--account", p.acc, p.newFolder("probe-removed")}
	},
	"folder settings set": func(p *fileProbe) []string { return []string{p.folder, "--allow-links=false"} },
	"folder hide":         func(p *fileProbe) []string { return []string{p.folder} },
	"folder show-again":   func(p *fileProbe) []string { return []string{p.folder} },
	"group create":        func(p *fileProbe) []string { return []string{"--account", p.acc, "probe-group"} },
	"group ls":            func(p *fileProbe) []string { return []string{"--account", p.acc} },
	"group show": func(p *fileProbe) []string {
		return []string{"--account", p.acc, p.newGroup("probe-show")}
	},
	"group rename": func(p *fileProbe) []string {
		return []string{"--account", p.acc, p.newGroup("probe-rename"), "probe-renamed-group"}
	},
	"group rm": func(p *fileProbe) []string {
		p.answer = "probe-group-removed\n"
		return []string{"--account", p.acc, p.newGroup("probe-group-removed")}
	},
	"group members": func(p *fileProbe) []string {
		return []string{"--account", p.acc, p.newGroup("probe-members")}
	},
	"group add-member": func(p *fileProbe) []string {
		return []string{"--account", p.acc, p.newGroup("probe-add"), p.member("add@example.test")}
	},
	"group remove-member": func(p *fileProbe) []string {
		g, u := p.newGroup("probe-remove"), p.member("remove@example.test")
		p.do("group", "add-member", "--account", p.acc, g, u)
		return []string{"--account", p.acc, g, u}
	},
	"user grants": func(p *fileProbe) []string {
		return []string{"--account", p.acc, p.member("grants@example.test")}
	},
}

// TestFolderLsMineOnTheRealServer: `folder ls --mine` is the other route of folder ls, read from the caller's own list.
func TestFolderLsMineOnTheRealServer(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	out := p.do("folder", "ls", "--mine", "--json")
	if !strings.Contains(out, p.folder) {
		t.Fatalf("--mine does not list the probe folder: %q", out)
	}
}

// TestManageCommandsRoundTripOnTheRealServer: the commands do what they say on the real API, not only reach its routes.
func TestManageCommandsRoundTripOnTheRealServer(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	team := p.newFolder("Round trip: ünï -x")
	if got := p.do("folder", "ls", "--account", p.acc); !strings.Contains(got, "Round trip: ünï -x") {
		t.Fatalf("the new folder is not listed: %q", got)
	}
	p.do("folder", "rename", "--account", p.acc, team, "--", "-renamed")
	if got := valueOf(t, p.do("folder", "show", "--account", p.acc, team), "name="); got != "-renamed" {
		t.Fatalf("name %q after the rename", got)
	}
	p.do("folder", "settings", "set", team, "--members-only=true")
	if got := valueOf(t, p.do("folder", "show", "--account", p.acc, team), "members_only="); got != "true" {
		t.Fatalf("members_only %q after the change", got)
	}
	g := p.newGroup("Round trip group")
	u := p.member("trip@example.test")
	p.do("group", "add-member", "--account", p.acc, g, u)
	if got := p.do("group", "members", "--account", p.acc, g); !strings.Contains(got, u) {
		t.Fatalf("the member is not listed: %q", got)
	}
	// A user the account does not have is not found (exit 4).
	var out, errOut bytes.Buffer
	unknown := []string{"group", "add-member", "--account", p.acc, g, id.New("usr"), "--url", p.url}
	if code := run(unknown, &out, &errOut); code != 4 {
		t.Fatalf("an unknown user: exit %d, want 4; stderr %q", code, errOut.String())
	}
	p.do("group", "remove-member", "--account", p.acc, g, u)
	if got := p.do("group", "members", "--account", p.acc, g); strings.Contains(got, u) {
		t.Fatalf("the member is still listed: %q", got)
	}
	p.do("folder", "rm", "--account", p.acc, team, "--yes")
	if got := p.do("folder", "ls", "--account", p.acc); slices.Contains(strings.Fields(got), team) {
		t.Fatalf("the removed folder is still listed: %q", got)
	}
	p.do("group", "rm", "--account", p.acc, g, "--yes")
}
