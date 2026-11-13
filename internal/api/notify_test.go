// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// notifyFixture is a comment fixture with Ann and Bob named, so a mention can find them by name. Ann comments on the file;
// Bob reads it as a viewer; Carol has no access. The mail recorder is the server's mailer.
type notifyFixture struct {
	*commentFixture
	rec *mail.Recorder
	// base is the time the worker's passes run at: after every comment the test writes.
	base time.Time
	// start is how many mails the setup sent (the share mail of the fixture), so a test counts its own mails only.
	start int
}

func newNotifyFixture(t *testing.T) *notifyFixture {
	t.Helper()
	fx := newCommentFixture(t)
	fx.named(t, fx.ann, "Ann Example")
	fx.named(t, fx.bob, "Bob Example")
	// The users are copies taken before the names were set, so the copies get the names too.
	fx.ann.DisplayName = "Ann Example"
	fx.bob.DisplayName = "Bob Example"
	rec, ok := fx.s.mailer.(*mail.Recorder)
	if !ok {
		t.Fatal("the test server does not record mail")
	}
	return &notifyFixture{commentFixture: fx, rec: rec, base: time.Now().Add(time.Minute), start: len(rec.Messages())}
}

// mentionOf posts a comment by sess on the file that names who, found in the people list.
func (fx *notifyFixture) mentionOf(t *testing.T, sess testSession, who userResponse, body string) commentJSON {
	t.Helper()
	items := fx.suggest(t, sess, fx.file, url.QueryEscape(fixtureName(who)))
	if len(items) != 1 {
		t.Fatalf("suggestions for %s = %+v, want one", fixtureName(who), items)
	}
	rec := callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
		map[string]any{"body": body, "mentions": []string{items[0].Ref}})
	wantStatus(t, rec, 201)
	return decode[commentJSON](t, rec)
}

// fixtureName is the display name the fixture gave a user.
func fixtureName(u userResponse) string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Email
}

// pass runs one pass of the worker at at, and returns how many mails went.
func (fx *notifyFixture) pass(t *testing.T, at time.Time) int {
	t.Helper()
	return fx.s.sendNotifications(context.Background(), at)
}

// inbox lists the notifications of sess, as the list route answers them.
func (fx *notifyFixture) inbox(t *testing.T, sess testSession) []notifyJSON {
	t.Helper()
	rec := callSession(t, fx.s, sess, "GET", "/api/v1/notifications", nil)
	wantStatus(t, rec, 200)
	return decode[listResponse[notifyJSON]](t, rec).Items
}

// unread is the bell's count of sess.
func (fx *notifyFixture) unread(t *testing.T, sess testSession) unreadJSON {
	t.Helper()
	rec := callSession(t, fx.s, sess, "GET", "/api/v1/notifications/unread", nil)
	wantStatus(t, rec, 200)
	return decode[unreadJSON](t, rec)
}

// outboxCount counts the mail rows in a state, or all of them for "".
func (fx *notifyFixture) outboxCount(t *testing.T, state string) int {
	t.Helper()
	if state == "" {
		return fx.dbCount(t, "SELECT COUNT(*) FROM notify_outbox")
	}
	return fx.dbCount(t, "SELECT COUNT(*) FROM notify_outbox WHERE state = ?", state)
}

// mark makes the mails sent so far the setup of the test: a permission the test sets later sends share mail, which the
// notification counts leave out.
func (fx *notifyFixture) mark() {
	fx.start = len(fx.rec.Messages())
}

// plain removes the invisible non-joiners a name carries in a mail (mail.Name), so a test compares the text a reader sees.
func plain(s string) string {
	return strings.ReplaceAll(s, "‌", "")
}

// newMessages returns the mails the test sent after its first since mails, and the count of them.
func (fx *notifyFixture) newMessages(since int) ([]mail.Message, int) {
	all := fx.rec.Messages()[fx.start:]
	return all[since:], len(all)
}

