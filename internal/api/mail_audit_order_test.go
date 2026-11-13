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

// fu2 (decision 74). The audit log is append-only (InsertAudit only appends), so one mail is two rows:
// an "attempted" row written before the send, and a result row after it, "ok" or "failed". The attempted row is never
// "ok", so a crash between the two leaves a record of an attempt, not of a mail that was sent.
func TestVerificationMailIsAuditedBeforeItIsSent(t *testing.T) {
	t.Parallel()
	var (
		s   *Server
		acc accountJSON
		key string
	)
	// attemptedAtSend is, for each send, how many attempted rows the log held when the mail left.
	var attemptedAtSend []int
	var sendErr error
	mailer := sendFunc(func(ctx context.Context, m mail.Message) error {
		attemptedAtSend = append(attemptedAtSend, countVerificationSends(t, s, acc.ID, key, "attempted"))
		return sendErr
	})
	s = newAuthServer(t, mailer)
	admin := adminKey(t, s)
	acc, key = newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"

	// 1. A successful send: the attempted row is there when the mail leaves, and one ok row follows it.
	ann := decode[userResponse](t, call(t, s, "POST", users, key,
		map[string]any{"email": "ann@example.com", "verification": "email"}))
	if ann.Verification == nil || ann.Verification.Sent == nil || !*ann.Verification.Sent {
		t.Fatalf("the mail was not reported sent: %+v", ann.Verification)
	}
	if len(attemptedAtSend) != 1 || attemptedAtSend[0] != 1 {
		t.Fatalf("attempted rows at the send: %v, want [1]: the mail left before its attempt was written", attemptedAtSend)
	}
	if n := countVerificationSends(t, s, acc.ID, key, "ok"); n != 1 {
		t.Errorf("ok rows after a successful send = %d, want 1", n)
	}
	if n := countVerificationSends(t, s, acc.ID, key, ""); n != 2 {
		t.Errorf("verification.send rows after a successful send = %d, want 2 (attempted, ok)", n)
	}

	// 2. The audit write fails: no mail goes out, and the answer says so, as a send that failed does.
	execOn(t, s, "CREATE TRIGGER refuse_mail_audit BEFORE INSERT ON audit_log WHEN NEW.action = 'verification.send' "+
		"BEGIN SELECT RAISE(ABORT, 'audit refused'); END")
	bob := decode[userResponse](t, call(t, s, "POST", users, key,
		map[string]any{"email": "bob@example.com", "verification": "email"}))
	if bob.Verification == nil || bob.Verification.Sent == nil || *bob.Verification.Sent {
		t.Errorf("with the audit refused, the answer says the mail was sent: %+v", bob.Verification)
	}
	if len(attemptedAtSend) != 1 {
		t.Errorf("a mail was sent (%d sends in all) although its audit row could not be written", len(attemptedAtSend))
	}
	execOn(t, s, "DROP TRIGGER refuse_mail_audit")

	// 3. The send fails: the attempt is written first, and the result is failed, with no ok row for that mail.
	sendErr = mail.ErrFake
	wantStatus(t, call(t, s, "POST", users, key, map[string]any{"email": "carl@example.com", "verification": "email"}),
		http.StatusCreated)
	if n := countVerificationSends(t, s, acc.ID, key, "failed"); n != 1 {
		t.Errorf("failed rows = %d, want 1", n)
	}
	if n := countVerificationSends(t, s, acc.ID, key, "ok"); n != 1 {
		t.Errorf("ok rows after a failed send = %d, want still 1 (the first mail's)", n)
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
