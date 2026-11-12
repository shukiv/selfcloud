// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// The sharing commands are proved against the real API with the account key, which holds manager everywhere. A token that
// acts for a user is refused by the sharing routes, so no other credential reaches them.

// member makes a user of the account who is a member, and returns its id.
func (p *fileProbe) member(name string) string {
	p.t.Helper()
	ctx := context.Background()
	now := time.Now()
	u := &store.User{ID: id.New("usr"), AccountID: p.acc, Email: name + "@example.test", EmailKey: name + "@example.test",
		DisplayName: name, Locale: "en", Role: store.RoleMember, Status: store.UserActive, CreatedAt: now, UpdatedAt: now,
		VerifiedAt: &now}
	if err := p.st.Write(ctx, func(tx *store.Tx) error { return tx.InsertUser(ctx, u) }); err != nil {
		p.t.Fatal(err)
	}
	return u.ID
}

// linksOn lets the probe's team folder have share links, which a new team folder does not.
func (p *fileProbe) linksOn() {
	p.t.Helper()
	ctx := context.Background()
	if err := p.st.Write(ctx, func(tx *store.Tx) error {
		f, err := tx.Folder(ctx, p.acc, p.folder)
		if err != nil {
			return err
		}
		f.AllowLinks = true
		return tx.UpdateFolderSettings(ctx, f)
	}); err != nil {
		p.t.Fatal(err)
	}
}

// anyoneLinksOn lets the account make links that anyone can open, in the probe's folder.
func (p *fileProbe) anyoneLinksOn() {
	p.t.Helper()
	p.linksOn()
	ctx := context.Background()
	if err := p.st.Write(ctx, func(tx *store.Tx) error { return tx.SetSharingPolicy(ctx, p.acc, true, time.Now()) }); err != nil {
		p.t.Fatal(err)
	}
}

// askers numbers the people who ask for access, so each has its own address.
var askers int

// accessRequest files a pending request of a new member for a role on node, as the web interface's request would, and
// returns its id.
func (p *fileProbe) accessRequest(nodeID string) string {
	p.t.Helper()
	ctx := context.Background()
	askers++
	ar := &store.AccessRequest{ID: id.New("arq"), AccountID: p.acc, NodeID: nodeID, UserID: p.member("asker-" + strconv.Itoa(askers)),
		Role: "viewer", Message: "please", Status: store.RequestPending, CreatedAt: time.Now()}
	if err := p.st.Write(ctx, func(tx *store.Tx) error {
		_, err := tx.InsertAccessRequest(ctx, ar)
		return err
	}); err != nil {
		p.t.Fatal(err)
	}
	return ar.ID
}

// link makes the organisation link of a new file and returns the file's node id and the link's id.
func (p *fileProbe) link(path string) (nodeID, linkID string) {
	p.t.Helper()
	p.linksOn()
	nodeID = p.touch(path)
	return nodeID, valueOf(p.t, p.do("link", "create", nodeID), "id=")
}