// TestMentionMailsWithoutTextAndListsIt: a mention reaches the mentioned person's inbox and mail. The mail names the actor
// and the item and links the item; it carries no comment text. The inbox row carries the item's and the actor's names.
func TestMentionMailsWithoutTextAndListsIt(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.mentionOf(t, fx.annSess, fx.bob, "secret plan text")
	if got := fx.inbox(t, fx.bobSess); len(got) != 1 || got[0].Kind != store.NotifyMention || got[0].ItemName != "file.txt" ||
		got[0].ActorName != "Ann Example" {
		t.Fatalf("bob's inbox = %+v, want one mention of file.txt by Ann", got)
	}
	if n := fx.pass(t, fx.base); n != 1 {
		t.Fatalf("passes sent %d mails, want 1", n)
	}
	msgs, _ := fx.newMessages(0)
	if len(msgs) != 1 || msgs[0].To != fx.bob.Email {
		t.Fatalf("mail = %+v, want one to bob", msgs)
	}
	m := msgs[0]
	if !strings.Contains(plain(m.Subject), "Ann Example") || !strings.Contains(plain(m.Subject), "file.txt") {
		t.Errorf("subject %q does not name the actor and the item", m.Subject)
	}
	if strings.Contains(m.Subject+m.Body, "secret plan text") {
		t.Errorf("mail carries the comment text: %q", m.Body)
	}
	if !strings.Contains(m.Body, "/n/"+fx.file) {
		t.Errorf("mail lacks the item link: %q", m.Body)
	}
	if m.ListUnsubscribe == "" || m.MessageID == "" {
		t.Errorf("mail lacks the unsubscribe address or message ID: %+v", m)
	}
	if n := fx.outboxCount(t, store.OutboxSent); n != 1 {
		t.Errorf("sent rows = %d, want 1", n)
	}
}

// TestAuthorIsNeverNotified: an author who names or answers themself gets no notification and no mail.
func TestAuthorIsNeverNotified(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	first := fx.say(t, fx.annSess, fx.file, "my own note")
	fx.reply(t, fx.annSess, fx.file, first.ID, "and my own answer")
	if got := fx.inbox(t, fx.annSess); len(got) != 0 {
		t.Fatalf("the author's inbox = %+v, want none", got)
	}
	fx.pass(t, fx.base)
	if msgs, _ := fx.newMessages(0); len(msgs) != 0 {
		t.Fatalf("the author was mailed: %+v", msgs)
	}
}

// TestReplyNotifiesTheParentAuthorOnly: a reply goes to the author of the comment it answers, and to nobody else.
func TestReplyNotifiesTheParentAuthorOnly(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.setPerm(t, fx.root, fx.bob.ID, "commenter")
	first := fx.say(t, fx.annSess, fx.file, "a question")
	fx.reply(t, fx.bobSess, fx.file, first.ID, "an answer")
	if got := fx.inbox(t, fx.annSess); len(got) != 1 || got[0].Kind != store.NotifyReply {
		t.Fatalf("ann's inbox = %+v, want one reply", got)
	}
	if got := fx.inbox(t, fx.bobSess); len(got) != 0 {
		t.Fatalf("bob, who wrote the reply, has %+v", got)
	}
}

// TestLosingReachHidesAndSkipsMailThenShowsAgain: a person who loses reach gets no mail and sees no text in the inbox. The
// notification is kept, and shows again when the reach comes back.
func TestLosingReachHidesAndSkipsMailThenShowsAgain(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.mentionOf(t, fx.annSess, fx.bob, "look at this")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.root+"/permissions/"+fx.bob.ID, fx.key, nil), 204)
	if got := fx.inbox(t, fx.bobSess); len(got) != 0 {
		t.Fatalf("bob sees %+v after losing reach", got)
	}
	if u := fx.unread(t, fx.bobSess); u.Count != 0 {
		t.Fatalf("bob's bell counts %d after losing reach", u.Count)
	}
	if rec := callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/notifications", nil); strings.Contains(rec.Body.String(), "file.txt") {
		t.Fatal("the item name leaked to a reader who lost reach")
	}
	fx.pass(t, fx.base)
	if msgs, _ := fx.newMessages(0); len(msgs) != 0 {
		t.Fatalf("bob was mailed after losing reach: %+v", msgs)
	}
	if n := fx.outboxCount(t, store.OutboxSkipped); n != 1 {
		t.Fatalf("skipped mail rows = %d, want 1", n)
	}
	fx.setPerm(t, fx.root, fx.bob.ID, "viewer")
	if got := fx.inbox(t, fx.bobSess); len(got) != 1 || got[0].ItemName != "file.txt" {
		t.Fatalf("after reach returns bob sees %+v, want the kept notification", got)
	}
}

// TestRenamedItemShowsItsCurrentName: the name in a mail is read at send time, so a file renamed before the send is named
// by its new name.
func TestRenamedItemShowsItsCurrentName(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.mentionOf(t, fx.annSess, fx.bob, "rename me")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.file, fx.key, map[string]any{"name": "renamed.txt"}), 200)
	fx.pass(t, fx.base)
	msgs, _ := fx.newMessages(0)
	if len(msgs) != 1 || !strings.Contains(plain(msgs[0].Subject), "renamed.txt") || strings.Contains(plain(msgs[0].Subject), "file.txt") {
		t.Fatalf("mail = %+v, want the new name only", msgs)
	}
}

