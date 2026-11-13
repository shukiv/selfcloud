// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Notifications for mentions and replies (design section 14, slice r, decision 43). A comment writes its notifications
// and their outbox rows in its own write (commentNotices). One worker sends the mail, a minute at a time (runNotifications),
// and the in-app routes read the notifications of the signed-in user, with the reach of each item checked at read time.
//
// The rules the worker keeps: a user gets at most one mail an hour and NotifyDayCap in a rolling day, charged in the
// database; an account sends at most NotifyAccountHourCap users' mail an hour; every row is checked again at send time
// (the user is active and has email on, the comment is live, and the user still reaches the node), and a row that fails
// a check is skipped with no text built. A mail carries names and a link, never a comment's text.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/webhook"
)

const (
	// notifyTick is how often the worker looks for due mail.
	notifyTick = time.Minute
	// notifyUsers is how many users one pass claims. notifyRowsPerUser is how many notifications one mail covers at most;
	// the rest wait for a later mail, which the hour makes at least an hour away.
	notifyUsers       = 50
	notifyRowsPerUser = 20
	// notifyLease is how long a pass holds the rows it claimed. A pass that dies loses them after this, and they are sent
	// again (at-least-once, design section 14).
	notifyLease = 2 * time.Minute
	// notifyMaxAttempts is how many failed sends give a row up (failed), as the webhook worker does.
	notifyMaxAttempts = 8
	// notifyStopAfter is how many failed sends one pass makes before it stops: a mail server that is down is not tried
	// for every user in turn, so the other accounts wait a tick, not the length of the timeouts.
	notifyStopAfter = 3
	// notifyUnreadScan is how many unread notifications the bell reads and checks for reach. The count is at most this.
	notifyUnreadScan = 100
	// notifyDigestEntries is how many notifications a digest names; the rest are counted.
	notifyDigestEntries = 5
	// notifyMailMaxAge is how old a queued row may be and still be mailed. Older rows are skipped as late, so a comment
	// switch that comes back on does not send a flood of old mail.
	notifyMailMaxAge = 48 * time.Hour
	// notifyHold is how long a row waits while its account's comments are off. It is kept, not sent, and not skipped.
	notifyHold = 10 * time.Minute
)

// Reasons a row is skipped or put back. Each is a fixed code, stored in the outbox's last_error, never a server's text.
const (
	notifyReasonUser    = "user"
	notifyReasonPref    = "pref"
	notifyReasonComment = "comment"
	notifyReasonReach   = "reach"
	notifyReasonActor   = "actor"
	notifyReasonSMTP    = "smtp"
	notifyReasonRender  = "render"
	notifyReasonCharge  = "charge"
	notifyReasonLate    = "late"
)

// notifyHoldComments is what checkNotice returns for a row whose account has comments off: the row waits (it is not
// skipped), so it is sent if the switch comes back on within notifyMailMaxAge.
const notifyHoldComments = "hold"

