// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/shukiv/selfcloud/internal/mail"
)

// sendFunc is a mail sender that runs a function, so a test can look at the store when a mail goes out.
type sendFunc func(ctx context.Context, m mail.Message) error

func (f sendFunc) Send(ctx context.Context, m mail.Message) error { return f(ctx, m) }

// fu2 item 4: a verification mail is audited before it is sent, so no mail goes out without its row, and a write that
// fails stops the send. A failed send is audited too, as a row of its own with the outcome "failed".
func TestVerificationMailIsAuditedBeforeItIsSent(t *testing.T) {
	t.Parallel()
	var (
		s   *Server
		acc accountJSON
		key string
	)
	// seenAtSend is, for each send, how many verification.send rows the audit log held when the mail left.
	var seenAtSend []int
	var sendErr error
	mailer := sendFunc(func(ctx context.Context, m mail.Message) error {
		seenAtSend = append(seenAtSend, countVerificationSends(t, s, acc.ID, key, ""))
		return sendErr
	})
	s = newAuthServer(t, mailer)
	admin := adminKey(t, s)
	acc, key = newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"
	// 1. The row is there when the mail leaves: one row, written before the send.
	ann := decode[userResponse](t, call(t, s, "POST", users, key,
		map[string]any{"email": "ann@example.com", "verification": "email"}))
	if ann.Verification == nil || ann.Verification.Sent == nil || !*ann.Verification.Sent {
		t.Fatalf("the mail was not reported sent: %+v", ann.Verification)
	}
	if len(seenAtSend) != 1 || seenAtSend[0] != 1 {
		t.Fatalf("rows held at the send: %v, want [1]: the mail left before its row was written", seenAtSend)
	}

	// 2. The audit write fails: no mail goes out, and the answer says so, as a send that failed does.
	execOn(t, s, "CREATE TRIGGER refuse_mail_audit BEFORE INSERT ON audit_log WHEN NEW.action = 'verification.send' "+
		"BEGIN SELECT RAISE(ABORT, 'audit refused'); END")
	bob := decode[userResponse](t, call(t, s, "POST", users, key,
		map[string]any{"email": "bob@example.com", "verification": "email"}))
	if bob.Verification == nil || bob.Verification.Sent == nil || *bob.Verification.Sent {
		t.Errorf("with the audit refused, the answer says the mail was sent: %+v", bob.Verification)
	}
	if len(seenAtSend) != 1 {
		t.Errorf("a mail was sent (%d sends in all) although its audit row could not be written", len(seenAtSend))
	}
	execOn(t, s, "DROP TRIGGER refuse_mail_audit")

	// 3. The send fails: the row is written first, and a row with the outcome failed follows it.
	sendErr = mail.ErrFake
	wantStatus(t, call(t, s, "POST", users, key, map[string]any{"email": "carl@example.com", "verification": "email"}),
		http.StatusCreated)
	if n := countVerificationSends(t, s, acc.ID, key, "failed"); n != 1 {
		t.Errorf("failed sends in the audit log = %d, want 1", n)
	}
}

// countVerificationSends counts the verification.send rows of the account, or only those with outcome when it is set.
func countVerificationSends(t *testing.T, s *Server, accountID, key, outcome string) int {
	t.Helper()
	rec := call(t, s, "GET", "/api/v1/accounts/"+accountID+"/audit", key, nil)
	wantStatus(t, rec, http.StatusOK)
	n := 0
	for _, it := range decode[listResponse[auditJSON]](t, rec).Items {
		if it.Action == "verification.send" && (outcome == "" || it.Outcome == outcome) {
			n++
		}
	}
	return n
}