func init() {
	fileProbes["share ls"] = func(p *fileProbe) []string { return []string{p.touch("/share-ls.txt")} }
	fileProbes["share grant"] = func(p *fileProbe) []string {
		return []string{p.touch("/share-grant.txt"), p.member("grantee"), "--role", "viewer"}
	}
	fileProbes["share revoke"] = func(p *fileProbe) []string {
		node, user := p.touch("/share-revoke.txt"), p.member("revokee")
		p.do("share", "grant", node, user, "--role", "viewer")
		p.answer = user + "\n"
		return []string{node, user}
	}
	fileProbes["share transfer-ownership"] = func(p *fileProbe) []string {
		p.answer = "transfer.txt\n"
		return []string{p.touch("/transfer.txt"), "--to", p.member("heir")}
	}
	fileProbes["link ls"] = func(p *fileProbe) []string {
		node, _ := p.link("/link-ls.txt")
		return []string{node}
	}
	fileProbes["link create"] = func(p *fileProbe) []string {
		p.linksOn()
		return []string{p.touch("/link-create.txt")}
	}
	fileProbes["link update"] = func(p *fileProbe) []string {
		_, link := p.link("/link-update.txt")
		return []string{link, "--role", "editor"}
	}
	fileProbes["link reset"] = func(p *fileProbe) []string {
		_, link := p.link("/link-reset.txt")
		return []string{link}
	}
	fileProbes["link rm"] = func(p *fileProbe) []string {
		_, link := p.link("/link-rm.txt")
		p.answer = link + "\n"
		return []string{link}
	}
	fileProbes["access ls"] = func(p *fileProbe) []string {
		node := p.touch("/access-ls.txt")
		p.accessRequest(node)
		return []string{node}
	}
	fileProbes["access approve"] = func(p *fileProbe) []string { return []string{p.accessRequest(p.touch("/access-approve.txt"))} }
	fileProbes["access deny"] = func(p *fileProbe) []string { return []string{p.accessRequest(p.touch("/access-deny.txt"))} }
}