// notifyJSON is one notification as its reader lists it: the kind, the item's name, the actor's name, and the times. A
// deleted actor shows actor_gone with no name. No comment text is in it.
type notifyJSON struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	NodeID    string     `json:"node_id"`
	ItemName  string     `json:"item_name"`
	ActorName string     `json:"actor_name,omitempty"`
	ActorGone bool       `json:"actor_gone,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
}

// commentNotices writes the notifications of a new comment, in its write. The mentions are the people the comment named,
// already resolved to people who reach the node and are not the author (resolveMentions). A reply notifies the author of
// its parent, when that is a person other than the writer who still reaches the node. A person who is both mentioned and
// the parent's author gets the mention only: one notification per person and comment. The rows are the comment's own,
// so at most one plus ten notifications are written per comment, and each reach check is one walk.
func (s *Server) commentNotices(ctx context.Context, tx *store.Tx, accountID, nodeID string, c *store.Comment,
	mentioned []string, parent *store.Comment) error {
	rows := make([]store.NewNotification, 0, len(mentioned)+1)
	seen := map[string]bool{}
	notice := func(user, kind string) store.NewNotification {
		return store.NewNotification{ID: id.New("ntf"), AccountID: accountID, UserID: user, Kind: kind, NodeID: nodeID,
			CommentID: c.ID, ActorID: c.AuthorID, At: c.CreatedAt}
	}
	for _, u := range mentioned {
		seen[u] = true
		rows = append(rows, notice(u, store.NotifyMention))
	}
	if parent != nil && parent.AuthorID != "" && parent.AuthorID != c.AuthorID && !seen[parent.AuthorID] {
		ok, err := reachesNode(ctx, tx, accountID, nodeID, parent.AuthorID)
		if err != nil {
			return err
		}
		if ok {
			rows = append(rows, notice(parent.AuthorID, store.NotifyReply))
		}
	}
	return tx.InsertNotifications(ctx, rows, s.mailer != nil)
}

// runNotifications runs sendNotifications every notifyTick until ctx ends.
func (s *Server) runNotifications(ctx context.Context) {
	t := time.NewTicker(notifyTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.sendNotifications(ctx, now)
		}
	}
}

// notifyPass is what one pass knows about the users it sends to. Each map is keyed by everything the answer depends on:
// users by user, actors by account and actor, and reach by user and node. Reach is the answer for one user; a node
// shared by two users of a pass has a different answer for each.
type notifyPass struct {
	users  map[string]*store.User
	actors map[string]*store.User
	reach  map[string]bool
}

// passKey joins the parts of a pass key. The parts are IDs, which contain no NUL byte.
func passKey(parts ...string) string {
	return strings.Join(parts, "\x00")
}

// sendNotifications is one pass of the worker at now. It claims the due rows of up to notifyUsers users, and sends each
// user's rows as one mail, or as a digest. It returns how many mails went. With no mail configured it does nothing; its
// rows wait until the sweep expires them.
func (s *Server) sendNotifications(ctx context.Context, now time.Time) int {
	if s.mailer == nil || ctx.Err() != nil {
		return 0
	}
	jobs, err := s.store.ClaimMail(ctx, now, notifyLease, notifyUsers, notifyRowsPerUser)
	if err != nil {
		s.log.Warn("cannot claim the notification mail", "error", err)
		return 0
	}
	pass := &notifyPass{users: map[string]*store.User{}, actors: map[string]*store.User{}, reach: map[string]bool{}}
	sent, failed := 0, 0
	for i := 0; i < len(jobs); {
		j := i
		for j < len(jobs) && jobs[j].UserID == jobs[i].UserID {
			j++
		}
		group := jobs[i:j]
		i = j
		if failed >= notifyStopAfter || ctx.Err() != nil {
			// The rest wait for the next pass, at once, with no attempt counted.
			for _, g := range group {
				s.finishMail(ctx, store.NotifyOutcome{OutboxID: g.OutboxID, Lease: g.Lease, At: now, NextAt: now})
			}
			continue
		}
		switch s.sendUserMail(ctx, pass, now, group) {
		case mailSent:
			sent++
		case mailFailed:
			failed++
		}
	}
	return sent
}

// mailOutcome is what sendUserMail did with a user's rows.
type mailOutcome int

const (
	mailNone   mailOutcome = iota // no row was sent: every row was skipped, or put back
	mailSent                      // the mail went
	mailFailed                    // the mail server refused the mail
)

// sendUserMail checks the rows of one user, charges the user's budget for one mail, and sends what is left as one mail.
func (s *Server) sendUserMail(ctx context.Context, pass *notifyPass, now time.Time, group []store.MailJob) mailOutcome {
	userID, accountID := group[0].UserID, group[0].AccountID
	u, err := s.notifyUser(ctx, pass, accountID, userID)
	if err != nil && err != store.ErrNotFound {
		s.log.Warn("cannot read the user of a notification", "error", err)
		for _, g := range group {
			s.finishMail(ctx, store.NotifyOutcome{OutboxID: g.OutboxID, Lease: g.Lease, At: now, NextAt: now})
		}
		return mailNone
	}
	var entries []mail.Entry
	var live []store.MailJob
	for _, g := range group {
		if u == nil {
			// The user is gone: nothing of it may be sent.
			s.finishMail(ctx, store.NotifyOutcome{OutboxID: g.OutboxID, Lease: g.Lease, State: store.OutboxSkipped,
				Reason: notifyReasonUser, At: now})
			continue
		}
		entry, reason, err := s.checkNotice(ctx, pass, u, g, now)
		if err != nil {
			// A failed read says nothing about the row. It waits for the next pass, and no attempt is counted.
			s.log.Warn("cannot check a notification mail", "error", err)
			s.finishMail(ctx, store.NotifyOutcome{OutboxID: g.OutboxID, Lease: g.Lease, At: now, NextAt: now})
			continue
		}
		if reason == notifyHoldComments {
			s.finishMail(ctx, store.NotifyOutcome{OutboxID: g.OutboxID, Lease: g.Lease, At: now, NextAt: now.Add(notifyHold)})
			continue
		}
		if reason != "" {
			s.finishMail(ctx, store.NotifyOutcome{OutboxID: g.OutboxID, Lease: g.Lease, State: store.OutboxSkipped,
				Reason: reason, At: now})
			continue
		}
		live = append(live, g)
		entries = append(entries, entry)
	}
	if len(live) == 0 {
		return mailNone
	}
	ok, reason, retry, err := s.store.ChargeMail(ctx, accountID, userID, now)
	if err != nil || !ok {
		if err != nil {
			s.log.Warn("cannot charge a notification mail", "error", err)
		}
		next := retry
		if err != nil || next.IsZero() {
			next = now.Add(notifyTick)
		}
		for _, g := range live {
			s.finishMail(ctx, store.NotifyOutcome{OutboxID: g.OutboxID, Lease: g.Lease, At: now, NextAt: next,
				Reason: reason})
		}
		return mailNone
	}
	purpose, data := mail.PurposeComment, any(entries[0])
	if len(entries) > 1 {
		shown := entries
		if len(shown) > notifyDigestEntries {
			shown = shown[:notifyDigestEntries]
		}
		purpose, data = mail.PurposeDigest, mail.Digest{Count: len(entries), Brand: s.settings().UI.Brand.Name, Entries: shown}
	}
	subject, body, err := s.templates.Render(purpose, u.Locale, data)
	if err != nil {
		s.log.Error("cannot render mail", "purpose", purpose, "error", err)
		for _, g := range live {
			s.failMail(ctx, g, now, notifyReasonRender)
		}
		return mailFailed
	}
	// The Message-ID comes from the lowest outbox row, so a resend of the same rows names the same message.
	lowest := live[0].OutboxID
	for _, g := range live[1:] {
		lowest = min(lowest, g.OutboxID)
	}
	msgID := fmt.Sprintf("ntf.%d@%s", lowest, s.mailHost())
	msg := mail.Message{To: u.Email, Subject: subject, Body: body, MessageID: msgID,
		ListUnsubscribe: s.unsubscribeURL(accountID, userID)}
	if err := s.mailer.Send(ctx, msg); err != nil {
		// The text of the error may name the recipient, so only the purpose is logged.
		s.log.Warn("cannot send mail", "purpose", purpose)
		for _, g := range live {
			s.failMail(ctx, g, now, notifyReasonSMTP)
		}
		return mailFailed
	}
	for _, g := range live {
		s.finishMail(ctx, store.NotifyOutcome{OutboxID: g.OutboxID, Lease: g.Lease, State: store.OutboxSent, At: now})
	}
	return mailSent
}

// failMail records one failed send of a row: its next try is after the webhook backoff, and after notifyMaxAttempts it is
// failed for good.
func (s *Server) failMail(ctx context.Context, g store.MailJob, now time.Time, reason string) {
	attempts := g.Attempts + 1
	if attempts >= notifyMaxAttempts {
		s.finishMail(ctx, store.NotifyOutcome{OutboxID: g.OutboxID, Lease: g.Lease, State: store.OutboxFailed,
			Reason: reason, At: now, Attempt: true})
		return
	}
	next := now.Add(webhook.Backoff(attempts, s.webhookJitter()))
	s.finishMail(ctx, store.NotifyOutcome{OutboxID: g.OutboxID, Lease: g.Lease, At: now, NextAt: next,
		Reason: reason, Attempt: true})
}

// finishMail records an outcome. A failure to record one is logged; the row then keeps its lease, and is sent again once
// the lease ends, which is the at-least-once rule.
func (s *Server) finishMail(ctx context.Context, o store.NotifyOutcome) {
	ctx = context.WithoutCancel(ctx)
	if err := s.store.FinishMail(ctx, o); err != nil {
		s.log.Warn("cannot record a notification outcome", "error", err)
	}
}

// mailHost is the host part of the Message-ID: the host of the public address, which every mail already names.
func (s *Server) mailHost() string {
	if u, err := url.Parse(s.cfg.PublicURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "selfcloud"
}

// notifyUser reads a user of a pass once, and returns nil for a user who is gone.
func (s *Server) notifyUser(ctx context.Context, pass *notifyPass, accountID, userID string) (*store.User, error) {
	if u, ok := pass.users[userID]; ok {
		if u == nil {
			return nil, store.ErrNotFound
		}
		return u, nil
	}
	u, err := s.store.User(ctx, accountID, userID)
	if err != nil && err != store.ErrNotFound {
		return nil, err
	}
	pass.users[userID] = u
	if u == nil {
		return nil, store.ErrNotFound
	}
	return u, nil
}

// checkNotice is the send-time check of one row (design section 7.4). It returns the entry the mail shows and "" when the
// row may be sent. Otherwise it returns the reason the row is skipped, or notifyHoldComments when the row waits. A read that
// fails returns an error instead: an error says nothing about the row, so the caller puts the row back with no attempt
// counted. Only a definite answer skips a row: the user is inactive or has email off, the comment is gone, the
// item is not reached, or the row is older than notifyMailMaxAge.
func (s *Server) checkNotice(ctx context.Context, pass *notifyPass, u *store.User, g store.MailJob, now time.Time) (mail.Entry, string, error) {
	if u.Status != store.UserActive {
		return mail.Entry{}, notifyReasonUser, nil
	}
	on, err := s.store.NotifyEmailOn(ctx, u.ID)
	if err != nil {
		return mail.Entry{}, "", err
	}
	if !on {
		return mail.Entry{}, notifyReasonPref, nil
	}
	comments, err := s.store.CommentsOn(ctx, g.AccountID)
	if err != nil {
		return mail.Entry{}, "", err
	}
	if !comments {
		return mail.Entry{}, notifyHoldComments, nil
	}
	if now.Sub(g.CreatedAt) > notifyMailMaxAge {
		return mail.Entry{}, notifyReasonLate, nil
	}
	c, err := s.store.CommentIn(ctx, g.AccountID, g.CommentID)
	if errors.Is(err, store.ErrNotFound) {
		return mail.Entry{}, notifyReasonComment, nil
	}
	if err != nil {
		return mail.Entry{}, "", err
	}
	if c.DeletedAt != nil {
		return mail.Entry{}, notifyReasonComment, nil
	}
	// The reach is for this user and this node. A node two users of a pass share has a different answer for each (D1).
	key := passKey(u.ID, g.NodeID)
	reach, seen := pass.reach[key]
	if !seen {
		reach, err = reachesNode(ctx, s.store, g.AccountID, g.NodeID, u.ID)
		if err != nil {
			return mail.Entry{}, "", err
		}
		pass.reach[key] = reach
	}
	if !reach {
		return mail.Entry{}, notifyReasonReach, nil
	}
	n, err := s.store.Node(ctx, g.AccountID, g.NodeID)
	if errors.Is(err, store.ErrNotFound) {
		return mail.Entry{}, notifyReasonReach, nil
	}
	if err != nil {
		return mail.Entry{}, "", err
	}
	f, err := s.store.Folder(ctx, g.AccountID, n.FolderID)
	if errors.Is(err, store.ErrNotFound) {
		return mail.Entry{}, notifyReasonReach, nil
	}
	if err != nil {
		return mail.Entry{}, "", err
	}
	names := map[string]string{}
	if g.ActorID != "" {
		a, err := s.notifyActor(ctx, pass, g.AccountID, g.ActorID)
		if err != nil {
			return mail.Entry{}, "", err
		}
		if a != nil && a.DisplayName != "" {
			names[a.ID] = a.DisplayName
		}
	}
	// The recipient reads the mail as a member of the account, so the name rule is the page's rule, with the recipient's own
	// principal. A name the rule does not give becomes the label of the recipient's language, never an address (D4).
	recipient := &auth.Principal{Kind: auth.KindUser, ID: u.ID, AccountID: u.AccountID, UserID: u.ID}
	actor := personName(recipient, g.AccountID, names, g.ActorID)
	if actor == "" {
		actor = mail.ActorLabel(u.Locale)
	}
	return mail.Entry{Actor: mail.Name(actor), Item: mail.Name(itemName(n, f)), Kind: g.Kind,
		Link: s.nodeLink(g.NodeID)}, "", nil
}

// notifyActor returns the user who acted, or nil when that user is gone. It is read once per account and actor in a pass.
func (s *Server) notifyActor(ctx context.Context, pass *notifyPass, accountID, actorID string) (*store.User, error) {
	key := passKey(accountID, actorID)
	a, ok := pass.actors[key]
	if !ok {
		var err error
		a, err = s.store.User(ctx, accountID, actorID)
		if err != nil && err != store.ErrNotFound {
			return nil, err
		}
		if err == store.ErrNotFound {
			a = nil
		}
		pass.actors[key] = a
	}
	return a, nil
}

// visibleNotices keeps the notifications whose item the reader reaches, and gives each one its item's name, read now. A
// notification whose item the reader lost reach to is left out: it is kept in the store, and shows again if reach returns.
// The actors' names are read for the kept rows only, and only when the reader may learn names.
func (s *Server) visibleNotices(ctx context.Context, p *auth.Principal, rows []store.NotifyRow) ([]notifyJSON, error) {
	// While the account's comments are off the list and the bell show nothing; the rows are kept.
	on, err := s.store.CommentsOn(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	if !on {
		return []notifyJSON{}, nil
	}
	nodeIDs := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, r := range rows {
		if !seen[r.NodeID] {
			seen[r.NodeID] = true
			nodeIDs = append(nodeIDs, r.NodeID)
		}
	}
	reach, err := reachRoles(ctx, s.store, p, nodeIDs)
	if err != nil {
		return nil, err
	}
	var kept []string
	for _, nid := range nodeIDs {
		if reach[nid] != "" {
			kept = append(kept, nid)
		}
	}
	nodes, err := s.store.NodesByID(ctx, p.AccountID, kept)
	if err != nil {
		return nil, err
	}
	folders := map[string]*store.Folder{}
	var actorIDs []string
	for _, r := range rows {
		if nodes[r.NodeID] != nil && reach[r.NodeID] != "" && r.ActorID != "" {
			actorIDs = append(actorIDs, r.ActorID)
		}
	}
	names := map[string]string{}
	if peopleNamesSeen(p, p.AccountID) && len(actorIDs) > 0 {
		if names, err = s.store.UserNames(ctx, p.AccountID, actorIDs); err != nil {
			return nil, err
		}
	}
	out := []notifyJSON{}
	for _, r := range rows {
		n := nodes[r.NodeID]
		if n == nil || reach[r.NodeID] == "" {
			continue
		}
		f := folders[n.FolderID]
		if n.IsRoot() && f == nil {
			if f, err = s.store.Folder(ctx, p.AccountID, n.FolderID); err != nil {
				return nil, err
			}
			folders[n.FolderID] = f
		}
		j := notifyJSON{ID: r.ID, Kind: r.Kind, NodeID: r.NodeID, ItemName: itemName(n, f), CreatedAt: r.CreatedAt,
			ReadAt: r.ReadAt, ActorGone: r.ActorID == ""}
		j.ActorName = personName(p, p.AccountID, names, r.ActorID)
		out = append(out, j)
	}
	return out, nil
}