// TestDeletedCommentSendsNothing: a comment deleted before the send takes its notification and mail with it. A tombstone
// (a deleted comment with replies) is left out of the inbox and skipped by the worker.
func TestDeletedCommentSendsNothing(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	gone := fx.mentionOf(t, fx.annSess, fx.bob, "delete me")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/comments/"+gone.ID, nil), 204)
	if n := fx.outboxCount(t, ""); n != 0 {
		t.Fatalf("mail rows after the comment went = %d, want 0", n)
	}
	// Bob mentions Ann in a live comment, and Bob answers Ann's thread. Ann's own tombstoned comment has a reply by Bob:
	// deleting it leaves a tombstone, whose reply notification to Ann is skipped.
	fx.setPerm(t, fx.root, fx.bob.ID, "commenter")
	fx.mark()
	fx.mentionOf(t, fx.bobSess, fx.ann, "live mention")
	kept := fx.say(t, fx.annSess, fx.file, "soon a tombstone")
	fx.reply(t, fx.bobSess, fx.file, kept.ID, "a reply that outlives its parent")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/comments/"+kept.ID, nil), 204)
	fx.pass(t, fx.base)
	// Ann gets one digest of two: her mention, and the reply. The reply is a live comment, so its notice is sent; the
	// parent is a tombstone, and its text is in no mail. Bob gets nothing, since he wrote the comments.
	msgs, _ := fx.newMessages(0)
	if len(msgs) != 1 || msgs[0].To != fx.ann.Email || !strings.Contains(msgs[0].Subject, "2 new") {
		t.Fatalf("mails = %+v, want one digest of two to Ann", msgs)
	}
	if strings.Contains(msgs[0].Subject+msgs[0].Body, "soon a tombstone") || strings.Contains(msgs[0].Body, "delete me") {
		t.Fatalf("a deleted comment's text appears in the mail: %+v", msgs[0])
	}
}

// TestEmailSwitchAndDisabledUserSendNothing: a user who turned email off gets no mail and keeps the inbox. A disabled user
// gets neither.
func TestEmailSwitchAndDisabledUserSendNothing(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "PATCH", "/api/v1/notifications/settings", map[string]any{"email": false}), 200)
	fx.mentionOf(t, fx.annSess, fx.bob, "quiet please")
	fx.pass(t, fx.base)
	if msgs, _ := fx.newMessages(0); len(msgs) != 0 {
		t.Fatalf("bob with email off was mailed: %+v", msgs)
	}
	if got := fx.inbox(t, fx.bobSess); len(got) != 1 {
		t.Fatalf("email off hid the inbox: %+v", got)
	}
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "PATCH", "/api/v1/notifications/settings", map[string]any{"email": true}), 200)
	fx.mentionOf(t, fx.annSess, fx.bob, "again")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key,
		map[string]any{"status": "disabled"}), 200)
	fx.pass(t, fx.base.Add(3*time.Hour))
	if msgs, _ := fx.newMessages(0); len(msgs) != 0 {
		t.Fatalf("a disabled user was mailed: %+v", msgs)
	}
	// The row is skipped for the status, the first check: the reach walk would also refuse a disabled user, so without this
	// reason the status check could be removed and nothing would show it.
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM notify_outbox WHERE user_id = ? AND state = ? AND last_error = ?",
		fx.bob.ID, store.OutboxSkipped, notifyReasonUser); n != 1 {
		t.Fatalf("the disabled user's row is not skipped for the status")
	}
}

// TestDigestIsOneMailAnHour: several notifications in one hour make one digest; the next mail waits for the hour.
func TestDigestIsOneMailAnHour(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	for _, body := range []string{"one", "two", "three"} {
		fx.mentionOf(t, fx.annSess, fx.bob, body)
	}
	if n := fx.pass(t, fx.base); n != 1 {
		t.Fatalf("first pass sent %d mails, want one digest", n)
	}
	msgs, _ := fx.newMessages(0)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Subject, "3 new mentions") {
		t.Fatalf("digest = %+v, want one that counts three", msgs)
	}
	fx.mentionOf(t, fx.annSess, fx.bob, "four")
	if n := fx.pass(t, fx.base.Add(10*time.Minute)); n != 0 {
		t.Fatalf("a pass inside the hour sent %d mails", n)
	}
	if n := fx.pass(t, fx.base.Add(61*time.Minute)); n != 1 {
		t.Fatalf("a pass after the hour sent %d mails, want 1", n)
	}
	if msgs, _ := fx.newMessages(1); len(msgs) != 1 {
		t.Fatalf("the later mail = %d, want 1", len(msgs))
	}
}

