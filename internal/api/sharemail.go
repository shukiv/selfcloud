// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"strings"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// maxGroupMail is the most members one group change may mail. A group with more members gets no mail
// at all, and the reply says so: a share to a large group is a broadcast, which an owner sends on
// purpose, not by accident.
const maxGroupMail = 50

// shareNotice says whether the subjects of a change were told by mail, and why not when they were not.
type shareNotice struct {
	Notified bool
	Reason   string
}

// shareData fills the share and access_request templates. Each field is text the mail shows as it is.
type shareData struct {
	Actor   string
	Item    string
	Role    string
	Link    string
	Message string
}

// decisionData fills the access_decision template.
type decisionData struct {
	Actor    string
	Item     string
	Role     string
	Link     string
	Approved bool
}

// cleanNote normalises a note a user wrote, before it is stored and again before it is mailed. The mail
// package does the work, so every user-written note meets the same rule.
func cleanNote(s string) string { return mail.CleanBody(s) }

// mailText makes a value safe for a mail line: a line break in a node name or an actor's name would
// start a header, so every break becomes a space. The mail sender refuses a subject with a break as well.
func mailText(v string) string {
	return strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(v)
}

// itemName is the name a mail shows for a node: a folder's root takes the folder's name.
func itemName(n *store.Node, f *store.Folder) string {
	if n.IsRoot() && f != nil {
		return f.Name
	}
	return n.Name
}

// nodeLink is the web address of a node. The web interface routes it (slice k).
func (s *Server) nodeLink(nodeID string) string {
	return strings.TrimRight(s.cfg.PublicURL, "/") + "/n/" + nodeID
}

// actorName is the name a mail gives as the sender of a change: the user's display name or email, or
// the account's name for an account key.
func (s *Server) actorName(ctx context.Context, p *auth.Principal) string {
	if p.Kind == auth.KindUser {
		if u, err := s.store.User(ctx, p.AccountID, p.UserID); err == nil {
			if u.DisplayName != "" {
				return u.DisplayName
			}
			return u.Email
		}
	}
	if acc, err := s.store.Account(ctx, p.AccountID); err == nil {
		return acc.Name
	}
	return ""
}

// shareMail tells the subject of a permission that it has a role on the node. A user subject is mailed
// when it is active; a group subject mails each active member, up to maxGroupMail members. The caller
// is not mailed about its own change.
func (s *Server) shareMail(ctx context.Context, p *auth.Principal, n *store.Node, f *store.Folder,
	kind, subj, role, message string) shareNotice {
	if s.mailer == nil {
		return shareNotice{Reason: "mail_not_configured"}
	}
	users, reason := s.activeRecipients(ctx, n.AccountID, kind, subj)
	if reason != "" {
		return shareNotice{Reason: reason}
	}
	actor := mailText(s.actorName(ctx, p))
	item := mailText(itemName(n, f))
	link := s.nodeLink(n.ID)
	note := cleanNote(message)
	return s.sendEach(ctx, p, users, func(u *store.User) error { return s.shareBudget(n.AccountID, u.Email) },
		func(u *store.User) (string, string, error) {
			return s.templates.Render("share", u.Locale, shareData{Actor: actor, Item: item,
				Role: mail.RoleLabel(u.Locale, role), Link: link, Message: note})
		})
}

// shareBudget charges one share mail to its own buckets, not the verification buckets. A sharer who
// uses up the share allowance therefore cannot stop a user from signing in: verification and reset mail
// draw on mail.account and mail.target alone. When a bucket is empty the share still happens; only its
// mail is skipped, and the reply says so.
func (s *Server) shareBudget(accountID, address string) error {
	return s.chargeAll(charge{"mail.share.account", accountID}, charge{"mail.share.target", emailKey(address)})
}

// activeRecipients returns the users a change reaches, and a reason when none can be mailed. Only
// active users get mail: a pending user cannot sign in to see the item, and a disabled one may not.
func (s *Server) activeRecipients(ctx context.Context, accountID, kind, subj string) ([]*store.User, string) {
	if kind == store.SubjectUser {
		u, err := s.store.User(ctx, accountID, subj)
		if err != nil {
			return nil, "no_recipients"
		}
		if u.Status != store.UserActive {
			return nil, "recipient_not_active"
		}
		return []*store.User{u}, ""
	}
	members, _, err := s.store.GroupMembers(ctx, subj, store.Page{Limit: maxGroupMail + 1})
	if err != nil {
		return nil, "mail_failed"
	}
	if len(members) > maxGroupMail {
		return nil, "too_many_recipients"
	}
	var out []*store.User
	for _, m := range members {
		u, err := s.store.User(ctx, accountID, m.UserID)
		if err == nil && u.Status == store.UserActive {
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		return nil, "no_recipients"
	}
	return out, ""
}

// sendEach mails each user the message render builds for it, in its own locale. Each send takes one
// unit from the mail budget, the same budget verification mail uses, so a refused send is skipped and
// reported rather than failing the change. The caller is never mailed. A send that fails is logged
// without the address.
func (s *Server) sendEach(ctx context.Context, p *auth.Principal, users []*store.User,
	charge func(*store.User) error, render func(*store.User) (subject, body string, err error)) shareNotice {
	eligible, reason := 0, ""
	for _, u := range users {
		if p.Kind == auth.KindUser && u.ID == p.UserID {
			continue
		}
		eligible++
		if err := charge(u); err != nil {
			reason = "mail_budget"
			continue
		}
		subject, body, err := render(u)
		if err != nil {
			s.log.Error("cannot render mail", "error", err)
			reason = "mail_failed"
			continue
		}
		if err := s.mailer.Send(ctx, mail.Message{To: u.Email, Subject: subject, Body: body}); err != nil {
			s.log.Warn("cannot send mail", "purpose", "share", "error", err)
			reason = "mail_failed"
		}
	}
	if eligible == 0 {
		return shareNotice{Reason: "no_recipients"}
	}
	return shareNotice{Notified: reason == "", Reason: reason}
}