// sharingRun runs a command against the real server and returns its exit code and streams.
func (p *fileProbe) sharingRun(args ...string) (int, string, string) {
	p.t.Helper()
	var out, errOut bytes.Buffer
	code := run(append(append([]string{}, args...), "--url", p.url), &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestSharingCommandsHaveProbes: the sharing commands' own probes are in fileProbes; the commands that cannot be
// probed with an account key name why in remote.go.
func TestSharingCommandsHaveProbes(t *testing.T) {
	for _, c := range remoteCommands {
		if !strings.HasPrefix(c.name, "share ") && !strings.HasPrefix(c.name, "link ") && !strings.HasPrefix(c.name, "access ") &&
			c.name != "shared" {
			continue
		}
		if _, ok := fileProbes[c.name]; !ok && c.notProbed == "" {
			t.Errorf("%s has no probe and no reason", c.name)
		}
	}
}

func TestEveryRoleIsGrantedThroughTheRealAPI(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	node := p.touch("/roles.txt")
	for _, role := range []string{"viewer", "commenter", "contributor", "editor", "manager"} {
		user := p.member("r-" + role)
		code, out, errOut := p.sharingRun("share", "grant", node, user, "--role", role, "--no-notify")
		if code != 0 {
			t.Fatalf("grant %s: exit %d, stderr %q", role, code, errOut)
		}
		if !strings.Contains(out, "role="+role+"\n") {
			t.Fatalf("grant %s: stdout %q", role, out)
		}
		_, listing, _ := p.sharingRun("share", "ls", node)
		if !strings.Contains(listing, user) || !strings.Contains(listing, role) {
			t.Fatalf("share ls after %s: %q", role, listing)
		}
	}
	// the API's own refusals come back with its exit codes
	if code, _, errOut := p.sharingRun("share", "grant", node, p.member("late"), "--role", "viewer", "--expires", "2020-01-01T00:00:00Z"); code != 2 {
		t.Errorf("an end in the past: exit %d, stderr %q, want 2", code, errOut)
	}
	if code, _, _ := p.sharingRun("share", "grant", node, id.New("usr"), "--role", "viewer"); code != 4 {
		t.Errorf("a user that does not exist: exit %d, want 4", code)
	}
}

func TestEveryLinkRoleAndTheRulesOfLinksThroughTheRealAPI(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	p.linksOn()
	for _, role := range []string{"viewer", "commenter", "contributor", "editor"} {
		node := p.touch("/link-" + role + ".txt")
		code, out, errOut := p.sharingRun("link", "create", node, "--role", role)
		if code != 0 || !strings.Contains(out, "role="+role+"\n") {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", role, code, out, errOut)
		}
	}
	node := p.touch("/link-rules.txt")
	if code, _, _ := p.sharingRun("link", "create", node, "--audience", "anyone"); code != 3 {
		t.Errorf("an anyone link while the account's switch is off: exit %d, want 3", code)
	}
	p.anyoneLinksOn()
	if code, _, errOut := p.sharingRun("link", "create", node, "--audience", "anyone", "--role", "editor"); code != 2 {
		t.Errorf("an anyone link above viewer: exit %d, stderr %q, want 2", code, errOut)
	}
	if code, _, errOut := p.sharingRun("link", "create", node, "--audience", "anyone", "--expires-in", "30d"); code != 0 {
		t.Fatalf("an anyone link: exit %d, stderr %q", code, errOut)
	}
	if code, _, _ := p.sharingRun("link", "create", node); code != 3 {
		t.Errorf("a second link on the node: exit %d, want 3", code)
	}
}

// TestLinkSecretsThroughTheRealAPI: the address of a link is shown once, by create and by reset, and a listing, an update
// and every error leave it out; the password goes in from a file and never comes back.
func TestLinkSecretsThroughTheRealAPI(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	p.anyoneLinksOn()
	node := p.touch("/secret.txt")
	pw := filepath.Join(t.TempDir(), "pw")
	const password = "correct horse battery staple"
	if err := os.WriteFile(pw, []byte(password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := p.sharingRun("link", "create", node, "--audience", "anyone", "--expires-in", "7d", "--password-file", pw)
	if code != 0 {
		t.Fatalf("create: exit %d, stderr %q", code, errOut)
	}
	address := valueOf(t, out, "url=")
	linkID := valueOf(t, out, "id=")
	i := strings.Index(address, "/s#")
	if i < 0 {
		t.Fatalf("the address %q is not a share link", address)
	}
	secret := address[i+3:]
	if strings.Contains(out+errOut, password) || strings.Count(out, secret) != 1 || strings.Contains(errOut, secret) {
		t.Fatalf("create leaked: stdout %q stderr %q", out, errOut)
	}
	quiet := func(args ...string) string {
		t.Helper()
		code, out, errOut := p.sharingRun(args...)
		if code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
		if strings.Contains(out+errOut, secret) || strings.Contains(out+errOut, password) || strings.Contains(out+errOut, "/s#") {
			t.Fatalf("%v leaked a secret: stdout %q stderr %q", args, out, errOut)
		}
		return out
	}
	quiet("link", "ls", node)
	quiet("link", "ls", node, "--json")
	quiet("link", "ls", "--account", p.acc)
	quiet("link", "ls", "--account", p.acc, "--json")
	quiet("link", "update", linkID, "--expires-in", "10d")
	quiet("link", "update", linkID, "--password-file", pw)
	quiet("link", "update", linkID, "--no-password")
	if listing := quiet("link", "ls", node); !strings.Contains(listing, "false") {
		t.Fatalf("the listing should say the password is gone: %q", listing)
	}
	// a refused change does not repeat the password
	if code, out, errOut := p.sharingRun("link", "update", linkID, "--audience", "organisation", "--password-file", pw); code == 0 ||
		strings.Contains(out+errOut, password) {
		t.Fatalf("an organisation link takes no password: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	// reset gives a new secret, shown once, and the old one is gone
	code, out, errOut = p.sharingRun("link", "reset", linkID)
	if code != 0 {
		t.Fatalf("reset: exit %d, stderr %q", code, errOut)
	}
	fresh := valueOf(t, out, "url=")
	if fresh == address || strings.Count(out, fresh[strings.Index(fresh, "/s#")+3:]) != 1 || strings.Contains(errOut, fresh[strings.Index(fresh, "/s#")+3:]) {
		t.Fatalf("reset: stdout %q stderr %q", out, errOut)
	}
}

func TestSignedLinkThroughTheRealAPI(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	node := p.touch("/signed.txt")
	code, out, errOut := p.sharingRun("link", "create", node, "--signed", "--ttl", "10m")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	address := valueOf(t, out, "url=")
	if !strings.Contains(address, "/api/v1/links/") || strings.Contains(errOut, address) {
		t.Fatalf("stdout %q stderr %q", out, errOut)
	}
	if code, _, _ := p.sharingRun("link", "create", p.mkdir("/a-dir"), "--signed"); code != 2 {
		t.Errorf("a signed link to a directory: exit %d, want 2", code)
	}
}

func TestFolderGrantsThroughTheRealAPI(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	user := p.member("folk")
	for _, level := range []string{"read", "write"} {
		code, out, errOut := p.sharingRun("share", "grant", "--folder", p.folder, "--account", p.acc, user, "--level", level)
		if code != 0 || !strings.Contains(out, "level="+level+"\n") {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", level, code, out, errOut)
		}
	}
	if _, out, _ := p.sharingRun("share", "ls", "--folder", p.folder, "--account", p.acc); !strings.Contains(out, user) {
		t.Fatalf("share ls: %q", out)
	}
	if code, _, _ := p.sharingRun("share", "revoke", "--folder", p.folder, "--account", p.acc, user, "--yes"); code != 0 {
		t.Fatalf("revoke: exit %d", code)
	}
	if code, _, _ := p.sharingRun("share", "revoke", "--folder", p.folder, "--account", p.acc, user, "--yes"); code != 4 {
		t.Errorf("a second revoke: exit %d, want 4", code)
	}
	// the account's id is the one of the key: another account's id is not found
	if code, _, _ := p.sharingRun("share", "ls", "--folder", p.folder, "--account", id.New("acc")); code != 4 {
		t.Errorf("another account: exit %d, want 4", code)
	}
}

func TestPublicLinksAreRemovedThroughTheRealAPI(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	p.anyoneLinksOn()
	for _, name := range []string{"/p1.txt", "/p2.txt"} {
		if code, _, errOut := p.sharingRun("link", "create", p.touch(name), "--audience", "anyone"); code != 0 {
			t.Fatalf("create: exit %d, stderr %q", code, errOut)
		}
	}
	p.touch("/org.txt")
	p.link("/org-link.txt")
	code, out, errOut := p.sharingRun("link", "rm", "--public", "--account", p.acc, "--yes")
	if code != 0 || out != "deleted=2\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if _, out, _ := p.sharingRun("link", "ls", "--account", p.acc, "--audience", "anyone"); strings.Contains(out, "anyone") {
		t.Fatalf("public links remain: %q", out)
	}
}

func TestAccessRequestsThroughTheRealAPI(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	node := p.touch("/asked.txt")
	req := p.accessRequest(node)
	if _, out, _ := p.sharingRun("access", "ls", "--pending"); !strings.Contains(out, req) {
		t.Fatalf("pending: %q", out)
	}
	if code, _, errOut := p.sharingRun("access", "approve", req, "--role", "commenter"); code != 0 {
		t.Fatalf("approve: exit %d, stderr %q", code, errOut)
	}
	if code, _, _ := p.sharingRun("access", "approve", req); code != 3 {
		t.Errorf("deciding twice: exit %d, want 3", code)
	}
	if _, out, _ := p.sharingRun("access", "ls", node); !strings.Contains(out, "approved") {
		t.Fatalf("access ls: %q", out)
	}
}

func TestOwnershipThroughTheRealAPI(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	user := p.member("heir")
	node := p.touch("/gift.txt")
	code, out, errOut := p.sharingRun("share", "transfer-ownership", node, "--to", user, "--yes")
	if code != 0 || !strings.Contains(out, "owner_id="+user+"\n") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if code, _, _ := p.sharingRun("share", "transfer-ownership", node, "--to", id.New("usr"), "--yes"); code != 4 {
		t.Errorf("a user that does not exist: exit %d, want 4", code)
	}
}