// TestDailyCapOfTwentyMails: a user gets at most twenty mails in a rolling day. The twenty-first waits for the day.
func TestDailyCapOfTwentyMails(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	for i := 0; i < store.NotifyDayCap+1; i++ {
		fx.mentionOf(t, fx.annSess, fx.bob, "mention")
		fx.pass(t, fx.base.Add(time.Duration(i)*time.Hour))
	}
	if _, n := fx.newMessages(0); n != store.NotifyDayCap {
		t.Fatalf("mails in the day = %d, want %d", n, store.NotifyDayCap)
	}
	if n := fx.pass(t, fx.base.Add(25*time.Hour)); n != 1 {
		t.Fatalf("the waiting mail after the day sent %d, want 1", n)
	}
}

// TestFailingMailServerBacksOffThenGivesUp: a refused mail is tried again after the backoff, and after eight refusals the
// row is failed. Nothing is lost on the way: the notification stays in the inbox.
func TestFailingMailServerBacksOffThenGivesUp(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.mentionOf(t, fx.annSess, fx.bob, "refused")
	fx.rec.Fail = true
	at := fx.base
	fx.pass(t, at)
	if n := fx.dbCount(t, "SELECT attempts FROM notify_outbox WHERE state = 'pending'"); n != 1 {
		t.Fatalf("attempts after one refusal = %d, want 1", n)
	}
	for i := 1; i < notifyMaxAttempts; i++ {
		at = at.Add(3 * time.Hour)
		fx.pass(t, at)
	}
	if n := fx.outboxCount(t, store.OutboxFailed); n != 1 {
		t.Fatalf("failed rows after %d refusals = %d, want 1", notifyMaxAttempts, n)
	}
	if got := fx.inbox(t, fx.bobSess); len(got) != 1 {
		t.Fatalf("a refused mail removed the inbox row: %+v", got)
	}
}

// TestFailingServerStopsThePassAndLeavesTheRest: a refusing mail server stops a pass after notifyStopAfter refusals, so the
// other users wait a tick with no attempt counted.
func TestFailingServerStopsThePassAndLeavesTheRest(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	users := []userResponse{fx.bob}
	sessions := []testSession{fx.bobSess}
	for _, email := range []string{"dan@example.com", "eve@example.com", "fay@example.com"} {
		u, sess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, email)
		fx.setPerm(t, fx.root, u.ID, "viewer")
		users = append(users, u)
		sessions = append(sessions, sess)
	}
	for i, u := range users {
		name := "Person " + string(rune('A'+i))
		fx.named(t, u, name)
		u.DisplayName = name
		fx.mentionOf(t, fx.annSess, u, "hello "+string(rune('A'+i)))
	}
	fx.rec.Fail = true
	fx.pass(t, fx.base)
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM notify_outbox WHERE attempts = 1"); n != notifyStopAfter {
		t.Fatalf("rows tried = %d, want %d (the pass stops after %d refusals)", n, notifyStopAfter, notifyStopAfter)
	}
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM notify_outbox WHERE state = 'pending' AND attempts = 0"); n != len(users)-notifyStopAfter {
		t.Fatalf("rows left for the next pass = %d, want %d", n, len(users)-notifyStopAfter)
	}
}

// TestTwoPassesSendOneMail: two workers on one database, running at once, send each row once.
func TestTwoPassesSendOneMail(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.mentionOf(t, fx.annSess, fx.bob, "once")
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fx.pass(t, fx.base)
		}()
	}
	wg.Wait()
	if _, n := fx.newMessages(0); n != 1 {
		t.Fatalf("two passes sent %d mails, want 1", n)
	}
}

// TestNoMailConfiguredQueuesNothing: with no mail configured, the inbox still works, nothing is queued, and a pass sends
// nothing.
func TestNoMailConfiguredQueuesNothing(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.s.mailer = nil
	fx.mentionOf(t, fx.annSess, fx.bob, "no mail here")
	if got := fx.inbox(t, fx.bobSess); len(got) != 1 {
		t.Fatalf("inbox without mail = %+v, want one", got)
	}
	if n := fx.outboxCount(t, ""); n != 0 {
		t.Fatalf("mail rows without mail configured = %d, want 0", n)
	}
	if n := fx.pass(t, fx.base); n != 0 {
		t.Fatalf("a pass with no mail sent %d", n)
	}
}

