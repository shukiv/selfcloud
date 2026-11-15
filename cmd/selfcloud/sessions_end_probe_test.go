// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

func init() {
	fileProbes["user sessions end"] = func(p *fileProbe) []string {
		return []string{"--account", p.acc, p.member("sessions-end@example.test")}
	}
}

// TestUserSessionsEndOnTheRealServer: the command ends the person's web sessions on the real API, and the answer
// counts them. A second call ends nothing.
func TestUserSessionsEndOnTheRealServer(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	usr := p.member("sessions-roundtrip@example.test")
	_, hash := auth.NewToken()
	csrf, _ := auth.NewToken()
	now := store.Now()
	ctx := context.Background()
	if err := p.st.Write(ctx, func(tx *store.Tx) error {
		return tx.InsertSession(ctx, &store.Session{IDHash: hash, UserID: usr, AccountID: p.acc, CSRFToken: csrf,
			CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour), SessionRef: "app-one"})
	}); err != nil {
		t.Fatal(err)
	}

	p.reset()
	var out, errOut bytes.Buffer
	code, matched := remote([]string{"user", "sessions", "end", "--account", p.acc, usr, "--ref", "app-one",
		"--url", p.recURL}, &out, &errOut)
	if !matched || code != 0 {
		t.Fatalf("exit %d, matched %v, stderr %q", code, matched, errOut.String())
	}
	if out.String() != "ended 1 session(s)\n" {
		t.Fatalf("stdout %q", out.String())
	}
	if got := p.recorded(); len(got) != 1 || got[0] != "POST /api/v1/accounts/"+p.acc+"/users/"+usr+"/sessions/end" {
		t.Fatalf("calls %v", got)
	}
	if _, err := p.st.SessionByHash(ctx, hash); err == nil {
		t.Fatal("the session is still in the store")
	}

	out.Reset()
	errOut.Reset()
	code, matched = remote([]string{"user", "sessions", "end", "--account", p.acc, usr, "--json",
		"--url", p.recURL}, &out, &errOut)
	if !matched || code != 0 {
		t.Fatalf("exit %d, matched %v, stderr %q", code, matched, errOut.String())
	}
	if !strings.Contains(out.String(), `"ended": 0`) {
		t.Fatalf("second answer %q, want ended 0", out.String())
	}
}
