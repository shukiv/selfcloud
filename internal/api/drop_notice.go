// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// The owner's mail about a drop link (design section 8, Share links). A drop link that asks to notify its creator
// mails the files that arrived: at most one mail an hour per link, reporting the count since the last one. A file
// that arrives inside the hour is reported once the hour ends, by the periodic run in runDropNotices.

import (
	"context"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// dropNoticeEvery is how often the notifier looks for drop links owed a mail. A mail is at most an hour apart, so
// a minute's wait is the most a file waits past the hour.
const dropNoticeEvery = time.Minute

// dropNoticeBatch bounds the links one run considers.
const dropNoticeBatch = 500

// dropData fills the drop template: how many files arrived, in which folder, and the folder's link.
type dropData struct {
	Count  int
	Folder string
	Link   string
}

// runDropNotices runs sendDropNotices every dropNoticeEvery until ctx ends.
func (s *Server) runDropNotices(ctx context.Context) {
	t := time.NewTicker(dropNoticeEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.sendDropNotices(ctx, now)
		}
	}
}

// sendDropNotices mails the creator of each drop link owed a mail at now. Each link's files are claimed before the
// mail is written, so a file that arrives meanwhile is counted for the next mail. A mail that cannot go gives its
// files back to the count, and the link waits an hour before it is tried again, so a failing mail server is not
// retried every minute and the hourly budget is not spent on retries.
func (s *Server) sendDropNotices(ctx context.Context, now time.Time) {
	links, err := s.store.DueDropNotices(ctx, now, dropNoticeBatch)
	if err != nil {
		s.log.Warn("cannot list the drop links owed mail", "error", err)
		return
	}
	for _, l := range links {
		if ctx.Err() != nil {
			return
		}
		s.notifyDrop(ctx, l, now)
	}
}

// notifyDrop sends one link's mail, if its creator may still receive it. The creator is mailed only while it is an
// active user who still manages the link's item; a link made by an account key has no one to mail, so its files are
// counted and not mailed.
func (s *Server) notifyDrop(ctx context.Context, l *store.ShareLink, now time.Time) {
	if s.mailer == nil {
		return
	}
	var count int
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		c, _, err := tx.ClaimDropNotice(ctx, l.AccountID, l.ID, now)
		count = c
		return err
	})
	if err != nil || count == 0 {
		return
	}
	to, subject, body, ok := s.dropMail(ctx, l, count)
	if !ok {
		// The creator may not receive this mail (gone, not active, or no longer a manager). The count is dropped
		// with nothing mailed, so the files are not reported again.
		return
	}
	if err := s.chargeAll(charge{"mail.drop.account", l.AccountID}, charge{"mail.drop.target", emailKey(to)}); err != nil {
		s.giveBackDrop(ctx, l, count, now)
		return
	}
	if err := s.mailer.Send(ctx, mail.Message{To: to, Subject: subject, Body: body}); err != nil {
		s.log.Warn("cannot send mail", "purpose", mail.PurposeDrop, "error", err)
		s.giveBackDrop(ctx, l, count, now)
	}
}

// dropMail renders the mail for a link's creator. ok is false when the creator is not to be mailed.
func (s *Server) dropMail(ctx context.Context, l *store.ShareLink, count int) (to, subject, body string, ok bool) {
	if l.CreatedKind != store.CreatorUser {
		return "", "", "", false
	}
	u, err := s.store.User(ctx, l.AccountID, l.CreatedBy)
	if err != nil || u.Status != store.UserActive {
		return "", "", "", false
	}
	n, err := s.store.Node(ctx, l.AccountID, l.NodeID)
	if err != nil {
		return "", "", "", false
	}
	p := &auth.Principal{Kind: auth.KindUser, ID: u.ID, AccountID: u.AccountID, UserID: u.ID, Role: u.Role}
	if _, role, err := nodeSharingAccess(ctx, s.store, p, l.NodeID); err != nil || role != store.RoleManager {
		return "", "", "", false
	}
	f, err := s.store.Folder(ctx, l.AccountID, n.FolderID)
	if err != nil {
		return "", "", "", false
	}
	subject, body, err = s.templates.Render(mail.PurposeDrop, u.Locale, dropData{Count: count,
		Folder: mailText(itemName(n, f)), Link: s.nodeLink(n.ID)})
	if err != nil {
		s.log.Error("cannot render mail", "purpose", mail.PurposeDrop, "error", err)
		return "", "", "", false
	}
	return u.Email, subject, body, true
}

// giveBackDrop returns the count of a mail that did not go. The failed try counts as the link's last mail, so the
// next try is an hour after it, not on the next run.
func (s *Server) giveBackDrop(ctx context.Context, l *store.ShareLink, count int, tried time.Time) {
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.RestoreDropNotice(ctx, l.AccountID, l.ID, count, &tried)
	})
	if err != nil {
		s.log.Warn("cannot give back the files of a drop link", "error", err)
	}
}