// TestNotificationRoutesRefuseTokensAndKeys: the inbox, the bell, the mark-read and the email switch are the signed-in
// user's own. A read token, a write token and an account key are refused on each.
func TestNotificationRoutesRefuseTokensAndKeys(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.mentionOf(t, fx.annSess, fx.bob, "for bob")
	read := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.bob.ID, "folders": []string{fx.fld.ID}, "permissions": "read"})
	write := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.bob.ID, "folders": []string{fx.fld.ID}, "permissions": "write"})
	for _, cred := range []string{read, write, fx.key} {
		for _, c := range []struct {
			method, path string
			body         any
		}{
			{"GET", "/api/v1/notifications", nil},
			{"GET", "/api/v1/notifications/unread", nil},
			{"POST", "/api/v1/notifications/ntf_none/read", nil},
			{"GET", "/api/v1/notifications/settings", nil},
			{"PATCH", "/api/v1/notifications/settings", map[string]any{"email": false}},
		} {
			rec := call(t, fx.s, c.method, c.path, cred, c.body)
			if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s with a token or key = %d, want refused", c.method, c.path, rec.Code)
			}
		}
	}
	on, err := fx.s.store.NotifyEmailOn(context.Background(), fx.bob.ID)
	if err != nil || !on {
		t.Fatalf("a refused switch change went through: on=%v err=%v", on, err)
	}
}

// TestMarkReadIsOwnRowsOnly: a user marks their own notification read. Another user's is not found, and the bell follows.
func TestMarkReadIsOwnRowsOnly(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.setPerm(t, fx.root, fx.bob.ID, "commenter")
	parent := fx.say(t, fx.annSess, fx.file, "question")
	fx.reply(t, fx.bobSess, fx.file, parent.ID, "answer")
	ann := fx.inbox(t, fx.annSess)
	if len(ann) != 1 {
		t.Fatalf("ann's inbox = %+v, want one", ann)
	}
	wantError(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/notifications/"+ann[0].ID+"/read", nil),
		http.StatusNotFound, "not_found")
	if u := fx.unread(t, fx.annSess); u.Count != 1 {
		t.Fatalf("ann's bell after bob's attempt = %d, want 1", u.Count)
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/notifications/"+ann[0].ID+"/read", nil), 204)
	if u := fx.unread(t, fx.annSess); u.Count != 0 {
		t.Fatalf("ann's bell after marking read = %d, want 0", u.Count)
	}
}

// TestUnsubscribeLinkOfAMail: the link of a mail opens a page that changes nothing. Its POST, from the page or a mail client,
// switches off the email of the person the token names, and no one else's. A changed token is refused.
func TestUnsubscribeLinkOfAMail(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.setPerm(t, fx.root, fx.bob.ID, "commenter")
	fx.mark()
	fx.mentionOf(t, fx.annSess, fx.bob, "for bob")
	fx.mentionOf(t, fx.bobSess, fx.ann, "for ann")
	fx.pass(t, fx.base)
	msgs, _ := fx.newMessages(0)
	if len(msgs) != 2 {
		t.Fatalf("mails = %d, want 2", len(msgs))
	}
	var bobLink string
	for _, m := range msgs {
		if m.To == fx.bob.Email {
			bobLink = m.ListUnsubscribe
		}
	}
	u, err := url.Parse(bobLink)
	if err != nil {
		t.Fatal(err)
	}
	path := u.Path + "?t=" + url.QueryEscape(u.Query().Get("t"))
	get := call(t, fx.s, "GET", path, "", nil)
	wantStatus(t, get, 200)
	if !strings.Contains(get.Body.String(), "<form") || strings.Contains(get.Body.String(), fx.bob.Email) {
		t.Fatalf("the page is not a confirmation without the address: %q", get.Body.String())
	}
	if on, _ := fx.s.store.NotifyEmailOn(context.Background(), fx.bob.ID); !on {
		t.Fatal("the GET switched the email off")
	}
	// The forgery changes one character of the nonce. The last characters of unpadded base64 carry bits the decoder ignores,
	// so changing those could leave the sealed bytes as they were.
	at := strings.Index(path, "?t="+notifyUnsubPrefix) + len("?t="+notifyUnsubPrefix) + 2
	swap := "A"
	if path[at] == 'A' {
		swap = "B"
	}
	forged := path[:at] + swap + path[at+1:]
	wantStatus(t, call(t, fx.s, "POST", forged, "", nil), 400)
	wantStatus(t, call(t, fx.s, "POST", path, "", nil), 200)
	if on, _ := fx.s.store.NotifyEmailOn(context.Background(), fx.bob.ID); on {
		t.Fatal("the POST did not switch the email off")
	}
	if on, _ := fx.s.store.NotifyEmailOn(context.Background(), fx.ann.ID); !on {
		t.Fatal("bob's link switched ann's email off")
	}
	fx.mentionOf(t, fx.annSess, fx.bob, "after")
	fx.pass(t, fx.base.Add(3*time.Hour))
	if got, _ := fx.newMessages(2); len(got) != 0 {
		t.Fatalf("bob was mailed after unsubscribing: %+v", got)
	}
	wantStatus(t, call(t, fx.s, "GET", "/api/v1/notifications", bobLinkTok(bobLink), nil), 401)
}

// bobLinkTok returns the token of an unsubscribe address.
func bobLinkTok(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return u.Query().Get("t")
}

// TestUnsubscribeWorksForADisabledOrGoneUser: the link works for a disabled user, and answers the same for a user who is gone
// as for one who is there, so the answer does not say whether the person exists.
func TestUnsubscribeWorksForADisabledOrGoneUser(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.mentionOf(t, fx.annSess, fx.bob, "for bob")
	fx.pass(t, fx.base)
	msgs, _ := fx.newMessages(0)
	u, err := url.Parse(msgs[0].ListUnsubscribe)
	if err != nil {
		t.Fatal(err)
	}
	path := u.Path + "?t=" + url.QueryEscape(u.Query().Get("t"))
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key,
		map[string]any{"status": "disabled"}), 200)
	wantStatus(t, call(t, fx.s, "POST", path, "", nil), 200)
	if on, _ := fx.s.store.NotifyEmailOn(context.Background(), fx.bob.ID); on {
		t.Fatal("a disabled user could not switch the email off")
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "POST", path, "", nil), 200)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/notifications/unsubscribe?t="+url.QueryEscape("nsu_AAAA"), "", nil), 400)
}

// TestUnsubscribeTokenOpensNothingElse: an unsubscribe token is not a credential. Sent as a bearer token it is refused.
func TestUnsubscribeTokenOpensNothingElse(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.mentionOf(t, fx.annSess, fx.bob, "for bob")
	fx.pass(t, fx.base)
	msgs, _ := fx.newMessages(0)
	if len(msgs) != 1 {
		t.Fatalf("mails = %d, want 1", len(msgs))
	}
	tok := bobLinkTok(msgs[0].ListUnsubscribe)
	if tok == "" {
		t.Fatal("no token in the unsubscribe address")
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/notifications"},
		{"GET", "/api/v1/notifications/settings"},
		{"GET", "/api/v1/nodes/" + fx.file},
	} {
		rec := call(t, fx.s, c.method, c.path, tok, nil)
		if rec.Code == http.StatusOK {
			t.Errorf("%s %s opened with an unsubscribe token", c.method, c.path)
		}
	}
}

// TestNotifyLogsCarryNoTextOrAddress: a refused mail is logged without the comment text, the name or the address.
func TestNotifyLogsCarryNoTextOrAddress(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	var buf bytes.Buffer
	fx.s.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fx.mentionOf(t, fx.annSess, fx.bob, "private words in a comment")
	fx.rec.Fail = true
	fx.pass(t, fx.base)
	out := buf.String()
	for _, bad := range []string{"private words", fx.bob.Email, "Bob Example", "Ann Example", "file.txt"} {
		if strings.Contains(out, bad) {
			t.Errorf("a log line carries %q: %s", bad, out)
		}
	}
}

// TestTombstonedMentionIsNotMailed: a mention notifies on the comment that names the person. When that comment is deleted
// and has a reply, it becomes a tombstone, and the mention is skipped: its text is gone for every reader.
func TestTombstonedMentionIsNotMailed(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.setPerm(t, fx.root, fx.bob.ID, "commenter")
	fx.mark()
	named := fx.mentionOf(t, fx.annSess, fx.bob, "you, Bob, see below")
	fx.reply(t, fx.bobSess, fx.file, named.ID, "answering the mention")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/comments/"+named.ID, nil), 204)
	fx.pass(t, fx.base)
	msgs, _ := fx.newMessages(0)
	for _, m := range msgs {
		if m.To == fx.bob.Email {
			t.Fatalf("bob was mailed about a mention whose comment is a tombstone: %+v", m)
		}
	}
	if got := fx.inbox(t, fx.bobSess); len(got) != 0 {
		t.Fatalf("bob's inbox shows the mention of a tombstone: %+v", got)
	}
}

// TestOnePassChecksReachPerUser: two users with mail on one node, sent in one pass. Only the one with access is mailed,
// whichever of the two the pass reaches first. The node's reach is checked for each user, not once for the node.
func TestOnePassChecksReachPerUser(t *testing.T) {
	t.Parallel()
	for _, carolFirst := range []bool{true, false} {
		name := "bobFirst"
		if carolFirst {
			name = "carolFirst"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fx := newNotifyFixture(t)
			fx.named(t, fx.carol, "Carol Example")
			fx.carol.DisplayName = "Carol Example"
			fx.setPerm(t, fx.root, fx.carol.ID, "commenter")
			first, second := fx.bob, fx.carol
			if carolFirst {
				first, second = fx.carol, fx.bob
			}
			fx.mentionOf(t, fx.annSess, first, "first")
			// Rows are claimed by their due time, so the first mention is due first.
			time.Sleep(5 * time.Millisecond)
			fx.mentionOf(t, fx.annSess, second, "second")
			// Dan has access and is then disabled: a disabled user is skipped for his status, not mailed by a cached reach.
			dan, _ := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
			fx.named(t, dan, "Dan Example")
			dan.DisplayName = "Dan Example"
			fx.setPerm(t, fx.root, dan.ID, "commenter")
			time.Sleep(5 * time.Millisecond)
			fx.mentionOf(t, fx.annSess, dan, "third")
			// Carol loses access before the send; Bob keeps his; Dan is disabled.
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.root+"/permissions/"+fx.carol.ID, fx.key, nil), 204)
			fx.dbExec(t, "UPDATE users SET status = ? WHERE id = ?", store.UserDisabled, dan.ID)
			fx.mark()
			if n := fx.pass(t, fx.base); n != 1 {
				t.Fatalf("passes sent %d mails, want 1 (bob only)", n)
			}
			if n := fx.dbCount(t, "SELECT COUNT(*) FROM notify_outbox WHERE user_id = ? AND state = ? AND last_error = ?",
				dan.ID, store.OutboxSkipped, notifyReasonUser); n != 1 {
				t.Fatalf("disabled dan's row is not skipped for his status")
			}
			msgs, _ := fx.newMessages(0)
			if len(msgs) != 1 || msgs[0].To != fx.bob.Email {
				t.Fatalf("mail = %+v, want one to bob and none to carol", msgs)
			}
			if n := fx.dbCount(t, "SELECT COUNT(*) FROM notify_outbox WHERE user_id = ? AND state = ? AND last_error = ?",
				fx.carol.ID, store.OutboxSkipped, notifyReasonReach); n != 1 {
				t.Fatalf("carol's row is not skipped with the reach reason")
			}
			if n := fx.dbCount(t, "SELECT COUNT(*) FROM notify_outbox WHERE user_id = ? AND state = ?",
				fx.bob.ID, store.OutboxSent); n != 1 {
				t.Fatalf("bob's row is not sent")
			}
		})
	}
}

// dbExec runs one statement on the test database, for a state no route sets.
func (fx *notifyFixture) dbExec(t *testing.T, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", fx.s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// setComments turns the account's comment switch on or off, as an owner would.
func (fx *notifyFixture) setComments(t *testing.T, on bool) {
	t.Helper()
	ctx := context.Background()
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.SetCommentsOn(ctx, fx.acc.ID, on, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
}

// TestActorWithoutNameIsLabelled: a person with no display name is named by a label in the mail, never by the address, and
// the inbox shows no name, as the comment page shows none.
func TestActorWithoutNameIsLabelled(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.dbExec(t, "UPDATE users SET display_name = '' WHERE id = ?", fx.ann.ID)
	fx.mentionOf(t, fx.annSess, fx.bob, "hello")
	got := fx.inbox(t, fx.bobSess)
	if len(got) != 1 || got[0].ActorName != "" || got[0].ActorGone {
		t.Fatalf("bob's inbox = %+v, want one item with no actor name and not gone", got)
	}
	fx.pass(t, fx.base)
	msgs, _ := fx.newMessages(0)
	if len(msgs) != 1 {
		t.Fatalf("mail = %+v, want one", msgs)
	}
	if !strings.Contains(plain(msgs[0].Subject), "Someone mentioned you") {
		t.Fatalf("subject %q does not carry the label", msgs[0].Subject)
	}
	for _, text := range []string{msgs[0].Subject, msgs[0].Body} {
		if strings.Contains(text, fx.ann.Email) {
			t.Fatalf("the mail shows the actor's address: %q", text)
		}
	}
}

// TestPersonNameFollowsTheRule: a name is given only to a caller the rule admits (a member or a key of the account); any
// other caller gets "", and so does a person with no name.
func TestPersonNameFollowsTheRule(t *testing.T) {
	t.Parallel()
	names := map[string]string{"usr_x": "Xena"}
	member := &auth.Principal{Kind: auth.KindUser, ID: "usr_y", AccountID: "acc_1", UserID: "usr_y"}
	link := &auth.Principal{Kind: auth.KindShareLink, AccountID: "acc_1"}
	if got := personName(member, "acc_1", names, "usr_x"); got != "Xena" {
		t.Fatalf("member sees %q, want the name", got)
	}
	if got := personName(member, "acc_2", names, "usr_x"); got != "" {
		t.Fatalf("a member of another account sees %q", got)
	}
	if got := personName(link, "acc_1", names, "usr_x"); got != "" {
		t.Fatalf("a link visitor sees %q", got)
	}
	if got := personName(member, "acc_1", names, "usr_z"); got != "" {
		t.Fatalf("a person with no name shows %q", got)
	}
}

// TestTransientReadErrorKeepsTheRow: a read that fails while a row is checked does not skip it. The row waits, with no
// attempt counted, and is sent by a later pass. The context is cancelled, as a shutdown does mid-pass.
func TestTransientReadErrorKeepsTheRow(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.mentionOf(t, fx.annSess, fx.bob, "keep me")
	ctx := context.Background()
	jobs, err := fx.s.store.ClaimMail(ctx, fx.base, notifyLease, notifyUsers, notifyRowsPerUser)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim = %+v, %v; want one row", jobs, err)
	}
	u, err := fx.s.store.User(ctx, fx.acc.ID, fx.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	pass := &notifyPass{users: map[string]*store.User{u.ID: u}, actors: map[string]*store.User{},
		reach: map[string]bool{}}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	fx.mark()
	fx.s.sendUserMail(cancelled, pass, fx.base, jobs)
	if n := fx.outboxCount(t, store.OutboxSkipped); n != 0 {
		t.Fatalf("a failed read skipped %d rows", n)
	}
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM notify_outbox WHERE state = ? AND attempts = 0 AND lease_until IS NULL",
		store.OutboxPending); n != 1 {
		t.Fatalf("the row is not back pending with no attempt counted")
	}
	if n := fx.pass(t, fx.base); n != 1 {
		t.Fatalf("a later pass sent %d mails, want the row", n)
	}
}

// TestCommentsOffHoldsMailAndHidesTheInbox: while an account's comments are off, its rows are kept, and no mail goes and
// the inbox shows nothing. When they are back on, a row still young enough is sent.
func TestCommentsOffHoldsMailAndHidesTheInbox(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.mentionOf(t, fx.annSess, fx.bob, "held")
	fx.setComments(t, false)
	if got := fx.inbox(t, fx.bobSess); len(got) != 0 {
		t.Fatalf("the inbox shows %+v while comments are off", got)
	}
	if u := fx.unread(t, fx.bobSess); u.Count != 0 {
		t.Fatalf("the bell counts %d while comments are off", u.Count)
	}
	fx.mark()
	if n := fx.pass(t, fx.base); n != 0 {
		t.Fatalf("a pass sent %d mails while comments are off", n)
	}
	if n := fx.outboxCount(t, store.OutboxPending); n != 1 {
		t.Fatalf("held rows = %d, want the row kept pending", n)
	}
	fx.setComments(t, true)
	if got := fx.inbox(t, fx.bobSess); len(got) != 1 {
		t.Fatalf("the inbox shows %d items after comments are back on, want the kept one", len(got))
	}
	if n := fx.pass(t, fx.base.Add(notifyHold+time.Minute)); n != 1 {
		t.Fatalf("after comments are back on a pass sent %d mails, want the held row", n)
	}
}

// TestOldRowsAreNotMailedWhenCommentsReturn: a row older than notifyMailMaxAge is skipped as late when its account's
// comments come back on, so a long switch-off does not flood the reader.
func TestOldRowsAreNotMailedWhenCommentsReturn(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	fx.mentionOf(t, fx.annSess, fx.bob, "long ago")
	fx.setComments(t, false)
	fx.pass(t, fx.base)
	fx.setComments(t, true)
	fx.mark()
	fx.pass(t, fx.base.Add(notifyMailMaxAge+time.Hour))
	if msgs, _ := fx.newMessages(0); len(msgs) != 0 {
		t.Fatalf("an old row was mailed: %+v", msgs)
	}
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM notify_outbox WHERE state = ? AND last_error = ?",
		store.OutboxSkipped, notifyReasonLate); n != 1 {
		t.Fatalf("the old row is not skipped as late")
	}
}
